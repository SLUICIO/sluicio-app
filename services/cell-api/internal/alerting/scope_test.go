// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Scope resolution for health checks (issue #13).
//
// The schema has permitted a rule to carry several scopes since 0009, and
// 0077 did not change that — a CHECK constraint added retroactively could
// have failed on live rows. So the exclusivity that actually matters is
// enforced in two softer places: the API rejects an ambiguous NEW rule,
// and everything downstream agrees on one precedence order.
//
// These pin the second half. The danger is not a crash: a rule that
// evaluates over a different scope than the one it claims to be bound to
// produces confident, wrong numbers — the same class of defect as an
// integration reporting its service's failures.

package alerting

import (
	"testing"

	"github.com/google/uuid"
)

func TestRuleHasScopeAcceptsASystem(t *testing.T) {
	sys := uuid.New()
	integ := uuid.New()

	cases := []struct {
		name string
		rule AlertRule
		want bool
	}{
		{"unbound", AlertRule{}, false},
		{"service", AlertRule{ServiceName: "svc"}, true},
		{"integration", AlertRule{IntegrationID: &integ}, true},
		// The case this test exists for: before 0077 a system-bound trace
		// rule looked unbound, so the evaluator skipped it silently and the
		// check simply never fired. Nothing errored; it just did nothing.
		{"system", AlertRule{SystemID: &sys}, true},
	}
	for _, c := range cases {
		if got := ruleHasScope(c.rule); got != c.want {
			t.Errorf("%s: ruleHasScope = %v, want %v", c.name, got, c.want)
		}
	}
}

// The trace evaluators refuse to run unbound, because "how many traces
// failed anywhere?" is not a health check and would scan the whole cell.
// That guard has to keep refusing.
func TestUnboundTraceRuleIsStillRefused(t *testing.T) {
	if ruleHasScope(AlertRule{Name: "no scope at all"}) {
		t.Fatal("an unbound rule must not be treated as scoped — the evaluator would scan every service")
	}
}
