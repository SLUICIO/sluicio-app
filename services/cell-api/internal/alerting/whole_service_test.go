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
