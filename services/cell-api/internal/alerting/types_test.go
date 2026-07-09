// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLogRuleSpecWindowDuration(t *testing.T) {
	cases := []struct {
		name string
		secs int
		want time.Duration
	}{
		{"zero → 5m default", 0, 5 * time.Minute},
		{"below 1m floor → 5m", 30, 5 * time.Minute},
		{"normal 10m", 600, 10 * time.Minute},
		{"exactly 1m", 60, time.Minute},
		{"above 30d cap → 30d", 31 * 24 * 60 * 60, 30 * 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LogRuleSpec{WindowSeconds: tc.secs}.WindowDuration()
			if got != tc.want {
				t.Fatalf("WindowDuration(%ds) = %s, want %s", tc.secs, got, tc.want)
			}
		})
	}
}

func TestAlertLinkPath(t *testing.T) {
	const base = "https://sluicio.example.com"
	t.Setenv("SLUICIO_APP_URL", base)
	inst := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	integ := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	cases := []struct {
		name string
		job  DeliveryJob
		want string
	}{
		// Every destination carries ?instance= — the frontend highlighter's
		// contract (lib/useInstanceHighlight): the recipient lands on the
		// exact alert that paged them, not just the right page.
		{
			name: "failed-trace rule bound to an integration → integration errors page",
			job:  DeliveryJob{AlertInstanceID: inst, RuleSignal: SignalTraceError, RuleKind: TraceErrorSpecKind, IntegrationID: &integ},
			want: "/integrations/" + integ.String() + "/errors?instance=" + inst.String(),
		},
		{
			name: "failed-trace rule with no integration → global errors overview",
			job:  DeliveryJob{AlertInstanceID: inst, RuleSignal: SignalTraceError, RuleKind: TraceErrorSpecKind},
			want: "/stuck?instance=" + inst.String(),
		},
		{
			name: "response-time (latency) trace rule → the alert itself",
			job:  DeliveryJob{AlertInstanceID: inst, RuleSignal: SignalTraceError, RuleKind: TraceLatencySpecKind, IntegrationID: &integ},
			want: "/alerts?instance=" + inst.String(),
		},
		{
			name: "metric rule → the alert itself",
			job:  DeliveryJob{AlertInstanceID: inst, RuleSignal: SignalMetric},
			want: "/alerts?instance=" + inst.String(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := alertLinkPath(tc.job); got != tc.want {
				t.Fatalf("alertLinkPath() = %q, want %q", got, tc.want)
			}
			// The full Link() should join cleanly onto the base.
			if got := Link(alertLinkPath(tc.job)); got != base+tc.want {
				t.Fatalf("Link(alertLinkPath()) = %q, want %q", got, base+tc.want)
			}
		})
	}
}
