// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// End-to-end HTTP-status test for the authorization gates against a real
// Postgres (testcontainers). The store-level tests prove the auth
// DECISIONS (CanUserWriteAnywhere, ResolveVisibleServiceSet); this proves
// the HTTP WIRING on top of them: a real session cookie is resolved to a
// Principal, and each gate returns the right status —
//
//	RequireRole(CanWrite)  — viewer 403, editor/admin 200
//	RequireRole(CanAdmin)  — editor 403, admin 200
//	RequireWriteAnywhere   — lone viewer 403, viewer-who-is-group-editor 200
//	RequireOperator        — non-operator 403, operator 200 (regardless of org role)
//	any gate, no cookie     — 401
//
// Run with:
//
//	go test -tags integration ./services/cell-api/internal/api/middleware/...
//
// or `make test-integration`.
package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/integration-monitor/integration-monitor/pkg/postgres"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/migrations"
)

func newGateDB(t *testing.T) (*identity.Store, context.Context) {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("controlplane"),
		tcpostgres.WithUsername("controlplane"),
		tcpostgres.WithPassword("controlplane"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(pg); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := impostgres.Pool(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := impostgres.Migrate(ctx, pool, migrations.FS, migrations.Dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return identity.NewStore(pool), ctx
}

func TestGatesHTTPStatus(t *testing.T) {
	store, ctx := newGateDB(t)
	r := &middleware.Resolver{Identity: store}

	org, err := store.CreateOrg(ctx, "Acme", "acme")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// mkSession creates a user with the given org role, optionally promotes
	// them to operator and/or makes them a group editor, then returns a
	// live session cookie value for them.
	mkSession := func(email string, role identity.Role, operator, groupEditor bool) string {
		t.Helper()
		u, err := store.CreateUser(ctx, email, email)
		if err != nil {
			t.Fatalf("create user %s: %v", email, err)
		}
		if err := store.AddMember(ctx, u.ID, org.ID, role); err != nil {
			t.Fatalf("add member %s: %v", email, err)
		}
		if operator {
			if err := store.SetUserOperator(ctx, u.ID, true); err != nil {
				t.Fatalf("set operator %s: %v", email, err)
			}
		}
		if groupEditor {
			g, err := store.CreateGroup(ctx, org.ID, identity.GroupInput{Name: email + "-team", Slug: email + "-team"})
			if err != nil {
				t.Fatalf("create group: %v", err)
			}
			if err := store.AddGroupMember(ctx, u.ID, g.ID, identity.RoleEditor); err != nil {
				t.Fatalf("add group member: %v", err)
			}
		}
		sess, err := store.CreateSession(ctx, u.ID, time.Hour, "test")
		if err != nil {
			t.Fatalf("create session %s: %v", email, err)
		}
		return sess.ID
	}

	adminC := mkSession("admin@acme", identity.RoleAdmin, false, false)
	editorC := mkSession("editor@acme", identity.RoleEditor, false, false)
	viewerC := mkSession("viewer@acme", identity.RoleViewer, false, false)
	viewerGroupEdC := mkSession("viewer-ge@acme", identity.RoleViewer, false, true)
	operatorC := mkSession("operator@acme", identity.RoleViewer, true, false) // operator ≠ org role

	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	writeGate := r.RequireRole(identity.Role.CanWrite, ok)
	adminGate := r.RequireRole(identity.Role.CanAdmin, ok)
	writeAnywhere := r.RequireWriteAnywhere(ok)
	operatorGate := r.RequireOperator(ok)

	run := func(gate http.HandlerFunc, cookie string) int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
		}
		rec := httptest.NewRecorder()
		gate(rec, req)
		return rec.Code
	}

	cases := []struct {
		name   string
		gate   http.HandlerFunc
		cookie string
		want   int
	}{
		{"write/viewer", writeGate, viewerC, http.StatusForbidden},
		{"write/editor", writeGate, editorC, http.StatusOK},
		{"write/admin", writeGate, adminC, http.StatusOK},

		{"admin/viewer", adminGate, viewerC, http.StatusForbidden},
		{"admin/editor", adminGate, editorC, http.StatusForbidden},
		{"admin/admin", adminGate, adminC, http.StatusOK},

		{"writeAnywhere/lone-viewer", writeAnywhere, viewerC, http.StatusForbidden},
		{"writeAnywhere/viewer-group-editor", writeAnywhere, viewerGroupEdC, http.StatusOK},
		{"writeAnywhere/admin", writeAnywhere, adminC, http.StatusOK},

		{"operator/non-operator-admin", operatorGate, adminC, http.StatusForbidden},
		{"operator/operator", operatorGate, operatorC, http.StatusOK},

		{"unauth/no-cookie", writeGate, "", http.StatusUnauthorized},
		{"unauth/operator-no-cookie", operatorGate, "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := run(tc.gate, tc.cookie); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
