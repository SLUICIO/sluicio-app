// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Who owns a managed instance.
//
// A self-hosted install's first user is its OPERATOR: a role above org
// admin that holds the instance-wide settings. On an instance run by a
// platform on behalf of someone else those settings belong to the
// platform, so nobody signed in is operator and the first user is an org
// admin instead.
//
// These run against real Postgres because every one of them is a question
// about rows: which user exists, who is operator, what the claim did.
//
//	go test -tags integration ./services/cell-api/internal/identity/...

package identity_test

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

func TestAManagedInstanceGetsNoOperator(t *testing.T) {
	f := newAuthzFixture(t)
	f.store.SetManaged(true)

	// The startup promotion must do nothing at all.
	promoted, err := f.store.EnsureBootstrapOperator(f.ctx, "admin@sluicio.local")
	if err != nil {
		t.Fatalf("ensure bootstrap operator: %v", err)
	}
	if promoted != "" {
		t.Errorf("managed mode promoted %q to operator", promoted)
	}
	if n, err := f.store.CountOperators(f.ctx); err != nil || n != 0 {
		t.Errorf("operators = %d (err %v), want none on a managed instance", n, err)
	}

	// And no other path may create one either: a script, an API token, a
	// later migration. Refusing in the store is what makes that true.
	u := f.user("claimer@acme", identity.RoleAdmin)
	if err := f.store.SetUserOperator(f.ctx, u, true); err == nil {
		t.Error("SetUserOperator succeeded on a managed instance")
	} else if !errorsIs(err, identity.ErrOperatorManaged) {
		t.Errorf("SetUserOperator: got %v, want ErrOperatorManaged", err)
	}
	if n, _ := f.store.CountOperators(f.ctx); n != 0 {
		t.Errorf("operators = %d after a refused promotion, want none", n)
	}
}

func TestSelfHostedStillPromotesItsFirstUser(t *testing.T) {
	// The other half of the same behaviour: nothing about an unmanaged
	// install changes. The seeded admin still becomes operator, and can
	// still be promoted or demoted by hand.
	f := newAuthzFixture(t)

	u := f.user("seeded@acme", identity.RoleAdmin)
	promoted, err := f.store.EnsureBootstrapOperator(f.ctx, "seeded@acme")
	if err != nil {
		t.Fatalf("ensure bootstrap operator: %v", err)
	}
	if promoted != "seeded@acme" {
		t.Fatalf("a self-hosted install promoted %q, want the seeded user", promoted)
	}
	if n, _ := f.store.CountOperators(f.ctx); n != 1 {
		t.Errorf("operators = %d, want 1", n)
	}
	if err := f.store.SetUserOperator(f.ctx, u, false); err != nil {
		t.Errorf("demoting on a self-hosted install failed: %v", err)
	}
}

// errorsIs is errors.Is, imported locally to keep the import list of this
// file to the package under test.
func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
