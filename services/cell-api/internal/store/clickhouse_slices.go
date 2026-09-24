// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// IntegrationSlice is one integration's selection of spans: the spans of
// its member services that its matcher predicate selects. It is the same
// selection the Messages tab searches (ServiceName IN members AND the
// DNF predicate), so a count taken here is a count of the messages the
// reader would find there.
type IntegrationSlice struct {
	// Key identifies the slice in the result. The integration id, as a
	// string, for every caller today.
	Key      string
	Services []string
	Groups   [][]LogAttrFilter
}

// SliceStat is one slice's traffic over a window.
type SliceStat struct {
	// Traces is the distinct traces with a selected span. Zero under
	// SliceQuery.ErrorsOnly, which does not read the healthy traffic.
	Traces uint64
	// ErrorTraces is the distinct traces with a selected span in error,
	// acknowledgements ignored: what the list card has always shown.
	ErrorTraces uint64
	// OpenErrorTraces is ErrorTraces with each service's "clear errors"
	// watermark applied: an error span counts only if it is newer than
	// the watermark of the service that emitted it. This is the number a
	// status reads, so clearing a service's errors clears the slices that
	// carried them, exactly as it clears the service.
	OpenErrorTraces uint64
	// The newest open error, for pointing a reader at it: when, which
	// member emitted it, and its trace. Zero when there is none.
	LastErrorAt      time.Time
	LastErrorService string
	SampleTraceID    string
}

// SliceQuery bounds a SliceTraceStats read.
type SliceQuery struct {
	From, To time.Time
	// AckedUntil is each service's clear-errors watermark. Services
	// without one, and watermarks before From, change nothing.
	AckedUntil map[string]time.Time
	// ErrorsOnly reads error spans alone. For a long lookback that only
	// needs the error side, it avoids counting every healthy trace.
	ErrorsOnly bool
}

// sliceBatchSize caps how many slices go into one statement. A slice
// with include_descendants compiles to a subtree lookup of several
// hundred bytes per group, and ClickHouse refuses a statement larger
// than max_query_size (256 KiB by default). At this size a cell of a few
// hundred integrations still answers in a handful of round trips, not
// one per integration.
const sliceBatchSize = 64

// SliceTraceStats counts many integrations' slices in one statement per
// sliceBatchSize, never one per integration.
//
// It exists for the integration status. A shared runtime - an Airflow
// scheduler running every DAG, a Node-RED process running every flow -
// carries many integrations, and the question "is this one failing" can
// only be asked of its own slice. Asked of the member service, one
// failing flow marks every sibling. The list asks it for every row on
// every render, which is why it is batched: each slice is a branch of a
// UNION ALL, aggregated on the server, and one row per slice comes back.
//
// A slice with no services is left out of the result, as is a slice
// whose branch returned nothing; callers read an absent key as zero.
func (s *Store) SliceTraceStats(ctx context.Context, slices []IntegrationSlice, q SliceQuery) (map[string]SliceStat, error) {
	out := make(map[string]SliceStat, len(slices))
	for start := 0; start < len(slices); start += sliceBatchSize {
		end := min(start+sliceBatchSize, len(slices))
		sql, args := sliceTraceStatsSQL(slices[start:end], q)
		if sql == "" {
			continue
		}
		rows, err := s.conn.Query(ctx, sql, args...)
		if err != nil {
			return nil, fmt.Errorf("slice trace stats: %w", err)
		}
		for rows.Next() {
			var (
				key string
				st  SliceStat
			)
			if err := rows.Scan(&key, &st.Traces, &st.ErrorTraces, &st.OpenErrorTraces,
				&st.LastErrorAt, &st.LastErrorService, &st.SampleTraceID); err != nil {
				rows.Close()
				return nil, err
			}
			if st.OpenErrorTraces == 0 {
				st.LastErrorAt, st.LastErrorService, st.SampleTraceID = time.Time{}, "", ""
			}
			out[key] = st
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("slice trace stats: %w", err)
		}
	}
	return out, nil
}

// sliceTraceStatsSQL renders the UNION ALL for one batch. Pure, so the
// placeholder order - the part that goes quietly wrong - is unit-tested.
func sliceTraceStatsSQL(slices []IntegrationSlice, q SliceQuery) (string, []any) {
	branches := make([]string, 0, len(slices))
	var args []any
	for _, sl := range slices {
		if len(sl.Services) == 0 {
			continue
		}
		open, openArgs := openErrorCondition(sl.Services, q)
		ph := make([]string, len(sl.Services))
		for i := range ph {
			ph[i] = "?"
		}
		where := "Timestamp >= ? AND Timestamp <= ? AND ServiceName IN (" + strings.Join(ph, ",") + ")"
		if q.ErrorsOnly {
			where += " AND StatusCode = 'Error'"
		}
		attrSQL, attrArgs := attrGroupsClause("SpanAttributes", sl.Groups, q.From, q.To)
		if attrSQL != "" {
			where += " AND " + attrSQL
		}
		traces := "toUInt64(uniqExact(TraceId))"
		if q.ErrorsOnly {
			traces = "toUInt64(0)"
		}
		// The open-error condition appears four times (count, newest
		// time, its service, its trace), so its args do too, in the
		// order the placeholders are read: the SELECT list first.
		branches = append(branches, `
		SELECT toString(?) AS k,
		       `+traces+` AS traces,
		       toUInt64(uniqExactIf(TraceId, StatusCode = 'Error')) AS errored,
		       toUInt64(uniqExactIf(TraceId, `+open+`)) AS open_errored,
		       maxIf(Timestamp, `+open+`) AS last_error_at,
		       argMaxIf(ServiceName, Timestamp, `+open+`) AS last_error_service,
		       argMaxIf(TraceId, Timestamp, `+open+`) AS sample
		FROM traces
		WHERE `+where)
		args = append(args, sl.Key)
		for range 4 {
			args = append(args, openArgs...)
		}
		args = append(args, q.From, q.To)
		for _, n := range sl.Services {
			args = append(args, n)
		}
		args = append(args, attrArgs...)
	}
	if len(branches) == 0 {
		return "", nil
	}
	return strings.Join(branches, "\n\t\tUNION ALL"), args
}

// openErrorCondition renders "this span is an error nobody has cleared":
// an error span, not at or before the watermark of the service that
// emitted it. Only watermarks inside the window can exclude anything, so
// only those are rendered - with no recent acknowledgement it is plain
// StatusCode = 'Error' and costs nothing extra.
func openErrorCondition(services []string, q SliceQuery) (string, []any) {
	cond := "StatusCode = 'Error'"
	var args []any
	names := make([]string, 0)
	for _, n := range services {
		if w, ok := q.AckedUntil[n]; ok && w.After(q.From) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		cond += " AND NOT (ServiceName = ? AND Timestamp <= ?)"
		args = append(args, n, q.AckedUntil[n])
	}
	return "(" + cond + ")", args
}
