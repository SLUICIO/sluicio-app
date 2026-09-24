// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Alert delivery used to stop after five attempts, which with a doubling
// backoff is about sixteen minutes: enough for a receiver's lunch break
// and not for its evening. The ceiling is a window of time now, and
// these pin what that means at the edges.

package alerting

import (
	"testing"
	"time"
)

const maxBackoff = 15 * time.Minute

func TestRetryPlanBacksOffButStopsGrowing(t *testing.T) {
	now := time.Now()
	far := now.Add(6 * time.Hour)

	for attempts, want := range map[int]time.Duration{
		0: 30 * time.Second,
		1: time.Minute,
		2: 2 * time.Minute,
		3: 4 * time.Minute,
		4: 8 * time.Minute,
	} {
		got, giveUp := retryPlan(attempts, far, now, maxBackoff)
		if got != want {
			t.Errorf("attempt %d: waited %s, want %s", attempts, got, want)
		}
		if giveUp {
			t.Errorf("attempt %d: gave up with hours of window left", attempts)
		}
	}

	// A receiver that comes back should not wait an hour because the
	// queue is still counting powers of two.
	got, _ := retryPlan(20, far, now, maxBackoff)
	if got != maxBackoff {
		t.Errorf("a long-running retry waited %s; the cap is %s", got, maxBackoff)
	}
}

func TestRetryPlanGivesUpOnTheClock(t *testing.T) {
	now := time.Now()

	// The next attempt would land after the window closed, so it is
	// finished now rather than left pending to be swept later.
	if _, giveUp := retryPlan(0, now.Add(10*time.Second), now, maxBackoff); !giveUp {
		t.Error("kept a delivery whose window closes before the next attempt")
	}
	if _, giveUp := retryPlan(0, now.Add(-time.Hour), now, maxBackoff); !giveUp {
		t.Error("kept a delivery whose window closed an hour ago")
	}
	// Six hours is the point of this change: the sixteen minutes the old
	// attempt count bought would have given up here.
	if _, giveUp := retryPlan(9, now.Add(5*time.Hour), now, maxBackoff); giveUp {
		t.Error("gave up with five hours of window left")
	}
	// A job queued before the window existed is not abandoned by the
	// change that introduced it.
	if _, giveUp := retryPlan(99, time.Time{}, now, maxBackoff); giveUp {
		t.Error("gave up on a job that carries no window")
	}
}

func TestStaleFiringIsTheOnlyOneDropped(t *testing.T) {
	cases := []struct {
		forState, instanceState string
		want                    bool
	}{
		{"firing", "resolved", true},   // queued to say it started; it has stopped
		{"firing", "firing", false},    // still true, still worth sending
		{"resolved", "resolved", false},// the one that says it is over
		{"resolved", "firing", false},  // fired again; both are news
	}
	for _, c := range cases {
		got := staleFiring(DeliveryJob{State: c.forState, InstanceState: c.instanceState})
		if got != c.want {
			t.Errorf("for=%s instance=%s: got %v, want %v", c.forState, c.instanceState, got, c.want)
		}
	}
}
