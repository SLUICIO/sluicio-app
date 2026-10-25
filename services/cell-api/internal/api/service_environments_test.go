// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// A service's status per environment (#40): its checks speak for every
// environment it runs in; the traffic is what differs.
func TestServiceEnvironmentStatuses(t *testing.T) {
	vols := []store.EnvironmentVolume{
		{Environment: "test", Messages: 5, ErrorTraces: 2},
		{Environment: "production", Messages: 40},
		{Environment: "staging", MetricRows: 12}, // metrics only, still running there
	}
	got := serviceEnvironmentStatuses(vols, false)
	if len(got) != 3 || got[0].Environment != "production" || got[0].Status != "ok" || !got[0].Production {
		t.Fatalf("ok everywhere, sorted by name within a status: %+v", got)
	}
	if got[1].Environment != "staging" || got[1].Status != "ok" || got[2].TraceCount != 5 || got[2].ErrorTraceCount != 2 {
		t.Errorf("figures: %+v", got)
	}
	for _, e := range serviceEnvironmentStatuses(vols, true) {
		if e.Status != "unhealthy" {
			t.Errorf("a firing check speaks for every environment: %+v", e)
		}
	}
	if !ranIn(vols, []string{"staging"}) || ranIn(vols, []string{"dev"}) {
		t.Error("ranIn")
	}
}
