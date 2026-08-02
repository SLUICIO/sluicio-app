// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The two properties that matter:
//
//  1. Turning the option OFF must reproduce the old behaviour exactly.
//     Every existing metric rule in every cell has it off, so any drift
//     here changes what already-deployed alerts do.
//  2. Turning it ON must fire on silence — but not on a check that has
//     never seen data, or saving a check before wiring its exporter
//     would page immediately.

package alerting

import "testing"

func spec(fireOnNoData bool) MetricRuleSpec {
	// "last value of httpcheck.status{2xx} < 1" — the synthetic-probe
	// shape this was built for.
	return MetricRuleSpec{
		MetricName:   "httpcheck.status",
		Aggregation:  AggLast,
		Operator:     OpLT,
		Threshold:    1,
		FireOnNoData: fireOnNoData,
	}
}

func TestDecideMetricWithoutTheOptionIsUnchanged(t *testing.T) {
	off := spec(false)
	cases := []struct {
		name         string
		samples      uint64
		value        float64
		everReported bool
		want         MetricOutcome
	}{
		{"breaching", 3, 0, true, OutcomeBreach},
		{"healthy", 3, 1, true, OutcomeClear},
		// The old code computed `samples > 0 && breach`, so no samples
		// could never fire — whatever the value alongside it said.
		{"silent, has history", 0, 0, true, OutcomeUnknown},
		{"silent, no history", 0, 0, false, OutcomeUnknown},
	}
	for _, c := range cases {
		if got := DecideMetric(off, c.samples, c.value, c.everReported); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if DecideMetric(off, 0, 0, true).Firing() {
			t.Fatal("no-data fired with the option off")
		}
	}
}

func TestDecideMetricFiresOnSilenceWhenAsked(t *testing.T) {
	on := spec(true)
	if got := DecideMetric(on, 0, 0, true); got != OutcomeNoData {
		t.Errorf("silent metric with history: got %q, want %q", got, OutcomeNoData)
	}
	if !DecideMetric(on, 0, 0, true).Firing() {
		t.Error("no-data outcome must hold an alert open")
	}
}

func TestDecideMetricDoesNotFireBeforeTheFirstReading(t *testing.T) {
	// Saving a check for a metric whose exporter is not deployed yet
	// must not page. The condition is "stops reporting", and something
	// that never started has not stopped.
	if got := DecideMetric(spec(true), 0, 0, false); got != OutcomeUnknown {
		t.Errorf("unarmed check: got %q, want %q", got, OutcomeUnknown)
	}
}

func TestDecideMetricPrefersTheMeasurement(t *testing.T) {
	// With data present the option is irrelevant — a measured value is
	// always judged against the threshold, never treated as absent.
	on := spec(true)
	if got := DecideMetric(on, 1, 1, true); got != OutcomeClear {
		t.Errorf("measured healthy: got %q, want %q", got, OutcomeClear)
	}
	if got := DecideMetric(on, 1, 0, true); got != OutcomeBreach {
		t.Errorf("measured breaching: got %q, want %q", got, OutcomeBreach)
	}
}

func TestFiringPartitionsTheOutcomes(t *testing.T) {
	// Every outcome is either firing or not; a new one added without a
	// decision here would show up as a silent non-firing default.
	firing := map[MetricOutcome]bool{
		OutcomeBreach:  true,
		OutcomeNoData:  true,
		OutcomeClear:   false,
		OutcomeUnknown: false,
	}
	for outcome, want := range firing {
		if outcome.Firing() != want {
			t.Errorf("%q.Firing() = %v, want %v", outcome, outcome.Firing(), want)
		}
	}
}
