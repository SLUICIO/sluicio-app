// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// One org's identity provider must not sign in as another org's user.
//
// An IdP asserts whatever email address it likes, and on a cell with
// several organizations every org admin can configure one. The callback
// used to link an SSO sign-in to any existing account with the asserted
// email, cell-wide: org B's IdP saying "alice@customer-a.example" got a
// session as Alice, with every role she holds, an operator's included.
// This drives resolveSSOUser through that reproduction against a real
// Postgres: a provider may only link to, or create, accounts in the
// domains assigned to it, except on a cell with a single org.
package api

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

func TestSSOProviderCannotReachAnotherOrgsAccounts(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	h := &Handlers{Identity: idStore, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	orgA, err := idStore.CreateOrg(ctx, "Customer A", "customer-a")
	if err != nil {
		t.Fatal(err)
	}
	orgB, err := idStore.CreateOrg(ctx, "Customer B", "customer-b")
	if err != nil {
		t.Fatal(err)
	}
	alice, err := idStore.CreateUser(ctx, "alice@customer-a.example", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := idStore.AddMember(ctx, alice.ID, orgA.ID, identity.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	newProvider := func(org uuid.UUID, name string, domains ...string) identity.AuthProvider {
		t.Helper()
		p, err := idStore.CreateAuthProvider(ctx, identity.AuthProvider{OrgID: org, Name: name, Kind: "oidc", IssuerURL: "https://" + name + ".example", ClientID: name, JITProvisioning: true, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(domains) > 0 {
			if _, err := idStore.SetProviderDomains(ctx, p.ID, domains); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	provA := newProvider(orgA.ID, "idp-a", "customer-a.example")
	provB := newProvider(orgB.ID, "idp-b", "customer-b.example")

	refused := func(err error) bool {
		var se *ssoError
		return errors.As(err, &se)
	}

	// The reproduction: B's IdP asserts Alice's address.
	if uid, err := h.resolveSSOUser(ctx, provB, "attacker-sub", "alice@customer-a.example", "Alice"); !refused(err) {
		t.Fatalf("B's provider resolved Alice's address to %s (err %v); want refused", uid, err)
	}
	// Nor may it create an account in A's domain, which A's provider would
	// then link to.
	if _, err := h.resolveSSOUser(ctx, provB, "attacker-sub-2", "carol@customer-a.example", "Carol"); !refused(err) {
		t.Error("B's provider created an account in A's domain")
	}
	if _, err := idStore.GetUserByEmail(ctx, "carol@customer-a.example"); err == nil {
		t.Error("the refused sign-in left an account behind")
	}
	// A partner's consultant who is a member of B, but whose domain B does
	// not hold, is not B's to vouch for either.
	if err := idStore.AddMember(ctx, alice.ID, orgB.ID, identity.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err := h.resolveSSOUser(ctx, provB, "attacker-sub-3", "alice@customer-a.example", "Alice"); !refused(err) {
		t.Error("membership in B let B's provider reach Alice's account")
	}

	// Inside their domains, providers work as before.
	if uid, err := h.resolveSSOUser(ctx, provA, "alice-sub", "alice@customer-a.example", "Alice"); err != nil || uid != alice.ID {
		t.Errorf("A's provider for Alice = %s, %v; want Alice's account", uid, err)
	}
	if uid, err := h.resolveSSOUser(ctx, provB, "dave-sub", "dave@customer-b.example", "Dave"); err != nil || uid == uuid.Nil {
		t.Errorf("B's provider provisioning in its own domain = %s, %v", uid, err)
	}
	// No verified email: nothing to match on.
	if _, err := h.resolveSSOUser(ctx, provA, "nobody-sub", "", ""); !refused(err) {
		t.Error("a sign-in without an email was matched")
	}

	// The rule itself, including the single-org cell, where the only IdP
	// belongs to the only org and keeps linking by email without domains.
	for _, c := range []struct {
		orgs    int
		domains []string
		email   string
		want    bool
	}{
		{2, []string{"customer-a.example"}, "alice@customer-a.example", true},
		{2, []string{"customer-a.example"}, "alice@Customer-A.example", true},
		{2, []string{"customer-b.example"}, "alice@customer-a.example", false},
		{2, nil, "alice@customer-a.example", false},
		{2, []string{"customer-a.example"}, "alice@sub.customer-a.example", false},
		{1, nil, "alice@customer-a.example", true},
		{1, nil, "no-domain", false},
	} {
		if got := ssoSpeaksForEmail(c.orgs, c.domains, c.email); got != c.want {
			t.Errorf("ssoSpeaksForEmail(%d orgs, %v, %q) = %v, want %v", c.orgs, c.domains, c.email, got, c.want)
		}
	}
}
