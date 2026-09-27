// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// An integration's matchers belong to the integration's org, and nobody
// else can write them.
//
// Every other integration write goes through a store query that filters
// on organization_id. Adding and removing a matcher did not: the route
// guard checks the caller's ROLE (and, for a group-scoped editor, the
// services involved), and an ordinary org editor passes it for any
// integration id at all. So an editor in one org who had another org's
// integration id could add matchers to it, and one with a matcher id
// could delete it - through their own integration's URL, even, since the
// delete never checked that the matcher belonged to the integration named.
//
//	go test -tags integration ./services/cell-api/internal/api/...

package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
)

func TestMatchersCannotBeWrittenAcrossOrgs(t *testing.T) {
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
	// Wired exactly as handlers.go wires them.
	mux.HandleFunc("POST /api/v1/integrations/{id}/matchers",
		h.writeAnywhere(h.requireManageIntegration(h.addMatcher)))
	mux.HandleFunc("DELETE /api/v1/integrations/{id}/matchers/{matcherId}",
		h.writeAnywhere(h.requireManageIntegration(h.removeMatcher)))

	orgA := createOrg(t, ctx, pool, "org-a", "Org A")
	orgB := createOrg(t, ctx, pool, "org-b", "Org B")
	mk := func(org uuid.UUID, slug string) integrations.IntegrationWithMatchers {
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
		return out
	}
	intA := mk(orgA, "a-int")
	intB := mk(orgB, "b-int")

	// An admin of org A, and of org A only.
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
	do := func(method, path, body string) int {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sess.ID})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	matchersOf := func(id uuid.UUID) []integrations.Matcher {
		t.Helper()
		ms, err := ints.MatchersForIntegration(ctx, id)
		if err != nil {
			t.Fatalf("matchers: %v", err)
		}
		return ms
	}
	const matcher = `{"attribute":"service.name","operator":"equals","value":"planted"}`

	t.Run("adding to another org's integration is refused", func(t *testing.T) {
		if got := do(http.MethodPost, "/api/v1/integrations/"+intB.ID.String()+"/matchers", matcher); got != http.StatusNotFound {
			t.Errorf("org A adding to org B's integration: got %d, want 404", got)
		}
		if n := len(matchersOf(intB.ID)); n != 1 {
			t.Errorf("org B's integration has %d matchers, want its original 1", n)
		}
	})

	t.Run("removing another org's matcher is refused, by either URL", func(t *testing.T) {
		victim := intB.Matchers[0].ID.String()
		// Through org B's integration...
		if got := do(http.MethodDelete, "/api/v1/integrations/"+intB.ID.String()+"/matchers/"+victim, ""); got != http.StatusNotFound {
			t.Errorf("via org B's integration: got %d, want 404", got)
		}
		// ...and through the caller's OWN integration, which every guard
		// in front of the handler is satisfied by.
		if got := do(http.MethodDelete, "/api/v1/integrations/"+intA.ID.String()+"/matchers/"+victim, ""); got != http.StatusNotFound {
			t.Errorf("via org A's own integration: got %d, want 404", got)
		}
		if n := len(matchersOf(intB.ID)); n != 1 {
			t.Errorf("org B's integration has %d matchers, want its original 1", n)
		}
	})

	// The control: none of the above is refused because the routes refuse
	// everything.
	t.Run("the org's own integration still takes both", func(t *testing.T) {
		if got := do(http.MethodPost, "/api/v1/integrations/"+intA.ID.String()+"/matchers", matcher); got != http.StatusCreated {
			t.Fatalf("adding to own integration: got %d, want 201", got)
		}
		own := intA.Matchers[0].ID.String()
		if got := do(http.MethodDelete, "/api/v1/integrations/"+intA.ID.String()+"/matchers/"+own, ""); got != http.StatusNoContent {
			t.Errorf("removing own matcher: got %d, want 204", got)
		}
		ms := matchersOf(intA.ID)
		if len(ms) != 1 || ms[0].Value != "planted" {
			t.Errorf("own integration after add+remove: %+v, want only the planted matcher", ms)
		}
	})
}
