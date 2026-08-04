// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Cloning must never hand the caller something they could not have made
// themselves. These tests pin that as a rule rather than trusting the
// SQL to keep expressing it.

package integrations

import "testing"

func TestPlanCloneNeverCarriesGroupAccessForAnEditor(t *testing.T) {
	// Granting a group access to an integration is an ADMIN action. An
	// editor may create integrations, so without this a clone would be a
	// sideways route to an admin-only grant.
	if PlanClone(false).GroupAccess {
		t.Error("an editor's clone reproduced group access policies")
	}
}

func TestPlanCloneCarriesGroupAccessForAnAdmin(t *testing.T) {
	// An admin can set these by hand on the new integration a second
	// later, so withholding them buys no safety and loses the scoping
	// the clone is supposed to preserve.
	if !PlanClone(true).GroupAccess {
		t.Error("an admin's clone dropped group access policies")
	}
}

func TestPlanCloneNeverPublishesABadge(t *testing.T) {
	// badge_public serves an UNAUTHENTICATED endpoint. No authority makes
	// it acceptable to publish a second public URL as a side effect of
	// pressing Clone.
	for _, admin := range []bool{false, true} {
		if PlanClone(admin).PublicBadge {
			t.Errorf("clone published a public badge (admin=%v)", admin)
		}
	}
}

func TestPlanCloneCarriesHealthChecks(t *testing.T) {
	// The checks define the integration's healthy state and need no
	// authority beyond creating the integration, so a clone without them
	// is not a copy of the thing the user was looking at.
	for _, admin := range []bool{false, true} {
		if !PlanClone(admin).HealthChecks {
			t.Errorf("clone dropped health checks (admin=%v)", admin)
		}
	}
}

func TestCloneRejectsAnEmptyIdentity(t *testing.T) {
	// Guarded before any transaction opens; a nil Store is enough to
	// prove the check runs first and cannot be reached with a blank name.
	var s *Store
	for _, opt := range []CloneOptions{
		{Name: "", Slug: "s"},
		{Name: "n", Slug: ""},
		{Name: "   ", Slug: "s"},
	} {
		if _, err := s.Clone(nil, [16]byte{}, [16]byte{}, opt); err == nil {
			t.Errorf("accepted %+v", opt)
		} else if !IsValidationError(err) {
			t.Errorf("%+v: want a validation error (400), got %T", opt, err)
		}
	}
}
