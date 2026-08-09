// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package cellhealth

import (
	"sync"
	"testing"
	"time"
)

var base = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

func fixed(t *time.Time) func() time.Time {
	return func() time.Time { return *t }
}

func TestARegisteredLoopThatNeverRanIsUnknownNotStale(t *testing.T) {
	// A cell that started ninety seconds ago is not broken. Reporting it
	// as stale would train operators to ignore this page during exactly
	// the window where it matters most.
	now := base
	r := WithClock(fixed(&now))
	r.Register("alerting", time.Minute)

	got := r.Loops()
	if len(got) != 1 || got[0].Status != StatusUnknown {
		t.Fatalf("got %+v, want one unknown", got)
	}
	if got[0].LastCompleted != nil {
		t.Error("a loop that never ran must not report a completion time")
	}
	if len(r.StaleLoops()) != 0 {
		t.Error("never-run must not count as stale")
	}
}

func TestALoopThatVanishedIsStillReported(t *testing.T) {
	// The whole point is noticing absence. A loop that dropped out of
	// the report would be indistinguishable from one never wired up.
	now := base
	r := WithClock(fixed(&now))
	r.Register("retention", time.Hour)
	r.Beat("retention")

	now = base.Add(24 * time.Hour)
	stale := r.StaleLoops()
	if len(stale) != 1 || stale[0] != "retention" {
		t.Fatalf("got %v, want [retention]", stale)
	}
	if l := r.Loops()[0]; l.Overdue == "" {
		t.Error("a stale loop must say how far overdue it is")
	}
}

func TestAHiccupIsNotAFault(t *testing.T) {
	// An alarm that cries wolf is worse than no alarm. One missed cycle
	// on a slow query must not read as a dead loop.
	now := base
	r := WithClock(fixed(&now))
	r.Register("reconciler", 10*time.Minute)
	r.Beat("reconciler")

	now = base.Add(25 * time.Minute) // two missed cycles
	if got := r.Loops()[0].Status; got != StatusOK {
		t.Fatalf("got %q after two missed cycles, want ok", got)
	}
	now = base.Add(45 * time.Minute) // past three
	if got := r.Loops()[0].Status; got != StatusStale {
		t.Fatalf("got %q well past the window, want stale", got)
	}
}

func TestFrequentLoopsGetAFloor(t *testing.T) {
	// A five-second loop would otherwise be stale after fifteen seconds,
	// which one slow ClickHouse query causes routinely.
	now := base
	r := WithClock(fixed(&now))
	r.Register("demand-writer", 5*time.Second)
	r.Beat("demand-writer")

	now = base.Add(90 * time.Second)
	if got := r.Loops()[0].Status; got != StatusOK {
		t.Fatalf("got %q at 90s on a 5s loop, want ok", got)
	}
	now = base.Add(5 * time.Minute)
	if got := r.Loops()[0].Status; got != StatusStale {
		t.Fatalf("got %q at 5m, want stale", got)
	}
}

func TestBeatingRecovers(t *testing.T) {
	now := base
	r := WithClock(fixed(&now))
	r.Register("advisor", time.Hour)
	r.Beat("advisor")

	now = base.Add(10 * time.Hour)
	if r.Loops()[0].Status != StatusStale {
		t.Fatal("setup: expected stale")
	}
	r.Beat("advisor")
	l := r.Loops()[0]
	if l.Status != StatusOK {
		t.Fatalf("got %q after a fresh beat, want ok", l.Status)
	}
	if l.Overdue != "" {
		t.Errorf("a recovered loop must not still claim to be overdue: %q", l.Overdue)
	}
}

func TestOrderIsStable(t *testing.T) {
	// The response is read by machines and diffed by humans; map order
	// is neither.
	now := base
	r := WithClock(fixed(&now))
	for _, n := range []string{"zeta", "alpha", "mid"} {
		r.Register(n, time.Minute)
	}
	a := r.Loops()
	b := r.Loops()
	if a[0].Name != "alpha" || a[2].Name != "zeta" {
		t.Fatalf("not sorted: %v", a)
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			t.Fatal("order varies between calls")
		}
	}
}

func TestNilRegistryIsSafe(t *testing.T) {
	// Beat is called from inside hot loops. A caller must never have to
	// nil-check, or someone will forget in the one loop that matters.
	var r *Registry
	r.Register("x", time.Minute)
	r.Beat("x")
	if r.Loops() != nil || r.StaleLoops() != nil {
		t.Error("nil registry should report nothing")
	}
}

func TestConcurrentBeatsAreSafe(t *testing.T) {
	now := base
	r := WithClock(fixed(&now))
	r.Register("busy", time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Beat("busy")
			_ = r.Loops()
		}()
	}
	wg.Wait()
	if r.Loops()[0].Status != StatusOK {
		t.Error("expected ok after concurrent beats")
	}
}

func TestBeatingAnUnregisteredLoopStillCounts(t *testing.T) {
	// Forgetting to register but remembering to beat should surface the
	// loop, not silently discard it.
	now := base
	r := WithClock(fixed(&now))
	r.Beat("surprise")
	got := r.Loops()
	if len(got) != 1 || got[0].Name != "surprise" || got[0].Status != StatusOK {
		t.Fatalf("got %+v", got)
	}
}
