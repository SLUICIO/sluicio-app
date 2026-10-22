// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package maintenance

import (
	"errors"
	"fmt"
	"sort"
	"time"
	// Recurrences are computed in a named timezone, and a self-built image
	// without a zoneinfo directory would otherwise refuse every one.
	_ "time/tzdata"
)

// Recurrence is when a recurring maintenance window happens (issue #9):
// weekly on chosen weekdays, or monthly on the Nth weekday ("the second
// Tuesday"), at a wall-clock time in a named timezone.
//
// Wall-clock time is the point. "Sunday 02:00" stays 02:00 across a
// daylight-saving change because each occurrence is placed with the tz
// database, never with a fixed offset. The duration is elapsed time, so a
// two-hour window over the switch still lasts two hours. A start time
// that falls in the hour skipped in spring moves to where the tz database
// normalizes it; one in the hour repeated in autumn is one of the two.
type Recurrence struct {
	Frequency string `json:"frequency"` // "weekly" | "monthly"
	// Weekdays, for weekly: 0 = Sunday … 6 = Saturday.
	Weekdays []int `json:"weekdays,omitempty"`
	// MonthWeek and MonthWeekday, for monthly: the Nth (1-4, or -1 for
	// the last) such weekday of the month.
	MonthWeek    int `json:"month_week,omitempty"`
	MonthWeekday int `json:"month_weekday,omitempty"`
	// StartTime is local wall-clock "HH:MM" in Timezone.
	StartTime       string `json:"start_time"`
	DurationMinutes int    `json:"duration_minutes"`
	Timezone        string `json:"timezone"`
	// EndsOn, optional, is the last local date ("YYYY-MM-DD") an
	// occurrence may start on.
	EndsOn string `json:"ends_on,omitempty"`
}

// Occurrence is one instance of a recurrence.
type Occurrence struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

// occurrenceHorizon bounds how far ahead an occurrence is searched for. A
// monthly recurrence always has one within a few weeks; this only stops a
// recurrence whose end date has passed from scanning forever.
const occurrenceHorizon = 400 * 24 * time.Hour

// Validate checks the recurrence is complete and within bounds. The
// duration bound is the one-shot window's: it applies to EACH occurrence,
// not the series, which may run indefinitely.
func (r Recurrence) Validate() error {
	switch r.Frequency {
	case "weekly":
		if len(r.Weekdays) == 0 {
			return errors.New("a weekly recurrence needs at least one weekday")
		}
		for _, d := range r.Weekdays {
			if d < 0 || d > 6 {
				return fmt.Errorf("weekday %d is out of range (0 = Sunday … 6 = Saturday)", d)
			}
		}
	case "monthly":
		if r.MonthWeek != -1 && (r.MonthWeek < 1 || r.MonthWeek > 4) {
			return errors.New("month_week must be 1-4, or -1 for the last")
		}
		if r.MonthWeekday < 0 || r.MonthWeekday > 6 {
			return errors.New("month_weekday must be 0 (Sunday) to 6 (Saturday)")
		}
	default:
		return errors.New(`frequency must be "weekly" or "monthly"`)
	}
	if _, _, err := r.clock(); err != nil {
		return err
	}
	if r.DurationMinutes <= 0 {
		return errors.New("duration_minutes must be positive")
	}
	if time.Duration(r.DurationMinutes)*time.Minute > MaxWindowDuration {
		return fmt.Errorf("an occurrence may last at most %d days", int(MaxWindowDuration.Hours()/24))
	}
	if r.Timezone == "" {
		return errors.New("timezone is required, e.g. Europe/Stockholm")
	}
	if _, err := time.LoadLocation(r.Timezone); err != nil {
		return fmt.Errorf("unknown timezone %q", r.Timezone)
	}
	if r.EndsOn != "" {
		if _, err := time.Parse("2006-01-02", r.EndsOn); err != nil {
			return errors.New("ends_on must be a date, YYYY-MM-DD")
		}
	}
	return nil
}

// Normalized returns the recurrence with only the fields its frequency
// uses, weekdays sorted and de-duplicated, so what is stored is what
// means something.
func (r Recurrence) Normalized() Recurrence {
	out := Recurrence{
		Frequency:       r.Frequency,
		StartTime:       r.StartTime,
		DurationMinutes: r.DurationMinutes,
		Timezone:        r.Timezone,
		EndsOn:          r.EndsOn,
	}
	if r.Frequency == "weekly" {
		seen := map[int]bool{}
		for _, d := range r.Weekdays {
			if !seen[d] {
				seen[d] = true
				out.Weekdays = append(out.Weekdays, d)
			}
		}
		sort.Ints(out.Weekdays)
	} else {
		out.MonthWeek, out.MonthWeekday = r.MonthWeek, r.MonthWeekday
	}
	return out
}

func (r Recurrence) clock() (hour, minute int, err error) {
	t, perr := time.Parse("15:04", r.StartTime)
	if perr != nil {
		return 0, 0, errors.New(`start_time must be "HH:MM", 24-hour`)
	}
	return t.Hour(), t.Minute(), nil
}

// matches reports whether a local date hosts an occurrence.
func (r Recurrence) matches(y int, m time.Month, d int, wd time.Weekday) bool {
	switch r.Frequency {
	case "weekly":
		for _, w := range r.Weekdays {
			if time.Weekday(w) == wd {
				return true
			}
		}
		return false
	case "monthly":
		if time.Weekday(r.MonthWeekday) != wd {
			return false
		}
		if r.MonthWeek == -1 {
			daysInMonth := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
			return d+7 > daysInMonth
		}
		return (d-1)/7+1 == r.MonthWeek
	}
	return false
}

// Next returns up to n occurrences that have not ended by `after`, in
// order: the one in progress first, if any. skip lists occurrence starts
// to leave out. An invalid recurrence yields none.
func (r Recurrence) Next(after time.Time, n int, skip []time.Time) []Occurrence {
	if r.Validate() != nil || n <= 0 {
		return nil
	}
	loc, _ := time.LoadLocation(r.Timezone)
	hour, minute, _ := r.clock()
	dur := time.Duration(r.DurationMinutes) * time.Minute
	var last time.Time
	hasEnd := false
	if r.EndsOn != "" {
		e, _ := time.ParseInLocation("2006-01-02", r.EndsOn, loc)
		last, hasEnd = e, true
	}
	skipped := map[int64]bool{}
	for _, s := range skip {
		skipped[s.Unix()] = true
	}

	var out []Occurrence
	// Start far enough back to catch an occurrence still in progress.
	day := after.Add(-dur).In(loc)
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	limit := after.Add(occurrenceHorizon)
	for len(out) < n && day.Before(limit) {
		y, m, d := day.Date()
		if hasEnd && day.After(last) {
			break
		}
		if r.matches(y, m, d, day.Weekday()) {
			start := time.Date(y, m, d, hour, minute, 0, 0, loc)
			end := start.Add(dur)
			if end.After(after) && !skipped[start.Unix()] {
				out = append(out, Occurrence{StartsAt: start.UTC(), EndsAt: end.UTC()})
			}
		}
		day = time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	}
	return out
}
