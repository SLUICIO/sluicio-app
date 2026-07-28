// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The brief's value depends on two properties that are easy to lose
// quietly: it must stay small, and it must never become the one endpoint
// that reports past a visibility boundary the rest of the API enforces.
//
// The caps are the part a future change is most likely to break — adding
// a field is easy, noticing that the payload doubled is not — so they're
// pinned here, along with the ordering that puts critical incidents
// first.

package api

import (
	"sort"
	"testing"
	"time"
)

func TestSeverityRankOrdersWorstFirst(t *testing.T) {
	// The incident list is truncated, so ordering decides what an agent
	// actually sees. A critical dropped in favour of an info because of
	// insertion order would be the worst kind of bug here: silent, and
	// only visible when it matters.
	in := []string{"info", "critical", "warning", "info", "critical"}
	sort.SliceStable(in, func(i, j int) bool {
		return severityRank(in[i]) > severityRank(in[j])
	})
	want := []string{"critical", "critical", "warning", "info", "info"}
	for i := range want {
		if in[i] != want[i] {
			t.Fatalf("ordering %v, want %v", in, want)
		}
	}
}

func TestSeverityRankTreatsUnknownAsLowest(t *testing.T) {
	// An unrecognised severity must sort last rather than accidentally
	// outranking critical — a new severity added upstream should degrade
	// to "shown last", never to "shown first".
	if severityRank("catastrophic") >= severityRank("info") {
		t.Errorf("unknown severity outranks info; it must sort lowest")
	}
	if severityRank("") >= severityRank("info") {
		t.Errorf("empty severity outranks info; it must sort lowest")
	}
}

func TestBriefCapsStaySmall(t *testing.T) {
	// This is an orientation call an agent may make every turn. If the
	// caps drift upward the brief stops being cheap, and nobody notices
	// until context bills do.
	if briefMaxIncidents > 25 {
		t.Errorf("briefMaxIncidents = %d — the brief is meant to orient, not enumerate", briefMaxIncidents)
	}
	if briefMaxUnmonitored > 50 {
		t.Errorf("briefMaxUnmonitored = %d — cap it and set the truncated flag instead", briefMaxUnmonitored)
	}
}

func TestHumanWindowDropsZeroUnits(t *testing.T) {
	// "24h0m0s" is what Duration.String gives and it reads badly in a
	// payload a model will quote back at a human.
	for _, tc := range []struct{ in, want string }{
		{"24h", "24h"},
		{"1h", "1h"},
		{"168h", "168h"},
		{"90m", "1h30m"},
	} {
		d, err := time.ParseDuration(tc.in)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.in, err)
		}
		if got := humanWindow(d); got != tc.want {
			t.Errorf("humanWindow(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
