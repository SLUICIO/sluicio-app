// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A rate limiter is one of those features where the failure modes are
// worse than the absence. Two in particular:
//
//   - Limiting the wrong callers. Throttling a person clicking through
//     the UI turns a protective measure into a usability bug, and one
//     nobody attributes correctly: the app just feels broken.
//   - Sharing a bucket between callers. If two agents draw on the same
//     budget, one team's runaway script silently starves another's, and
//     the symptom appears in the innocent party's logs.
//
// Both are pinned below, alongside the mechanics.

package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// withPrincipal builds a request carrying an authenticated principal.
func withPrincipal(p identity.Principal, bearer bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/services", nil)
	if bearer {
		r.Header.Set("Authorization", "Bearer con_sa_test")
	}
	return r.WithContext(middleware.WithPrincipal(r.Context(), p))
}

func TestBrowserSessionsAreNeverLimited(t *testing.T) {
	// A user principal WITHOUT a bearer token is someone in a browser.
	// They must never be charged, however fast they click.
	uid := uuid.New()
	r := withPrincipal(identity.Principal{Kind: identity.PrincipalUser, UserID: &uid}, false)
	if key := rateLimitKey(r); key != "" {
		t.Errorf("rateLimitKey = %q for a session user; sessions must be exempt", key)
	}
}

func TestScriptedUserAndServiceAccountAreLimited(t *testing.T) {
	uid, said := uuid.New(), uuid.New()

	scripted := withPrincipal(identity.Principal{Kind: identity.PrincipalUser, UserID: &uid}, true)
	if rateLimitKey(scripted) == "" {
		t.Error("a user calling with a bearer token is scripted access and must be limited")
	}

	agent := withPrincipal(identity.Principal{
		Kind: identity.PrincipalServiceAccount, ServiceAccountID: &said,
	}, true)
	if rateLimitKey(agent) == "" {
		t.Error("service accounts must be limited — they are the looping-agent case")
	}
}

func TestServiceAccountKeysOnTheAccountNotTheToken(t *testing.T) {
	// Minting a second token must not double an agent's budget, or the
	// limit is trivially escaped by anyone who notices.
	said := uuid.New()
	a := withPrincipal(identity.Principal{Kind: identity.PrincipalServiceAccount, ServiceAccountID: &said}, true)
	b := withPrincipal(identity.Principal{Kind: identity.PrincipalServiceAccount, ServiceAccountID: &said}, true)
	if rateLimitKey(a) != rateLimitKey(b) {
		t.Error("two tokens for one service account must share a budget")
	}
}

func TestBurstThenBlockThenRefill(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	now := base
	l := NewRateLimiter(60, 5) // 1/sec sustained, burst 5
	l.now = func() time.Time { return now }

	// The burst is available immediately — an agent opening a session
	// makes several calls at once and must not be punished for it.
	for i := 0; i < 5; i++ {
		if ok, _ := l.allow("sa:x"); !ok {
			t.Fatalf("request %d within the burst was blocked", i+1)
		}
	}
	ok, wait := l.allow("sa:x")
	if ok {
		t.Fatal("the 6th request exceeded the burst and should have been blocked")
	}
	if wait < time.Second {
		t.Errorf("Retry-After was %v; telling a client to retry immediately makes it hammer", wait)
	}

	// After enough time, capacity returns.
	now = base.Add(3 * time.Second)
	if ok, _ := l.allow("sa:x"); !ok {
		t.Error("bucket did not refill after 3 seconds at 1/sec")
	}
}

func TestCallersDoNotShareABudget(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	l := NewRateLimiter(60, 2)
	l.now = func() time.Time { return now }

	// Exhaust one caller entirely.
	l.allow("sa:looping")
	l.allow("sa:looping")
	if ok, _ := l.allow("sa:looping"); ok {
		t.Fatal("test setup: the looping caller should be exhausted")
	}

	// A different caller is unaffected. Getting this wrong means one
	// team's runaway script takes another team's agent down with it.
	if ok, _ := l.allow("sa:innocent"); !ok {
		t.Error("an unrelated caller was blocked by someone else's usage")
	}
}

func TestIdleBucketsAreEvicted(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	now := base
	l := NewRateLimiter(600, 10)
	l.now = func() time.Time { return now }

	// Fill past the sweep threshold with callers that then go idle.
	for i := 0; i < 1200; i++ {
		l.allow("sa:" + uuid.NewString())
	}
	before := len(l.buckets)

	// Long after their TTL, a new caller triggers the sweep.
	now = base.Add(idleBucketTTL + time.Minute)
	l.allow("sa:" + uuid.NewString())

	if len(l.buckets) >= before {
		t.Errorf("buckets grew from %d to %d — idle entries are leaking", before, len(l.buckets))
	}
}

func TestNilLimiterPassesEverythingThrough(t *testing.T) {
	// A cell with limiting off must behave exactly as before.
	h := &Handlers{Limiter: nil}
	said := uuid.New()
	served := 0
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served++ })

	for i := 0; i < 50; i++ {
		w := httptest.NewRecorder()
		h.RateLimit(next).ServeHTTP(w, withPrincipal(identity.Principal{
			Kind: identity.PrincipalServiceAccount, ServiceAccountID: &said,
		}, true))
		if w.Code == http.StatusTooManyRequests {
			t.Fatal("a nil limiter blocked a request")
		}
	}
	if served != 50 {
		t.Errorf("served %d of 50 requests with limiting disabled", served)
	}
}

func TestBlockedResponseCarriesRetryAfter(t *testing.T) {
	said := uuid.New()
	h := &Handlers{
		Limiter: NewRateLimiter(60, 1),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.RateLimit(next).ServeHTTP(w, withPrincipal(identity.Principal{
			Kind: identity.PrincipalServiceAccount, ServiceAccountID: &said,
		}, true))
		return w
	}

	if got := req().Code; got != http.StatusOK {
		t.Fatalf("first request = %d, want 200", got)
	}
	w := req()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("429 without Retry-After — a client has nothing to back off against")
	}
}
