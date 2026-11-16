// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
)

// A dry run of a tuning sets the threshold and window on whichever spec
// the check reads, so "what if the threshold were 12" rehearses that
// check and not a copy of it with the old values.
func TestBacktestOverridesReachTheCheckSpec(t *testing.T) {
	threshold := 12.0

	metric := alerting.AlertRule{Signal: alerting.SignalMetric, Spec: alerting.MetricRuleSpec{Threshold: 5, ForWindow: "5m"}}
	if err := applyBacktestOverrides(&metric, backtestBody{Threshold: &threshold, ForWindow: "10m"}); err != nil {
		t.Fatal(err)
	}
	if metric.Spec.Threshold != 12 || metric.Spec.ForWindow != "10m" {
		t.Fatalf("metric: %+v", metric.Spec)
	}
	if err := applyBacktestOverrides(&metric, backtestBody{WindowSeconds: 600}); err == nil {
		t.Fatal("window_seconds is not a metric check's window")
	}

	logRule := alerting.AlertRule{Signal: "log", LogSpec: &alerting.LogRuleSpec{Threshold: 1, WindowSeconds: 300}}
	if err := applyBacktestOverrides(&logRule, backtestBody{Threshold: &threshold, WindowSeconds: 900}); err != nil {
		t.Fatal(err)
	}
	if logRule.LogSpec.Threshold != 12 || logRule.LogSpec.WindowSeconds != 900 {
		t.Fatalf("log: %+v", logRule.LogSpec)
	}

	latency := alerting.AlertRule{Signal: "trace", TraceLatencySpec: &alerting.TraceLatencyRuleSpec{ThresholdMs: 500, WindowSeconds: 300}}
	if err := applyBacktestOverrides(&latency, backtestBody{Threshold: &threshold}); err != nil {
		t.Fatal(err)
	}
	if latency.TraceLatencySpec.ThresholdMs != 12 {
		t.Fatalf("latency: %+v", latency.TraceLatencySpec)
	}

	if err := applyBacktestOverrides(&metric, backtestBody{ForWindow: "soon"}); err == nil {
		t.Fatal("a window that is not a duration is refused")
	}
}
