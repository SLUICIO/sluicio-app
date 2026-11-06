// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package clickhouse

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// StorageUsage is how much disk the telemetry database occupies.
type StorageUsage struct {
	// Bytes is every active part of every table in the database, as it
	// sits on disk: compressed data, marks, indexes. It is what a storage
	// limit is measured against, because it is what fills the disk.
	Bytes uint64
	// ByTable splits Bytes per table (traces, logs, metrics, and the
	// smaller tables the instance keeps alongside them).
	ByTable map[string]uint64
}

// MeasureStorage reads the telemetry database's size from system.parts.
// Cheap - one aggregate over part metadata, no data is read - so it is
// safe to run every minute.
func MeasureStorage(ctx context.Context, conn driver.Conn) (StorageUsage, error) {
	const q = `
		SELECT table, toUInt64(sum(bytes_on_disk))
		FROM system.parts
		WHERE active AND database = currentDatabase()
		GROUP BY table`
	rows, err := conn.Query(ctx, q)
	if err != nil {
		return StorageUsage{}, fmt.Errorf("measure storage: %w", err)
	}
	defer rows.Close()
	out := StorageUsage{ByTable: map[string]uint64{}}
	for rows.Next() {
		var (
			table string
			bytes uint64
		)
		if err := rows.Scan(&table, &bytes); err != nil {
			return StorageUsage{}, fmt.Errorf("measure storage: %w", err)
		}
		out.ByTable[table] = bytes
		out.Bytes += bytes
	}
	return out, rows.Err()
}

// StorageLimit is a cap on StorageUsage.Bytes, with the state it implies.
//
// What reaching it does is Action: by default the oldest day of telemetry
// is deleted to make room, and collection only pauses when there is no
// older day left to delete. With StoragePause, reaching the limit stops
// ingest outright; ingest resumes once usage falls below ResumeFraction of
// it. The gap keeps an instance hovering at the limit - retention dropping
// a part, the next batch filling it - from flipping between accepting and
// refusing every minute.
type StorageLimit struct {
	Bytes  uint64
	Action StorageAction
}

// StorageAction is what an instance does when its storage limit is
// reached. The acceptable use policy makes deleting the oldest telemetry
// the default and pausing collection the alternative a customer may
// choose, so that monitoring keeps running unless someone decided
// otherwise.
type StorageAction string

const (
	// StorageDeleteOldest deletes the oldest day of telemetry, across
	// traces, logs and metrics, until usage is under the limit again.
	StorageDeleteOldest StorageAction = "delete_oldest"
	// StoragePause refuses new telemetry until usage falls under the
	// limit, keeping everything already stored.
	StoragePause StorageAction = "pause"
)

// ResumeFraction is the share of the limit usage must fall below before a
// stopped instance accepts data again.
const ResumeFraction = 0.98

// Stopped reports whether ingest should be refused at usage, given whether
// it was refused before. A zero limit never stops anything.
func (l StorageLimit) Stopped(usage uint64, wasStopped bool) bool {
	if l.Bytes == 0 {
		return false
	}
	if usage >= l.Bytes {
		return true
	}
	if wasStopped {
		return float64(usage) >= float64(l.Bytes)*ResumeFraction
	}
	return false
}

// StorageLimitFromGB turns a limit in decimal gigabytes (10^9 bytes, as
// disks are sold) into a StorageLimit that deletes the oldest telemetry
// when reached. Zero or negative means no limit.
func StorageLimitFromGB(gb float64) StorageLimit {
	if gb <= 0 {
		return StorageLimit{}
	}
	return StorageLimit{Bytes: uint64(gb * 1e9), Action: StorageDeleteOldest}
}

// StorageLimitEnv names the deployment's storage limit, in decimal GB.
const StorageLimitEnv = "SLUICIO_STORAGE_LIMIT_GB"

// StorageActionEnv names what reaching the limit does: delete_oldest (the
// default) or pause.
const StorageActionEnv = "SLUICIO_STORAGE_LIMIT_ACTION"

// StorageLimitFromEnv reads SLUICIO_STORAGE_LIMIT_GB. Unset or empty is no
// limit. A value that does not parse as a non-negative number is an
// error, not "no limit": a limit someone set and mistyped would otherwise
// vanish without a word, and an instance meant to stop at 50 GB would
// fill its disk.
func StorageLimitFromEnv() (StorageLimit, error) {
	raw := strings.TrimSpace(os.Getenv(StorageLimitEnv))
	if raw == "" {
		return StorageLimit{}, nil
	}
	gb, err := strconv.ParseFloat(raw, 64)
	if err != nil || gb < 0 {
		return StorageLimit{}, fmt.Errorf("%s=%q: want a number of gigabytes, e.g. 50 or 2.5", StorageLimitEnv, raw)
	}
	l := StorageLimitFromGB(gb)
	// A mistyped action is refused for the same reason as a mistyped
	// limit: an instance meant to keep its history would otherwise start
	// deleting it.
	switch a := StorageAction(strings.ToLower(strings.TrimSpace(os.Getenv(StorageActionEnv)))); a {
	case "", StorageDeleteOldest:
	case StoragePause:
		if l.Bytes > 0 {
			l.Action = StoragePause
		}
	default:
		return StorageLimit{}, fmt.Errorf("%s=%q: want delete_oldest or pause", StorageActionEnv, a)
	}
	return l, nil
}

// telemetryTables are the tables a storage limit deletes from, all
// partitioned by day (toDate(Timestamp)), so a day is one partition each.
var telemetryTables = []string{"traces", "logs", "metrics"}

// DeleteOldestDay drops the oldest day of telemetry before today from
// traces, logs and metrics, and returns that day as a partition id
// (YYYYMMDD), or "" when there is no earlier day left. Today is never
// dropped: it is the data the instance is there to show.
//
// A partition drop is a metadata operation, unlike a DELETE mutation, and
// frees the day's parts at once, so the next measurement sees the space.
func DeleteOldestDay(ctx context.Context, conn driver.Conn) (string, error) {
	var day string
	if err := conn.QueryRow(ctx, `
		SELECT min(partition_id)
		FROM system.parts
		WHERE active AND database = currentDatabase()
		  AND table IN ('traces', 'logs', 'metrics')
		  AND partition_id < toString(toYYYYMMDD(today()))`).Scan(&day); err != nil {
		return "", fmt.Errorf("find oldest day: %w", err)
	}
	if day == "" {
		return "", nil
	}
	// The id goes into the statement as a literal, so it is checked to be
	// the eight digits a toDate partition has rather than trusted.
	if len(day) != 8 || strings.Trim(day, "0123456789") != "" {
		return "", fmt.Errorf("unexpected partition id %q", day)
	}
	for _, t := range telemetryTables {
		// Dropping a partition a table does not have is a no-op.
		if err := conn.Exec(ctx, fmt.Sprintf("ALTER TABLE %s DROP PARTITION ID '%s'", t, day)); err != nil {
			return "", fmt.Errorf("drop %s day %s: %w", t, day, err)
		}
	}
	return day, nil
}
