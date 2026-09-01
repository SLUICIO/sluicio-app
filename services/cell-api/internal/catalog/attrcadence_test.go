// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The resource-attribute snapshot is an ARRAY JOIN over the traces
// table. It used to run on every 30-second reconcile tick over the
// reconcile window's ninety days: measured on a customer cell at two
// seconds and 94 MiB a pass, twice a minute, for ever. Not slow enough
// to fail - just enough that every page shared a small box with it.

package catalog

import (
	"testing"
	"time"
)

func TestAttrPassIsDueOnTheFirstTick(t *testing.T) {
	r := &Reconciler{}
	if !r.attrDue(time.Now()) {
		t.Fatal("the first pass must run, or the snapshot is never taken")
	}
}

func TestAttrPassIsSkippedUntilTheIntervalElapses(t *testing.T) {
	now := time.Now()
	r := &Reconciler{AttrInterval: 15 * time.Minute, lastAttrPass: now}

	// The reconcile tick is 30s. Every one of these used to run the join.
	for _, after := range []time.Duration{30 * time.Second, 5 * time.Minute, 14*time.Minute + 59*time.Second} {
		if r.attrDue(now.Add(after)) {
			t.Errorf("ran again after %s, want the interval to gate it", after)
		}
	}
	if !r.attrDue(now.Add(15 * time.Minute)) {
		t.Error("did not run once the interval elapsed")
	}
}

func TestAttrCadenceDefaultsWithoutConfiguration(t *testing.T) {
	now := time.Now()
	r := &Reconciler{lastAttrPass: now}
	if r.attrDue(now.Add(time.Minute)) {
		t.Error("an unset interval fell back to running every tick")
	}
	if !r.attrDue(now.Add(15 * time.Minute)) {
		t.Error("the default interval is not 15 minutes")
	}
}

// A day, not the reconcile window's ninety. The snapshot answers what a
// service carries NOW, and ninety days also drags in every value a
// churning pod name ever had.
func TestAttrWindowIsADayByDefault(t *testing.T) {
	if got := (&Reconciler{}).attrWindow(); got != 24*time.Hour {
		t.Fatalf("default window = %s, want 24h", got)
	}
	if got := (&Reconciler{AttrWindow: 6 * time.Hour}).attrWindow(); got != 6*time.Hour {
		t.Fatalf("configured window = %s, want 6h", got)
	}
}
