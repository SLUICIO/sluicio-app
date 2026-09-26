// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// What a managed instance refuses, and to whom.
//
// A managed instance has no operator: the instance-wide settings belong to
// the platform running it. Two consequences have to hold together, and
// only one of them is obvious.
//
// The obvious one: operator routes are unreachable. The other: a setting
// that is instance-wide but belongs to the PEOPLE using the instance must
// not become unreachable with them. Requiring two-factor for your own
// users is the org's decision, so it moves to an org admin rather than
// disappearing along with the operator.
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

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/settings"
)

// managedSettingsFixture is an instance with an org admin and a user whose
// row still says operator - the shape an instance switched INTO managed
// mode has.
type managedSettingsFixture struct {
	t     *testing.T
	mux   *http.ServeMux
	admin string // session cookie
	stale string // session cookie of the leftover operator
}

func newManagedSettingsFixture(t *testing.T, managed bool) *managedSettingsFixture {
	t.Helper()
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	idStore.SetManaged(managed)

	h := &Handlers{
		Identity: idStore,
		Settings: settings.NewStore(pool),
		Managed:  managed,
		AuthMW:   &middleware.Resolver{Identity: idStore},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	// The two routes under test, wired exactly as handlers.go wires them.
	mux.HandleFunc("PATCH /api/v1/cell-settings/retention", h.AuthMW.RequireOperator(h.patchRetention))
	mux.HandleFunc("GET /api/v1/cell-settings/security", h.operatorOrOrgAdmin(h.getSecuritySettings))

	orgs, err := idStore.ListOrgs(ctx)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("no seeded org: %v", err)
	}
	org := orgs[0].Org

	session := func(email string, role identity.Role, operator bool) string {
		t.Helper()
		u, err := idStore.CreateUser(ctx, email, email)
		if err != nil {
			t.Fatalf("create %s: %v", email, err)
		}
		if err := idStore.AddMember(ctx, u.ID, org.ID, role); err != nil {
			t.Fatalf("add member: %v", err)
		}
		if operator {
			// Written directly: SetUserOperator refuses while managed, which
			// is the point - this is a row that predates the switch.
			if _, err := pool.Exec(ctx,
				`UPDATE users SET is_operator = true WHERE id = $1`, u.ID); err != nil {
				t.Fatalf("stale operator flag: %v", err)
			}
		}
		sess, err := idStore.CreateSession(ctx, u.ID, time.Hour, "test")
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		return sess.ID
	}

	return &managedSettingsFixture{
		t:     t,
		mux:   mux,
		admin: session("admin@acme.test", identity.RoleAdmin, false),
		stale: session("was-operator@acme.test", identity.RoleAdmin, true),
	}
}

func (f *managedSettingsFixture) do(method, path, cookie, body string) int {
	f.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec.Code
}

func TestAManagedInstanceRefusesOperatorRoutesToALeftoverOperator(t *testing.T) {
	f := newManagedSettingsFixture(t, true)

	// The row says operator; managed mode says the instance has none.
	if got := f.do(http.MethodPatch, "/api/v1/cell-settings/retention", f.stale,
		`{"traces_days":30}`); got != http.StatusForbidden {
		t.Errorf("leftover operator patching retention: got %d, want 403", got)
	}
	if got := f.do(http.MethodPatch, "/api/v1/cell-settings/retention", f.admin,
		`{"traces_days":30}`); got != http.StatusForbidden {
		t.Errorf("org admin patching retention: got %d, want 403", got)
	}
}

func TestAManagedInstanceGivesTheMFAPolicyToTheOrgAdmin(t *testing.T) {
	f := newManagedSettingsFixture(t, true)
	// Not 403: with no operator, an operator-only MFA policy would be a
	// setting nobody in the instance could ever reach.
	if got := f.do(http.MethodGet, "/api/v1/cell-settings/security", f.admin, ""); got != http.StatusOK {
		t.Errorf("org admin reading the security policy on a managed instance: got %d, want 200", got)
	}
}

func TestASelfHostedInstanceKeepsBothWithTheOperator(t *testing.T) {
	// Unchanged: the operator holds retention and the security policy, and
	// an org admin holds neither.
	f := newManagedSettingsFixture(t, false)

	if got := f.do(http.MethodGet, "/api/v1/cell-settings/security", f.stale, ""); got != http.StatusOK {
		t.Errorf("operator reading the security policy: got %d, want 200", got)
	}
	if got := f.do(http.MethodGet, "/api/v1/cell-settings/security", f.admin, ""); got != http.StatusForbidden {
		t.Errorf("org admin reading the security policy on a self-hosted install: got %d, want 403", got)
	}
}
