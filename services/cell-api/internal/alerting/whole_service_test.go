// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import "testing"

// Which service-bound checks speak for every integration on a shared
// service. The rows are the Airflow system type's checks.
func TestDescribesWholeService(t *testing.T) {
	cases := []struct {
		name   string
		signal string
		spec   MetricRuleSpec
		want   bool
	}{
		{"Scheduler not heartbeating", SignalMetric, MetricRuleSpec{MetricName: "airflow.scheduler_heartbeat"}, true},
		{"Executor saturated", SignalMetric, MetricRuleSpec{MetricName: "airflow.executor.open_slots"}, true},
		{"Task failures, split by dag_id", SignalMetric, MetricRuleSpec{MetricName: "airflow.ti_failures", SplitBy: "dag_id"}, false},
		{"a metric filtered to one DAG", SignalMetric, MetricRuleSpec{Attrs: []AttrFilter{{Key: "dag_id", Op: "eq", Value: "gl_posting"}}}, false},
		{"DAG run failed", SignalTraceError, MetricRuleSpec{}, false},
		{"Task error logs spiking", SignalLog, MetricRuleSpec{}, false},
	}
	for _, c := range cases {
		if got := DescribesWholeService(c.signal, c.spec); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// A declaration wins over the inference, and the case that motivated
// letting checks declare at all: a heartbeat narrowed to one replica.
//
// Inferred, an attribute filter reads as "about one flow", because on a
// shared runtime that is what a filter usually selects. But
// instance=scheduler-1 selects a SERIES OF THE SAME PROCESS, and reading
// it as a flow means the heartbeat fires while every integration that
// needed to hear about it goes on reading ok. It fails quiet, which is
// the worst direction for a liveness check.
func TestADeclaredScopeWinsOverTheInference(t *testing.T) {
	narrowedHeartbeat := MetricRuleSpec{
		MetricName: "airflow.scheduler_heartbeat",
		Attrs:      []AttrFilter{{Key: "instance", Op: "eq", Value: "scheduler-1"}},
	}
	if inferredWholeService(SignalMetric, narrowedHeartbeat) {
		t.Fatal("the inference is expected to read a filtered metric as flow-level; the premise of this test is gone")
	}
	if !ScopedWholeService(CheckScopeProcess, SignalMetric, narrowedHeartbeat) {
		t.Error("a heartbeat declared process-level did not reach the slices of its service")
	}

	// And the other direction: a check with nothing to infer from can
	// still say it is about one flow, which an unsplit unfiltered metric
	// would otherwise be read as describing the whole process.
	wholeLooking := MetricRuleSpec{MetricName: "airflow.ti_failures"}
	if !inferredWholeService(SignalMetric, wholeLooking) {
		t.Fatal("premise: an unsplit unfiltered metric infers as process-level")
	}
	if ScopedWholeService(CheckScopeFlow, SignalMetric, wholeLooking) {
		t.Error("a check declared flow-level was still propagated to every sibling")
	}
}

func TestAnUndeclaredScopeKeepsTheOldMeaning(t *testing.T) {
	// Every rule that predates the column, and every hand-written one,
	// arrives with no declaration and must classify exactly as before.
	cases := []struct {
		signal string
		spec   MetricRuleSpec
	}{
		{SignalMetric, MetricRuleSpec{MetricName: "airflow.scheduler_heartbeat"}},
		{SignalMetric, MetricRuleSpec{MetricName: "airflow.ti_failures", SplitBy: "dag_id"}},
		{SignalLog, MetricRuleSpec{}},
		{SignalTraceError, MetricRuleSpec{}},
	}
	for _, c := range cases {
		if ScopedWholeService("", c.signal, c.spec) != DescribesWholeService(c.signal, c.spec) {
			t.Errorf("%s/%+v: an empty scope changed the answer", c.signal, c.spec)
		}
	}
}
