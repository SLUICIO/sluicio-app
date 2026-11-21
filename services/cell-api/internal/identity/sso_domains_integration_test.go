// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// The email domains an SSO provider speaks for: one provider per domain
// on the whole cell, so one org cannot take a domain another org's
// provider holds, and the login page routes an address to the provider
// that holds its domain.
//
// Run with:
//
//	go test -tags integration ./services/cell-api/internal/identity/...
package identity_test

import (
	"errors"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

func TestProviderDomainsAreOnePerCell(t *testing.T) {
	store, ctx := newMigratedStore(t)

	orgA, err := store.CreateOrg(ctx, "Customer A", "customer-a")
	if err != nil {
		t.Fatal(err)
	}
	orgB, err := store.CreateOrg(ctx, "Customer B", "customer-b")
	if err != nil {
		t.Fatal(err)
	}
	provA, err := store.CreateAuthProvider(ctx, identity.AuthProvider{OrgID: orgA.ID, Name: "A", Kind: "oidc", IssuerURL: "https://idp.a.example", ClientID: "a", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	provB, err := store.CreateAuthProvider(ctx, identity.AuthProvider{OrgID: orgB.ID, Name: "B", Kind: "oidc", IssuerURL: "https://idp.b.example", ClientID: "b", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.SetProviderDomains(ctx, provA.ID, []string{"@Customer-A.example", "customer-a.example", "a-subsidiary.example"})
	if err != nil {
		t.Fatalf("assign A's domains: %v", err)
	}
	if len(got) != 2 || got[0] != "a-subsidiary.example" || got[1] != "customer-a.example" {
		t.Errorf("A's domains = %v, want normalised, deduplicated and sorted", got)
	}

	// B cannot take a domain A holds, and a failed change leaves B as it was.
	if _, err := store.SetProviderDomains(ctx, provB.ID, []string{"customer-b.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProviderDomains(ctx, provB.ID, []string{"customer-b.example", "customer-a.example"}); !errors.Is(err, identity.ErrDomainTaken) {
		t.Fatalf("B claiming A's domain: err = %v, want ErrDomainTaken", err)
	}
	if d, _ := store.ProviderDomains(ctx, provB.ID); len(d) != 1 || d[0] != "customer-b.example" {
		t.Errorf("B's domains after the refused change = %v", d)
	}
	if _, err := store.SetProviderDomains(ctx, provB.ID, []string{"not a domain"}); !errors.Is(err, identity.ErrInvalidDomain) {
		t.Errorf("invalid domain: err = %v", err)
	}

	// Routing: an address goes to the provider holding its domain.
	p, ok, err := store.ProviderForEmail(ctx, "Alice@Customer-A.example")
	if err != nil || !ok || p.ID != provA.ID {
		t.Errorf("alice routes to %v ok=%v err=%v, want provider A", p.ID, ok, err)
	}
	if _, ok, _ := store.ProviderForEmail(ctx, "bob@elsewhere.example"); ok {
		t.Error("an unassigned domain routed to a provider")
	}

	// A disabled provider routes nowhere.
	provA.Enabled = false
	if _, err := store.UpdateAuthProvider(ctx, orgA.ID, provA); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.ProviderForEmail(ctx, "alice@customer-a.example"); ok {
		t.Error("a disabled provider still receives its domain's sign-ins")
	}

	// The admin list carries each provider's domains.
	list, err := store.ListAuthProviders(ctx, orgB.ID)
	if err != nil || len(list) != 1 || len(list[0].Domains) != 1 {
		t.Errorf("B's provider list = %+v, err %v", list, err)
	}

	n, err := store.CountOrgs(ctx)
	if err != nil || n < 3 {
		t.Errorf("CountOrgs = %d, %v; want the seeded org plus two", n, err)
	}
}
