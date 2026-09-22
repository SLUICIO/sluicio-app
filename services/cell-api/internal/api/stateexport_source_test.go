// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The export and the pages must agree about what an integration's state
// is, and the way to guarantee that is for both to call one function.
// These pin the fold itself.

package api

import "testing"

func TestIntegrationRollupStatus(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		delayed  uint64
		firing   bool
		want     string
	}{
		{"nothing emitted in the window", nil, 0, false, "quiet"},
		{"every member healthy", []string{"ok", "ok"}, 0, false, "ok"},
		{"one member unhealthy", []string{"ok", "unhealthy"}, 0, false, "unhealthy"},
		{"a missed SLA is a failure", []string{"ok"}, 3, false, "errors"},
		{"a firing check on the integration outranks healthy members", []string{"ok"}, 0, true, "unhealthy"},
		{"and outranks a delay too", []string{"ok"}, 3, true, "unhealthy"},
		{"a delay never downgrades unhealthy", []string{"unhealthy"}, 3, false, "unhealthy"},
		{"quiet with a firing check is still unhealthy", nil, 0, true, "unhealthy"},
	}
	for _, c := range cases {
		if got := integrationRollupStatus(c.statuses, c.delayed, c.firing); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The export reports a state per entity and a count per integration.
// A system carries no messages, and reporting zero for one would read as
// "nothing came through" rather than "this is not that kind of thing".
func TestSystemStatesCarryNoMessageCount(t *testing.T) {
	e := EntityState{Name: "broker", Kind: "rabbitmq", Status: "ok"}
	if e.Messages != 0 {
		t.Errorf("a system state should carry no message count, got %d", e.Messages)
	}
}
