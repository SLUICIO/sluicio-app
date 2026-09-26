// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Claiming a fresh instance, over HTTP, against real Postgres.
//
// The unit test beside this one covers the token comparison. This covers
// what the endpoint DOES with the answer, which is the part that decides
// whether an instance can be taken: the status codes, the sealing after a
// successful claim, and who the claiming user ends up being.
//
// The last of those is the reason this needs a database. On a managed
// instance - one run by a platform on behalf of someone else - the
// claiming user must come out an ADMIN OF THE ORG and NOT the instance
// operator, and both of those are rows.
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
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// claimFixture is a fresh instance with one seeded, password-less user,
// which is the state a real one boots into: migration-seeded row, nobody
// signed in.
type claimFixture struct {
	t    *testing.T
	mux  *http.ServeMux
	id   *identity.Store
	seed identity.User
	org  identity.Org
}

func newClaimFixture(t *testing.T, managed bool, token string) *claimFixture {
	t.Helper()
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	idStore.SetManaged(managed)

	h := &Handlers{
		Identity:       idStore,
		Managed:        managed,
		BootstrapToken: token,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/bootstrap-admin", h.bootstrapAdmin)
	mux.HandleFunc("GET /api/v1/auth/install-state", h.installState)

	// No fixture rows: the migrations already seed the default org and the
	// password-less admin, which IS the state a fresh instance boots into.
	// Seeding a second one here was the first version of this fixture and
	// it tested the wrong thing - the claim targets the OLDEST user and the
	// OLDEST org, so a fixture that adds its own ends up asserting against
	// rows the claim never touched.
	seed, err := idStore.GetUserByEmail(ctx, "admin@sluicio.local")
	if err != nil {
		t.Fatalf("the migrations did not seed admin@sluicio.local: %v", err)
	}
	orgs, err := idStore.ListOrgs(ctx)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("no seeded org: %v", err)
	}
	org := orgs[0].Org

	// Startup, in each mode: the seeded admin becomes operator on a
	// self-hosted install and nobody does on a managed one.
	promoted, err := idStore.EnsureBootstrapOperator(ctx, "admin@sluicio.local")
	if err != nil {
		t.Fatalf("bootstrap operator: %v", err)
	}
	if managed && promoted != "" {
		t.Fatalf("managed startup promoted %q to operator", promoted)
	}
	if !managed && promoted == "" {
		t.Fatal("self-hosted startup promoted nobody to operator")
	}
	return &claimFixture{t: t, mux: mux, id: idStore, seed: seed, org: org}
}

// claim posts a bootstrap request and returns the status code.
func (f *claimFixture) claim(email, token string) int {
	f.t.Helper()
	body, _ := json.Marshal(map[string]string{
		"name": "Ada Lovelace", "email": email, "password": "correct horse battery",
		"token": token,
	})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/v1/auth/bootstrap-admin", bytes.NewReader(body)))
	return rec.Code
}

func (f *claimFixture) installStateManaged() bool {
	f.t.Helper()
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/install-state", nil))
	var out struct {
		Fresh   bool `json:"fresh"`
		Managed bool `json:"managed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		f.t.Fatalf("install-state: %v", err)
	}
	return out.Managed
}

func TestAManagedInstanceIsClaimedOnlyWithItsToken(t *testing.T) {
	const token = "the-one-true-token"
	f := newClaimFixture(t, true, token)

	if !f.installStateManaged() {
		t.Error("install-state does not report a managed instance as managed")
	}

	if got := f.claim("wrong@acme.test", "not-the-token"); got != http.StatusForbidden {
		t.Errorf("wrong token: got %d, want 403", got)
	}
	if got := f.claim("missing@acme.test", ""); got != http.StatusForbidden {
		t.Errorf("missing token: got %d, want 403", got)
	}
	// A refused claim must leave the instance claimable: otherwise one
	// wrong guess locks the owner out of their own instance.
	if got := f.claim("owner@acme.test", token); got != http.StatusNoContent {
		t.Fatalf("right token after two refusals: got %d, want 204", got)
	}

	// Who they became. On a managed instance: org admin, not operator.
	ctx := t.Context()
	if n, err := f.id.CountOperators(ctx); err != nil || n != 0 {
		t.Errorf("operators after the claim = %d (err %v), want none", n, err)
	}
	claimed, err := f.id.GetUserByEmail(ctx, "owner@acme.test")
	if err != nil {
		t.Fatalf("the claiming user does not exist: %v", err)
	}
	if claimed.IsOperator {
		t.Error("the claiming user is the instance operator on a managed instance")
	}
	role, err := f.id.GetMembership(ctx, claimed.ID, f.org.ID)
	if err != nil {
		t.Fatalf("membership: %v", err)
	}
	if role != identity.RoleAdmin {
		t.Errorf("the claiming user is %q of the org, want admin", role)
	}
}

func TestAClaimedInstanceStaysClaimed(t *testing.T) {
	const token = "the-one-true-token"
	f := newClaimFixture(t, true, token)

	if got := f.claim("owner@acme.test", token); got != http.StatusNoContent {
		t.Fatalf("first claim: got %d, want 204", got)
	}
	// Signing in is what seals it in production; the endpoint's own
	// freshness check is what this asserts.
	if err := f.id.TouchLastLogin(t.Context(), mustUser(t, f, "owner@acme.test").ID); err != nil {
		t.Fatalf("touch last login: %v", err)
	}
	if got := f.claim("second@acme.test", token); got != http.StatusConflict {
		t.Errorf("second claim with the right token: got %d, want 409", got)
	}
}

func TestAManagedInstanceWithNoTokenCannotBeClaimedAtAll(t *testing.T) {
	// The deployment mistake: managed mode on, no token configured. Every
	// claim is refused, which is the safe end of the trade - the
	// alternative hands the instance to whoever finds the address.
	f := newClaimFixture(t, true, "")
	if got := f.claim("anyone@acme.test", ""); got != http.StatusForbidden {
		t.Errorf("no token configured, none presented: got %d, want 403", got)
	}
	if got := f.claim("anyone@acme.test", "guess"); got != http.StatusForbidden {
		t.Errorf("no token configured, one guessed: got %d, want 403", got)
	}
}

func TestASelfHostedInstanceIsClaimedWithoutAToken(t *testing.T) {
	// The behaviour that must not change. Whoever reaches the first-run
	// screen just installed the thing, and the seeded admin is the
	// instance's operator as it always was.
	f := newClaimFixture(t, false, "")

	if f.installStateManaged() {
		t.Error("install-state reports a self-hosted install as managed")
	}
	if got := f.claim("owner@acme.test", ""); got != http.StatusNoContent {
		t.Fatalf("self-hosted claim with no token: got %d, want 204", got)
	}
	claimed := mustUser(t, f, "owner@acme.test")
	if !claimed.IsOperator {
		t.Error("the claiming user is not the operator on a self-hosted install")
	}
}

func mustUser(t *testing.T, f *claimFixture, email string) identity.User {
	t.Helper()
	u, err := f.id.GetUserByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("user %s: %v", email, err)
	}
	return u
}

// Leaving managed mode. The instance was claimed while managed, so it has
// no operator and its seed row now carries the claimant's address -
// which is exactly why promoting by the seed email is not enough on the
// way back.
//
// This is the sequence a real switch-over is: claim, then restart with
// SLUICIO_MANAGED removed.
func TestLeavingManagedModeGivesTheClaimantTheOperatorRole(t *testing.T) {
	const token = "the-one-true-token"
	f := newClaimFixture(t, true, token)

	if got := f.claim("owner@acme.test", token); got != http.StatusNoContent {
		t.Fatalf("claim: got %d, want 204", got)
	}
	ctx := t.Context()
	if n, _ := f.id.CountOperators(ctx); n != 0 {
		t.Fatalf("operators while managed = %d, want none", n)
	}

	// The restart: same database, managed mode off.
	f.id.SetManaged(false)
	promoted, err := f.id.EnsureBootstrapOperator(ctx, "admin@sluicio.local")
	if err != nil {
		t.Fatalf("bootstrap operator after leaving managed mode: %v", err)
	}
	if promoted != "owner@acme.test" {
		t.Fatalf("promoted %q, want the claimant - the seed email no longer matches any row", promoted)
	}
	claimed := mustUser(t, f, "owner@acme.test")
	if !claimed.IsOperator {
		t.Error("the claimant is not operator after leaving managed mode")
	}
	if n, _ := f.id.CountOperators(ctx); n != 1 {
		t.Errorf("operators = %d after the switch, want exactly 1", n)
	}

	// Idempotent: a second restart must not promote anybody else, so a
	// later demotion sticks.
	again, err := f.id.EnsureBootstrapOperator(ctx, "admin@sluicio.local")
	if err != nil {
		t.Fatalf("second restart: %v", err)
	}
	if again != "" {
		t.Errorf("a second restart promoted %q; the instance already had an operator", again)
	}
}

func TestLeavingManagedModeOnAnUnclaimedInstancePromotesTheSeed(t *testing.T) {
	// The ordinary path must keep working: never claimed, so the seed row
	// still has its own address and is promoted by email as it always was.
	f := newClaimFixture(t, true, "unused-token")
	f.id.SetManaged(false)

	promoted, err := f.id.EnsureBootstrapOperator(t.Context(), "admin@sluicio.local")
	if err != nil {
		t.Fatalf("bootstrap operator: %v", err)
	}
	if promoted != "admin@sluicio.local" {
		t.Errorf("promoted %q, want the seeded admin", promoted)
	}
}
