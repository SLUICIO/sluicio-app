// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Saving an integration's matchers is one request that lands whole or
// not at all.
//
// The editor used to save by adding the new rows and then deleting the
// old ones, one request each, so a failure part-way through left the
// integration matching both sets, or half of one. What matters here is
// less that the new set arrives than that a bad one changes NOTHING.
//
//	go test -tags integration ./services/cell-api/internal/api/...

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
)

func TestReplacingMatchersIsAllOrNothing(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	ints := integrations.NewStore(pool)
	h := &Handlers{
		Identity:     idStore,
		Integrations: ints,
		Resolver:     integrations.NewResolver(ints, time.Minute),
		AuthMW:       &middleware.Resolver{Identity: idStore},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	// Wired exactly as handlers.go wires it.
	mux.HandleFunc("PUT /api/v1/integrations/{id}/matchers",
		h.writeAnywhere(h.requireManageIntegration(h.replaceMatchers)))

	orgA := createOrg(t, ctx, pool, "org-a", "Org A")
	orgB := createOrg(t, ctx, pool, "org-b", "Org B")
	mk := func(org uuid.UUID, slug string) uuid.UUID {
		t.Helper()
		out, err := ints.Create(ctx, integrations.IntegrationWithMatchers{
			Integration: integrations.Integration{OrganizationID: org, Slug: slug, Name: slug},
			Matchers: []integrations.Matcher{{
				Attribute: "service.name", Operator: integrations.OperatorEquals, Value: slug + "-svc",
			}},
		})
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		return out.Integration.ID
	}
	intA := mk(orgA, "a-int")
	intB := mk(orgB, "b-int")

	u, err := idStore.CreateUser(ctx, "admin-a@example.test", "Admin A")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := idStore.AddMember(ctx, u.ID, orgA, identity.RoleAdmin); err != nil {
		t.Fatalf("add member: %v", err)
	}
	sess, err := idStore.CreateSession(ctx, u.ID, time.Hour, "test")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	put := func(id uuid.UUID, body string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/integrations/"+id.String()+"/matchers", bytes.NewReader([]byte(body)))
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sess.ID})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	// What is stored, as "attribute op value @group" sorted, plus the mode.
	state := func(id uuid.UUID) ([]string, integrations.RuleMatch) {
		t.Helper()
		ms, err := ints.MatchersForIntegration(ctx, id)
		if err != nil {
			t.Fatalf("matchers: %v", err)
		}
		out := make([]string, 0, len(ms))
		for _, m := range ms {
			b, _ := json.Marshal([]any{m.Attribute, m.Operator, m.Value, m.MatchGroup})
			out = append(out, string(b))
		}
		sort.Strings(out)
		mode, err := ints.RuleMatchForIntegration(ctx, id)
		if err != nil {
			t.Fatalf("rule match: %v", err)
		}
		return out, mode
	}
	same := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	const twoRules = `{"rule_match":"all","matchers":[
		{"attribute":"service.name","operator":"equals","value":"billing","match_group":0},
		{"attribute":"service.name","operator":"prefix","value":"order-","match_group":1},
		{"attribute":"tenant","operator":"equals","value":"acme","match_group":1}]}`

	t.Run("swaps the whole set and the mode in one request", func(t *testing.T) {
		if got := put(intA, twoRules); got != http.StatusOK {
			t.Fatalf("replace: got %d, want 200", got)
		}
		ms, mode := state(intA)
		if len(ms) != 3 || mode != integrations.RuleMatchAll {
			t.Fatalf("after replace: %v mode=%s, want the 3 new rows and mode all", ms, mode)
		}
	})

	t.Run("a set with one bad row changes nothing at all", func(t *testing.T) {
		before, modeBefore := state(intA)
		bad := `{"rule_match":"any","matchers":[
			{"attribute":"service.name","operator":"equals","value":"fine","match_group":0},
			{"attribute":"service.name","operator":"no-such-operator","value":"x","match_group":1}]}`
		if got := put(intA, bad); got != http.StatusBadRequest {
			t.Fatalf("bad set: got %d, want 400", got)
		}
		after, modeAfter := state(intA)
		if !same(before, after) || modeBefore != modeAfter {
			t.Fatalf("a refused save changed the integration: %v/%s -> %v/%s", before, modeBefore, after, modeAfter)
		}
	})

	// The editor sends the mode only when it changed; a save of the rules
	// alone must not reset it.
	t.Run("leaves the mode alone when the request does not name one", func(t *testing.T) {
		if got := put(intA, `{"matchers":[{"attribute":"service.name","operator":"equals","value":"only","match_group":0}]}`); got != http.StatusOK {
			t.Fatalf("replace without mode: got %d, want 200", got)
		}
		ms, mode := state(intA)
		if len(ms) != 1 || mode != integrations.RuleMatchAll {
			t.Fatalf("after replace without mode: %v mode=%s, want 1 row and mode still all", ms, mode)
		}
	})

	t.Run("another org's integration is not found, and untouched", func(t *testing.T) {
		before, modeBefore := state(intB)
		if got := put(intB, twoRules); got != http.StatusNotFound {
			t.Fatalf("org A replacing org B's matchers: got %d, want 404", got)
		}
		after, modeAfter := state(intB)
		if !same(before, after) || modeBefore != modeAfter {
			t.Fatalf("org B's integration changed: %v/%s -> %v/%s", before, modeBefore, after, modeAfter)
		}
	})
}
