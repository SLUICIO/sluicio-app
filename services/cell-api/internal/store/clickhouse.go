// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package store contains the cell-api's ClickHouse query layer. Each
// function maps cleanly to one of the API endpoints; the SQL is kept
// in a single place so it can be reviewed and tuned without touching
// the HTTP layer.
package store

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/facetmappings"
)

// Store exposes the read queries cell-api needs.
type Store struct {
	conn driver.Conn
}

// New returns a Store backed by the given ClickHouse connection.
func New(conn driver.Conn) *Store {
	return &Store{conn: conn}
}

// Conn exposes the underlying ClickHouse connection. Used by the
// servicetypes widget engine, which issues its own SQL but should
// share the same connection / pool as the rest of cell-api.
func (s *Store) Conn() driver.Conn {
	return s.conn
}

// ServiceRow is one row from the services-list query.
//
// Counts are at *trace* granularity — a trace is one logical unit of
// work, regardless of how many spans the service emitted within it.
// TraceCount is the number of distinct traces this service
// participated in over the window; ErrorTraceCount is the subset of
// those whose at-least-one span ended with status Error.
//
// FirstSeen is *all-time* across the whole table, not bounded by the
// listing's time range — so the value answers "when did we first
// hear from this service" rather than "when did it first send within
// this window". LastSeen and the counts are bounded by the window.
type ServiceRow struct {
	ServiceName      string
	ServiceNamespace string
	FirstSeen        time.Time
	LastSeen         time.Time
	TraceCount       uint64
	ErrorTraceCount  uint64
}

// ListServices returns one row per service seen in the given range,
// most recently active first. Each row carries an all-time FirstSeen
// joined in from the unbounded traces table.
func (s *Store) ListServices(ctx context.Context, from, to time.Time) ([]ServiceRow, error) {
	const q = `
		SELECT
			s.ServiceName,
			s.ServiceNamespace,
			s.LastSeen,
			s.TraceCount,
			s.ErrorTraceCount,
			f.FirstSeen
		FROM (
			SELECT
				ServiceName,
				any(ServiceNamespace) AS ServiceNamespace,
				max(Timestamp) AS LastSeen,
				toUInt64(uniqExact(TraceId)) AS TraceCount,
				toUInt64(uniqExactIf(TraceId, StatusCode = 'Error')) AS ErrorTraceCount
			FROM traces
			WHERE Timestamp >= ? AND Timestamp <= ?
			GROUP BY ServiceName
		) AS s
		LEFT JOIN (
			-- Bounded to the TTL window so this isn't a full-table
			-- scan on every Services page render. FirstSeen older than
			-- the TTL has been dropped on the server anyway, so this
			-- yields the same answer for any row that still exists.
			-- Long-term fix: source FirstSeen from the Postgres catalog
			-- (service_catalog.first_seen_at, maintained by the
			-- reconciler). Tracked in docs/performance-audit.md → P0-2.
			SELECT ServiceName, min(Timestamp) AS FirstSeen
			FROM traces
			WHERE Timestamp >= toDate(now()) - INTERVAL 30 DAY
			GROUP BY ServiceName
		) AS f ON f.ServiceName = s.ServiceName
		ORDER BY s.LastSeen DESC
		LIMIT 5000
	`
	rows, err := s.conn.Query(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()

	var out []ServiceRow
	for rows.Next() {
		var r ServiceRow
		if err := rows.Scan(
			&r.ServiceName, &r.ServiceNamespace, &r.LastSeen,
			&r.TraceCount, &r.ErrorTraceCount, &r.FirstSeen,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ServiceDiscovery is one row returned by DiscoverServices — the
// minimum info the catalog reconciler needs to keep the Postgres
// services table in sync.
type ServiceDiscovery struct {
	ServiceName      string
	ServiceNamespace string
	FirstSeen        time.Time
	LastSeen         time.Time
}

// DiscoverServices returns the distinct services seen across `traces`,
// `logs`, and `metrics` over the given window, with their first / last
// telemetry times in that window. A service that only ever emits metrics
// (e.g. a RabbitMQ exporter) or only logs is registered in the catalog
// just like one that emits traces. This is what the catalog reconciler
// uses to upsert the persisted services list — it's intentionally
// simpler than ListServices (no counts) so the reconciler can run
// frequently. All three tables lead their sorting key with ServiceName,
// so the GROUP BY is index-friendly. Empty service names are skipped.
func (s *Store) DiscoverServices(ctx context.Context, from, to time.Time) ([]ServiceDiscovery, error) {
	const q = `
		SELECT ServiceName,
		       any(ServiceNamespace) AS ServiceNamespace,
		       min(Timestamp)         AS FirstSeen,
		       max(Timestamp)         AS LastSeen
		FROM (
		    SELECT ServiceName, ServiceNamespace, Timestamp FROM traces
		        WHERE Timestamp >= ? AND Timestamp <= ? AND ServiceName != ''
		    UNION ALL
		    SELECT ServiceName, ServiceNamespace, Timestamp FROM logs
		        WHERE Timestamp >= ? AND Timestamp <= ? AND ServiceName != ''
		    UNION ALL
		    SELECT ServiceName, ServiceNamespace, Timestamp FROM metrics
		        WHERE Timestamp >= ? AND Timestamp <= ? AND ServiceName != ''
		)
		GROUP BY ServiceName`
	rows, err := s.conn.Query(ctx, q, from, to, from, to, from, to)
	if err != nil {
		return nil, fmt.Errorf("discover services: %w", err)
	}
	defer rows.Close()
	out := make([]ServiceDiscovery, 0)
	for rows.Next() {
		var r ServiceDiscovery
		if err := rows.Scan(&r.ServiceName, &r.ServiceNamespace, &r.FirstSeen, &r.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ServiceAttribute is one (service, key, value) tuple observed in the
// recent telemetry window. Used by the catalog reconciler to feed
// service_resource_attributes — the local snapshot the policy
// resolver joins against for attribute-based access decisions.
type ServiceAttribute struct {
	ServiceName string
	Key         string
	Value       string
}

// DiscoverServiceResourceAttributes returns the distinct (service,
// resource-attribute-key, value) tuples seen over the window. Used
// by the catalog reconciler to populate the local
// service_resource_attributes table. The query reads from the
// traces table — logs and metrics carry the same ResourceAttributes
// shape (it's the OTel resource), so sampling traces is enough.
//
// We cap the per-service value-count at a sane default to avoid a
// long tail of one-off attribute values from blowing up the snapshot
// when a service emits high-cardinality resource attrs (anti-pattern,
// but defensive).
func (s *Store) DiscoverServiceResourceAttributes(ctx context.Context, from, to time.Time) ([]ServiceAttribute, error) {
	// ARRAY JOIN over the Map directly yields (key, value) tuples in
	// lockstep — one row per map entry per source row. The previous
	// formulation with two parallel arrayJoin() calls produced an N²
	// cartesian product, then post-filtered with `value = expected`
	// to keep the matched N. At a service with 30 resource attributes
	// that's 900 rows of work per source row to keep 30; this version
	// does 30, period.
	const q = `
		SELECT ServiceName, kv.1 AS key, kv.2 AS value
		FROM traces
		ARRAY JOIN ResourceAttributes AS kv
		WHERE Timestamp >= ? AND Timestamp <= ?
		GROUP BY ServiceName, key, value
		ORDER BY ServiceName, key, value`
	rows, err := s.conn.Query(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("discover service resource attributes: %w", err)
	}
	defer rows.Close()
	out := make([]ServiceAttribute, 0)
	for rows.Next() {
		var sa ServiceAttribute
		if err := rows.Scan(&sa.ServiceName, &sa.Key, &sa.Value); err != nil {
			return nil, err
		}
		out = append(out, sa)
	}
	return out, rows.Err()
}

// DistinctTraceCounts returns the number of distinct traces that touched
// any of the given services in the window, and the subset of those whose
// at-least-one span (within those services) ended with status Error.
//
// DistinctSpanNames returns the distinct span names seen across the given
// services within the window, most-frequent first, capped at `limit`. Used
// to suggest start/stage spans when building a trace-completion rule (so the
// user picks from what actually flows through the integration rather than
// typing blind).
// attrs (when non-empty) AND-s extra span-attribute predicates — e.g. an
// integration's attribute matchers — so the span vocabulary reflects only
// the matching rows.
func (s *Store) DistinctSpanNames(ctx context.Context, serviceNames []string, from, to time.Time, limit int, attrGroups [][]LogAttrFilter) ([]string, error) {
	if len(serviceNames) == 0 {
		return []string{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	placeholders := make([]string, len(serviceNames))
	args := make([]any, 0, len(serviceNames)+2)
	for i, n := range serviceNames {
		placeholders[i] = "?"
		args = append(args, n)
	}
	args = append(args, from, to)
	attrSQL := ""
	if clause, cargs := attrGroupsClause("SpanAttributes", attrGroups); clause != "" {
		attrSQL = " AND " + clause
		args = append(args, cargs...)
	}
	q := `
		SELECT SpanName
		FROM traces
		WHERE ServiceName IN (` + strings.Join(placeholders, ",") + `)
		  AND Timestamp >= ? AND Timestamp <= ?
		  AND SpanName != ''` + attrSQL + `
		GROUP BY SpanName
		ORDER BY count() DESC
		LIMIT ` + strconv.Itoa(limit)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("distinct span names: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// This is the integration-level count: unlike summing per-service
// TraceCount (ListServices), a trace that spans two of the integration's
// services is counted once here — matching what the Messages tab shows,
// which groups by TraceId.
// attrs (when non-empty) AND-s span-attribute predicates — e.g. an
// integration's attribute matchers — so the counts reflect only traces
// that have a matching span. Pass nil for no attribute filtering.
func (s *Store) DistinctTraceCounts(ctx context.Context, serviceNames []string, from, to time.Time, attrGroups [][]LogAttrFilter) (total uint64, errored uint64, err error) {
	if len(serviceNames) == 0 {
		return 0, 0, nil
	}
	args := []any{from, to}
	placeholders := make([]string, len(serviceNames))
	for i, name := range serviceNames {
		placeholders[i] = "?"
		args = append(args, name)
	}
	attrSQL := ""
	if clause, cargs := attrGroupsClause("SpanAttributes", attrGroups); clause != "" {
		attrSQL = " AND " + clause
		args = append(args, cargs...)
	}
	q := `
		SELECT
			toUInt64(uniqExact(TraceId)) AS total,
			toUInt64(uniqExactIf(TraceId, StatusCode = 'Error')) AS errored
		FROM traces
		WHERE Timestamp >= ? AND Timestamp <= ?
		  AND ServiceName IN (` + strings.Join(placeholders, ",") + `)` + attrSQL
	if err := s.conn.QueryRow(ctx, q, args...).Scan(&total, &errored); err != nil {
		return 0, 0, fmt.Errorf("distinct trace counts: %w", err)
	}
	return total, errored, nil
}

// IntegrationTrafficSeries returns a fixed-length series (length `buckets`)
// of distinct trace counts bucketed evenly across [from,to] for the given
// service set — backs the dashboard's per-integration traffic sparkline.
// Empty buckets stay zero, so a quiet integration renders a flat line
// instead of falling back to placeholder data. The org filter is applied
// centrally via ctx (Phase-2 additional_table_filters), same as the other
// trace reads. Returns a zero-filled series (never nil) on empty input or
// error so the caller can render honestly regardless.
func (s *Store) IntegrationTrafficSeries(ctx context.Context, serviceNames []string, from, to time.Time, buckets int) ([]int, error) {
	if buckets <= 0 {
		buckets = 24
	}
	series := make([]int, buckets) // zero-filled = honest "no traffic" default
	if len(serviceNames) == 0 {
		return series, nil
	}
	totalSec := int64(to.Sub(from).Seconds())
	if totalSec <= 0 {
		return series, nil
	}
	step := totalSec / int64(buckets)
	if step < 1 {
		step = 1
	}
	// toStartOfInterval aligns buckets to absolute step boundaries from the
	// epoch; align `from` the same way so a bucket timestamp maps to an index.
	alignedFrom := from.Unix() - (from.Unix() % step)

	args := []any{from, to}
	placeholders := make([]string, len(serviceNames))
	for i, name := range serviceNames {
		placeholders[i] = "?"
		args = append(args, name)
	}
	q := fmt.Sprintf(`
		SELECT toStartOfInterval(Timestamp, INTERVAL %d SECOND) AS bucket,
		       toUInt64(uniqExact(TraceId)) AS cnt
		FROM traces
		WHERE Timestamp >= ? AND Timestamp <= ?
		  AND ServiceName IN (%s)
		GROUP BY bucket
		ORDER BY bucket ASC`, step, strings.Join(placeholders, ","))
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return series, fmt.Errorf("integration traffic series: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket time.Time
		var cnt uint64
		if err := rows.Scan(&bucket, &cnt); err != nil {
			return series, err
		}
		idx := int((bucket.Unix() - alignedFrom) / step)
		if idx < 0 {
			idx = 0
		}
		if idx >= buckets {
			idx = buckets - 1
		}
		series[idx] += int(cnt)
	}
	return series, rows.Err()
}

// CountDistinctTracesIn returns how many of the given trace_ids actually
// appear on the given services within [from,to]. Used to make the
// success-rate "delayed" count window-consistent: intersect the
// (sticky, all-time) open-delayed trace ids with the window's traffic so
// delayed can never exceed messages. Empty inputs → 0.
// ServiceTraceCountsFiltered returns, per service, the distinct trace
// count (total + errored) of traces that touch the service AND belong to
// an integration's attribute-matching set (a span anywhere in the trace,
// across the member services, satisfies every attribute matcher). Used for
// attribute-scoped flow-graph node counts. Keyed by service name; services
// with no matching traffic are omitted. Returns an empty map when attrs is
// empty (the caller should use the unfiltered per-service counts then).
func (s *Store) ServiceTraceCountsFiltered(ctx context.Context, services []string, from, to time.Time, attrGroups [][]LogAttrFilter) (map[string][2]uint64, error) {
	out := map[string][2]uint64{}
	if len(services) == 0 || len(attrGroups) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(services))
	for i := range services {
		placeholders[i] = "?"
	}
	in := strings.Join(placeholders, ",")
	args := []any{from, to}
	for _, n := range services {
		args = append(args, n)
	}
	// Subquery args: from, to, services, then attribute-clause args.
	args = append(args, from, to)
	for _, n := range services {
		args = append(args, n)
	}
	attrSQL := ""
	if clause, cargs := attrGroupsClause("SpanAttributes", attrGroups); clause != "" {
		attrSQL = " AND " + clause
		args = append(args, cargs...)
	}
	q := `
		SELECT ServiceName,
		       toUInt64(uniqExact(TraceId)) AS total,
		       toUInt64(uniqExactIf(TraceId, StatusCode = 'Error')) AS errored
		FROM traces
		WHERE Timestamp >= ? AND Timestamp <= ?
		  AND ServiceName IN (` + in + `)
		  AND TraceId IN (
		      SELECT TraceId FROM traces
		      WHERE Timestamp >= ? AND Timestamp <= ?
		        AND ServiceName IN (` + in + `)` + attrSQL + `
		  )
		GROUP BY ServiceName`
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("service trace counts filtered: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var total, errored uint64
		if err := rows.Scan(&name, &total, &errored); err != nil {
			return nil, err
		}
		out[name] = [2]uint64{total, errored}
	}
	return out, rows.Err()
}

func (s *Store) CountDistinctTracesIn(ctx context.Context, serviceNames, traceIDs []string, from, to time.Time, attrGroups [][]LogAttrFilter) (uint64, error) {
	if len(serviceNames) == 0 || len(traceIDs) == 0 {
		return 0, nil
	}
	args := []any{from, to}
	svc := make([]string, len(serviceNames))
	for i, n := range serviceNames {
		svc[i] = "?"
		args = append(args, n)
	}
	tids := make([]string, len(traceIDs))
	for i, t := range traceIDs {
		tids[i] = "?"
		args = append(args, t)
	}
	attrSQL := ""
	if clause, cargs := attrGroupsClause("SpanAttributes", attrGroups); clause != "" {
		attrSQL = " AND " + clause
		args = append(args, cargs...)
	}
	q := `
		SELECT toUInt64(uniqExact(TraceId))
		FROM traces
		WHERE Timestamp >= ? AND Timestamp <= ?
		  AND ServiceName IN (` + strings.Join(svc, ",") + `)
		  AND TraceId IN (` + strings.Join(tids, ",") + `)` + attrSQL
	var n uint64
	if err := s.conn.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count distinct traces in: %w", err)
	}
	return n, nil
}

// LatencyMsForServices returns the aggregate span duration in milliseconds
// over [from,to] for the given services, at the requested quantile (use
// 1.0 for max). samples is the span count behind the aggregate — 0 means no
// data (the caller treats that as "don't evaluate"). Powers the
// trace-latency ("response time > X") health check.
func (s *Store) LatencyMsForServices(ctx context.Context, serviceNames []string, quantile float64, from, to time.Time) (float64, uint64, error) {
	if len(serviceNames) == 0 {
		return 0, 0, nil
	}
	args := []any{quantile, from, to}
	svc := make([]string, len(serviceNames))
	for i, n := range serviceNames {
		svc[i] = "?"
		args = append(args, n)
	}
	q := `
		SELECT quantile(?)(DurationNs) / 1e6 AS latency_ms, toUInt64(count()) AS samples
		FROM traces
		WHERE Timestamp >= ? AND Timestamp <= ?
		  AND ServiceName IN (` + strings.Join(svc, ",") + `)`
	var latencyMs float64
	var samples uint64
	if err := s.conn.QueryRow(ctx, q, args...).Scan(&latencyMs, &samples); err != nil {
		return 0, 0, fmt.Errorf("latency for services: %w", err)
	}
	return safeFloat(latencyMs), samples, nil
}

// errorSpanCondition renders the "this span is a failure" condition for
// failed-trace counting: StatusCode='Error', AND-ed with any attribute
// predicates (over span + resource attributes). Returns the condition
// SQL and its bind args — the caller must place those args FIRST when
// the condition sits in the SELECT clause (placeholder order).
func errorSpanCondition(attrs []LogAttrFilter) (string, []any) {
	cond := "StatusCode = 'Error'"
	var args []any
	for _, f := range attrs {
		c, a := attrClauseIn("SpanAttributes", f)
		cond += " AND " + c
		args = append(args, a...)
	}
	return cond, args
}

// CountErrorTracesForServices counts the distinct traces that have at
// least one error span on any of the given services within [from,to].
// Powers the trace_error alert evaluator ("alert when this integration
// has ≥ N failed traces in the last W minutes"). attrs narrow which
// error spans count (AND-ed span/resource attribute predicates; keys
// validated upstream via attrKeyRe); empty = any error span.
func (s *Store) CountErrorTracesForServices(ctx context.Context, serviceNames []string, from, to time.Time, attrs []LogAttrFilter) (uint64, error) {
	if len(serviceNames) == 0 {
		return 0, nil
	}
	cond, condArgs := errorSpanCondition(attrs)
	// Placeholder order: the condition (SELECT clause) binds first, then
	// the Timestamp bounds, then the ServiceName IN-list.
	args := append(condArgs, from, to)
	svc := make([]string, len(serviceNames))
	for i, n := range serviceNames {
		svc[i] = "?"
		args = append(args, n)
	}
	q := `
		SELECT toUInt64(uniqExactIf(TraceId, ` + cond + `))
		FROM traces
		WHERE Timestamp >= ? AND Timestamp <= ?
		  AND ServiceName IN (` + strings.Join(svc, ",") + `)`
	var n uint64
	if err := s.conn.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count error traces for services: %w", err)
	}
	return n, nil
}

// DistinctTraceCountsGated is DistinctTraceCounts restricted to traces
// that contain at least one of the given start spans — the start-span
// gate of a trace-completion rule ("only traces that begin with 'Start'
// are this integration's messages"). When startSpans is empty it is
// equivalent to DistinctTraceCounts. Errored = the gated subset whose
// at-least-one span ended with status Error.
func (s *Store) DistinctTraceCountsGated(ctx context.Context, serviceNames, startSpans []string, from, to time.Time, attrGroups [][]LogAttrFilter) (total uint64, errored uint64, err error) {
	if len(serviceNames) == 0 {
		return 0, 0, nil
	}
	if len(startSpans) == 0 {
		return s.DistinctTraceCounts(ctx, serviceNames, from, to, attrGroups)
	}
	// Arg order must follow the order the placeholders appear in the SQL
	// string: the start-span IN-list (in the SELECT) first, then any
	// attribute-matcher aggregates (also in the SELECT), then the
	// Timestamp bounds, then the ServiceName IN-list (both in WHERE).
	svcPlaceholders := make([]string, len(serviceNames))
	for i := range serviceNames {
		svcPlaceholders[i] = "?"
	}
	startPlaceholders := make([]string, len(startSpans))
	for i := range startSpans {
		startPlaceholders[i] = "?"
	}
	args := make([]any, 0, len(startSpans)+2+len(serviceNames))
	for _, name := range startSpans {
		args = append(args, name)
	}
	// The integration's DNF attribute predicate becomes a single per-trace
	// "has a span matching the predicate" aggregate that must hold (HAVING).
	attrAgg := ""
	attrHaving := ""
	if clause, cargs := attrGroupsClause("SpanAttributes", attrGroups); clause != "" {
		attrAgg = ",\n\t\t\t\tcountIf(" + clause + ") > 0 AS has_attr"
		attrHaving = " AND has_attr"
		args = append(args, cargs...)
	}
	args = append(args, from, to)
	for _, name := range serviceNames {
		args = append(args, name)
	}
	q := `
		SELECT
			toUInt64(count())                AS total,
			toUInt64(countIf(has_error))     AS errored
		FROM (
			SELECT
				TraceId,
				countIf(StatusCode = 'Error') > 0 AS has_error,
				countIf(SpanName IN (` + strings.Join(startPlaceholders, ",") + `)) > 0 AS has_start` + attrAgg + `
			FROM traces
			WHERE Timestamp >= ? AND Timestamp <= ?
			  AND ServiceName IN (` + strings.Join(svcPlaceholders, ",") + `)
			GROUP BY TraceId
			HAVING has_start` + attrHaving + `
		)`
	if err := s.conn.QueryRow(ctx, q, args...).Scan(&total, &errored); err != nil {
		return 0, 0, fmt.Errorf("gated distinct trace counts: %w", err)
	}
	return total, errored, nil
}

// ServiceStatsRow is the aggregate stats for one service. Counts are
// at trace granularity; latency percentiles are per-span (operation
// duration) since that's the right grain for "how slow is this
// service".
type ServiceStatsRow struct {
	ServiceNamespace string
	TraceCount       uint64
	ErrorTraceCount  uint64
	P50DurationNs    float64
	P95DurationNs    float64
}

// ServiceStats returns aggregate stats for a single service over the range.
func (s *Store) ServiceStats(ctx context.Context, service string, from, to time.Time) (ServiceStatsRow, error) {
	const q = `
		SELECT
			any(ServiceNamespace),
			toUInt64(uniqExact(TraceId)),
			toUInt64(uniqExactIf(TraceId, StatusCode = 'Error')),
			quantile(0.5)(DurationNs),
			quantile(0.95)(DurationNs)
		FROM traces
		WHERE ServiceName = ? AND Timestamp >= ? AND Timestamp <= ?
	`
	row := s.conn.QueryRow(ctx, q, service, from, to)
	var r ServiceStatsRow
	if err := row.Scan(&r.ServiceNamespace, &r.TraceCount, &r.ErrorTraceCount, &r.P50DurationNs, &r.P95DurationNs); err != nil {
		return ServiceStatsRow{}, fmt.Errorf("service stats: %w", err)
	}
	// quantile() over an empty result set returns NaN, which encoding/json
	// then refuses to serialize — leaving the API with a 200 + empty body
	// and the frontend with a "JSON.parse: unexpected end of data" crash.
	// Normalise to 0 so an empty window just shows zero latency.
	r.P50DurationNs = safeFloat(r.P50DurationNs)
	r.P95DurationNs = safeFloat(r.P95DurationNs)
	return r, nil
}

// ServiceStatsSeriesResult holds the per-bucket time series behind the
// service's golden-signal sparklines. All slices are the same length
// (buckets) and zero-filled, so an empty window draws a flat line
// rather than a fabricated shape. ErrorRate is a fraction (0..1) to
// match ServiceStatsRow; latencies are in milliseconds.
type ServiceStatsSeriesResult struct {
	Traces    []int
	ErrorRate []float64
	P50Ms     []float64
	P95Ms     []float64
}

// ServiceStatsSeries returns the bucketed golden signals for a service
// over [from,to]: distinct traces, error rate, and p50/p95 latency per
// bucket. One pass over `traces`, mirroring IntegrationTrafficSeries's
// bucket alignment so a bucket timestamp maps cleanly to an index.
func (s *Store) ServiceStatsSeries(ctx context.Context, service string, from, to time.Time, buckets int) (ServiceStatsSeriesResult, error) {
	if buckets <= 0 {
		buckets = 24
	}
	res := ServiceStatsSeriesResult{
		Traces:    make([]int, buckets),
		ErrorRate: make([]float64, buckets),
		P50Ms:     make([]float64, buckets),
		P95Ms:     make([]float64, buckets),
	}
	totalSec := int64(to.Sub(from).Seconds())
	if totalSec <= 0 {
		return res, nil
	}
	step := totalSec / int64(buckets)
	if step < 1 {
		step = 1
	}
	alignedFrom := from.Unix() - (from.Unix() % step)

	q := fmt.Sprintf(`
		SELECT toStartOfInterval(Timestamp, INTERVAL %d SECOND) AS bucket,
		       toUInt64(uniqExact(TraceId)) AS traces,
		       toUInt64(uniqExactIf(TraceId, StatusCode = 'Error')) AS err_traces,
		       quantile(0.5)(DurationNs) AS p50,
		       quantile(0.95)(DurationNs) AS p95
		FROM traces
		WHERE ServiceName = ? AND Timestamp >= ? AND Timestamp <= ?
		GROUP BY bucket
		ORDER BY bucket ASC`, step)
	rows, err := s.conn.Query(ctx, q, service, from, to)
	if err != nil {
		return res, fmt.Errorf("service stats series: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket time.Time
		var traces, errTraces uint64
		var p50, p95 float64
		if err := rows.Scan(&bucket, &traces, &errTraces, &p50, &p95); err != nil {
			return res, err
		}
		idx := int((bucket.Unix() - alignedFrom) / step)
		if idx < 0 {
			idx = 0
		}
		if idx >= buckets {
			idx = buckets - 1
		}
		res.Traces[idx] = int(traces)
		if traces > 0 {
			res.ErrorRate[idx] = float64(errTraces) / float64(traces)
		}
		res.P50Ms[idx] = safeFloat(p50) / 1_000_000
		res.P95Ms[idx] = safeFloat(p95) / 1_000_000
	}
	return res, rows.Err()
}

// ErrorTraceCountSince counts the distinct error traces for a service
// with Timestamp strictly after `since` (and up to `to`). Backs the
// "clear errors" acknowledgement: after the team clears errors, a
// service's effective error count is the number of NEW error traces
// since the acknowledgement watermark, so it reads healthy again until
// fresh failures arrive. Same error definition as everywhere else
// (uniqExactIf(TraceId, StatusCode='Error')).
func (s *Store) ErrorTraceCountSince(ctx context.Context, service string, since, to time.Time, attrs []LogAttrFilter) (uint64, error) {
	cond, condArgs := errorSpanCondition(attrs)
	q := `
		SELECT toUInt64(uniqExactIf(TraceId, ` + cond + `))
		FROM traces
		WHERE ServiceName = ? AND Timestamp > ? AND Timestamp <= ?`
	args := append(condArgs, service, since, to)
	var n uint64
	if err := s.conn.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("error trace count since: %w", err)
	}
	return n, nil
}

// ServiceErrorStat is the per-service error summary behind the persistent
// "unacknowledged errors" feed: how many distinct error traces a service
// produced over the scanned window, when they first/last occurred, and a
// sample (the most recent) trace id to drill into.
type ServiceErrorStat struct {
	ServiceName   string
	ErrorTraces   uint64
	FirstErrorAt  time.Time
	LastErrorAt   time.Time
	SampleTraceID string
}

// ErrorTraceStatsByService returns, per service, the distinct error-trace
// count + first/last error time + the most-recent error trace id over
// [from, to]. Unlike the window-scoped error counts on the dashboards,
// the errors feed scans a broad retention window so an error stays
// surfaced until it's acknowledged (cleared), independent of the page's
// time selector. Same error definition as everywhere else (a span with
// StatusCode='Error').
func (s *Store) ErrorTraceStatsByService(ctx context.Context, from, to time.Time) ([]ServiceErrorStat, error) {
	const q = `
		SELECT ServiceName,
		       toUInt64(uniqExact(TraceId)) AS errs,
		       min(Timestamp) AS first_at,
		       max(Timestamp) AS last_at,
		       argMax(TraceId, Timestamp) AS sample
		FROM traces
		WHERE Timestamp >= ? AND Timestamp <= ? AND StatusCode = 'Error'
		GROUP BY ServiceName`
	rows, err := s.conn.Query(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("error trace stats by service: %w", err)
	}
	defer rows.Close()
	out := make([]ServiceErrorStat, 0)
	for rows.Next() {
		var st ServiceErrorStat
		if err := rows.Scan(&st.ServiceName, &st.ErrorTraces, &st.FirstErrorAt, &st.LastErrorAt, &st.SampleTraceID); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// safeFloat replaces NaN / ±Inf with 0 so the value round-trips through
// encoding/json. Latency / quantile aggregates over empty rows come back
// as NaN from ClickHouse; in that case 0 is the meaningful display value.
func safeFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// SpanRow is the projection used by the recent-spans and search queries.
// Both ResourceAttributes (attached to the originating service /
// process) and SpanAttributes (attached to the individual operation)
// are exposed so callers can present them as one merged view —
// OpenTelemetry semantically treats both as "attributes about this
// span", and that's how users think about them.
type SpanRow struct {
	Timestamp          time.Time
	TraceID            string
	SpanID             string
	ParentSpanID       string
	ServiceName        string
	SpanName           string
	SpanKind           string
	StatusCode         string
	StatusMessage      string
	DurationNs         uint64
	ResourceAttributes map[string]string
	SpanAttributes     map[string]string
}

// RecentSpans returns the most recent spans for a service in the range.
func (s *Store) RecentSpans(ctx context.Context, service string, from, to time.Time, limit int) ([]SpanRow, error) {
	const q = `
		SELECT
			Timestamp, TraceId, SpanId, ParentSpanId, ServiceName,
			SpanName, SpanKind, StatusCode, StatusMessage, DurationNs,
			ResourceAttributes, SpanAttributes
		FROM traces
		WHERE ServiceName = ? AND Timestamp >= ? AND Timestamp <= ?
		ORDER BY Timestamp DESC
		LIMIT ?
	`
	return s.querySpans(ctx, q, service, from, to, limit)
}

// failedFilterClause renders the optional WHERE applied after the
// matching/summary CTEs join, restricting results to traces that
// contain at least one Error-status span.
func failedFilterClause(onlyFailed bool) string {
	if onlyFailed {
		return "WHERE s.has_error = 1"
	}
	return ""
}

// MessageCursor is a keyset position for paging messages: the
// (latest_match, TraceId) of the last row of a page. LatestMatchNano is
// the latest matching span's timestamp in unix nanoseconds (integer
// comparison avoids a DateTime64 precision round-trip); TraceId is the
// unique tiebreaker. The next page is everything ordered strictly after.
type MessageCursor struct {
	LatestMatchNano int64
	TraceID         string
}

// MessagesSearchParams is the parameter bag for SearchMessages — the
// structured-filter sibling of SearchTraces. The caller is expected
// to have already translated the UI's filter list into the bits and
// pieces this struct carries (clause fragments + scalar hints); see
// messageviews.Build for the canonical translation.
type MessagesSearchParams struct {
	From          time.Time
	To            time.Time
	Limit         int
	ServiceFilter []string
	OnlyFailed    bool
	StatusOK      bool
	// Clauses are extra predicates already in ClickHouse syntax,
	// AND-ed into the matching CTE's WHERE. Each clause carries its
	// own bind placeholders (`?`) which the corresponding Args fill.
	Clauses []string
	Args    []any
	// Before is the keyset cursor; nil = first page.
	Before *MessageCursor
}

// SearchMessages is the structured-filter trace search. Behaves like
// SearchTraces but instead of one free-text needle it takes a list of
// already-built ClickHouse predicate fragments plus their arguments.
// Returns the same SearchTraceRow shape so the API layer is uniform.
//
// When the filter list is empty the result set is "every trace in the
// window" capped at limit, ordered most-recent-first — the UI uses
// this for the empty Messages view.
func (s *Store) SearchMessages(ctx context.Context, p MessagesSearchParams) ([]SearchTraceRow, error) {
	whereClauses := []string{"Timestamp >= ? AND Timestamp <= ?"}
	args := []any{p.From, p.To}
	for _, c := range p.Clauses {
		whereClauses = append(whereClauses, c)
	}
	args = append(args, p.Args...)

	if len(p.ServiceFilter) > 0 {
		placeholders := make([]string, len(p.ServiceFilter))
		for i, name := range p.ServiceFilter {
			placeholders[i] = "?"
			args = append(args, name)
		}
		whereClauses = append(whereClauses, "ServiceName IN ("+strings.Join(placeholders, ",")+")")
	}

	// The status filter (err only / ok only) and the keyset cursor both
	// go in the matching CTE's HAVING so they apply BEFORE the candidate
	// LIMIT. A post-JOIN status filter (the previous design) shrinks a
	// page below the limit, which both hides matches beyond the first
	// candidate window and makes keyset pagination stop early. has_error
	// is computed over the matched spans here and reused for display, so
	// the filter and the row's status pill always agree.
	havingConds := []string{}
	switch {
	case p.OnlyFailed:
		havingConds = append(havingConds, "has_error = 1")
	case p.StatusOK:
		havingConds = append(havingConds, "has_error = 0")
	}
	if p.Before != nil {
		havingConds = append(havingConds, "(toUnixTimestamp64Nano(latest_match) < ? OR (toUnixTimestamp64Nano(latest_match) = ? AND TraceId < ?))")
		args = append(args, p.Before.LatestMatchNano, p.Before.LatestMatchNano, p.Before.TraceID)
	}
	having := ""
	if len(havingConds) > 0 {
		having = "HAVING " + strings.Join(havingConds, " AND ")
	}

	args = append(args, p.Limit)

	sql := fmt.Sprintf(`
		WITH matching AS (
		    SELECT TraceId,
		           argMax(ServiceName, Timestamp)         AS matched_service,
		           argMax(SpanName, Timestamp)            AS matched_span_name,
		           argMax(ResourceAttributes, Timestamp)  AS matched_resource_attrs,
		           argMax(SpanAttributes, Timestamp)      AS matched_span_attrs,
		           max(Timestamp)                         AS latest_match,
		           countIf(StatusCode = 'Error') > 0      AS has_error
		    FROM traces
		    WHERE %s
		    GROUP BY TraceId
		    %s
		    ORDER BY latest_match DESC, TraceId DESC
		    LIMIT ?
		),
		summary AS (
		    SELECT TraceId,
		           min(Timestamp)                                                                                     AS trace_start,
		           (max(toUnixTimestamp64Nano(Timestamp) + DurationNs) - min(toUnixTimestamp64Nano(Timestamp))) / 1000000 AS duration_ms,
		           toUInt64(count())                                                                                  AS total_spans,
		           toUInt64(length(arrayDistinct(groupArray(ServiceName))))                                           AS service_count
		    FROM traces
		    WHERE TraceId IN (SELECT TraceId FROM matching)
		    GROUP BY TraceId
		)
		SELECT
		    s.TraceId,
		    s.trace_start,
		    s.duration_ms,
		    m.has_error,
		    s.total_spans,
		    s.service_count,
		    m.matched_service,
		    m.matched_span_name,
		    m.matched_resource_attrs,
		    m.matched_span_attrs,
		    m.latest_match
		FROM summary AS s
		INNER JOIN matching AS m ON s.TraceId = m.TraceId
		ORDER BY m.latest_match DESC, m.TraceId DESC
	`, strings.Join(whereClauses, " AND "), having)

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	var out []SearchTraceRow
	for rows.Next() {
		var r SearchTraceRow
		if err := rows.Scan(
			&r.TraceID, &r.TraceStart, &r.DurationMs, &r.HasError,
			&r.TotalSpans, &r.ServiceCount,
			&r.MatchedService, &r.MatchedSpanName,
			&r.MatchedResourceAttrs, &r.MatchedSpanAttrs,
			&r.LatestMatch,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AttributeKeysRow is one entry in the attribute-key catalog. Source is
// either "span" or "resource" — useful for the UI to badge each key
// with where it comes from.
type AttributeKeysRow struct {
	Key      string
	Source   string
	UseCount uint64
}

// SignalUsageRow is one service's row/byte footprint for a signal in a
// window — the raw material of the usage report's "what could you trim"
// nudges.
type SignalUsageRow struct {
	ServiceName string
	Rows        uint64
}

// RowsByService counts rows per service in the window for one signal
// table ("logs" or "traces"). Backs the usage report.
func (s *Store) RowsByService(ctx context.Context, table string, from, to time.Time) ([]SignalUsageRow, error) {
	if table != "logs" && table != "traces" {
		return nil, fmt.Errorf("rows by service: unsupported table %q", table)
	}
	q := fmt.Sprintf(`
		SELECT ServiceName, toUInt64(count()) AS rows
		FROM %s
		WHERE Timestamp >= ? AND Timestamp <= ?
		GROUP BY ServiceName
		ORDER BY rows DESC`, table)
	rows, err := s.conn.Query(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("rows by service (%s): %w", table, err)
	}
	defer rows.Close()
	var out []SignalUsageRow
	for rows.Next() {
		var r SignalUsageRow
		if err := rows.Scan(&r.ServiceName, &r.Rows); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TableAvgRowBytes returns the average COMPRESSED on-disk bytes per row
// of one telemetry table (system.tables total_bytes / total_rows) — the
// honest basis for "this data costs you ~X MB/day" estimates. Returns 0
// when the table is empty.
func (s *Store) TableAvgRowBytes(ctx context.Context, table string) (float64, error) {
	if table != "logs" && table != "traces" && table != "metrics" {
		return 0, fmt.Errorf("avg row bytes: unsupported table %q", table)
	}
	const q = `
		SELECT toUInt64(coalesce(total_bytes, 0)), toUInt64(coalesce(total_rows, 0))
		FROM system.tables
		WHERE database = currentDatabase() AND name = ?`
	var bytes, rows uint64
	if err := s.conn.QueryRow(ctx, q, table).Scan(&bytes, &rows); err != nil {
		return 0, fmt.Errorf("avg row bytes (%s): %w", table, err)
	}
	if rows == 0 {
		return 0, nil
	}
	return float64(bytes) / float64(rows), nil
}

// DistinctErrorTypes returns the error identifiers observed on error
// spans in the window — the conventional exception.type attribute when
// present, else the span's StatusMessage — most frequent first. Backs
// the message-search error-type picker: the set isn't static (it's
// whatever the org's telemetry emits), so the UI offers the observed
// values with a free-text fallback.
func (s *Store) DistinctErrorTypes(ctx context.Context, from, to time.Time, limit int) ([]string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
		SELECT etype, toUInt64(count()) AS uses FROM (
		    SELECT if(SpanAttributes['exception.type'] != '', SpanAttributes['exception.type'], StatusMessage) AS etype
		    FROM traces
		    WHERE Timestamp >= ? AND Timestamp <= ? AND StatusCode = 'Error'
		)
		WHERE etype != ''
		GROUP BY etype
		ORDER BY uses DESC, etype ASC
		LIMIT ?
	`
	rows, err := s.conn.Query(ctx, q, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("distinct error types: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var etype string
		var uses uint64
		if err := rows.Scan(&etype, &uses); err != nil {
			return nil, err
		}
		out = append(out, etype)
	}
	return out, rows.Err()
}

// DistinctAttributeKeys returns the attribute keys recently seen on
// spans in the given window, deduplicated across the SpanAttributes
// and ResourceAttributes maps. The query reads a bounded recent sample
// (LIMIT inside the subquery) so the cost stays predictable.
//
// The UI uses this to populate the "payload field path" picker — so
// users can choose orderId, http.route, file.name, etc. without
// having to remember them.
func (s *Store) DistinctAttributeKeys(ctx context.Context, from, to time.Time, sampleLimit int) ([]AttributeKeysRow, error) {
	if sampleLimit <= 0 {
		sampleLimit = 2000
	}
	const q = `
		SELECT key, source, toUInt64(count()) AS uses FROM (
		    SELECT arrayJoin(mapKeys(SpanAttributes))      AS key, 'span' AS source
		    FROM (
		        SELECT SpanAttributes FROM traces
		        WHERE Timestamp >= ? AND Timestamp <= ?
		        LIMIT ?
		    )
		    UNION ALL
		    SELECT arrayJoin(mapKeys(ResourceAttributes)) AS key, 'resource' AS source
		    FROM (
		        SELECT ResourceAttributes FROM traces
		        WHERE Timestamp >= ? AND Timestamp <= ?
		        LIMIT ?
		    )
		)
		GROUP BY key, source
		ORDER BY uses DESC, key ASC
		LIMIT 200
	`
	rows, err := s.conn.Query(ctx, q, from, to, sampleLimit, from, to, sampleLimit)
	if err != nil {
		return nil, fmt.Errorf("distinct attribute keys: %w", err)
	}
	defer rows.Close()
	var out []AttributeKeysRow
	for rows.Next() {
		var r AttributeKeysRow
		if err := rows.Scan(&r.Key, &r.Source, &r.UseCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DistinctAttributeKeysScoped is the integration-scoped form of
// DistinctAttributeKeys: it returns the attribute keys (span + resource)
// seen on traces that flow through the given services AND match the
// integration's DNF attribute predicate, in the window. Powers the
// payload-field typeahead on an integration's Messages tab, so the user
// only sees attributes that actually appear within *that* integration
// rather than the whole org's vocabulary.
//
// The scope predicate (ServiceName IN … AND the matcher groups) is applied
// inside each LIMIT-bounded subquery so the sample is drawn from matching
// rows, not the global stream — same scoping shape as DistinctSpanNames.
func (s *Store) DistinctAttributeKeysScoped(ctx context.Context, serviceNames []string, from, to time.Time, sampleLimit int, attrGroups [][]LogAttrFilter) ([]AttributeKeysRow, error) {
	if len(serviceNames) == 0 {
		return []AttributeKeysRow{}, nil
	}
	if sampleLimit <= 0 {
		sampleLimit = 2000
	}
	placeholders := make([]string, len(serviceNames))
	svcArgs := make([]any, 0, len(serviceNames))
	for i, n := range serviceNames {
		placeholders[i] = "?"
		svcArgs = append(svcArgs, n)
	}
	svcIn := "ServiceName IN (" + strings.Join(placeholders, ",") + ")"
	// The matcher predicate is evaluated against SpanAttributes (and the
	// ServiceName column for service.name conditions) — same as the other
	// integration-scoped span queries.
	attrSQL := ""
	var attrArgs []any
	if clause, cargs := attrGroupsClause("SpanAttributes", attrGroups); clause != "" {
		attrSQL = " AND " + clause
		attrArgs = cargs
	}
	q := `
		SELECT key, source, toUInt64(count()) AS uses FROM (
		    SELECT arrayJoin(mapKeys(SpanAttributes))      AS key, 'span' AS source
		    FROM (
		        SELECT SpanAttributes FROM traces
		        WHERE ` + svcIn + ` AND Timestamp >= ? AND Timestamp <= ?` + attrSQL + `
		        LIMIT ?
		    )
		    UNION ALL
		    SELECT arrayJoin(mapKeys(ResourceAttributes)) AS key, 'resource' AS source
		    FROM (
		        SELECT ResourceAttributes FROM traces
		        WHERE ` + svcIn + ` AND Timestamp >= ? AND Timestamp <= ?` + attrSQL + `
		        LIMIT ?
		    )
		)
		GROUP BY key, source
		ORDER BY uses DESC, key ASC
		LIMIT 200
	`
	// Both subqueries take the same args in the same order:
	// service names…, from, to, matcher args…, sample limit.
	args := make([]any, 0, (len(svcArgs)+len(attrArgs)+3)*2)
	for i := 0; i < 2; i++ {
		args = append(args, svcArgs...)
		args = append(args, from, to)
		args = append(args, attrArgs...)
		args = append(args, sampleLimit)
	}
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("distinct attribute keys (scoped): %w", err)
	}
	defer rows.Close()
	var out []AttributeKeysRow
	for rows.Next() {
		var r AttributeKeysRow
		if err := rows.Scan(&r.Key, &r.Source, &r.UseCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SearchTraceRow is one row in the SearchTraces result: a full trace
// summary plus a representative snapshot from the matching span
// (which service the match came from, what the span was called, and
// the merged resource + span attributes — so the UI can show the
// matching file.name / http.route / messaging.destination as chips).
type SearchTraceRow struct {
	TraceID              string
	TraceStart           time.Time
	DurationMs           float64
	HasError             bool
	TotalSpans           uint64
	ServiceCount         uint64
	MatchedService       string
	MatchedSpanName      string
	MatchedResourceAttrs map[string]string
	MatchedSpanAttrs     map[string]string
	// LatestMatch is the timestamp of the most recent matching span,
	// used as the keyset cursor key by SearchMessages. Unset (zero) by
	// SearchTraces, which doesn't paginate.
	LatestMatch time.Time
}

// SearchTraces runs a case-insensitive search across span attributes
// and surfaces matching traces (not spans) within the supplied range.
//
// onlyFailed restricts the result to traces that contain at least
// one Error-status span — useful for "show me what's failing"
// without having to scan everything.
func (s *Store) SearchTraces(ctx context.Context, q string, from, to time.Time, limit int, serviceFilter []string, onlyFailed bool) ([]SearchTraceRow, error) {
	var serviceFilterClause string
	var serviceFilterArgs []any
	if len(serviceFilter) > 0 {
		placeholders := make([]string, len(serviceFilter))
		for i, name := range serviceFilter {
			placeholders[i] = "?"
			serviceFilterArgs = append(serviceFilterArgs, name)
		}
		serviceFilterClause = " AND ServiceName IN (" + strings.Join(placeholders, ",") + ")"
	}

	sql := fmt.Sprintf(`
		WITH matching AS (
		    SELECT TraceId,
		           argMax(ServiceName, Timestamp)         AS matched_service,
		           argMax(SpanName, Timestamp)            AS matched_span_name,
		           argMax(ResourceAttributes, Timestamp)  AS matched_resource_attrs,
		           argMax(SpanAttributes, Timestamp)      AS matched_span_attrs,
		           max(Timestamp)                         AS latest_match
		    FROM traces
		    WHERE Timestamp >= ? AND Timestamp <= ?
		    AND (
		        positionCaseInsensitive(ServiceName, ?) > 0
		        OR positionCaseInsensitive(SpanName, ?) > 0
		        OR positionCaseInsensitive(StatusMessage, ?) > 0
		        OR arrayExists(x -> positionCaseInsensitive(x, ?) > 0, mapKeys(SpanAttributes))
		        OR arrayExists(x -> positionCaseInsensitive(x, ?) > 0, mapValues(SpanAttributes))
		        OR arrayExists(x -> positionCaseInsensitive(x, ?) > 0, mapKeys(ResourceAttributes))
		        OR arrayExists(x -> positionCaseInsensitive(x, ?) > 0, mapValues(ResourceAttributes))
		    )%s
		    GROUP BY TraceId
		    ORDER BY latest_match DESC
		    LIMIT ?
		),
		summary AS (
		    SELECT TraceId,
		           min(Timestamp)                                                                                     AS trace_start,
		           (max(toUnixTimestamp64Nano(Timestamp) + DurationNs) - min(toUnixTimestamp64Nano(Timestamp))) / 1000000 AS duration_ms,
		           countIf(StatusCode = 'Error') > 0                                                                  AS has_error,
		           toUInt64(count())                                                                                  AS total_spans,
		           toUInt64(length(arrayDistinct(groupArray(ServiceName))))                                           AS service_count
		    FROM traces
		    WHERE TraceId IN (SELECT TraceId FROM matching)
		    GROUP BY TraceId
		)
		SELECT
		    s.TraceId,
		    s.trace_start,
		    s.duration_ms,
		    s.has_error,
		    s.total_spans,
		    s.service_count,
		    m.matched_service,
		    m.matched_span_name,
		    m.matched_resource_attrs,
		    m.matched_span_attrs
		FROM summary AS s
		INNER JOIN matching AS m ON s.TraceId = m.TraceId
		%s
		ORDER BY s.trace_start DESC
	`, serviceFilterClause, failedFilterClause(onlyFailed))

	args := []any{
		from, to,
		q, q, q, q, q, q, q,
	}
	args = append(args, serviceFilterArgs...)
	args = append(args, limit)

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search traces: %w", err)
	}
	defer rows.Close()

	var out []SearchTraceRow
	for rows.Next() {
		var r SearchTraceRow
		if err := rows.Scan(
			&r.TraceID, &r.TraceStart, &r.DurationMs, &r.HasError,
			&r.TotalSpans, &r.ServiceCount,
			&r.MatchedService, &r.MatchedSpanName,
			&r.MatchedResourceAttrs, &r.MatchedSpanAttrs,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TraceSummaryRow describes a single trace from the point of view of
// one of the services that participated in it. It's what the service
// detail page renders one row per — the unit of work, not the span.
type TraceSummaryRow struct {
	TraceID              string
	TraceStart           time.Time
	DurationMs           float64
	HasError             bool
	TotalSpans           uint64
	ServiceCount         uint64
	ServiceFirstSpanName string
	ServiceResourceAttrs map[string]string
	ServiceSpanAttrs     map[string]string
}

// RecentTracesForService returns the most recent traces in which the
// supplied service participated within the given range, with summary
// data and a representative attribute snapshot from the service's
// first span in the trace.
func (s *Store) RecentTracesForService(ctx context.Context, service string, from, to time.Time, limit int, onlyFailed bool) ([]TraceSummaryRow, error) {
	// When onlyFailed, restrict the candidate trace set to traces that
	// had at least one error span on THIS service in the window.
	innerHaving := ""
	if onlyFailed {
		innerHaving = "HAVING countIf(StatusCode = 'Error') > 0"
	}
	q := fmt.Sprintf(`
		SELECT
		    TraceId,
		    min(Timestamp) AS trace_start,
		    (max(toUnixTimestamp64Nano(Timestamp) + DurationNs) - min(toUnixTimestamp64Nano(Timestamp))) / 1000000 AS duration_ms,
		    countIf(StatusCode = 'Error') > 0 AS has_error,
		    toUInt64(count()) AS total_spans,
		    toUInt64(length(arrayDistinct(groupArray(ServiceName)))) AS service_count,
		    argMinIf(SpanName, Timestamp, ServiceName = ?) AS first_span_name,
		    argMinIf(ResourceAttributes, Timestamp, ServiceName = ?) AS resource_attrs,
		    argMinIf(SpanAttributes, Timestamp, ServiceName = ?) AS span_attrs
		FROM traces
		WHERE TraceId IN (
		    SELECT TraceId FROM (
		        SELECT TraceId, max(Timestamp) AS latest
		        FROM traces
		        WHERE ServiceName = ? AND Timestamp >= ? AND Timestamp <= ?
		        GROUP BY TraceId
		        %s
		        ORDER BY latest DESC
		        LIMIT 500
		    )
		)
		GROUP BY TraceId
		ORDER BY trace_start DESC
		LIMIT ?
	`, innerHaving)
	rows, err := s.conn.Query(ctx, q,
		service, service, service, // argMinIf x3
		service, from, to, // inner subquery
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("recent traces for service: %w", err)
	}
	defer rows.Close()

	var out []TraceSummaryRow
	for rows.Next() {
		var r TraceSummaryRow
		if err := rows.Scan(
			&r.TraceID, &r.TraceStart, &r.DurationMs, &r.HasError,
			&r.TotalSpans, &r.ServiceCount,
			&r.ServiceFirstSpanName, &r.ServiceResourceAttrs, &r.ServiceSpanAttrs,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ServiceEdgeRow is one directed edge in the service flow graph —
// "source service called target service" — aggregated over the
// supplied range. The counts are at trace granularity so a single
// trace that crosses the boundary several times still counts once.
type ServiceEdgeRow struct {
	Source     string
	Target     string
	TraceCount uint64
	ErrorCount uint64
}

// ServiceEdges returns the set of cross-service edges (parent→child
// span service hops) observed in the supplied range, restricted to
// the supplied service allowlist. Same-service hops are dropped —
// we want service-level topology, not span-level.
//
// Self-joins on the traces table are bounded on both sides by the
// time range so ClickHouse can prune partitions for both halves.
// For pathological traces that straddle a window boundary, the edge
// is dropped for that window; acceptable for monitoring traffic
// which is typically short-lived.
// attrs (when non-empty) restricts edges to traces that match an
// integration's attribute matchers — via a TraceId-membership subquery —
// so the topology counts reflect only the integration's slice of traffic.
func (s *Store) ServiceEdges(ctx context.Context, services []string, from, to time.Time, attrGroups [][]LogAttrFilter) ([]ServiceEdgeRow, error) {
	if len(services) < 2 {
		return nil, nil
	}
	placeholders := make([]string, len(services))
	for i := range services {
		placeholders[i] = "?"
	}
	in := strings.Join(placeholders, ",")

	args := []any{from, to, from, to}
	for _, name := range services {
		args = append(args, name)
	}
	for _, name := range services {
		args = append(args, name)
	}

	// Optional attribute scoping: keep only traces having a span that
	// matches the integration's DNF predicate. Expressed as a TraceId
	// subquery so the predicate isn't confined to the parent/child join rows.
	attrFilter := ""
	if clause, cargs := attrGroupsClause("SpanAttributes", attrGroups); clause != "" {
		subArgs := []any{from, to}
		for _, name := range services {
			subArgs = append(subArgs, name)
		}
		attrSQL := " AND " + clause
		subArgs = append(subArgs, cargs...)
		attrFilter = `
		    AND child.TraceId IN (
		        SELECT TraceId FROM traces
		        WHERE Timestamp >= ? AND Timestamp <= ?
		          AND ServiceName IN (` + in + `)` + attrSQL + `
		    )`
		args = append(args, subArgs...)
	}

	sql := fmt.Sprintf(`
		SELECT
		    parent.ServiceName AS source,
		    child.ServiceName  AS target,
		    toUInt64(uniqExact(child.TraceId)) AS trace_count,
		    toUInt64(uniqExactIf(child.TraceId, child.StatusCode = 'Error' OR parent.StatusCode = 'Error')) AS error_count
		FROM traces AS child
		INNER JOIN traces AS parent
		    ON parent.TraceId = child.TraceId
		    AND parent.SpanId = child.ParentSpanId
		WHERE child.Timestamp >= ? AND child.Timestamp <= ?
		    AND parent.Timestamp >= ? AND parent.Timestamp <= ?
		    AND child.ServiceName IN (%s)
		    AND parent.ServiceName IN (%s)
		    AND child.ServiceName != parent.ServiceName%s
		GROUP BY source, target
		ORDER BY trace_count DESC
	`, in, in, attrFilter)

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("service edges: %w", err)
	}
	defer rows.Close()

	var out []ServiceEdgeRow
	for rows.Next() {
		var r ServiceEdgeRow
		if err := rows.Scan(&r.Source, &r.Target, &r.TraceCount, &r.ErrorCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ServiceNeighborRow is one neighbor of a focal service in the trace
// graph — i.e. a service that either calls into the focal service
// (Direction == "upstream") or is called by it (Direction == "downstream").
// Counts mirror ServiceEdgeRow: trace granularity, error attribution on
// either endpoint of the hop.
type ServiceNeighborRow struct {
	Direction   string // "upstream" or "downstream"
	ServiceName string
	TraceCount  uint64
	ErrorCount  uint64
}

// ServiceNeighbors returns every service that talks directly to the
// focal service in the supplied window, grouped by direction.
//
// Both halves of the self-join are bounded by the time range so
// ClickHouse can prune partitions on each side. The OR predicate
// dispatches to one of two cases — the focal service is either the
// parent (we're collecting its callees / downstream) or the child
// (we're collecting its callers / upstream). Same-service hops are
// dropped so a service that calls itself doesn't appear in its own
// neighbor list.
//
// The result is at trace granularity: if a single trace crosses the
// boundary several times, it counts once toward TraceCount; if any of
// the crossings was an error (on either span), it counts once toward
// ErrorCount.
func (s *Store) ServiceNeighbors(ctx context.Context, serviceName string, from, to time.Time) ([]ServiceNeighborRow, error) {
	if serviceName == "" {
		return nil, nil
	}
	const q = `
		SELECT
		    if(parent.ServiceName = ?, 'downstream', 'upstream') AS direction,
		    if(parent.ServiceName = ?, child.ServiceName, parent.ServiceName) AS service_name,
		    toUInt64(uniqExact(child.TraceId)) AS trace_count,
		    toUInt64(uniqExactIf(child.TraceId, child.StatusCode = 'Error' OR parent.StatusCode = 'Error')) AS error_count
		FROM traces AS child
		INNER JOIN traces AS parent
		    ON parent.TraceId = child.TraceId
		    AND parent.SpanId = child.ParentSpanId
		WHERE child.Timestamp >= ? AND child.Timestamp <= ?
		    AND parent.Timestamp >= ? AND parent.Timestamp <= ?
		    AND (parent.ServiceName = ? OR child.ServiceName = ?)
		    AND child.ServiceName != parent.ServiceName
		GROUP BY direction, service_name
		ORDER BY direction ASC, trace_count DESC
	`
	rows, err := s.conn.Query(ctx, q,
		serviceName, serviceName, // for the two if() branches
		from, to, from, to, // window bounds for both sides
		serviceName, serviceName, // OR predicate
	)
	if err != nil {
		return nil, fmt.Errorf("service neighbors: %w", err)
	}
	defer rows.Close()

	var out []ServiceNeighborRow
	for rows.Next() {
		var r ServiceNeighborRow
		if err := rows.Scan(&r.Direction, &r.ServiceName, &r.TraceCount, &r.ErrorCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SpansForTrace returns spans for the given trace ID, ordered by start
// time ascending, up to `limit` rows. Frontends use this to build the
// parent-child waterfall.
//
// The limit is a safety net: typical traces have ≤ 200 spans, but a
// pathological / runaway workload can produce thousands. Returning all
// of them blows the JSON payload AND the browser's waterfall renderer
// (no virtualization at the span level). At limit the caller knows the
// trace was truncated and can render a "showing first N of M spans"
// banner.
//
// limit=0 means "use the default cap"; a caller can pass an explicit
// value up to spansForTraceCeiling.
func (s *Store) SpansForTrace(ctx context.Context, traceID string, limit int) ([]SpanRow, error) {
	if limit <= 0 {
		limit = spansForTraceDefault
	}
	if limit > spansForTraceCeiling {
		limit = spansForTraceCeiling
	}
	const q = `
		SELECT
			Timestamp, TraceId, SpanId, ParentSpanId, ServiceName,
			SpanName, SpanKind, StatusCode, StatusMessage, DurationNs,
			ResourceAttributes, SpanAttributes
		FROM traces
		WHERE TraceId = ?
		ORDER BY Timestamp ASC
		LIMIT ?
	`
	return s.querySpans(ctx, q, traceID, limit)
}

// Bounds for the SpansForTrace query. The default works for ~99% of
// integration traces we've seen; the ceiling is a hard server-side
// cap so a misbehaving client can't ask for everything.
const (
	spansForTraceDefault = 5000
	spansForTraceCeiling = 50000
)

// ServiceProfileRow summarizes recent activity for a single service.
// It feeds the servicetypes registry to classify the service.
//
// IOFacets is the distinct set of (io.kind, io.role) pairs observed on
// the service's spans, encoded as "<kind>:<role>" strings — e.g.
// "file:input", "queue:output". The cell-api maps this into the
// servicetypes.ServiceProfile.IOFacets lookup table.
type ServiceProfileRow struct {
	SpanKinds        []string
	ResourceAttrKeys []string
	SpanAttrKeys     []string
	IOFacets         []string
}

// ServiceProfile reads a bounded recent sample of a service's spans
// in the supplied range and returns the distinct span kinds, attribute
// keys, and effective io.kind/io.role pairs it has used. The inner
// LIMIT keeps the work O(constant) regardless of volume.
//
// The resolver substitutes user-defined facet attribute mappings for
// the raw SpanAttributes['io.kind'] / ['io.role'] lookups when
// deriving the io_facet column. Without rules the resolver is the
// identity and the query behaves exactly as before. With rules, a
// service that emits e.g. peer.service='sftp.bank.com' but no
// io.kind / io.role will still classify as file-input.
func (s *Store) ServiceProfile(ctx context.Context, resolver facetmappings.Resolver, service string, from, to time.Time) (ServiceProfileRow, error) {
	// The resolver expressions are referenced four times in the
	// generated SQL (twice in the `!= ''` checks, twice in the
	// concat). ClickHouse placeholders are positional, so we append
	// the args in the same left-to-right order — the SQL fragment
	// below mirrors that ordering.
	q := fmt.Sprintf(`
		SELECT
		    groupUniqArray(SpanKind) AS kinds,
		    arrayDistinct(arrayFlatten(groupArray(mapKeys(ResourceAttributes)))) AS resource_keys,
		    arrayDistinct(arrayFlatten(groupArray(mapKeys(SpanAttributes)))) AS span_keys,
		    arrayFilter(x -> x != '', groupUniqArray(io_facet)) AS io_facets
		FROM (
		    SELECT
		        SpanKind,
		        ResourceAttributes,
		        SpanAttributes,
		        if((%[1]s) != '' AND (%[2]s) != '',
		           concat((%[1]s), ':', (%[2]s)),
		           '') AS io_facet
		    FROM traces
		    WHERE ServiceName = ? AND Timestamp >= ? AND Timestamp <= ?
		    LIMIT 500
		)
	`, resolver.KindExpr, resolver.RoleExpr)

	args := make([]any, 0,
		2*len(resolver.KindArgs)+2*len(resolver.RoleArgs)+3,
	)
	// The %[1]s / %[2]s positional verbs duplicate the expressions
	// but their placeholders must each be paired with a fresh copy
	// of the resolver's args, in source order: kind, role, kind,
	// role. Then the WHERE clause's ServiceName + window.
	args = append(args, resolver.KindArgs...)
	args = append(args, resolver.RoleArgs...)
	args = append(args, resolver.KindArgs...)
	args = append(args, resolver.RoleArgs...)
	args = append(args, service, from, to)

	var p ServiceProfileRow
	row := s.conn.QueryRow(ctx, q, args...)
	if err := row.Scan(&p.SpanKinds, &p.ResourceAttrKeys, &p.SpanAttrKeys, &p.IOFacets); err != nil {
		return ServiceProfileRow{}, fmt.Errorf("service profile: %w", err)
	}
	return p, nil
}

// DistinctServiceNames returns the distinct service names seen in the
// range. Used to enumerate candidates when resolving services for
// an integration.
func (s *Store) DistinctServiceNames(ctx context.Context, from, to time.Time) ([]string, error) {
	const q = `
		SELECT DISTINCT ServiceName
		FROM traces
		WHERE Timestamp >= ? AND Timestamp <= ?
	`
	rows, err := s.conn.Query(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("distinct services: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (s *Store) querySpans(ctx context.Context, query string, args ...any) ([]SpanRow, error) {
	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query spans: %w", err)
	}
	defer rows.Close()

	var out []SpanRow
	for rows.Next() {
		var r SpanRow
		if err := rows.Scan(
			&r.Timestamp, &r.TraceID, &r.SpanID, &r.ParentSpanID, &r.ServiceName,
			&r.SpanName, &r.SpanKind, &r.StatusCode, &r.StatusMessage, &r.DurationNs,
			&r.ResourceAttributes, &r.SpanAttributes,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LogRow is the projection used by the logs query. It mirrors the
// ingest-side row (and the `logs` table) but lives here so the read
// layer has no dependency on cell-ingest. ResourceAttributes and
// LogAttributes are exposed separately; the API layer merges them for
// display the same way it does for spans.
type LogRow struct {
	Timestamp          time.Time
	ObservedTimestamp  time.Time
	TraceID            string
	SpanID             string
	SeverityNumber     int32
	SeverityText       string
	ServiceName        string
	ServiceNamespace   string
	ScopeName          string
	Body               string
	ResourceAttributes map[string]string
	LogAttributes      map[string]string
	// LogID is the row's unique id (the LogId UUID column, as a string),
	// used as the keyset tiebreaker. Paired with Timestamp it gives a
	// stable total order even for byte-identical rows at the same
	// timestamp — which a content hash could not distinguish.
	LogID string
}

// LogCursor is a keyset position: the (TSNano, Ord) of the last row of
// a page, where TSNano is the row's timestamp in unix nanoseconds and
// Ord is its LogId. Comparing integer nanoseconds (rather than binding
// a time.Time against DateTime64) avoids a sub-second precision
// round-trip that would otherwise drop rows at a boundary timestamp.
type LogCursor struct {
	TSNano int64
	Ord    string
}

// LogAttrFilter is one attribute predicate over the merged resource +
// log attributes of a log row. Key is validated by the caller (safe
// charset) because it is interpolated into the map subscript. Op is one
// of the AttrOp* constants. Value is compared as a string for the text
// ops and parsed as a float for the numeric ones.
type LogAttrFilter struct {
	Key   string `json:"key"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// Attribute filter operators. Text ops compare the attribute value as a
// string; numeric ops cast it via toFloat64OrNull (non-numeric / absent
// values simply don't match).
const (
	AttrOpEq          = "eq"
	AttrOpNeq         = "neq"
	AttrOpContains    = "contains"
	AttrOpNotContains = "not_contains"
	AttrOpStartsWith  = "starts_with"
	AttrOpEndsWith    = "ends_with"
	AttrOpMatches     = "matches" // RE2 regex (ClickHouse match())
	AttrOpExists      = "exists"
	AttrOpGt          = "gt"
	AttrOpGte         = "gte"
	AttrOpLt          = "lt"
	AttrOpLte         = "lte"
)

// LogQueryParams is the parameter bag for SearchLogs. The zero value of
// an optional filter (empty Service, MinSeverity 0, empty BodyContains /
// TraceID, nil Before, no Attrs) means "don't filter on it".
type LogQueryParams struct {
	Service      string   // exact match; "" = all services (global Logs page)
	ServiceIn    []string // ServiceName IN (...); used to scope to an integration's services
	From         time.Time
	To           time.Time
	Limit        int
	MinSeverity  int32           // OTLP SeverityNumber floor; 0 = no floor
	BodyContains string          // case-insensitive substring of Body; "" = no filter
	TraceID      string          // exact match; "" = no filter
	Attrs        []LogAttrFilter // attribute predicates, AND-ed
	// AttrGroups is a DNF predicate AND-ed on top of Attrs — used for an
	// integration's OR attribute matchers. Empty = no extra predicate.
	AttrGroups [][]LogAttrFilter
	Before     *LogCursor // keyset cursor; nil = first page
}

// attrEffectiveExprIn is the SQL for an attribute's effective value: the
// primary attribute (LogAttributes for logs, MetricAttributes for
// metrics) if present, else the resource attribute. The key is
// interpolated (caller must have validated it against a safe charset).
func attrEffectiveExprIn(primaryMap, key string) string {
	return fmt.Sprintf("if(mapContains(%[1]s, '%[2]s'), %[1]s['%[2]s'], ResourceAttributes['%[2]s'])", primaryMap, key)
}

// serviceNameAttrKey is the matcher attribute that addresses a span/log/metric
// row's owning service. It maps to the indexed `ServiceName` column on every
// signal table — not to the attribute maps — so a condition on it compiles to
// `ServiceName <op> ?`. This is what lets an integration rule scope attribute
// predicates per service (e.g. `ServiceName='A' AND producer='B'`).
const serviceNameAttrKey = "service.name"

// attrEffectiveExpr is the log-table specialisation of
// attrEffectiveExprIn (primary map = LogAttributes).
func attrEffectiveExpr(key string) string { return attrEffectiveExprIn("LogAttributes", key) }

// attrClauseIn renders one attribute predicate to a SQL fragment plus
// its bind args, against the given primary attribute map. Values are
// always bound; the key is interpolated (validated upstream).
func attrClauseIn(primaryMap string, f LogAttrFilter) (string, []any) {
	// service.name addresses the indexed ServiceName column directly, so an
	// integration rule's per-service condition (ServiceName='A') stays fast
	// and works identically across traces/logs/metrics.
	eff := attrEffectiveExprIn(primaryMap, f.Key)
	if f.Key == serviceNameAttrKey {
		eff = "ServiceName"
	}
	switch f.Op {
	case AttrOpEq:
		return "(" + eff + ") = ?", []any{f.Value}
	case AttrOpNeq:
		return "(" + eff + ") != ?", []any{f.Value}
	case AttrOpContains:
		return "positionCaseInsensitive(" + eff + ", ?) > 0", []any{f.Value}
	case AttrOpNotContains:
		return "positionCaseInsensitive(" + eff + ", ?) = 0", []any{f.Value}
	case AttrOpStartsWith:
		return "startsWithUTF8(lower(" + eff + "), lower(?))", []any{f.Value}
	case AttrOpEndsWith:
		return "endsWithUTF8(lower(" + eff + "), lower(?))", []any{f.Value}
	case AttrOpMatches:
		// RE2 regex over the effective attribute value. match() returns 0
		// for an invalid pattern, so a bad regex simply matches nothing.
		return "match(" + eff + ", ?)", []any{f.Value}
	case AttrOpExists:
		if f.Key == serviceNameAttrKey {
			return "ServiceName != ''", nil
		}
		return fmt.Sprintf("(mapContains(%[1]s, '%[2]s') OR mapContains(ResourceAttributes, '%[2]s'))", primaryMap, f.Key), nil
	case AttrOpGt, AttrOpGte, AttrOpLt, AttrOpLte:
		cmp := map[string]string{AttrOpGt: ">", AttrOpGte: ">=", AttrOpLt: "<", AttrOpLte: "<="}[f.Op]
		n, err := strconv.ParseFloat(f.Value, 64)
		if err != nil {
			return "1 = 0", nil // non-numeric value with a numeric op → matches nothing
		}
		return "toFloat64OrNull(" + eff + ") " + cmp + " ?", []any{n}
	default:
		return "1 = 0", nil
	}
}

// attrClause renders one log-attribute predicate (primary map =
// LogAttributes). See attrClauseIn.
func attrClause(f LogAttrFilter) (string, []any) { return attrClauseIn("LogAttributes", f) }

// SpanAttrClause renders one attribute predicate against the span tables
// (primary map = SpanAttributes, falling back to ResourceAttributes). It's
// the exported form callers use to AND extra attribute predicates — e.g.
// an integration's attribute matchers — into the Messages/trace search,
// whose Clauses are raw ClickHouse fragments.
func SpanAttrClause(f LogAttrFilter) (string, []any) { return attrClauseIn("SpanAttributes", f) }

// attrGroupsClause renders a DNF attribute predicate against primaryMap:
// the filters within a group are AND-ed, and the groups are OR-ed —
// `(g0a AND g0b) OR (g1a) OR …`. This is how an integration's grouped
// attribute matchers express OR. Empty groups/filters are skipped; returns
// ("", nil) when there's nothing to match so the caller adds no clause.
func attrGroupsClause(primaryMap string, groups [][]LogAttrFilter) (string, []any) {
	orParts := make([]string, 0, len(groups))
	var args []any
	for _, g := range groups {
		andParts := make([]string, 0, len(g))
		for _, f := range g {
			clause, cargs := attrClauseIn(primaryMap, f)
			andParts = append(andParts, clause)
			args = append(args, cargs...)
		}
		if len(andParts) == 0 {
			continue
		}
		orParts = append(orParts, "("+strings.Join(andParts, " AND ")+")")
	}
	if len(orParts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(orParts, " OR ") + ")", args
}

// SpanAttrGroupsClause is the span-table form of attrGroupsClause (primary
// map = SpanAttributes) for callers that AND a raw DNF predicate into a
// trace / message search.
func SpanAttrGroupsClause(groups [][]LogAttrFilter) (string, []any) {
	return attrGroupsClause("SpanAttributes", groups)
}

// SearchLogs returns logs in the range, newest first, with the optional
// filters applied. An empty Service searches across all services (the
// global Logs page); a non-empty Service narrows to one (the
// per-service section). Before pages by keyset on (Timestamp, ord
// hash): each returned row carries its Ord so the caller can build the
// next cursor from the last row.
func (s *Store) SearchLogs(ctx context.Context, p LogQueryParams) ([]LogRow, error) {
	where, args := logPredicates(p)
	if p.Before != nil {
		where = append(where, "(toUnixTimestamp64Nano(Timestamp) < ? OR (toUnixTimestamp64Nano(Timestamp) = ? AND toString(LogId) < ?))")
		args = append(args, p.Before.TSNano, p.Before.TSNano, p.Before.Ord)
	}
	limit := p.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	args = append(args, limit)

	sql := fmt.Sprintf(`
		SELECT
			Timestamp, ObservedTimestamp, TraceId, SpanId,
			SeverityNumber, SeverityText, ServiceName, ServiceNamespace,
			ScopeName, Body, ResourceAttributes, LogAttributes,
			toString(LogId) AS _ord
		FROM logs
		WHERE %s
		ORDER BY Timestamp DESC, _ord DESC
		LIMIT ?
	`, strings.Join(where, " AND "))

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search logs: %w", err)
	}
	defer rows.Close()

	var out []LogRow
	for rows.Next() {
		var r LogRow
		if err := rows.Scan(
			&r.Timestamp, &r.ObservedTimestamp, &r.TraceID, &r.SpanID,
			&r.SeverityNumber, &r.SeverityText, &r.ServiceName, &r.ServiceNamespace,
			&r.ScopeName, &r.Body, &r.ResourceAttributes, &r.LogAttributes,
			&r.LogID,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountLogs returns how many logs match the params' filters in the
// range. Reuses logPredicates (severity floor, body-contains, service /
// ServiceIn scope, attribute predicates) so it stays in lockstep with
// SearchLogs. Backs the log-alert evaluator: it counts matches over the
// rule's trailing window and compares to the rule's threshold. Cursor /
// limit fields on the params are ignored.
// GetLogByID fetches a single log row by its LogId. Returns (row, true,
// nil) when found, (zero, false, nil) when not. The org filter applied
// via ctx (additional_table_filters) scopes this to the caller's org,
// so a LogId leaked from another tenant returns not-found.
func (s *Store) GetLogByID(ctx context.Context, id string) (LogRow, bool, error) {
	const sql = `
		SELECT
			Timestamp, ObservedTimestamp, TraceId, SpanId,
			SeverityNumber, SeverityText, ServiceName, ServiceNamespace,
			ScopeName, Body, ResourceAttributes, LogAttributes,
			toString(LogId) AS _ord
		FROM logs
		WHERE toString(LogId) = ?
		LIMIT 1
	`
	rows, err := s.conn.Query(ctx, sql, id)
	if err != nil {
		return LogRow{}, false, fmt.Errorf("get log by id: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return LogRow{}, false, rows.Err()
	}
	var r LogRow
	if err := rows.Scan(
		&r.Timestamp, &r.ObservedTimestamp, &r.TraceID, &r.SpanID,
		&r.SeverityNumber, &r.SeverityText, &r.ServiceName, &r.ServiceNamespace,
		&r.ScopeName, &r.Body, &r.ResourceAttributes, &r.LogAttributes,
		&r.LogID,
	); err != nil {
		return LogRow{}, false, err
	}
	return r, true, nil
}

func (s *Store) CountLogs(ctx context.Context, p LogQueryParams) (uint64, error) {
	where, args := logPredicates(p)
	sql := "SELECT toUInt64(count()) FROM logs WHERE " + strings.Join(where, " AND ")
	var n uint64
	if err := s.conn.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count logs: %w", err)
	}
	return n, nil
}

// DistinctLogServices returns the service names that have logs in the
// range, alphabetically. Sourced from the logs table (not traces) so
// log-only emitters — a broker, a streaming platform — are included.
// Backs the searchable service filter on the Logs page.
func (s *Store) DistinctLogServices(ctx context.Context, from, to time.Time) ([]string, error) {
	const q = `
		SELECT DISTINCT ServiceName
		FROM logs
		WHERE Timestamp >= ? AND Timestamp <= ?
		ORDER BY ServiceName ASC
	`
	rows, err := s.conn.Query(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("distinct log services: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// LogAttrKeyRow is one entry in the log attribute-key catalog: the key,
// how many times it was seen in the sample, an approximate distinct
// value count (uniqExact over the sample — under-counts very high
// cardinality keys, like the design's HLL sketch but cheaper), and
// whether every non-empty value parsed as a number (so the UI can offer
// numeric operators).
type LogAttrKeyRow struct {
	Key         string
	UseCount    uint64
	Cardinality uint64
	Numeric     bool
}

// LogAttrValueRow is one top-N value for an attribute key, with how many
// log events carried it. Backs the value-picker's second step.
type LogAttrValueRow struct {
	Value  string
	Events uint64
}

// DistinctLogAttributeKeys returns the attribute keys seen on a bounded
// recent sample of logs (both resource and log attributes, merged),
// each tagged numeric when all of its non-empty sampled values parse as
// a float. Backs the Logs page's attribute-filter key autocomplete and
// the per-key operator set. The inner LIMIT keeps the scan O(constant).
func (s *Store) DistinctLogAttributeKeys(ctx context.Context, from, to time.Time, sampleLimit int) ([]LogAttrKeyRow, error) {
	if sampleLimit <= 0 {
		sampleLimit = 2000
	}
	// arrayJoin over a Map yields (key, value) tuples. Per key we count
	// uses and decide numeric = (≥1 non-empty value) AND (no non-empty
	// value fails toFloat64OrNull). min() across the two sources ANDs
	// their numeric verdicts.
	const q = `
		SELECT key, toUInt64(sum(uses)) AS uses, toUInt64(max(card)) AS cardinality, min(is_numeric) AS numeric
		FROM (
			SELECT kv.1 AS key,
			       toUInt64(count()) AS uses,
			       toUInt64(uniqExact(kv.2)) AS card,
			       toUInt8(countIf(kv.2 != '') > 0 AND countIf(kv.2 != '' AND isNull(toFloat64OrNull(kv.2))) = 0) AS is_numeric
			FROM (SELECT arrayJoin(LogAttributes) AS kv FROM logs WHERE Timestamp >= ? AND Timestamp <= ? LIMIT ?)
			GROUP BY key
			UNION ALL
			SELECT kv.1 AS key,
			       toUInt64(count()) AS uses,
			       toUInt64(uniqExact(kv.2)) AS card,
			       toUInt8(countIf(kv.2 != '') > 0 AND countIf(kv.2 != '' AND isNull(toFloat64OrNull(kv.2))) = 0) AS is_numeric
			FROM (SELECT arrayJoin(ResourceAttributes) AS kv FROM logs WHERE Timestamp >= ? AND Timestamp <= ? LIMIT ?)
			GROUP BY key
		)
		GROUP BY key
		ORDER BY uses DESC, key ASC
		LIMIT 500
	`
	rows, err := s.conn.Query(ctx, q, from, to, sampleLimit, from, to, sampleLimit)
	if err != nil {
		return nil, fmt.Errorf("distinct log attribute keys: %w", err)
	}
	defer rows.Close()
	var out []LogAttrKeyRow
	for rows.Next() {
		var r LogAttrKeyRow
		var numeric uint8
		if err := rows.Scan(&r.Key, &r.UseCount, &r.Cardinality, &numeric); err != nil {
			return nil, err
		}
		r.Numeric = numeric == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// LogAttrValues returns the top-N values for one attribute key in the
// range, ranked by how many log events carried each value. The key is
// interpolated (caller validates the charset); it reads the effective
// value (log attribute, else resource attribute) so it matches how the
// filters compare. Backs the value picker's second step.
func (s *Store) LogAttrValues(ctx context.Context, key string, from, to time.Time, limit int) ([]LogAttrValueRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	eff := attrEffectiveExpr(key)
	sql := fmt.Sprintf(`
		SELECT v, toUInt64(count()) AS events
		FROM (
			SELECT %s AS v
			FROM logs
			WHERE Timestamp >= ? AND Timestamp <= ?
			  AND (mapContains(LogAttributes, '%s') OR mapContains(ResourceAttributes, '%s'))
		)
		WHERE v != ''
		GROUP BY v
		ORDER BY events DESC, v ASC
		LIMIT ?
	`, eff, key, key)
	rows, err := s.conn.Query(ctx, sql, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("log attr values: %w", err)
	}
	defer rows.Close()
	var out []LogAttrValueRow
	for rows.Next() {
		var r LogAttrValueRow
		if err := rows.Scan(&r.Value, &r.Events); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TraceAttrValuesScoped returns the top-N values for one span/resource
// attribute key, ranked by how many spans carried each value, scoped to the
// given services AND the integration's DNF matcher predicate in the window.
// The traces-table analogue of LogAttrValues; backs the value typeahead on
// the integration Messages tab. High-cardinality keys are bounded by LIMIT —
// the GROUP BY scans matching rows but only the top N are returned, so cost
// tracks scan volume (services ∩ window ∩ has-attribute), not value count.
//
// The key is interpolated (caller validates the charset via attrKeyRe); the
// service names, matcher args and limit bind positionally.
func (s *Store) TraceAttrValuesScoped(ctx context.Context, serviceNames []string, key string, from, to time.Time, limit int, attrGroups [][]LogAttrFilter) ([]LogAttrValueRow, error) {
	if len(serviceNames) == 0 {
		return []LogAttrValueRow{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	placeholders := make([]string, len(serviceNames))
	args := make([]any, 0, len(serviceNames)+3)
	for i, n := range serviceNames {
		placeholders[i] = "?"
		args = append(args, n)
	}
	args = append(args, from, to)
	// Same matcher predicate the message search applies — so the value list
	// reflects only rows that actually belong to the integration.
	attrSQL := ""
	if clause, cargs := attrGroupsClause("SpanAttributes", attrGroups); clause != "" {
		attrSQL = " AND " + clause
		args = append(args, cargs...)
	}
	args = append(args, limit)
	// Effective value = span attribute, else resource attribute — matching how
	// the filters compare.
	eff := attrEffectiveExprIn("SpanAttributes", key)
	sql := fmt.Sprintf(`
		SELECT v, toUInt64(count()) AS events
		FROM (
			SELECT %s AS v
			FROM traces
			WHERE ServiceName IN (%s)
			  AND Timestamp >= ? AND Timestamp <= ?
			  AND (mapContains(SpanAttributes, '%s') OR mapContains(ResourceAttributes, '%s'))%s
		)
		WHERE v != ''
		GROUP BY v
		ORDER BY events DESC, v ASC
		LIMIT ?
	`, eff, strings.Join(placeholders, ","), key, key, attrSQL)
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("trace attr values (scoped): %w", err)
	}
	defer rows.Close()
	var out []LogAttrValueRow
	for rows.Next() {
		var r LogAttrValueRow
		if err := rows.Scan(&r.Value, &r.Events); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// logPredicates builds the WHERE clauses + bind args shared by the log
// search and the volume histogram: time window, service, severity
// floor, body substring, trace id, and attribute filters. The cursor
// and limit are query-specific and added by the caller.
func logPredicates(p LogQueryParams) ([]string, []any) {
	where := []string{"Timestamp >= ?", "Timestamp <= ?"}
	args := []any{p.From, p.To}
	if p.Service != "" {
		where = append(where, "ServiceName = ?")
		args = append(args, p.Service)
	}
	if len(p.ServiceIn) > 0 {
		clause, cargs := inClause("ServiceName", p.ServiceIn)
		where = append(where, clause)
		args = append(args, cargs...)
	}
	if p.MinSeverity > 0 {
		where = append(where, "SeverityNumber >= ?")
		args = append(args, p.MinSeverity)
	}
	if p.BodyContains != "" {
		// Single-token query (no whitespace): AND a hasTokenCaseInsensitive
		// predicate so the tokenbf_v1 skip-index on Body can prune parts
		// before we touch them. The position match still runs for
		// confirmation (bloom filters can false-positive). For multi-word
		// queries we fall through to the slow path — bloom on a phrase
		// doesn't help. See docs/performance-audit.md → P0-4.
		if !strings.ContainsAny(p.BodyContains, " \t\n") {
			where = append(where, "hasTokenCaseInsensitive(Body, ?) AND positionCaseInsensitive(Body, ?) > 0")
			args = append(args, p.BodyContains, p.BodyContains)
		} else {
			where = append(where, "positionCaseInsensitive(Body, ?) > 0")
			args = append(args, p.BodyContains)
		}
	}
	if p.TraceID != "" {
		where = append(where, "TraceId = ?")
		args = append(args, p.TraceID)
	}
	for _, f := range p.Attrs {
		clause, cargs := attrClause(f)
		where = append(where, clause)
		args = append(args, cargs...)
	}
	// AttrGroups is a DNF predicate (e.g. an integration's OR matchers),
	// AND-ed in against the log attribute map.
	if clause, cargs := attrGroupsClause("LogAttributes", p.AttrGroups); clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	return where, args
}

// LogVolumeBucket is one time bucket of the volume histogram: log
// counts split into the four severity bands the histogram stacks
// (bottom→top: info → warn → err → fatal). "info" folds in debug/trace
// (everything below WARN) so the neutral bar represents quiet volume.
type LogVolumeBucket struct {
	Bucket time.Time
	Info   uint64
	Warn   uint64
	Err    uint64
	Fatal  uint64
}

// LogVolume returns per-bucket severity-banded counts for the volume
// histogram, applying the same filters as SearchLogs (minus cursor).
// stepSeconds is the bucket width the handler derives from the window.
func (s *Store) LogVolume(ctx context.Context, p LogQueryParams, stepSeconds int) ([]LogVolumeBucket, error) {
	if stepSeconds <= 0 {
		stepSeconds = 60
	}
	where, args := logPredicates(p)
	sql := fmt.Sprintf(`
		SELECT
			toStartOfInterval(Timestamp, INTERVAL %d SECOND) AS bucket,
			toUInt64(countIf(SeverityNumber < 13)) AS info,
			toUInt64(countIf(SeverityNumber >= 13 AND SeverityNumber < 17)) AS warn,
			toUInt64(countIf(SeverityNumber >= 17 AND SeverityNumber < 21)) AS err,
			toUInt64(countIf(SeverityNumber >= 21)) AS fatal
		FROM logs
		WHERE %s
		GROUP BY bucket
		ORDER BY bucket ASC
	`, stepSeconds, strings.Join(where, " AND "))
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("log volume: %w", err)
	}
	defer rows.Close()
	var out []LogVolumeBucket
	for rows.Next() {
		var b LogVolumeBucket
		if err := rows.Scan(&b.Bucket, &b.Info, &b.Warn, &b.Err, &b.Fatal); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LogGroupRow is one rollup row of a log search grouped by a dimension:
// total matching logs and how many were errors (severity ≥ 17).
type LogGroupRow struct {
	Key        string
	Count      uint64
	ErrorCount uint64
}

// LogGroups rolls up a log search by a dimension — "severity",
// "attribute" (using attrKey), or "service" (default) — honouring the
// same filters as SearchLogs (minus cursor/limit). attrKey must be
// validated by the caller. Integration grouping is done by the handler
// over the "service" rollup.
func (s *Store) LogGroups(ctx context.Context, p LogQueryParams, by, attrKey string) ([]LogGroupRow, error) {
	where, args := logPredicates(p)
	var groupExpr string
	switch by {
	case "severity":
		groupExpr = "multiIf(SeverityNumber >= 21, 'fatal', SeverityNumber >= 17, 'error', SeverityNumber >= 13, 'warn', 'info')"
	case "attribute":
		groupExpr = attrEffectiveExprIn("LogAttributes", attrKey)
		where = append(where, fmt.Sprintf("(mapContains(LogAttributes, '%[1]s') OR mapContains(ResourceAttributes, '%[1]s'))", attrKey))
	default:
		groupExpr = "ServiceName"
	}
	sql := fmt.Sprintf(`
		SELECT %[1]s AS gk,
		       toUInt64(count()) AS c,
		       toUInt64(countIf(SeverityNumber >= 17)) AS errs
		FROM logs
		WHERE %[2]s
		GROUP BY gk
		HAVING gk != ''
		ORDER BY c DESC, gk ASC
		LIMIT 300
	`, groupExpr, strings.Join(where, " AND "))
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("log groups: %w", err)
	}
	defer rows.Close()
	var out []LogGroupRow
	for rows.Next() {
		var r LogGroupRow
		if err := rows.Scan(&r.Key, &r.Count, &r.ErrorCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MetricNameRow is one entry in a metric catalog: the metric name plus
// its OTLP type, unit, how many data points landed in the window, how
// many distinct services emitted it, and when it was last seen. The UI
// uses it to populate the metric picker before charting any one series.
type MetricNameRow struct {
	MetricName   string
	MetricType   string
	Unit         string
	PointCount   uint64
	ServiceCount uint64
	LastSeen     time.Time
}

// DistinctMetricServices returns the service names that emitted metrics
// in the range, alphabetically — the candidate set for resolving an
// integration to its member services.
func (s *Store) DistinctMetricServices(ctx context.Context, from, to time.Time) ([]string, error) {
	const q = `SELECT DISTINCT ServiceName FROM metrics WHERE Timestamp >= ? AND Timestamp <= ? ORDER BY ServiceName ASC`
	rows, err := s.conn.Query(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("distinct metric services: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// metricServiceIn builds a bare "ServiceName IN (?,?,…)" clause plus its
// bind args for the metrics table, or ("", nil) when serviceIn is empty.
// Callers prefix " AND " as needed. Used to enforce group-policy service
// visibility on the global metric endpoints (a teamless caller never gets
// here — the handler short-circuits on empty access — but a team-scoped
// caller is constrained to exactly the services their policies grant).
func metricServiceIn(serviceIn []string) (clause string, args []any) {
	if len(serviceIn) == 0 {
		return "", nil
	}
	ph := make([]string, len(serviceIn))
	args = make([]any, len(serviceIn))
	for i, n := range serviceIn {
		ph[i] = "?"
		args[i] = n
	}
	return "ServiceName IN (" + strings.Join(ph, ",") + ")", args
}

// MetricNames returns the distinct metrics emitted in the range,
// alphabetically. An empty service catalogs every service's metrics
// (the global Metrics page); a non-empty service narrows to one (the
// per-service section). serviceIn, when non-empty, further restricts the
// catalog to a policy-derived allowlist of services. ServiceCount is how
// many distinct services reported each metric. A metric keeps one
// type/unit in practice, so any() is safe for those columns.
func (s *Store) MetricNames(ctx context.Context, service string, serviceIn []string, from, to time.Time) ([]MetricNameRow, error) {
	where := []string{"Timestamp >= ?", "Timestamp <= ?"}
	args := []any{from, to}
	if service != "" {
		where = append(where, "ServiceName = ?")
		args = append(args, service)
	}
	if c, a := metricServiceIn(serviceIn); c != "" {
		where = append(where, c)
		args = append(args, a...)
	}
	q := fmt.Sprintf(`
		SELECT
			MetricName,
			any(MetricType) AS metric_type,
			any(Unit) AS unit,
			toUInt64(count()) AS point_count,
			toUInt64(uniqExact(ServiceName)) AS service_count,
			max(Timestamp) AS last_seen
		FROM metrics
		WHERE %s
		GROUP BY MetricName
		ORDER BY MetricName ASC
	`, strings.Join(where, " AND "))
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("metric names: %w", err)
	}
	defer rows.Close()

	var out []MetricNameRow
	for rows.Next() {
		var r MetricNameRow
		if err := rows.Scan(&r.MetricName, &r.MetricType, &r.Unit, &r.PointCount, &r.ServiceCount, &r.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MetricSeriesPoint is one time bucket of a charted metric series.
type MetricSeriesPoint struct {
	Bucket time.Time
	Value  float64
}

// MetricSeriesResult is a charted series plus the metadata the UI needs
// to label it: the OTLP type, the unit, and which aggregation we picked
// for that type (so the y-axis can read "avg" vs "increase").
type MetricSeriesResult struct {
	MetricType  string
	Unit        string
	Aggregation string // "avg" | "increase"
	Points      []MetricSeriesPoint
}

// metricBucketValue maps one bucket's raw aggregates to the charted
// value, applying the type-appropriate aggregation, and reports which
// aggregation it used:
//
//   - gauge / non-monotonic sum → avg(Value): the mean reading.
//   - monotonic sum (a counter) → increase: the rise in the cumulative
//     value from the previous bucket to this one (delta of per-bucket
//     max values), floored at 0 so a counter reset doesn't render
//     negative. The first bucket has no predecessor (havePrev false), so
//     its increase is reported as 0. Computed across buckets — a
//     within-bucket max-min would read 0 whenever a bucket holds a
//     single point.
//   - histogram → mean observation: sum(Value)/sum(Count), where Value
//     is the bucket sum and Count the observation count.
//
// The caller owns prevMax/havePrev state (it differs per series) and
// must update prevMax = maxValue after each monotonic-sum bucket.
func metricBucketValue(metricType string, isMonotonic uint8, avgValue, maxValue, sumValue float64, sumCount uint64, prevMax float64, havePrev bool) (value float64, aggregation string) {
	switch {
	case metricType == "histogram":
		aggregation = "avg"
		if sumCount > 0 {
			value = sumValue / float64(sumCount)
		}
	case metricType == "sum" && isMonotonic == 1:
		aggregation = "increase"
		if havePrev {
			if d := maxValue - prevMax; d > 0 {
				value = d
			}
		}
	default:
		aggregation = "avg"
		value = avgValue
	}
	return value, aggregation
}

// metricSeriesValue applies an explicit per-bucket transform, falling back to
// the type-based default (metricBucketValue) for "" / "auto". Lets a chart turn
// a counter into an increase or a per-second rate over the bucket, or show the
// raw reading — the bucket width (stepSeconds) IS the interval.
//
//	raw      — the value as stored (counter: cumulative max; else avg)
//	increase — counter rise within the bucket (Δ, reset-guarded)
//	rate     — increase / stepSeconds (per-second)
//	auto/""  — metricBucketValue's type-based choice
func metricSeriesValue(transform, metricType string, isMonotonic uint8, avgValue, maxValue, sumValue float64, sumCount uint64, prevMax float64, havePrev bool, stepSeconds int) (value float64, aggregation string) {
	switch transform {
	case "raw":
		if isMonotonic == 1 {
			return maxValue, "raw"
		}
		return avgValue, "raw"
	case "increase", "rate":
		var inc float64
		if havePrev {
			if d := maxValue - prevMax; d > 0 {
				inc = d
			}
		}
		if transform == "rate" {
			if stepSeconds > 0 {
				return inc / float64(stepSeconds), "rate"
			}
			return inc, "rate"
		}
		return inc, "increase"
	default:
		return metricBucketValue(metricType, isMonotonic, avgValue, maxValue, sumValue, sumCount, prevMax, havePrev)
	}
}

// metricSeriesSelect is the per-bucket projection shared by the
// single-series and by-service queries. %s is the GROUP BY / ORDER BY
// key prefix (empty for single-series, "ServiceName, " for by-service).
const metricSeriesSelect = `
		SELECT
			%s
			toStartOfInterval(Timestamp, INTERVAL %d SECOND) AS bucket,
			any(MetricType) AS metric_type,
			max(IsMonotonic) AS is_monotonic,
			any(Unit) AS unit,
			avg(Value) AS avg_value,
			max(Value) AS max_value,
			sum(Value) AS sum_value,
			toUInt64(sum(Count)) AS sum_count
		FROM metrics
		WHERE %s
		GROUP BY %s bucket
		ORDER BY %s bucket ASC`

// MetricSeries returns a single time-bucketed series for one metric of
// one service. stepSeconds is the bucket width — an integer the handler
// derives from the window (never user free-text) so interpolating it
// into the INTERVAL literal is safe. All label combinations of the
// metric are folded together — v1 charts the metric, not per-attribute
// series.
func (s *Store) MetricSeries(ctx context.Context, service, metricName string, from, to time.Time, stepSeconds int, transform string) (MetricSeriesResult, error) {
	if stepSeconds <= 0 {
		stepSeconds = 60
	}
	sql := fmt.Sprintf(metricSeriesSelect, "", stepSeconds,
		"ServiceName = ? AND MetricName = ? AND Timestamp >= ? AND Timestamp <= ?", "", "")

	rows, err := s.conn.Query(ctx, sql, service, metricName, from, to)
	if err != nil {
		return MetricSeriesResult{}, fmt.Errorf("metric series: %w", err)
	}
	defer rows.Close()

	var res MetricSeriesResult
	var prevMax float64
	havePrev := false
	for rows.Next() {
		var (
			bucket      time.Time
			metricType  string
			isMonotonic uint8
			unit        string
			avgValue    float64
			maxValue    float64
			sumValue    float64
			sumCount    uint64
		)
		if err := rows.Scan(&bucket, &metricType, &isMonotonic, &unit, &avgValue, &maxValue, &sumValue, &sumCount); err != nil {
			return MetricSeriesResult{}, err
		}
		res.MetricType = metricType
		res.Unit = unit
		value, agg := metricSeriesValue(transform, metricType, isMonotonic, avgValue, maxValue, sumValue, sumCount, prevMax, havePrev, stepSeconds)
		res.Aggregation = agg
		prevMax, havePrev = maxValue, true
		res.Points = append(res.Points, MetricSeriesPoint{Bucket: bucket, Value: safeFloat(value)})
	}
	return res, rows.Err()
}

// MetricServiceSeries is one service's charted series of a metric.
type MetricServiceSeries struct {
	ServiceName string
	Points      []MetricSeriesPoint
}

// MetricSeriesByServiceResult is the by-service charting result: the
// shared metadata (type, unit, aggregation) plus one series per
// emitting service, ordered by service name.
type MetricSeriesByServiceResult struct {
	MetricType  string
	Unit        string
	Aggregation string
	Series      []MetricServiceSeries
}

// MetricSeriesByService returns one time-bucketed series per service
// emitting the metric — the data behind the global Metrics page's
// one-line-per-service chart. serviceFilter narrows to specific
// services when non-empty. Buckets align across services (same
// toStartOfInterval boundaries) so the UI can pivot them into a single
// multi-line dataset. The monotonic-counter increase is tracked
// per-service: prevMax resets at each service boundary.
func (s *Store) MetricSeriesByService(ctx context.Context, metricName string, from, to time.Time, stepSeconds int, serviceFilter []string, transform string) (MetricSeriesByServiceResult, error) {
	if stepSeconds <= 0 {
		stepSeconds = 60
	}
	where := []string{"MetricName = ?", "Timestamp >= ?", "Timestamp <= ?"}
	args := []any{metricName, from, to}
	if len(serviceFilter) > 0 {
		placeholders := make([]string, len(serviceFilter))
		for i, name := range serviceFilter {
			placeholders[i] = "?"
			args = append(args, name)
		}
		where = append(where, "ServiceName IN ("+strings.Join(placeholders, ",")+")")
	}
	sql := fmt.Sprintf(metricSeriesSelect, "ServiceName,", stepSeconds,
		strings.Join(where, " AND "), "ServiceName,", "ServiceName,")

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return MetricSeriesByServiceResult{}, fmt.Errorf("metric series by service: %w", err)
	}
	defer rows.Close()

	var res MetricSeriesByServiceResult
	var cur *MetricServiceSeries
	var prevMax float64
	havePrev := false
	for rows.Next() {
		var (
			serviceName string
			bucket      time.Time
			metricType  string
			isMonotonic uint8
			unit        string
			avgValue    float64
			maxValue    float64
			sumValue    float64
			sumCount    uint64
		)
		if err := rows.Scan(&serviceName, &bucket, &metricType, &isMonotonic, &unit, &avgValue, &maxValue, &sumValue, &sumCount); err != nil {
			return MetricSeriesByServiceResult{}, err
		}
		res.MetricType = metricType
		res.Unit = unit
		// Rows are ordered by ServiceName; a change starts a new series
		// and resets the per-service counter state.
		if cur == nil || cur.ServiceName != serviceName {
			res.Series = append(res.Series, MetricServiceSeries{ServiceName: serviceName})
			cur = &res.Series[len(res.Series)-1]
			prevMax, havePrev = 0, false
		}
		value, agg := metricSeriesValue(transform, metricType, isMonotonic, avgValue, maxValue, sumValue, sumCount, prevMax, havePrev, stepSeconds)
		res.Aggregation = agg
		prevMax, havePrev = maxValue, true
		cur.Points = append(cur.Points, MetricSeriesPoint{Bucket: bucket, Value: safeFloat(value)})
	}
	return res, rows.Err()
}

// MetricCatalogParams narrows the metric explorer catalog: an optional
// name substring, an optional OTLP type, and attribute filters over
// MetricAttributes/ResourceAttributes, AND-ed. StepSeconds is the
// sparkline bucket width.
type MetricCatalogParams struct {
	Service     string
	ServiceIn   []string // ServiceName IN (...); scopes to an integration's services
	NameQuery   string
	MetricType  string
	From        time.Time
	To          time.Time
	StepSeconds int
	Attrs       []LogAttrFilter
	// AttrGroups is a DNF predicate AND-ed on top of Attrs — an
	// integration's OR attribute matchers, against MetricAttributes.
	AttrGroups [][]LogAttrFilter
	// Limit caps how many metrics (alphabetical, after the name filter)
	// are returned — so a broad/empty query never computes values +
	// sparklines for the whole estate. 0 = no limit.
	Limit int
}

// MetricCatalogRow is one metric in the explorer table: the headline
// (type-aware) current value, a sparkline of the windowed series, and
// how many distinct (service, label-set) series roll up into it.
type MetricCatalogRow struct {
	MetricName  string
	MetricType  string
	Unit        string
	IsMonotonic uint8
	Aggregation string // "latest" | "rate" | "mean"
	Value       float64
	SeriesCount uint64
	PointCount  uint64
	LastSeen    time.Time
	Spark       []float64
}

// metricPredicates builds the WHERE clauses + bind args shared by the
// catalog's aggregate and sparkline queries. Attribute filters resolve
// against MetricAttributes first, then ResourceAttributes.
func metricPredicates(p MetricCatalogParams) ([]string, []any) {
	where := []string{"Timestamp >= ?", "Timestamp <= ?"}
	args := []any{p.From, p.To}
	if p.Service != "" {
		where = append(where, "ServiceName = ?")
		args = append(args, p.Service)
	}
	if len(p.ServiceIn) > 0 {
		clause, cargs := inClause("ServiceName", p.ServiceIn)
		where = append(where, clause)
		args = append(args, cargs...)
	}
	if p.NameQuery != "" {
		where = append(where, "positionCaseInsensitive(MetricName, ?) > 0")
		args = append(args, p.NameQuery)
	}
	if p.MetricType != "" {
		where = append(where, "MetricType = ?")
		args = append(args, p.MetricType)
	}
	for _, f := range p.Attrs {
		clause, cargs := attrClauseIn("MetricAttributes", f)
		where = append(where, clause)
		args = append(args, cargs...)
	}
	if clause, cargs := attrGroupsClause("MetricAttributes", p.AttrGroups); clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	return where, args
}

// metricHeadlineValue maps a metric's windowed aggregates to the single
// "current" value the explorer table shows, by OTLP type:
//   - histogram          → mean observation (sum/count)
//   - monotonic sum       → per-second rate over the window
//   - gauge / other sum   → latest reading
func metricHeadlineValue(metricType string, isMonotonic uint8, latest, earliest, sumValue float64, sumCount uint64, spanSeconds float64) (float64, string) {
	switch {
	case metricType == "histogram":
		if sumCount > 0 {
			return sumValue / float64(sumCount), "mean"
		}
		return 0, "mean"
	case metricType == "sum" && isMonotonic == 1:
		if spanSeconds > 0 {
			if r := (latest - earliest) / spanSeconds; r > 0 {
				return r, "rate"
			}
		}
		return 0, "rate"
	default:
		return latest, "latest"
	}
}

// MetricCatalog returns the explorer table: one row per metric matching
// the filters, each with a type-aware headline value, a sparkline, and
// a distinct-series count. Two passes: an aggregate query for the
// headline numbers + series count, and a bucketed query (reusing
// metricSeriesSelect grouped by MetricName) for the sparklines.
func (s *Store) MetricCatalog(ctx context.Context, p MetricCatalogParams) ([]MetricCatalogRow, error) {
	if p.StepSeconds <= 0 {
		p.StepSeconds = 60
	}
	where, args := metricPredicates(p)
	whereSQL := strings.Join(where, " AND ")

	// Bound how many metrics we materialize (values + sparklines) so a
	// broad query never scans the whole estate. p.Limit is an int we
	// control, so it's safe to inline.
	limitSQL := ""
	if p.Limit > 0 {
		limitSQL = fmt.Sprintf("\n\t\tLIMIT %d", p.Limit)
	}

	// Pass 1: per-metric aggregates.
	aggSQL := fmt.Sprintf(`
		SELECT
			MetricName,
			any(MetricType) AS mtype,
			any(Unit) AS unit,
			max(IsMonotonic) AS is_monotonic,
			toUInt64(uniqExact(ServiceName, MetricAttributes)) AS series,
			toUInt64(count()) AS points,
			max(Timestamp) AS last_seen,
			argMax(Value, Timestamp) AS latest_v,
			argMin(Value, Timestamp) AS earliest_v,
			sum(Value) AS sum_v,
			toUInt64(sum(Count)) AS sum_c,
			min(Timestamp) AS first_ts,
			max(Timestamp) AS last_ts
		FROM metrics
		WHERE %s
		GROUP BY MetricName
		ORDER BY MetricName ASC%s
	`, whereSQL, limitSQL)

	rows, err := s.conn.Query(ctx, aggSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("metric catalog aggregate: %w", err)
	}
	defer rows.Close()

	type agg struct {
		idx int
		row MetricCatalogRow
	}
	byName := map[string]*agg{}
	var out []MetricCatalogRow
	for rows.Next() {
		var (
			name        string
			mtype, unit string
			isMono      uint8
			series      uint64
			points      uint64
			lastSeen    time.Time
			latestV     float64
			earliestV   float64
			sumV        float64
			sumC        uint64
			firstTS     time.Time
			lastTS      time.Time
		)
		if err := rows.Scan(&name, &mtype, &unit, &isMono, &series, &points, &lastSeen, &latestV, &earliestV, &sumV, &sumC, &firstTS, &lastTS); err != nil {
			return nil, err
		}
		value, aggregation := metricHeadlineValue(mtype, isMono, latestV, earliestV, sumV, sumC, lastTS.Sub(firstTS).Seconds())
		out = append(out, MetricCatalogRow{
			MetricName:  name,
			MetricType:  mtype,
			Unit:        unit,
			IsMonotonic: isMono,
			Aggregation: aggregation,
			Value:       safeFloat(value),
			SeriesCount: series,
			PointCount:  points,
			LastSeen:    lastSeen,
		})
		byName[name] = &agg{idx: len(out) - 1}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	// Pass 2: sparklines, bucketed per metric (reuse the shared select).
	sparkSQL := fmt.Sprintf(metricSeriesSelect, "MetricName,", p.StepSeconds, whereSQL, "MetricName,", "MetricName,")
	srows, err := s.conn.Query(ctx, sparkSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("metric catalog sparkline: %w", err)
	}
	defer srows.Close()

	var curName string
	var prevMax float64
	havePrev := false
	for srows.Next() {
		var (
			name        string
			bucket      time.Time
			metricType  string
			isMonotonic uint8
			unit        string
			avgValue    float64
			maxValue    float64
			sumValue    float64
			sumCount    uint64
		)
		if err := srows.Scan(&name, &bucket, &metricType, &isMonotonic, &unit, &avgValue, &maxValue, &sumValue, &sumCount); err != nil {
			return nil, err
		}
		if name != curName {
			curName, prevMax, havePrev = name, 0, false
		}
		value, _ := metricBucketValue(metricType, isMonotonic, avgValue, maxValue, sumValue, sumCount, prevMax, havePrev)
		prevMax, havePrev = maxValue, true
		if a, ok := byName[name]; ok {
			out[a.idx].Spark = append(out[a.idx].Spark, safeFloat(value))
		}
	}
	return out, srows.Err()
}

// inClause renders a `col IN (?, ?, …)` predicate plus its bind args.
func inClause(col string, vals []string) (string, []any) {
	ph := make([]string, len(vals))
	args := make([]any, len(vals))
	for i, v := range vals {
		ph[i] = "?"
		args[i] = v
	}
	return col + " IN (" + strings.Join(ph, ", ") + ")", args
}

// MetricGroupRow is one rollup row of the metric catalog grouped by a
// dimension: how many distinct metrics, series, and points fall in it.
type MetricGroupRow struct {
	Key         string
	MetricCount uint64
	SeriesCount uint64
	PointCount  uint64
}

// MetricGroups rolls up the catalog by a dimension — "type", "attribute"
// (using attrKey), or "service" (default) — honouring the same filters
// as MetricCatalog. Empty group keys are dropped. attrKey must be
// validated by the caller (it is interpolated). Integration grouping is
// done by the handler over the "service" rollup.
func (s *Store) MetricGroups(ctx context.Context, p MetricCatalogParams, by, attrKey string) ([]MetricGroupRow, error) {
	where, args := metricPredicates(p)
	var groupExpr string
	switch by {
	case "type":
		groupExpr = "MetricType"
	case "attribute":
		groupExpr = attrEffectiveExprIn("MetricAttributes", attrKey)
		where = append(where, fmt.Sprintf("(mapContains(MetricAttributes, '%[1]s') OR mapContains(ResourceAttributes, '%[1]s'))", attrKey))
	default:
		groupExpr = "ServiceName"
	}
	sql := fmt.Sprintf(`
		SELECT %[1]s AS gk,
		       toUInt64(uniqExact(MetricName)) AS metrics,
		       toUInt64(uniqExact(ServiceName, MetricAttributes)) AS series,
		       toUInt64(count()) AS points
		FROM metrics
		WHERE %[2]s
		GROUP BY gk
		HAVING gk != ''
		ORDER BY metrics DESC, gk ASC
		LIMIT 300
	`, groupExpr, strings.Join(where, " AND "))
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("metric groups: %w", err)
	}
	defer rows.Close()
	var out []MetricGroupRow
	for rows.Next() {
		var r MetricGroupRow
		if err := rows.Scan(&r.Key, &r.MetricCount, &r.SeriesCount, &r.PointCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MetricAttributeKeys discovers the attribute keys present on metrics in
// the window, with usage count, cardinality, and a numeric flag — the
// metrics analogue of DistinctLogAttributeKeys. Both the per-point
// MetricAttributes and the ResourceAttributes are surfaced. A non-empty
// metricName scopes discovery to one metric (the drawer's per-metric
// Attributes section); "" spans all metrics (the explorer filter picker).
func (s *Store) MetricAttributeKeys(ctx context.Context, metricName string, serviceIn []string, from, to time.Time, sampleLimit int) ([]LogAttrKeyRow, error) {
	if sampleLimit <= 0 {
		sampleLimit = 4000
	}
	// The inner samples take the most RECENT rows (ORDER BY Timestamp
	// DESC) so accumulated history — e.g. many cumulative-counter points
	// from earlier with sparser labels — doesn't crowd current
	// attributes out of the bounded sample.
	metricPred := ""
	if metricName != "" {
		metricPred = " AND MetricName = ?"
	}
	// Group-policy service allowlist, appended to BOTH UNION subqueries'
	// WHERE (after the optional MetricName predicate, before the LIMIT).
	svcClause, svcArgs := metricServiceIn(serviceIn)
	if svcClause != "" {
		metricPred += " AND " + svcClause
	}
	q := fmt.Sprintf(`
		SELECT key, toUInt64(sum(uses)) AS uses, toUInt64(max(card)) AS cardinality, min(is_numeric) AS numeric
		FROM (
			SELECT kv.1 AS key,
			       toUInt64(count()) AS uses,
			       toUInt64(uniqExact(kv.2)) AS card,
			       toUInt8(countIf(kv.2 != '') > 0 AND countIf(kv.2 != '' AND isNull(toFloat64OrNull(kv.2))) = 0) AS is_numeric
			FROM (SELECT arrayJoin(MetricAttributes) AS kv FROM (SELECT MetricAttributes FROM metrics WHERE Timestamp >= ? AND Timestamp <= ?%[1]s ORDER BY Timestamp DESC LIMIT ?))
			GROUP BY key
			UNION ALL
			SELECT kv.1 AS key,
			       toUInt64(count()) AS uses,
			       toUInt64(uniqExact(kv.2)) AS card,
			       toUInt8(countIf(kv.2 != '') > 0 AND countIf(kv.2 != '' AND isNull(toFloat64OrNull(kv.2))) = 0) AS is_numeric
			FROM (SELECT arrayJoin(ResourceAttributes) AS kv FROM (SELECT ResourceAttributes FROM metrics WHERE Timestamp >= ? AND Timestamp <= ?%[1]s ORDER BY Timestamp DESC LIMIT ?))
			GROUP BY key
		)
		GROUP BY key
		ORDER BY uses DESC, key ASC
		LIMIT 500
	`, metricPred)
	args := []any{from, to}
	if metricName != "" {
		args = append(args, metricName)
	}
	args = append(args, svcArgs...)
	args = append(args, sampleLimit, from, to)
	if metricName != "" {
		args = append(args, metricName)
	}
	args = append(args, svcArgs...)
	args = append(args, sampleLimit)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("metric attribute keys: %w", err)
	}
	defer rows.Close()
	var out []LogAttrKeyRow
	for rows.Next() {
		var r LogAttrKeyRow
		var numeric uint8
		if err := rows.Scan(&r.Key, &r.UseCount, &r.Cardinality, &numeric); err != nil {
			return nil, err
		}
		r.Numeric = numeric == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// MetricAttributeValues returns the top-N values for one attribute key on
// metrics in the window, ranked by point count — the metrics analogue of
// LogAttrValues. A non-empty metricName scopes to one metric. A non-empty
// search restricts to values containing it (case-insensitive), so the UI
// can show the top 10 yet still find a value buried beyond the cap when an
// attribute has hundreds of distinct values.
func (s *Store) MetricAttributeValues(ctx context.Context, key, metricName, search string, serviceIn []string, from, to time.Time, limit int) ([]LogAttrValueRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	eff := attrEffectiveExprIn("MetricAttributes", key)
	metricPred := ""
	args := []any{from, to}
	if metricName != "" {
		metricPred = " AND MetricName = ?"
		args = append(args, metricName)
	}
	if c, a := metricServiceIn(serviceIn); c != "" {
		metricPred += " AND " + c
		args = append(args, a...)
	}
	searchPred := ""
	if search != "" {
		searchPred = " AND positionCaseInsensitive(v, ?) > 0"
		args = append(args, search)
	}
	args = append(args, limit)
	sql := fmt.Sprintf(`
		SELECT v, toUInt64(count()) AS events
		FROM (
			SELECT %s AS v
			FROM metrics
			WHERE Timestamp >= ? AND Timestamp <= ?%s
			  AND (mapContains(MetricAttributes, '%s') OR mapContains(ResourceAttributes, '%s'))
		)
		WHERE v != ''%s
		GROUP BY v
		ORDER BY events DESC, v ASC
		LIMIT ?
	`, eff, metricPred, key, key, searchPred)
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("metric attribute values: %w", err)
	}
	defer rows.Close()
	var out []LogAttrValueRow
	for rows.Next() {
		var r LogAttrValueRow
		if err := rows.Scan(&r.Value, &r.Events); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UsageVolumeRow is a per-service tally of ingested telemetry: trace
// spans, metric data points + distinct active series, and log records.
type UsageVolumeRow struct {
	Service      string
	Spans        uint64
	MetricPoints uint64
	MetricSeries uint64
	Logs         uint64
}

// UsageVolume returns per-service ingestion counts across the three
// signals — spans (traces), metric data points + active series (metrics)
// and log records (logs). When windowed is false it counts everything
// currently stored in each table (total at rest, after retention/TTL);
// when true it bounds the count to [from, to]. serviceIn, when non-empty,
// restricts to the caller's visible services (G5). One grouped count per
// signal table, merged by service in Go so a service that emits only one
// signal still appears. A metric "series" is a distinct
// (MetricName, MetricAttributes) within a service — the same definition the
// metric catalog uses. Order is unspecified; callers sort. Grand totals
// are the sum of the rows.
func (s *Store) UsageVolume(ctx context.Context, serviceIn []string, from, to time.Time, windowed bool) ([]UsageVolumeRow, error) {
	svcClause, svcArgs := metricServiceIn(serviceIn) // generic "ServiceName IN (...)"
	byService := map[string]*UsageVolumeRow{}
	row := func(name string) *UsageVolumeRow {
		r := byService[name]
		if r == nil {
			r = &UsageVolumeRow{Service: name}
			byService[name] = r
		}
		return r
	}
	buildWhere := func() (string, []any) {
		conds := []string{}
		args := []any{}
		if windowed {
			conds = append(conds, "Timestamp >= ?", "Timestamp <= ?")
			args = append(args, from, to)
		}
		if svcClause != "" {
			conds = append(conds, svcClause)
			args = append(args, svcArgs...)
		}
		if len(conds) == 0 {
			return "", args
		}
		return "WHERE " + strings.Join(conds, " AND "), args
	}

	// Spans + logs: a plain row count per service.
	countOnly := []struct {
		table string
		set   func(*UsageVolumeRow, uint64)
	}{
		{"traces", func(r *UsageVolumeRow, n uint64) { r.Spans = n }},
		{"logs", func(r *UsageVolumeRow, n uint64) { r.Logs = n }},
	}
	for _, sig := range countOnly {
		where, args := buildWhere()
		sql := fmt.Sprintf("SELECT ServiceName, toUInt64(count()) AS n FROM %s %s GROUP BY ServiceName", sig.table, where)
		rows, err := s.conn.Query(ctx, sql, args...)
		if err != nil {
			return nil, fmt.Errorf("usage volume %s: %w", sig.table, err)
		}
		for rows.Next() {
			var name string
			var n uint64
			if err := rows.Scan(&name, &n); err != nil {
				rows.Close()
				return nil, err
			}
			sig.set(row(name), n)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// Metrics: data points (count) + active series (distinct metric+labels).
	{
		where, args := buildWhere()
		sql := fmt.Sprintf(
			"SELECT ServiceName, toUInt64(count()) AS n, toUInt64(uniqExact(MetricName, MetricAttributes)) AS series FROM metrics %s GROUP BY ServiceName",
			where,
		)
		rows, err := s.conn.Query(ctx, sql, args...)
		if err != nil {
			return nil, fmt.Errorf("usage volume metrics: %w", err)
		}
		for rows.Next() {
			var name string
			var n, series uint64
			if err := rows.Scan(&name, &n, &series); err != nil {
				rows.Close()
				return nil, err
			}
			r := row(name)
			r.MetricPoints = n
			r.MetricSeries = series
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	out := make([]UsageVolumeRow, 0, len(byService))
	for _, r := range byService {
		out = append(out, *r)
	}
	return out, nil
}

// SignalStorageRow is the on-disk footprint of one telemetry table.
type SignalStorageRow struct {
	Table           string
	CompressedBytes uint64
	Rows            uint64
}

// TableStorage returns the on-disk (compressed) size and row count of the
// three telemetry tables from system.parts (active parts only) — the real
// storage footprint, whole-table and all-time. Lets the API derive an
// approximate bytes-per-row so per-service counts can be shown as an
// estimated size (exact per-service compressed bytes aren't available;
// compression spans rows of many services).
func (s *Store) TableStorage(ctx context.Context) (map[string]SignalStorageRow, error) {
	const q = `
		SELECT table,
		       toUInt64(sum(data_compressed_bytes)) AS bytes,
		       toUInt64(sum(rows)) AS rows
		FROM system.parts
		WHERE active AND database = currentDatabase() AND table IN ('traces', 'metrics', 'logs')
		GROUP BY table`
	rows, err := s.conn.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("table storage: %w", err)
	}
	defer rows.Close()
	out := map[string]SignalStorageRow{}
	for rows.Next() {
		var r SignalStorageRow
		if err := rows.Scan(&r.Table, &r.CompressedBytes, &r.Rows); err != nil {
			return nil, err
		}
		out[r.Table] = r
	}
	return out, rows.Err()
}

// metricAggExpr maps an alert-rule aggregation keyword to its ClickHouse
// projection over the raw Value column. Unknown keywords fall back to
// avg. The keyword is from a fixed allow-list (never user free-text), so
// interpolating it is safe.
func metricAggExpr(aggregation string) string {
	switch aggregation {
	case "max":
		return "max(Value)"
	case "min":
		return "min(Value)"
	case "sum":
		return "sum(Value)"
	case "p95":
		return "quantile(0.95)(Value)"
	case "last":
		// The reading at the latest timestamp in the window — a
		// point-in-time gauge check (e.g. queue depth) rather than a
		// reduction over the window.
		return "argMax(Value, Timestamp)"
	case "age":
		// Treats the latest VALUE as Unix-epoch seconds and returns how
		// long ago that was — now − value, in seconds. Powers staleness
		// checks on timestamp metrics like file.mtime ("how old is the
		// file", i.e. now − mtime). NOTE: this is now − the metric VALUE,
		// not now − the ingest Timestamp (that would be data freshness).
		// argMax picks the value at the latest sample; pair with gt so the
		// rule fires when the file is older than the threshold seconds.
		return "toUnixTimestamp(now()) - argMax(Value, Timestamp)"
	default:
		return "avg(Value)"
	}
}

// MetricAggregate reduces a metric's points to a single number over the
// window, applying the given aggregation and attribute filters — the
// value an alert rule compares against its threshold. Returns the
// aggregate and the matched sample count (0 ⇒ no data, value is 0).
// serviceIn (when non-empty) restricts the aggregate to a set of
// services — the caller's policy allowlist. Pass nil for no restriction
// (org admins, and the alert evaluator, which is a trusted system path).
func (s *Store) MetricAggregate(ctx context.Context, metricName string, attrs []LogAttrFilter, aggregation string, from, to time.Time, serviceIn []string) (float64, uint64, error) {
	where := []string{"MetricName = ?", "Timestamp >= ?", "Timestamp <= ?"}
	args := []any{metricName, from, to}
	for _, f := range attrs {
		clause, cargs := attrClauseIn("MetricAttributes", f)
		where = append(where, clause)
		args = append(args, cargs...)
	}
	if len(serviceIn) > 0 {
		ph := make([]string, len(serviceIn))
		for i, n := range serviceIn {
			ph[i] = "?"
			args = append(args, n)
		}
		where = append(where, "ServiceName IN ("+strings.Join(ph, ",")+")")
	}
	var sql string
	if aggregation == "increase" || aggregation == "rate" {
		// Counter delta: rise per series (guarded against resets), summed
		// across series. A "series" is one (service, namespace, resource
		// attrs, metric attrs) stream — pooling max/min across series would
		// undercount, so reduce per series first then sum.
		sql = fmt.Sprintf(`
			SELECT sum(delta) AS v, sum(cnt) AS n FROM (
				SELECT greatest(max(Value) - min(Value), 0) AS delta, toUInt64(count()) AS cnt
				FROM metrics
				WHERE %s
				GROUP BY ServiceName, ServiceNamespace, MetricAttributes, ResourceAttributes
			)
		`, strings.Join(where, " AND "))
	} else {
		sql = fmt.Sprintf(`
			SELECT %s AS v, toUInt64(count()) AS n
			FROM metrics
			WHERE %s
		`, metricAggExpr(aggregation), strings.Join(where, " AND "))
	}

	var (
		v float64
		n uint64
	)
	if err := s.conn.QueryRow(ctx, sql, args...).Scan(&v, &n); err != nil {
		return 0, 0, fmt.Errorf("metric aggregate: %w", err)
	}
	if n == 0 {
		return 0, 0, nil
	}
	if aggregation == "rate" {
		if secs := to.Sub(from).Seconds(); secs > 0 {
			v = v / secs
		}
	}
	return safeFloat(v), n, nil
}

// MetricGroupAggregate is one (attribute value → aggregate) row from
// MetricAggregateGrouped.
type MetricGroupAggregate struct {
	Label   string
	Value   float64
	Samples uint64
}

// metricGroupCap bounds how many distinct split values a single grouped
// evaluation returns — a guard against a high-cardinality split key
// (e.g. accidentally splitting by a per-message id) blowing up the
// alert. Highest values first, so the cap keeps the most-breaching ones.
const metricGroupCap = 500

// MetricAggregateGrouped reduces a metric the same way as MetricAggregate
// but per distinct value of splitKey (a metric attribute), returning one
// row per value. Backs split-by alert rules ("DLQ depth split by
// queue_name"): the caller compares each row to the threshold and
// enumerates the breaching values. Rows are ordered by value descending
// and capped at metricGroupCap.
// serviceIn (when non-empty) restricts the aggregate to the caller's
// policy-visible services; pass nil for no restriction (admins / the
// trusted evaluator path).
func (s *Store) MetricAggregateGrouped(ctx context.Context, metricName string, attrs []LogAttrFilter, aggregation, splitKey string, from, to time.Time, serviceIn []string) ([]MetricGroupAggregate, error) {
	where := []string{"MetricName = ?", "Timestamp >= ?", "Timestamp <= ?"}
	args := []any{metricName, from, to}
	for _, f := range attrs {
		clause, cargs := attrClauseIn("MetricAttributes", f)
		where = append(where, clause)
		args = append(args, cargs...)
	}
	if len(serviceIn) > 0 {
		ph := make([]string, len(serviceIn))
		for i, n := range serviceIn {
			ph[i] = "?"
			args = append(args, n)
		}
		where = append(where, "ServiceName IN ("+strings.Join(ph, ",")+")")
	}
	label := attrEffectiveExprIn("MetricAttributes", splitKey)
	var sql string
	if aggregation == "increase" || aggregation == "rate" {
		// Counter delta per split value: reduce per (label, series) then sum
		// per label — same per-series-then-sum reasoning as MetricAggregate.
		sql = fmt.Sprintf(`
			SELECT label, sum(delta) AS v, sum(cnt) AS n FROM (
				SELECT %s AS label, greatest(max(Value) - min(Value), 0) AS delta, toUInt64(count()) AS cnt
				FROM metrics
				WHERE %s
				GROUP BY label, ServiceName, ServiceNamespace, MetricAttributes, ResourceAttributes
			)
			GROUP BY label
			HAVING n > 0
			ORDER BY v DESC
			LIMIT %d
		`, label, strings.Join(where, " AND "), metricGroupCap)
	} else {
		sql = fmt.Sprintf(`
			SELECT %s AS label, %s AS v, toUInt64(count()) AS n
			FROM metrics
			WHERE %s
			GROUP BY label
			HAVING n > 0
			ORDER BY v DESC
			LIMIT %d
		`, label, metricAggExpr(aggregation), strings.Join(where, " AND "), metricGroupCap)
	}

	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("metric aggregate grouped: %w", err)
	}
	defer rows.Close()
	secs := to.Sub(from).Seconds()
	var out []MetricGroupAggregate
	for rows.Next() {
		var g MetricGroupAggregate
		if err := rows.Scan(&g.Label, &g.Value, &g.Samples); err != nil {
			return nil, err
		}
		g.Value = safeFloat(g.Value)
		if aggregation == "rate" && secs > 0 {
			g.Value = g.Value / secs
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
