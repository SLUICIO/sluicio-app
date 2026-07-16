// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Regression tests for DefaultMembership — the header-less active-org
// default. The original bug: the auth middleware used memberships[0],
// and ListMemberships orders alphabetically by org name, so adding a
// user to an org that sorts BEFORE their original one (observed live:
// "CT Probe" vs "Default") silently flipped every header-less client
// they owned to the new org. The default must follow join order, not
// sort order.
package identity

import (
	"testing"
	"time"
)

func TestDefaultMembership(t *testing.T) {
	t0 := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

	// Helper: a membership as ListMemberships would return it.
	mk := func(name string, joined time.Time) Membership {
		return Membership{
			Org:      Org{Name: name, Slug: name},
			Role:     RoleAdmin,
			JoinedAt: joined,
		}
	}

	t.Run("oldest joined wins over alphabetical order", func(t *testing.T) {
		// Input is alphabetical (ListMemberships contract): the
		// hijacker org "CT Probe" sorts first but was joined later.
		ms := []Membership{
			mk("CT Probe", t0.Add(time.Hour)),
			mk("Default", t0),
		}
		got := DefaultMembership(ms)
		if got == nil || got.Org.Name != "Default" {
			t.Fatalf("DefaultMembership = %+v, want the oldest-joined org %q", got, "Default")
		}
	})

	t.Run("stable when a later-sorting org is joined later too", func(t *testing.T) {
		ms := []Membership{
			mk("Default", t0),
			mk("Zenith", t0.Add(time.Hour)),
		}
		if got := DefaultMembership(ms); got == nil || got.Org.Name != "Default" {
			t.Fatalf("DefaultMembership = %+v, want %q", got, "Default")
		}
	})

	t.Run("ties fall back to slice (alphabetical) order", func(t *testing.T) {
		// Batch-seeded memberships can share a joined_at; the default
		// must still be deterministic.
		ms := []Membership{
			mk("Alpha", t0),
			mk("Beta", t0),
		}
		if got := DefaultMembership(ms); got == nil || got.Org.Name != "Alpha" {
			t.Fatalf("DefaultMembership = %+v, want first-listed %q on a tie", got, "Alpha")
		}
	})

	t.Run("empty slice returns nil", func(t *testing.T) {
		if got := DefaultMembership(nil); got != nil {
			t.Fatalf("DefaultMembership(nil) = %+v, want nil", got)
		}
	})
}
