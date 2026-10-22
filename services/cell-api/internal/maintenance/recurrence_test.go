// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package maintenance

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestWeeklyKeepsWallClockAcrossDaylightSaving(t *testing.T) {
	sto := mustLoc(t, "Europe/Stockholm")
	r := Recurrence{Frequency: "weekly", Weekdays: []int{0}, StartTime: "02:00", DurationMinutes: 120, Timezone: "Europe/Stockholm"}
	// Summer time ends on Sunday 2026-10-25, whose 02:00 happens twice;
	// the Sundays either side show the offset moving under a fixed 02:00.
	got := r.Next(time.Date(2026, 10, 13, 12, 0, 0, 0, sto), 3, nil)
	if len(got) != 3 {
		t.Fatalf("want 3 occurrences, got %d", len(got))
	}
	for i, wantDay := range []int{18, 25, 1} {
		local := got[i].StartsAt.In(sto)
		if local.Weekday() != time.Sunday || local.Day() != wantDay || local.Hour() != 2 || local.Minute() != 0 {
			t.Errorf("occurrence %d starts %s, want Sunday the %d at 02:00 local", i, local, wantDay)
		}
		if d := got[i].EndsAt.Sub(got[i].StartsAt); d != 2*time.Hour {
			t.Errorf("occurrence %d lasts %s, want 2h", i, d)
		}
	}
	// The offset changed underneath, so the UTC starts differ by an hour.
	if got[0].StartsAt.Hour() != 0 || got[2].StartsAt.Hour() != 1 {
		t.Errorf("UTC starts %s and %s: the DST change was not applied", got[0].StartsAt, got[2].StartsAt)
	}
}

func TestSpringForwardGapStillHasAnOccurrence(t *testing.T) {
	// 02:30 does not exist on 2026-03-29 in Stockholm (02:00 -> 03:00).
	// The occurrence still happens, at the moment the gap normalizes to,
	// rather than silently disappearing for that week.
	r := Recurrence{Frequency: "weekly", Weekdays: []int{0}, StartTime: "02:30", DurationMinutes: 60, Timezone: "Europe/Stockholm"}
	got := r.Next(time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC), 1, nil)
	if len(got) != 1 || got[0].StartsAt.In(mustLoc(t, "Europe/Stockholm")).Day() != 29 {
		t.Fatalf("got %+v", got)
	}
}

func TestMonthlyNthWeekday(t *testing.T) {
	cases := []struct {
		name string
		r    Recurrence
		want []string // local dates
	}{
		{"second Tuesday", Recurrence{Frequency: "monthly", MonthWeek: 2, MonthWeekday: 2}, []string{"2026-11-10", "2026-12-08", "2027-01-12"}},
		{"first Sunday", Recurrence{Frequency: "monthly", MonthWeek: 1, MonthWeekday: 0}, []string{"2026-11-01", "2026-12-06", "2027-01-03"}},
		{"last Friday", Recurrence{Frequency: "monthly", MonthWeek: -1, MonthWeekday: 5}, []string{"2026-10-30", "2026-11-27", "2026-12-25"}},
		{"fourth Thursday", Recurrence{Frequency: "monthly", MonthWeek: 4, MonthWeekday: 4}, []string{"2026-10-22", "2026-11-26", "2026-12-24"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := c.r
			r.StartTime, r.DurationMinutes, r.Timezone = "22:00", 60, "UTC"
			got := r.Next(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), 3, nil)
			if len(got) != 3 {
				t.Fatalf("want 3, got %d", len(got))
			}
			for i, w := range c.want {
				if d := got[i].StartsAt.Format("2006-01-02"); d != w {
					t.Errorf("occurrence %d on %s, want %s", i, d, w)
				}
			}
		})
	}
}

func TestOccurrenceInProgressComesFirst(t *testing.T) {
	r := Recurrence{Frequency: "weekly", Weekdays: []int{4}, StartTime: "10:00", DurationMinutes: 180, Timezone: "UTC"}
	// Thursday 2026-10-22 11:00: inside 10:00-13:00.
	now := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	got := r.Next(now, 2, nil)
	if len(got) != 2 || !got[0].StartsAt.Equal(time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %+v", got)
	}
	if !got[1].StartsAt.Equal(time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("second occurrence %s", got[1].StartsAt)
	}
}

func TestSkippedOccurrenceIsLeftOut(t *testing.T) {
	r := Recurrence{Frequency: "weekly", Weekdays: []int{0}, StartTime: "02:00", DurationMinutes: 60, Timezone: "UTC"}
	first := time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC)
	got := r.Next(time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), 1, []time.Time{first})
	if len(got) != 1 || !got[0].StartsAt.Equal(first.AddDate(0, 0, 7)) {
		t.Fatalf("got %+v", got)
	}
}

func TestEndsOnStopsTheSeries(t *testing.T) {
	r := Recurrence{Frequency: "weekly", Weekdays: []int{0}, StartTime: "02:00", DurationMinutes: 60, Timezone: "UTC", EndsOn: "2026-11-01"}
	got := r.Next(time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), 5, nil)
	if len(got) != 2 {
		t.Fatalf("want the 25th and the 1st, got %+v", got)
	}
	if after := r.Next(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), 1, nil); len(after) != 0 {
		t.Errorf("series continued past its end: %+v", after)
	}
}

func TestRecurrenceValidation(t *testing.T) {
	ok := Recurrence{Frequency: "weekly", Weekdays: []int{0}, StartTime: "02:00", DurationMinutes: 60, Timezone: "Europe/Stockholm"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid recurrence refused: %v", err)
	}
	bad := map[string]func(*Recurrence){
		"no weekdays":      func(r *Recurrence) { r.Weekdays = nil },
		"weekday 7":        func(r *Recurrence) { r.Weekdays = []int{7} },
		"bad time":         func(r *Recurrence) { r.StartTime = "25:00" },
		"no duration":      func(r *Recurrence) { r.DurationMinutes = 0 },
		"over seven days":  func(r *Recurrence) { r.DurationMinutes = 7*24*60 + 1 },
		"no timezone":      func(r *Recurrence) { r.Timezone = "" },
		"unknown timezone": func(r *Recurrence) { r.Timezone = "Mars/Olympus" },
		"bad end date":     func(r *Recurrence) { r.EndsOn = "next year" },
		"bad frequency":    func(r *Recurrence) { r.Frequency = "daily" },
		"fifth week":       func(r *Recurrence) { r.Frequency, r.MonthWeek = "monthly", 5 },
	}
	for name, mutate := range bad {
		r := ok
		r.Weekdays = append([]int(nil), ok.Weekdays...)
		mutate(&r)
		if r.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
