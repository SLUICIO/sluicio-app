// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"reflect"
	"testing"
)

// The rule preview's candidates are every service that sent anything.
// A broker read by a collector sends only metrics; before this it was in
// none of the lists the preview looked at, and matched no rule at all.
func TestPreviewCandidatesIncludeServicesThatOnlySendMetricsOrLogs(t *testing.T) {
	got := unionServiceNames(
		[]string{"order-intake", "order-fulfillment"}, // traces
		[]string{"rabbitmq", "order-intake", ""},      // metrics
		[]string{"legacy-batch", "rabbitmq"},          // logs
	)
	want := []string{"legacy-batch", "order-fulfillment", "order-intake", "rabbitmq"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %v, want %v", got, want)
	}
}

func TestPreviewCandidatesSurviveAMissingList(t *testing.T) {
	// A metrics or logs listing that failed arrives as nil; the traces
	// still count.
	if got := unionServiceNames([]string{"a"}, nil, nil); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("candidates = %v, want [a]", got)
	}
}
