// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package eventsubs

import "testing"

// TestMatchesFilter pins the three filter forms — exact, trailing-*
// prefix, bare * — and that near-misses stay misses.
func TestMatchesFilter(t *testing.T) {
	cases := []struct {
		typ, filter string
		want        bool
	}{
		{"com.sluicio.integration.created", "com.sluicio.integration.created", true},
		{"com.sluicio.integration.created", "com.sluicio.integration.*", true},
		{"com.sluicio.integration.created", "com.sluicio.*", true},
		{"com.sluicio.integration.created", "*", true},
		{"com.sluicio.integration.created", "com.sluicio.service.*", false},
		{"com.sluicio.integration.created", "com.sluicio.integration.deleted", false},
		{"com.sluicio.integration.created", "", false},
		// A prefix glob must not match a shorter type.
		{"com.sluicio.alert", "com.sluicio.alert.*", false},
		{"com.sluicio.alert.fired", "com.sluicio.alert.*", true},
	}
	for _, c := range cases {
		if got := MatchesFilter(c.typ, c.filter); got != c.want {
			t.Errorf("MatchesFilter(%q, %q) = %v, want %v", c.typ, c.filter, got, c.want)
		}
	}
	if !MatchesAny("com.sluicio.alert.fired", []string{"com.sluicio.integration.*", "com.sluicio.alert.*"}) {
		t.Error("MatchesAny should hit the second filter")
	}
	if MatchesAny("com.sluicio.alert.fired", nil) {
		t.Error("no filters must match nothing")
	}
}
