// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Per-caller request limits (issue #8, WS5).
//
// The hazard this exists for is specific: an agent in a retry loop, or a
// script with an off-by-one, issuing thousands of calls a minute. Most
// of the read surface fans out to ClickHouse, so a runaway caller
// doesn't just waste its own quota — it degrades the cell for everyone
// sharing that database, including the humans trying to work out why
// their dashboards went slow.
//
// Three decisions worth stating.
//
// It limits TOKEN callers, never browser sessions. A person clicking
// through the UI is not the failure mode, and throttling them would turn
// a protective measure into a usability bug. Sessions pass through
// untouched.
//
// It is IN-MEMORY, which is correct here rather than a shortcut:
// cell-api runs single-replica by design (it owns in-process schedulers
// and applies migrations at startup), so one process sees every request
// and a shared store would add a dependency to buy nothing.
//
// The default ceiling is DELIBERATELY GENEROUS. This is a safety valve
// against loops, not a quota product — an agent doing real work makes
// tens of calls a minute, while a loop makes thousands, and the gap
// between those is wide enough that a blunt limit separates them without
// touching legitimate use. Tighter, configurable per-token budgets are
// the Enterprise layer; this floor protects every cell, because keeping
// the database up is hygiene rather than a feature.

package api

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

const (
	// defaultTokenRatePerMin is the sustained ceiling per caller.
	defaultTokenRatePerMin = 600
	// defaultTokenBurst absorbs the legitimate spike of an agent opening
	// a session — a cell brief plus a handful of follow-ups — without
	// letting a loop run free.
	defaultTokenBurst = 120
	// idleBucketTTL bounds memory. Without eviction, a cell issuing many
	// short-lived tokens would accumulate a bucket per token forever:
	// a slow leak that only shows up in a long-running process, which is
	// exactly where it is hardest to notice.
	idleBucketTTL = 30 * time.Minute
)

// tokenBucket is a classic leaky bucket: tokens refill at a steady rate
// up to a burst ceiling, and each request costs one.
type tokenBucket struct {
	tokens   float64
	last     time.Time
	lastSeen time.Time
}

// RateLimiter caps how fast a single token-authenticated caller may hit
// the API. The zero value is not usable — construct with NewRateLimiter.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	// perSecond and burst define the refill; kept as floats because the
	// refill is fractional between requests.
	perSecond float64
	burst     float64
	// now is swappable so tests can advance time without sleeping.
	now func() time.Time
}

func NewRateLimiter(perMinute, burst int) *RateLimiter {
	if perMinute <= 0 {
		perMinute = defaultTokenRatePerMin
	}
	if burst <= 0 {
		burst = defaultTokenBurst
	}
	return &RateLimiter{
		buckets:   map[string]*tokenBucket{},
		perSecond: float64(perMinute) / 60,
		burst:     float64(burst),
		now:       time.Now,
	}
}

// allow reports whether the caller may proceed, and if not, how long
// until one token is available.
func (l *RateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		// A new caller starts full, so a first request is never delayed.
		b = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
		l.sweepLocked(now)
	}
	// Refill for the elapsed time, capped at the burst ceiling.
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.perSecond
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	// Round up: telling a client to retry in 0s would have it hammer.
	wait := time.Duration((1-b.tokens)/l.perSecond*float64(time.Second)) + time.Second
	return false, wait
}

// sweepLocked drops buckets nothing has touched recently. Called on new
// keys only — sweeping on every request would cost more than it saves,
// and new keys are exactly when the map grows.
func (l *RateLimiter) sweepLocked(now time.Time) {
	if len(l.buckets) < 1000 {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.lastSeen) > idleBucketTTL {
			delete(l.buckets, k)
		}
	}
}

// rateLimitKey identifies the caller to charge.
//
// Service accounts key on the SA, not the individual token: an agent is
// one actor whether it holds one credential or three, and letting it
// multiply its budget by minting tokens would defeat the point. Users
// key on the user for the same reason. Browser sessions return "",
// meaning unlimited.
func rateLimitKey(r *http.Request) string {
	p := middleware.Principal(r)
	if p.Kind == identity.PrincipalServiceAccount && p.ServiceAccountID != nil {
		return "sa:" + p.ServiceAccountID.String()
	}
	// A user principal holding a bearer token is scripted access; a user
	// with a session cookie is a person in a browser and is exempt.
	if p.UserID != nil && r.Header.Get("Authorization") != "" {
		return "user:" + p.UserID.String()
	}
	return ""
}

// RateLimit wraps a handler with the per-caller ceiling. A nil limiter
// disables it entirely, so a cell can turn this off without the wrapper
// having to know.
func (h *Handlers) RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.Limiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		key := rateLimitKey(r)
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		if ok, wait := h.Limiter.allow(key); !ok {
			secs := int(wait.Seconds())
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", fmt.Sprintf("%d", secs))
			// A plain-language body: the caller is usually an agent, and
			// "you are looping" is more actionable than a bare status.
			http.Error(w,
				`{"error":{"status":429,"message":"rate limit exceeded for this token — you are calling faster than this cell allows. Back off and retry after the Retry-After interval; if you are polling, consider subscribing to events instead."}}`,
				http.StatusTooManyRequests)
			h.Logger.Warn("rate limit exceeded", "caller", key, "path", r.URL.Path)
			return
		}
		next.ServeHTTP(w, r)
	})
}
