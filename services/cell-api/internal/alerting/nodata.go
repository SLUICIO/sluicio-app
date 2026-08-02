// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What a metric evaluation MEANS — including the case where the metric
// said nothing at all.
//
// A metric rule only ever fired on a measured breach: `samples > 0 &&
// breach`. That is the right default (a 0 read from an empty series
// would page people about metrics that are merely intermittent), but it
// leaves a hole precisely where it hurts most. A synthetic HTTP check
// exists to keep reporting; when the collector or the probe dies, the
// series stops, and "the endpoint is fine" and "nothing is watching the
// endpoint any more" look identical to the evaluator.
//
// So no-data becomes an opt-in FIRING condition, and the four possible
// meanings of one evaluation get names — because "no data" and "measured
// and fine" must not share a code path that reports a value it never
// measured.
//
// Kept as a pure function: Engine holds a concrete *Store, so the
// evaluation loop itself cannot be exercised without Postgres, and the
// subtle part here is the decision, not the plumbing.

package alerting

// MetricOutcome is what a single metric evaluation means for a rule.
type MetricOutcome string

const (
	// OutcomeClear: measured, and not breaching. Resolves an open alert.
	OutcomeClear MetricOutcome = "clear"
	// OutcomeBreach: measured, and breaching. Fires.
	OutcomeBreach MetricOutcome = "breach"
	// OutcomeNoData: nothing measured, and the rule asked to be told.
	// Fires, with its own wording — reporting a threshold comparison here
	// would quote a value that was never read.
	OutcomeNoData MetricOutcome = "no_data"
	// OutcomeUnknown: nothing measured, and the rule did not ask. Treated
	// exactly as it always has been (an open alert resolves), so enabling
	// nothing changes nothing.
	OutcomeUnknown MetricOutcome = "unknown"
)

// Firing reports whether this outcome should hold an alert open.
func (o MetricOutcome) Firing() bool { return o == OutcomeBreach || o == OutcomeNoData }

// DecideMetric turns one evaluation into an outcome.
//
// everReported is whether the rule has ever recorded a reading, and it
// ARMS the no-data condition. Without it, saving a check for a metric
// whose exporter is not wired up yet would page immediately — the
// check would fire before the thing it watches had ever run once. The
// cost of arming is that a metric which never arrives at all stays
// silent, which is why the editor says "stops reporting" rather than
// "is not reporting".
func DecideMetric(spec MetricRuleSpec, samples uint64, value float64, everReported bool) MetricOutcome {
	if samples == 0 {
		if spec.FireOnNoData && everReported {
			return OutcomeNoData
		}
		return OutcomeUnknown
	}
	if EvaluateBreach(spec.Operator, value, spec.Threshold) {
		return OutcomeBreach
	}
	return OutcomeClear
}
