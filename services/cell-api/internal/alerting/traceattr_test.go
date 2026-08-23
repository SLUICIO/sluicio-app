// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The span-attribute rule exists because the failed-trace rule cannot say
// this. If someone ever "simplifies" the two into one, these are the
// assertions that should stop them.

func TestTraceAttributeWindowClamps(t *testing.T) {
	cases := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"below the floor falls back to 5m", 30, 5 * time.Minute},
		{"zero falls back to 5m", 0, 5 * time.Minute},
		{"an hour is kept", 3600, time.Hour},
		{"above the ceiling clamps", 999 * 24 * 3600, maxCheckWindow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TraceAttributeRuleSpec{WindowSeconds: c.seconds}.WindowDuration()
			if got != c.want {
				t.Fatalf("WindowDuration(%d) = %v, want %v", c.seconds, got, c.want)
			}
		})
	}
}

// The summary is what lands in the notification. It must never borrow the
// failed-trace wording: the traces this rule counts have usually
// succeeded, and calling them failures misreports what happened in the
// one place somebody acts on it.
func TestTraceAttributeSummaryDoesNotSayFailed(t *testing.T) {
	rule := AlertRule{
		ID:   uuid.New(),
		Name: "Documents failed to ingest",
		TraceAttributeSpec: &TraceAttributeRuleSpec{
			Kind:          TraceAttributeSpecKind,
			Threshold:     1,
			WindowSeconds: 3600,
			Attrs:         []AttrFilter{{Key: "documents.failed", Op: "gt", Value: "0"}},
		},
	}
	for _, state := range []string{"firing", "resolved"} {
		got := traceAttributeRuleSummary(rule, 4, state)
		if strings.Contains(got, "failed trace") {
			t.Errorf("%s summary calls them failed traces: %q", state, got)
		}
		if !strings.Contains(got, "matching trace") {
			t.Errorf("%s summary does not say matching traces: %q", state, got)
		}
		if !strings.Contains(got, "documents.failed gt 0") {
			t.Errorf("%s summary drops the criteria: %q", state, got)
		}
	}
}

// The label distinguishes this signal downstream (routing, templates,
// the UI). Sharing "trace_error" would make the two indistinguishable
// everywhere they are consumed.
func TestTraceAttributeLabelsCarryOwnSignal(t *testing.T) {
	rule := AlertRule{ID: uuid.New(), Name: "x", Severity: SeverityWarning}
	got := traceAttributeRuleLabels(rule, 2)
	if got["signal"] != "trace_attribute" {
		t.Fatalf("signal label = %q, want trace_attribute", got["signal"])
	}
	if got["count"] != "2" {
		t.Fatalf("count label = %q, want 2", got["count"])
	}
}
