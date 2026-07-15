// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// CreatePolicy duplicate rejection against a real Postgres: an exact
// re-post of an existing policy returns ErrPolicyExists (409 at the
// API), while near-misses — different signal narrowing, different
// group — still insert. Companion to the unit matrix in
// policies_dedup_test.go, which pins the field-by-field comparator.
//
// Run with:
//
//	go test -tags integration ./services/cell-api/internal/identity/...
package identity_test

import (
	"errors"
	"testing"

	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
)

func TestCreatePolicyRejectsExactDuplicate(t *testing.T) {
	f := newAuthzFixture(t)
	owner := f.user("dup@acme", identity.RoleViewer)
	payments := f.group("payments", owner, identity.RoleEditor)
	checkout := f.group("checkout", owner, identity.RoleEditor)

	svc := identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "orders-api"}
	if _, err := f.store.CreatePolicy(f.ctx, payments, svc); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Byte-identical re-post → ErrPolicyExists.
	if _, err := f.store.CreatePolicy(f.ctx, payments, svc); !errors.Is(err, identity.ErrPolicyExists) {
		t.Fatalf("duplicate create: got %v, want ErrPolicyExists", err)
	}

	// Same shape, narrowed to one signal → a different policy, allowed.
	narrowed := svc
	narrowed.Signals = []identity.Signal{identity.SignalTraces, identity.SignalLogs}
	if _, err := f.store.CreatePolicy(f.ctx, payments, narrowed); err != nil {
		t.Fatalf("signal-narrowed create: %v", err)
	}

	// Same signal SET in a different order → duplicate.
	reordered := svc
	reordered.Signals = []identity.Signal{identity.SignalLogs, identity.SignalTraces}
	if _, err := f.store.CreatePolicy(f.ctx, payments, reordered); !errors.Is(err, identity.ErrPolicyExists) {
		t.Fatalf("signal-reordered duplicate: got %v, want ErrPolicyExists", err)
	}

	// The identical policy on ANOTHER group is not a duplicate.
	if _, err := f.store.CreatePolicy(f.ctx, checkout, svc); err != nil {
		t.Fatalf("same policy on other group: %v", err)
	}

	// Attribute policies compare by map content, not insertion order.
	attrs := identity.AccessPolicyInput{
		Kind:           identity.PolicyAttributes,
		AttributeMatch: map[string]string{"env": "prod", "team": "orders"},
	}
	if _, err := f.store.CreatePolicy(f.ctx, payments, attrs); err != nil {
		t.Fatalf("attributes create: %v", err)
	}
	attrsAgain := identity.AccessPolicyInput{
		Kind:           identity.PolicyAttributes,
		AttributeMatch: map[string]string{"team": "orders", "env": "prod"},
	}
	if _, err := f.store.CreatePolicy(f.ctx, payments, attrsAgain); !errors.Is(err, identity.ErrPolicyExists) {
		t.Fatalf("attributes duplicate: got %v, want ErrPolicyExists", err)
	}

	// Expression policies compare by tree content.
	expr := identity.AccessPolicyInput{
		Kind: identity.PolicyExpression,
		Conditions: &identity.PolicyExpr{Op: "and", Children: []identity.PolicyExpr{
			{Match: identity.MatchPrefix, Value: "ABC"},
			{Attr: "env", Match: identity.MatchEquals, Value: "prod"},
		}},
	}
	if _, err := f.store.CreatePolicy(f.ctx, payments, expr); err != nil {
		t.Fatalf("expression create: %v", err)
	}
	if _, err := f.store.CreatePolicy(f.ctx, payments, expr); !errors.Is(err, identity.ErrPolicyExists) {
		t.Fatalf("expression duplicate: got %v, want ErrPolicyExists", err)
	}

	// The rejected duplicates must not have left rows behind:
	// payments carries exactly {svc, narrowed, attrs, expr}.
	policies, err := f.store.ListPoliciesForGroup(f.ctx, payments)
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	if len(policies) != 4 {
		t.Fatalf("payments has %d policies, want 4", len(policies))
	}
}
