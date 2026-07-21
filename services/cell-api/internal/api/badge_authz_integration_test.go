// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Regression guard for the tightened public-badge gate on a service.
// Publishing a badge exposes an entity's health with NO auth, so the
// toggle is gated like the entity's other edits: writeService =
// RequireRole(CanWrite) + canSeeService. This asserts, through the real
// handler chain against a real Postgres, that:
//
//   - a viewer is refused outright (role gate → 403), and
//   - an editor can only badge services their group policies make visible
//     — they cannot publish one outside their scope (visibility → 404),
//   - while an admin (who sees everything) can badge any service.
//
// This is the gap the old RequireWriteAnywhere gate left open: a group-
// editor (org-viewer) could publish any service in the org.
package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/catalog"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

func TestServiceBadgeAuthz(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	cat := catalog.NewStore(pool)
	h := &Handlers{
		Identity: idStore,
		Catalog:  cat,
		AuthMW:   &middleware.Resolver{Identity: idStore},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/services/{name}/badge", h.writeService(h.putServiceBadge))

	org, err := idStore.CreateOrg(ctx, "Acme", "acme")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := cat.UpsertServices(ctx, org.ID, []catalog.Discovery{
		{ServiceName: "svc-a", FirstSeen: now, LastSeen: now},
		{ServiceName: "svc-b", FirstSeen: now, LastSeen: now},
	}); err != nil {
		t.Fatalf("seed services: %v", err)
	}

	// mkUser creates a user with an org role and a live session cookie. When
	// grant != "" they also get a group whose policy makes exactly that
	// service visible to them (their whole visible set).
	mkUser := func(email string, role identity.Role, grant string) string {
		t.Helper()
		u, err := idStore.CreateUser(ctx, email, email)
		if err != nil {
			t.Fatalf("create user %s: %v", email, err)
		}
		if err := idStore.AddMember(ctx, u.ID, org.ID, role); err != nil {
			t.Fatalf("add member: %v", err)
		}
		if grant != "" {
			g, err := idStore.CreateGroup(ctx, org.ID, identity.GroupInput{Name: email + "-g", Slug: email + "-g"})
			if err != nil {
				t.Fatalf("create group: %v", err)
			}
			if err := idStore.AddGroupMember(ctx, u.ID, g.ID, identity.RoleViewer); err != nil {
				t.Fatalf("add group member: %v", err)
			}
			if _, err := idStore.CreatePolicy(ctx, g.ID, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: grant}); err != nil {
				t.Fatalf("create policy: %v", err)
			}
		}
		sess, err := idStore.CreateSession(ctx, u.ID, time.Hour, "test")
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		return sess.ID
	}

	admin := mkUser("admin@acme", identity.RoleAdmin, "")               // sees everything
	editorScoped := mkUser("editor@acme", identity.RoleEditor, "svc-a") // visible set = {svc-a}
	viewer := mkUser("viewer@acme", identity.RoleViewer, "svc-a")       // grant irrelevant — role gate

	put := func(cookie, svc string) int {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/services/"+svc+"/badge", strings.NewReader(`{"public":true}`))
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	cases := []struct {
		name   string
		cookie string
		svc    string
		want   int
	}{
		{"viewer refused by role", viewer, "svc-a", http.StatusForbidden},
		{"editor badges in-scope service", editorScoped, "svc-a", http.StatusOK},
		{"editor blocked on out-of-scope service", editorScoped, "svc-b", http.StatusNotFound},
		{"admin badges any service", admin, "svc-b", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := put(tc.cookie, tc.svc); got != tc.want {
				t.Fatalf("PUT badge status = %d, want %d", got, tc.want)
			}
		})
	}
}
