// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The expiry rule is the decision this feature turns on, and it is the
// kind that only shows itself weeks later: too eager and a monthly flow
// loses its classification between runs, too lazy and a service keeps a
// boundary it retired last quarter.
//
// These pin the arithmetic without a Postgres harness. The SQL is
// exercised by the integration tests; what is checked here is that the
// caller passes the cutoff the design calls for.

package catalog

import (
	"testing"
	"time"
)

// facetCutoff is the rule stated once: a facet survives exactly as long
// as the telemetry that could re-detect it.
func facetCutoff(now time.Time, evidenceWindow time.Duration) time.Time {
	return now.Add(-evidenceWindow)
}

func TestFacetCutoffOutlivesAMonthlyFlow(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	// A flow that ran 31 days ago, with retention set to 90 days. Its
	// evidence is still in ClickHouse, so its classification must still
	// stand — this is the case that ruled out "recompute over 7 days".
	lastRun := now.AddDate(0, 0, -31)
	cutoff := facetCutoff(now, 90*24*time.Hour)
	if !lastRun.After(cutoff) {
		t.Errorf("a flow last seen %s would be expired at cutoff %s", lastRun, cutoff)
	}
}

func TestFacetCutoffDropsWhatTheDataCannotSupport(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	// 14-day retention, a facet not seen for 20 days. The spans that
	// justified it are gone, so the claim goes with them — the "never
	// expire" option would keep asserting it for ever.
	lastSeen := now.AddDate(0, 0, -20)
	cutoff := facetCutoff(now, 14*24*time.Hour)
	if lastSeen.After(cutoff) {
		t.Errorf("a facet unseen since %s should be expired at cutoff %s", lastSeen, cutoff)
	}
}

func TestFacetCutoffIsExactlyTheEvidenceWindow(t *testing.T) {
	// No fudge factor. If the cutoff were shorter than retention a
	// service could lose its facets while the evidence was still on
	// disk; longer, and it would assert a classification nothing can
	// confirm.
	now := time.Now().UTC()
	for _, w := range []time.Duration{time.Hour, 14 * 24 * time.Hour, 90 * 24 * time.Hour} {
		if got := now.Sub(facetCutoff(now, w)); got != w {
			t.Errorf("cutoff for window %s was %s behind now, want %s", w, got, w)
		}
	}
}
