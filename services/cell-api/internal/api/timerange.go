// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TimeRange is the canonical (from, to) time window every read
// endpoint operates over. The handler layer parses HTTP query params
// into this; the store layer takes the two timestamps directly.
type TimeRange struct {
	From time.Time
	To   time.Time
}

// Window returns a JSON-friendly representation for response bodies.
func (r TimeRange) Window() WindowSummary {
	return WindowSummary{From: r.From, To: r.To}
}

// ParseRange reads the requested time range from `?range=…` (or the
// legacy `?window=…`). Two formats are accepted:
//
//   - **Relative** like `1h`, `15m`, `2d` — parsed as a Go duration
//     anchored to "now". `2d` is normalized to `48h` since the stdlib
//     duration parser does not handle days.
//   - **Absolute** like `2026-05-12T05:15/2026-05-12T16:15` (RFC 3339
//     timestamps separated by a slash). Both `Z` (UTC) and the
//     no-seconds `datetime-local` shape are accepted; missing
//     timezone is treated as UTC.
//
// If neither parameter is present, or parsing fails, the supplied
// fallback duration is used (anchored to now).
func ParseRange(r *http.Request, fallback time.Duration) TimeRange {
	q := r.URL.Query()
	for _, name := range []string{"range", "window"} {
		if v := strings.TrimSpace(q.Get(name)); v != "" {
			if rng, ok := parseRangeString(v); ok {
				return rng
			}
		}
	}
	now := time.Now().UTC()
	return TimeRange{From: now.Add(-fallback), To: now}
}

func parseRangeString(s string) (TimeRange, bool) {
	if strings.Contains(s, "/") {
		parts := strings.SplitN(s, "/", 2)
		from, ok1 := parseAbsolute(parts[0])
		to, ok2 := parseAbsolute(parts[1])
		if ok1 && ok2 && to.After(from) {
			// Cap to one year to defend against accidental huge
			// scans that would tank ClickHouse.
			if to.Sub(from) > 365*24*time.Hour {
				return TimeRange{}, false
			}
			return TimeRange{From: from, To: to}, true
		}
		return TimeRange{}, false
	}

	d, err := parseRelative(s)
	if err != nil || d <= 0 || d > 365*24*time.Hour {
		return TimeRange{}, false
	}
	now := time.Now().UTC()
	return TimeRange{From: now.Add(-d), To: now}, true
}

// parseRelative extends time.ParseDuration to understand "Nd" (days)
// since the stdlib only supports h/m/s/ms/us/ns. "2d" → 48h.
func parseRelative(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		var n int
		if _, err := fmt.Sscanf(s, "%dd", &n); err != nil {
			return 0, err
		}
		if n <= 0 {
			return 0, fmt.Errorf("non-positive duration")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func parseAbsolute(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
