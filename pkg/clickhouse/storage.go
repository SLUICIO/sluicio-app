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
// Reaching the limit stops ingest; ingest resumes once usage falls below
// ResumeFraction of it. The gap keeps an instance hovering at the limit -
// retention dropping a part, the next batch filling it - from flipping
// between accepting and refusing every minute.
type StorageLimit struct {
	Bytes uint64
}

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
// disks are sold) into a StorageLimit. Zero or negative means no limit.
func StorageLimitFromGB(gb float64) StorageLimit {
	if gb <= 0 {
		return StorageLimit{}
	}
	return StorageLimit{Bytes: uint64(gb * 1e9)}
}

// StorageLimitEnv names the deployment's storage limit, in decimal GB.
const StorageLimitEnv = "SLUICIO_STORAGE_LIMIT_GB"

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
	return StorageLimitFromGB(gb), nil
}
