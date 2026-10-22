// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// SignalTables are the tables that hold telemetry, by signal. Each is
// partitioned by day (toDate(Timestamp)), which is what makes the
// per-day figures below readable from part metadata alone.
var SignalTables = map[string]string{
	"traces":  "traces",
	"logs":    "logs",
	"metrics": "metrics",
}

// DayVolume is what one signal stored for one UTC day: rows, and bytes
// as they sit on disk (compressed).
type DayVolume struct {
	Day   string `json:"day"` // YYYY-MM-DD, UTC
	Rows  uint64 `json:"rows"`
	Bytes uint64 `json:"bytes"`
}

// SignalVolume is what the instance holds for one signal.
type SignalVolume struct {
	Rows  uint64 `json:"rows"`
	Bytes uint64 `json:"bytes"`
	// OldestDay and NewestDay bound the days held; DaysHeld counts the
	// days with any data. Empty when the signal holds nothing.
	OldestDay string `json:"oldest_day,omitempty"`
	NewestDay string `json:"newest_day,omitempty"`
	DaysHeld  int    `json:"days_held"`
	// LastFullDay is yesterday (UTC), the most recent day that is
	// complete: the ingest rate. Today is the day so far. A day with no
	// data reports zeros rather than disappearing, because "nothing
	// arrived yesterday" is the reading that matters most.
	LastFullDay DayVolume `json:"last_full_day"`
	Today       DayVolume `json:"today"`
}

// MeasureSignals reads, per signal, what is held and what arrived
// yesterday and today. Part metadata only (system.parts), no data is
// read, so it is as cheap as MeasureStorage.
//
// Data that arrives late lands in the partition of its own timestamp,
// so yesterday's figure can still grow a little after midnight. That is
// what was stored for that day, which is the question being asked.
func MeasureSignals(ctx context.Context, conn driver.Conn, now time.Time) (map[string]SignalVolume, error) {
	tables := make([]string, 0, len(SignalTables))
	signalOf := map[string]string{}
	for signal, table := range SignalTables {
		tables = append(tables, table)
		signalOf[table] = signal
	}
	sort.Strings(tables)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(tables)), ",")
	args := make([]any, 0, len(tables))
	for _, t := range tables {
		args = append(args, t)
	}
	q := `
		SELECT table, partition, toUInt64(sum(rows)), toUInt64(sum(bytes_on_disk))
		FROM system.parts
		WHERE active AND database = currentDatabase() AND table IN (` + placeholders + `)
		GROUP BY table, partition`
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("measure signals: %w", err)
	}
	defer rows.Close()

	today := now.UTC().Format("2006-01-02")
	yesterday := now.UTC().AddDate(0, 0, -1).Format("2006-01-02")
	out := map[string]SignalVolume{}
	for signal := range SignalTables {
		out[signal] = SignalVolume{
			LastFullDay: DayVolume{Day: yesterday},
			Today:       DayVolume{Day: today},
		}
	}
	for rows.Next() {
		var (
			table, day  string
			nRows, size uint64
		)
		if err := rows.Scan(&table, &day, &nRows, &size); err != nil {
			return nil, fmt.Errorf("measure signals: %w", err)
		}
		signal, ok := signalOf[table]
		if !ok {
			continue
		}
		v := out[signal]
		v.Rows += nRows
		v.Bytes += size
		if nRows > 0 {
			v.DaysHeld++
			if v.OldestDay == "" || day < v.OldestDay {
				v.OldestDay = day
			}
			if day > v.NewestDay {
				v.NewestDay = day
			}
		}
		switch day {
		case yesterday:
			v.LastFullDay.Rows, v.LastFullDay.Bytes = nRows, size
		case today:
			v.Today.Rows, v.Today.Bytes = nRows, size
		}
		out[signal] = v
	}
	return out, rows.Err()
}

// Disk is one of ClickHouse's disks as ClickHouse itself reports it.
type Disk struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	FreeBytes  uint64 `json:"free_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
	// KeepFreeBytes is space ClickHouse is configured never to use, so
	// it is free on the filesystem but not to ClickHouse.
	KeepFreeBytes uint64 `json:"keep_free_bytes"`
}

// MeasureDisks reads system.disks: what the filesystems ClickHouse
// writes to have left. Free space is the operating system's figure, so
// it includes whatever else shares the disk.
func MeasureDisks(ctx context.Context, conn driver.Conn) ([]Disk, error) {
	rows, err := conn.Query(ctx, `
		SELECT name, path, toUInt64(free_space), toUInt64(total_space), toUInt64(keep_free_space)
		FROM system.disks
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("measure disks: %w", err)
	}
	defer rows.Close()
	var out []Disk
	for rows.Next() {
		var d Disk
		if err := rows.Scan(&d.Name, &d.Path, &d.FreeBytes, &d.TotalBytes, &d.KeepFreeBytes); err != nil {
			return nil, fmt.Errorf("measure disks: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
