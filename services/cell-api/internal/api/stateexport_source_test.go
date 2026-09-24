// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The export and the pages must agree about what an integration's state
// is, and the way to guarantee that is for both to call one function.
// These pin the fold itself.

package api

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
)

func TestIntegrationRollupStatus(t *testing.T) {
	spanCheck := alerting.ServiceFiring{Any: true}
	processCheck := alerting.ServiceFiring{Any: true, Process: true}
	cases := []struct {
		name string
		in   integrationHealth
		want string
	}{
		// A service-only integration: the rule as it always was.
		{"nothing emitted in the window", integrationHealth{}, "quiet"},
		{"members emitted, nothing firing", integrationHealth{Active: true}, "ok"},
		{"a member's check fires", integrationHealth{Active: true, MemberFiring: spanCheck}, "unhealthy"},
		{"a quiet member's check still fires", integrationHealth{MemberFiring: spanCheck}, "unhealthy"},
		{"a missed SLA is a failure", integrationHealth{Active: true, Delayed: 3}, "errors"},
		{"a firing check on the integration outranks healthy members", integrationHealth{Active: true, IntegrationFiring: true}, "unhealthy"},
		{"and outranks a delay too", integrationHealth{Active: true, Delayed: 3, IntegrationFiring: true}, "unhealthy"},
		{"a delay never downgrades unhealthy", integrationHealth{Active: true, Delayed: 3, MemberFiring: spanCheck}, "unhealthy"},
		{"quiet with a firing check is still unhealthy", integrationHealth{IntegrationFiring: true}, "unhealthy"},
		{"trace errors do not colour a service-only integration", integrationHealth{Active: true, OpenSliceErrors: 5}, "ok"},

		// A slice of a shared service.
		{"a slice with open errors", integrationHealth{Slice: true, Active: true, OpenSliceErrors: 2}, "errors"},
		{"a slice whose errors were all cleared", integrationHealth{Slice: true, Active: true}, "ok"},
		{"a slice that did not run, on a busy member", integrationHealth{Slice: true}, "quiet"},
		{"a span-level member check does not speak for a slice", integrationHealth{Slice: true, Active: true, MemberFiring: spanCheck}, "ok"},
		{"a process-level member check does", integrationHealth{Slice: true, Active: true, MemberFiring: processCheck}, "unhealthy"},
		{"even for a slice that did not run", integrationHealth{Slice: true, MemberFiring: processCheck}, "unhealthy"},
		{"a check bound to the slice itself does", integrationHealth{Slice: true, Active: true, OpenSliceErrors: 2, IntegrationFiring: true}, "unhealthy"},
		{"a missed SLA on a slice", integrationHealth{Slice: true, Active: true, Delayed: 1}, "errors"},
	}
	for _, c := range cases {
		if got := integrationRollupStatus(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The case this fold was reshaped for: two DAGs of one Airflow scheduler,
// each an integration selecting its own dag_id, and a third integration
// that is the scheduler itself. gl_posting fails; the scheduler's "DAG run
// failed" check fires, as it would. Only gl_posting and the scheduler's
// own integration may say so.
func TestIntegrationRollupStatusSharedService(t *testing.T) {
	scheduler := "airflow-scheduler"
	// The Airflow template's "DAG run failed": a failed-trace check bound
	// to the scheduler.
	firing := map[string]alerting.ServiceFiring{
		scheduler: {Any: true, Process: alerting.DescribesWholeService(alerting.SignalTraceError, alerting.MetricRuleSpec{})},
	}
	dag := func(id string) []integrations.Matcher {
		return []integrations.Matcher{
			{Attribute: integrations.ServiceNameAttribute, Operator: integrations.OperatorEquals, Value: scheduler},
			{Attribute: "airflow.dag_id", Operator: integrations.OperatorEquals, Value: id, IncludeDescendants: true},
		}
	}
	plain := []integrations.Matcher{{Attribute: integrations.ServiceNameAttribute, Operator: integrations.OperatorEquals, Value: scheduler}}
	members := []string{scheduler}

	// What SliceTraceStats returned for each slice: both ran, one failed.
	slice := map[string]uint64{"gl_posting": 3, "orders_export": 0}

	status := func(ms []integrations.Matcher, openErrors uint64) string {
		return integrationRollupStatus(integrationHealth{
			Slice:           integrations.SelectsSlice(ms, integrations.RuleMatchAny),
			Active:          true,
			OpenSliceErrors: openErrors,
			MemberFiring:    memberFiring(members, firing),
		})
	}
	if got := status(dag("gl_posting"), slice["gl_posting"]); got != "errors" {
		t.Errorf("gl_posting failed and should read errors, got %q", got)
	}
	if got := status(dag("orders_export"), slice["orders_export"]); got != "ok" {
		t.Errorf("orders_export only shares the scheduler with gl_posting, got %q", got)
	}
	// The integration that IS the scheduler owns the check: unchanged.
	if got := status(plain, 0); got != "unhealthy" {
		t.Errorf("a service-only integration reads its member's check as before, got %q", got)
	}

	// The scheduler stops heartbeating: that is every DAG's problem.
	firing[scheduler] = alerting.ServiceFiring{Any: true, Process: true}
	for _, id := range []string{"gl_posting", "orders_export"} {
		if got := status(dag(id), 0); got != "unhealthy" {
			t.Errorf("%s: a dead scheduler breaks every DAG, got %q", id, got)
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
