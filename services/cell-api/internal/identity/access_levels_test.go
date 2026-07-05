// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Access-level test framework (issue #31, layer 1).
//
// Purpose: lock down exactly what each user level is allowed to do, so a
// refactor can't silently widen access (e.g. let a viewer mutate, or
// grant a group-scoped user org-wide reach). This layer covers the
// PURE authorization decisions that don't need a database:
//   - org role capabilities (viewer / editor / admin),
//   - group access-policy shape validation (per PolicyKind),
//   - the strict-isolation rule (no policies => no access).
//
// Layer 2 (separate, follow-up) is HTTP-integration tests that hit real
// endpoints as each user level and assert visibility/status — that needs
// a test Postgres+ClickHouse harness, tracked on #31.
package identity

import "testing"

// TestRoleCapabilities is the authoritative matrix of what each org role
// can do. If you change a role's powers, you change this table — and the
// review will show exactly which capability moved.
func TestRoleCapabilities(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role               Role
		valid              bool
		canWrite, canAdmin bool
	}{
		{RoleViewer, true, false, false}, // read-only
		{RoleEditor, true, true, false},  // mutate, but not org admin
		{RoleAdmin, true, true, true},    // everything
		{Role("owner"), false, false, false},
		{Role(""), false, false, false},
		{Role("Admin"), false, false, false}, // case-sensitive: not valid
	}
	for _, c := range cases {
		t.Run(string(c.role), func(t *testing.T) {
			if got := c.role.IsValid(); got != c.valid {
				t.Errorf("IsValid()=%v want %v", got, c.valid)
			}
			if got := c.role.CanWrite(); got != c.canWrite {
				t.Errorf("CanWrite()=%v want %v", got, c.canWrite)
			}
			if got := c.role.CanAdmin(); got != c.canAdmin {
				t.Errorf("CanAdmin()=%v want %v", got, c.canAdmin)
			}
		})
	}
}

// TestRoleInvariants pins the relationships between the predicates so the
// hierarchy (admin ⊃ editor-write, only admin admins) can't drift.
func TestRoleInvariants(t *testing.T) {
	t.Parallel()
	for _, r := range []Role{RoleViewer, RoleEditor, RoleAdmin} {
		// Anyone who can admin must also be able to write.
		if r.CanAdmin() && !r.CanWrite() {
			t.Errorf("%s: CanAdmin but not CanWrite — admin must imply write", r)
		}
		// Viewer must never write or admin.
		if r == RoleViewer && (r.CanWrite() || r.CanAdmin()) {
			t.Errorf("viewer must be read-only, got write=%v admin=%v", r.CanWrite(), r.CanAdmin())
		}
	}
	// Exactly one role can admin.
	admins := 0
	for _, r := range []Role{RoleViewer, RoleEditor, RoleAdmin} {
		if r.CanAdmin() {
			admins++
		}
	}
	if admins != 1 {
		t.Errorf("expected exactly one admin-capable role, got %d", admins)
	}
}

// TestValidatePolicyInput covers the group access-policy shapes — the
// config that drives per-group visibility of metrics/logs/traces. Each
// PolicyKind requires its own target and forbids the others; a malformed
// policy must be rejected before it can grant unintended access.
func TestValidatePolicyInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   AccessPolicyInput
		ok   bool
	}{
		{"service ok", AccessPolicyInput{Kind: PolicyService, TargetServiceName: "orders-api"}, true},
		{"service missing target", AccessPolicyInput{Kind: PolicyService}, false},
		{"service with stray integration", AccessPolicyInput{Kind: PolicyService, TargetServiceName: "x", TargetIntegrationID: "i"}, false},
		{"service with stray attrs", AccessPolicyInput{Kind: PolicyService, TargetServiceName: "x", AttributeMatch: map[string]string{"k": "v"}}, false},

		{"integration ok", AccessPolicyInput{Kind: PolicyIntegration, TargetIntegrationID: "00000000-0000-0000-0000-000000000001"}, true},
		{"integration missing target", AccessPolicyInput{Kind: PolicyIntegration}, false},
		{"integration with stray service", AccessPolicyInput{Kind: PolicyIntegration, TargetIntegrationID: "i", TargetServiceName: "x"}, false},

		{"attributes ok", AccessPolicyInput{Kind: PolicyAttributes, AttributeMatch: map[string]string{"team": "payments"}}, true},
		{"attributes empty", AccessPolicyInput{Kind: PolicyAttributes}, false},
		{"attributes with stray service", AccessPolicyInput{Kind: PolicyAttributes, AttributeMatch: map[string]string{"k": "v"}, TargetServiceName: "x"}, false},

		{"compound ok", AccessPolicyInput{Kind: PolicyCompound, TargetServiceName: "x", AttributeMatch: map[string]string{"k": "v"}}, true},
		{"compound missing attrs", AccessPolicyInput{Kind: PolicyCompound, TargetServiceName: "x"}, false},
		{"compound missing target", AccessPolicyInput{Kind: PolicyCompound, AttributeMatch: map[string]string{"k": "v"}}, false},

		{"all_org ok", AccessPolicyInput{Kind: PolicyAllOrg}, true},
		{"all_org with stray service", AccessPolicyInput{Kind: PolicyAllOrg, TargetServiceName: "x"}, false},

		{"unknown kind", AccessPolicyInput{Kind: PolicyKind("everything")}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in // validatePolicyInput mutates (trims) its arg
			err := validatePolicyInput(&in)
			if c.ok && err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
			if !c.ok && err == nil {
				t.Errorf("expected rejection, got nil error")
			}
		})
	}
}

// TestEffectiveAccessHasNoAccess pins the strict-isolation rule: a user
// with no policies sees nothing. Any single grant (org-wide, a service,
// an attribute predicate, or a compound) lifts them out of no-access.
func TestEffectiveAccessHasNoAccess(t *testing.T) {
	t.Parallel()
	if !(EffectiveAccess{}).HasNoAccess() {
		t.Error("empty EffectiveAccess should be no-access")
	}
	cases := []struct {
		name string
		ea   EffectiveAccess
	}{
		{"all_org", EffectiveAccess{AllOrg: true}},
		{"one service", EffectiveAccess{Services: map[string]struct{}{"x": {}}}},
		{"attr predicate", EffectiveAccess{AttributePredicates: []map[string]string{{"k": "v"}}}},
		{"compound", EffectiveAccess{CompoundPredicates: []CompoundPredicate{{Services: []string{"x"}}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.ea.HasNoAccess() {
				t.Errorf("%s grant should NOT be no-access", c.name)
			}
		})
	}
}
