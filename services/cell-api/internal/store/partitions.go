// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AttrPartitionRow is what one span attribute looks like across one
// service's traces (issue #18): how many traces carry it, how many
// values it takes, and how many values a trace carries.
type AttrPartitionRow struct {
	Service         string
	Key             string
	TracesWithKey   uint64
	TracesTotal     uint64
	DistinctValues  uint64
	TraceValuePairs uint64
	// NumericValues is how many of the distinct values parse as numbers,
	// BooleanValues how many are true or false.
	NumericValues uint64
	BooleanValues uint64
}

// AttrPartitionStats measures every span attribute of the given services
// over [from, to], on a sample of up to perService spans per service so a
// busy service does not crowd out a quiet one, and walking every span's
// attribute map over a long window stays bounded. Only keys with at most
// maxValues distinct values are returned: anything wider is an
// identifier and is dropped on the server.
func (s *Store) AttrPartitionStats(ctx context.Context, services []string, from, to time.Time, perService int, maxValues uint64) ([]AttrPartitionRow, error) {
	if len(services) == 0 {
		return nil, nil
	}
	if perService <= 0 {
		perService = 20000
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(services)), ",")
	args := []any{from, to}
	for _, n := range services {
		args = append(args, n)
	}
	args = append(args, perService, maxValues)
	rows, err := s.conn.Query(ctx, `
		WITH sampled AS (
			SELECT ServiceName, TraceId, SpanAttributes
			FROM traces
			WHERE Timestamp >= ? AND Timestamp <= ? AND ServiceName IN (`+in+`)
			LIMIT ? BY ServiceName
		),
		totals AS (
			SELECT ServiceName, uniqExact(TraceId) AS traces FROM sampled GROUP BY ServiceName
		)
		SELECT s.ServiceName,
		       kv.1                              AS k,
		       toUInt64(uniqExact(s.TraceId))     AS with_key,
		       toUInt64(any(t.traces))            AS total,
		       toUInt64(uniqExact(kv.2))          AS distinct_values,
		       toUInt64(uniqExact((s.TraceId, kv.2))) AS pairs,
		       toUInt64(uniqExactIf(kv.2, toFloat64OrNull(kv.2) IS NOT NULL)) AS numeric_values,
		       toUInt64(uniqExactIf(kv.2, lower(kv.2) IN ('true', 'false'))) AS boolean_values
		FROM sampled AS s
		ARRAY JOIN SpanAttributes AS kv
		INNER JOIN totals AS t ON t.ServiceName = s.ServiceName
		GROUP BY s.ServiceName, k
		HAVING distinct_values <= ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("attribute partition stats: %w", err)
	}
	defer rows.Close()
	var out []AttrPartitionRow
	for rows.Next() {
		var r AttrPartitionRow
		if err := rows.Scan(&r.Service, &r.Key, &r.TracesWithKey, &r.TracesTotal, &r.DistinctValues, &r.TraceValuePairs, &r.NumericValues, &r.BooleanValues); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PartitionValueRow is one value of a partitioning attribute and the
// traffic carrying it.
type PartitionValueRow struct {
	Value       string
	Traces      uint64
	ErrorTraces uint64
	FirstSeen   time.Time
	LastSeen    time.Time
}

// PartitionValues breaks one service's traffic down by one span
// attribute over [from, to], busiest value first. Unsampled: it runs
// only for the few attributes AttrPartitionStats nominated.
func (s *Store) PartitionValues(ctx context.Context, service, key string, from, to time.Time, limit int) ([]PartitionValueRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.conn.Query(ctx, `
		SELECT SpanAttributes[?] AS v,
		       toUInt64(uniqExact(TraceId)),
		       toUInt64(uniqExactIf(TraceId, StatusCode = 'Error')),
		       min(Timestamp), max(Timestamp)
		FROM traces
		WHERE Timestamp >= ? AND Timestamp <= ? AND ServiceName = ? AND mapContains(SpanAttributes, ?)
		GROUP BY v
		ORDER BY 2 DESC, v
		LIMIT ?`, key, from, to, service, key, limit)
	if err != nil {
		return nil, fmt.Errorf("partition values: %w", err)
	}
	defer rows.Close()
	var out []PartitionValueRow
	for rows.Next() {
		var r PartitionValueRow
		if err := rows.Scan(&r.Value, &r.Traces, &r.ErrorTraces, &r.FirstSeen, &r.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
