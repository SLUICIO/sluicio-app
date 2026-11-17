// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tracecompletion"
)

// A completion_rule proposal is merged into the rule as it is: the
// fields it names change, the rest stay. A lookback that was the derived
// default follows longer timeouts, or the merged rule would be refused
// for a lookback shorter than twice its longest stage; one chosen by
// hand is kept.
func TestMergeCompletionRule(t *testing.T) {
	rule := func(lookback int) tracecompletion.Rule {
		r := tracecompletion.Rule{Name: "orders", Severity: "warning", Enabled: true, Spec: tracecompletion.RuleSpec{
			Kind: "trace_completion", StartSpanName: "process orders",
			Stages: []tracecompletion.Stage{{Spans: []tracecompletion.SpanMatch{{Service: "erp", SpanName: "Step.Send"}}, TimeoutSeconds: 60}},
			LookbackSeconds: lookback,
		}}
		r.Spec.Normalize()
		return r
	}
	longer := proposals.Change{Field: "stages", After: json.RawMessage(`[{"spans":[{"service":"erp","span_name":"Step.Send"}],"timeout_seconds":600}]`)}

	got, err := mergeCompletionRule(rule(0), []proposals.Change{{Field: "start_span_name", After: json.RawMessage(`"Pipeline.orders.Receive"`)}})
	if err != nil || got.StartSpanName != "Pipeline.orders.Receive" || got.Name != "orders" || len(got.Stages) != 1 || got.LookbackSeconds != 240 {
		t.Fatalf("only the start span changes: %+v %v", got, err)
	}
	if got, _ := mergeCompletionRule(rule(0), []proposals.Change{longer}); got.LookbackSeconds != 0 {
		t.Fatalf("a default lookback is left to follow the new timeouts: %d", got.LookbackSeconds)
	}
	if got, _ := mergeCompletionRule(rule(9000), []proposals.Change{longer}); got.LookbackSeconds != 9000 {
		t.Fatalf("a chosen lookback is kept: %d", got.LookbackSeconds)
	}
}
