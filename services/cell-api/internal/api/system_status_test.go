// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A system's rolled-up status.
//
// The reported symptom: three systems whose health was defined entirely
// by their own checks — no member services, because one service emits the
// telemetry of all three and is told apart by attribute — all read
// "Quiet". Quiet means nothing is watching. These were watched, and
// passing. Reporting a monitored thing as unmonitored is the failure mode
// worth a test: it reads as "no data yet" and gets ignored.

package api

import "testing"

// Calls the REAL rollup rather than restating it. An earlier draft of
// this file copied the logic, which would have gone on passing after the
// handler changed — a test that agrees with itself instead of with the
// code.

func TestSystemStatusReflectsItsOwnChecks(t *testing.T) {
	cases := []struct {
		name    string
		members []string
		checks  bool
		firing  bool
		want    string
	}{
		// The reported bug.
		{"no members, passing checks", nil, true, false, "ok"},
		{"no members, firing check", nil, true, true, "unhealthy"},
		// Genuinely unwatched: no members AND no checks. Still quiet —
		// widening "quiet" out of existence would be the opposite error.
		{"no members, no checks", nil, false, false, "quiet"},
		// Members still drive the rollup; checks do not paper over them.
		{"unhealthy member, passing checks", []string{"unhealthy"}, true, false, "unhealthy"},
		{"errored member, passing checks", []string{"errors"}, true, false, "errors"},
		{"healthy members, no checks", []string{"ok", "ok"}, false, false, "ok"},
		// A firing system check outranks healthy members: that is the
		// point of binding a check to the cluster rather than a broker.
		{"healthy members, firing system check", []string{"ok"}, true, true, "unhealthy"},
	}
	for _, c := range cases {
		if got := systemRollupStatus(c.members, c.checks, c.firing); got != c.want {
			t.Errorf("%s: status = %q, want %q", c.name, got, c.want)
		}
	}
}
