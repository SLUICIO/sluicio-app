// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Issue #26 decided that a facet "lasts exactly as long as the data
// behind it". The first implementation expressed that as one constant
// serving two purposes - how far back a pass profiles, and how long a
// facet survives unseen - and fourteen days is only the right answer
// for the first.
//
// Telemetry retention is a per-cell setting from one day to five years.
// Where it exceeds the profile window, a flow that runs less often than
// the profile window is invisible to every pass between its runs, and
// the old rule expired it. A monthly flow on a cell retaining thirty
// days therefore went unclassified for half of every month, which is
// precisely the case the issue was raised for.

package catalog

import (
	"testing"
	"time"
)

const day = 24 * time.Hour

func TestAFacetOutlivesTheProfileWindowUpToTheRetention(t *testing.T) {
	scan, expiry := facetWindows(14*day, 30*day)
	if scan != 14*day {
		t.Errorf("profile window = %s, want it bounded at 14 days", scan)
	}
	if expiry != 30*day {
		t.Errorf("expiry = %s, want the retention (30 days)", expiry)
	}
	// The property, stated as the issue states it: a monthly flow seen
	// once every 30 days is absent from every profile in between, and
	// must still be classified when the next run comes.
	if expiry < 30*day {
		t.Error("a monthly flow loses its classification between runs")
	}
}

func TestTheProfileNeverReadsPastTheRetention(t *testing.T) {
	// Nothing is there to read, so a wider window is pure cost.
	scan, expiry := facetWindows(14*day, 3*day)
	if scan != 3*day {
		t.Errorf("profile window = %s, want it clamped to the 3-day retention", scan)
	}
	if expiry != 3*day {
		t.Errorf("expiry = %s, want the retention", expiry)
	}
}

func TestUnconfiguredRetentionKeepsTheSingleWindow(t *testing.T) {
	// A retention lookup that failed returns zero. Falling back to the
	// profile window is the old behaviour, which is safe rather than
	// surprising: worst case a facet expires as early as it used to.
	scan, expiry := facetWindows(14*day, 0)
	if scan != 14*day || expiry != 14*day {
		t.Errorf("facetWindows(14d, 0) = (%s, %s), want both 14 days", scan, expiry)
	}
}

func TestTheProfileWindowHasADefault(t *testing.T) {
	// A zero-valued Reconciler must not profile "since the epoch".
	scan, expiry := facetWindows(0, 0)
	if scan != 14*day || expiry != 14*day {
		t.Errorf("facetWindows(0, 0) = (%s, %s), want both 14 days", scan, expiry)
	}
	if scan, _ := facetWindows(0, 90*day); scan != 14*day {
		t.Errorf("unset profile window with a 90-day retention = %s, want the 14-day default", scan)
	}
}
