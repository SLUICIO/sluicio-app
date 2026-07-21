// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package servicetypes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetmappings"
)

// AttrSource is where a widget should pull an attribute from when
// grouping or filtering.
type AttrSource string

const (
	AttrSourceSpan     AttrSource = "span"
	AttrSourceResource AttrSource = "resource"
)

// SpanAttrEqual is one key/value equality constraint on SpanAttributes.
type SpanAttrEqual struct {
	Key   string
	Value string
}

// SpanFilter narrows the spans a widget considers. Multiple non-zero
// fields are AND-ed; multi-value lists inside a field are OR-ed.
// Intentionally constrained — every condition maps cleanly to a
// ClickHouse predicate.
type SpanFilter struct {
	// SpanKinds restricts to the listed span kinds (OR within).
	SpanKinds []string
	// HasSpanAttrAny matches spans where at least one of these
	// attribute keys is present in SpanAttributes.
	HasSpanAttrAny []string
	// AttrEquals matches spans where every listed key in
	// SpanAttributes equals the given value (AND across the slice).
	// Used by I/O facets to scope widgets to a specific
	// (io.kind, io.role) pair without listing every attribute key
	// that might be present on those spans.
	AttrEquals []SpanAttrEqual
}

// SQL renders the filter as a SQL fragment and its arguments using
// the supplied IO attribute resolver. The resolver lets the cell-api
// substitute user-defined facet attribute mappings for the raw
// SpanAttributes['io.kind'] / SpanAttributes['io.role'] lookups, so
// legacy services that don't emit those attributes can still be
// classified into the I/O facets. Callers without rules can pass
// facetmappings.IdentityResolver() to preserve the original behaviour.
//
// Returned fragment is suffixable to a WHERE clause (returns "" if
// empty). Callers join with " AND ".
func (f SpanFilter) SQL(resolver facetmappings.Resolver) (string, []any) {
	var parts []string
	var args []any
	if len(f.SpanKinds) > 0 {
		placeholders := make([]string, len(f.SpanKinds))
		for i, k := range f.SpanKinds {
			placeholders[i] = "?"
			args = append(args, k)
		}
		parts = append(parts, "SpanKind IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(f.HasSpanAttrAny) > 0 {
		var or []string
		for _, k := range f.HasSpanAttrAny {
			or = append(or, "mapContains(SpanAttributes, ?)")
			args = append(args, k)
		}
		parts = append(parts, "("+strings.Join(or, " OR ")+")")
	}
	for _, e := range f.AttrEquals {
		switch e.Key {
		case "io.kind":
			parts = append(parts, "("+resolver.KindExpr+") = ?")
			args = append(args, resolver.KindArgs...)
			args = append(args, e.Value)
		case "io.role":
			parts = append(parts, "("+resolver.RoleExpr+") = ?")
			args = append(args, resolver.RoleArgs...)
			args = append(args, e.Value)
		default:
			parts = append(parts, "SpanAttributes[?] = ?")
			args = append(args, e.Key, e.Value)
		}
	}
	return strings.Join(parts, " AND "), args
}

// Widget is a single dashboard tile a service facet contributes to
// the service detail page. The resolver is passed in at compute time
// so widgets can apply user-defined facet attribute mappings without
// any per-widget configuration — the caller builds the resolver once
// per service per request and threads it through to every widget.
type Widget interface {
	Kind() string
	Name() string
	Description() string
	Compute(ctx context.Context, conn driver.Conn, resolver facetmappings.Resolver, serviceName string, from, to time.Time) (any, error)
}

// --- shared SQL helpers ---

// composeFilters builds a WHERE clause from the always-present
// ServiceName + range plus the widget's SpanFilter. The resolver
// flows through SpanFilter.SQL so io.kind / io.role checks pick up
// any user-defined mappings.
func composeFilters(base SpanFilter, resolver facetmappings.Resolver, serviceName string, from, to time.Time) (string, []any) {
	args := []any{serviceName, from, to}
	clause := "ServiceName = ? AND Timestamp >= ? AND Timestamp <= ?"
	if extra, extraArgs := base.SQL(resolver); extra != "" {
		clause += " AND " + extra
		args = append(args, extraArgs...)
	}
	return clause, args
}

// --- widget data types ---

// TimePoint is one observation in a time series.
type TimePoint struct {
	Timestamp time.Time `json:"ts"`
	Value     float64   `json:"value"`
}

// ErrorRatePoint reports total + errors per bucket so the frontend can
// show both the rate and the absolute volumes.
type ErrorRatePoint struct {
	Timestamp time.Time `json:"ts"`
	Total     uint64    `json:"total"`
	Errors    uint64    `json:"errors"`
	Rate      float64   `json:"rate"`
}

// LatencyPoint reports the configured percentiles per bucket.
type LatencyPoint struct {
	Timestamp time.Time `json:"ts"`
	P50Ms     float64   `json:"p50_ms"`
	P95Ms     float64   `json:"p95_ms"`
	P99Ms     float64   `json:"p99_ms"`
}

// BreakdownRow is one entry in a top-N breakdown.
type BreakdownRow struct {
	Key    string `json:"key"`
	Total  uint64 `json:"total"`
	Errors uint64 `json:"errors"`
}

// --- ThroughputWidget ---

type ThroughputWidget struct {
	WName        string
	WDescription string
	Filter       SpanFilter
}

func (w ThroughputWidget) Kind() string        { return "throughput" }
func (w ThroughputWidget) Name() string        { return w.WName }
func (w ThroughputWidget) Description() string { return w.WDescription }

func (w ThroughputWidget) Compute(ctx context.Context, conn driver.Conn, resolver facetmappings.Resolver, serviceName string, from, to time.Time) (any, error) {
	clause, args := composeFilters(w.Filter, resolver, serviceName, from, to)
	sql := fmt.Sprintf(`
		SELECT toStartOfMinute(Timestamp) AS ts, toUInt64(count()) AS v
		FROM traces
		WHERE %s
		GROUP BY ts
		ORDER BY ts
	`, clause)
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("throughput query: %w", err)
	}
	defer rows.Close()
	out := make([]TimePoint, 0)
	for rows.Next() {
		var ts time.Time
		var v uint64
		if err := rows.Scan(&ts, &v); err != nil {
			return nil, err
		}
		out = append(out, TimePoint{Timestamp: ts, Value: float64(v)})
	}
	return out, rows.Err()
}

// --- ErrorRateWidget ---

type ErrorRateWidget struct {
	WName        string
	WDescription string
	Filter       SpanFilter
}

func (w ErrorRateWidget) Kind() string        { return "error_rate" }
func (w ErrorRateWidget) Name() string        { return w.WName }
func (w ErrorRateWidget) Description() string { return w.WDescription }

func (w ErrorRateWidget) Compute(ctx context.Context, conn driver.Conn, resolver facetmappings.Resolver, serviceName string, from, to time.Time) (any, error) {
	clause, args := composeFilters(w.Filter, resolver, serviceName, from, to)
	sql := fmt.Sprintf(`
		SELECT toStartOfMinute(Timestamp) AS ts,
		       toUInt64(count()) AS total,
		       toUInt64(countIf(StatusCode = 'Error')) AS errors
		FROM traces
		WHERE %s
		GROUP BY ts
		ORDER BY ts
	`, clause)
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("error_rate query: %w", err)
	}
	defer rows.Close()
	out := make([]ErrorRatePoint, 0)
	for rows.Next() {
		var p ErrorRatePoint
		if err := rows.Scan(&p.Timestamp, &p.Total, &p.Errors); err != nil {
			return nil, err
		}
		if p.Total > 0 {
			p.Rate = float64(p.Errors) / float64(p.Total)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- LatencyWidget ---

type LatencyWidget struct {
	WName        string
	WDescription string
	Filter       SpanFilter
}

func (w LatencyWidget) Kind() string        { return "latency" }
func (w LatencyWidget) Name() string        { return w.WName }
func (w LatencyWidget) Description() string { return w.WDescription }

func (w LatencyWidget) Compute(ctx context.Context, conn driver.Conn, resolver facetmappings.Resolver, serviceName string, from, to time.Time) (any, error) {
	clause, args := composeFilters(w.Filter, resolver, serviceName, from, to)
	sql := fmt.Sprintf(`
		SELECT toStartOfMinute(Timestamp) AS ts,
		       quantile(0.5)(DurationNs) / 1000000 AS p50,
		       quantile(0.95)(DurationNs) / 1000000 AS p95,
		       quantile(0.99)(DurationNs) / 1000000 AS p99
		FROM traces
		WHERE %s
		GROUP BY ts
		ORDER BY ts
	`, clause)
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("latency query: %w", err)
	}
	defer rows.Close()
	out := make([]LatencyPoint, 0)
	for rows.Next() {
		var p LatencyPoint
		if err := rows.Scan(&p.Timestamp, &p.P50Ms, &p.P95Ms, &p.P99Ms); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- BreakdownWidget ---

type BreakdownWidget struct {
	WName        string
	WDescription string
	Filter       SpanFilter
	// Source picks resource vs span attribute.
	Source AttrSource
	// Attribute is the key inside the chosen map. For intrinsic span
	// fields, see IntrinsicColumn below.
	Attribute string
	// IntrinsicColumn, when set, overrides Source/Attribute and groups
	// by a ClickHouse column directly (e.g. "SpanName", "SpanKind").
	IntrinsicColumn string
	// TopN limits how many rows are returned. Defaults to 10.
	TopN int
}

func (w BreakdownWidget) Kind() string        { return "breakdown" }
func (w BreakdownWidget) Name() string        { return w.WName }
func (w BreakdownWidget) Description() string { return w.WDescription }

func (w BreakdownWidget) Compute(ctx context.Context, conn driver.Conn, resolver facetmappings.Resolver, serviceName string, from, to time.Time) (any, error) {
	clauseArgs := []any{}
	clause, baseArgs := composeFilters(w.Filter, resolver, serviceName, from, to)
	clauseArgs = append(clauseArgs, baseArgs...)

	// keyExpr is interpolated TWICE into the final SQL (once in SELECT
	// AS k, once in the WHERE k != '' guard). When that expression
	// contains a `?` placeholder (attribute-source case), each copy
	// needs its own arg — otherwise the ClickHouse driver complains
	// "have no arg for param ? at last 1 positions". keyArgs captures
	// what to bind PER OCCURRENCE; we splice it in twice below in the
	// right positional order.
	var keyExpr string
	var keyArgs []any
	switch {
	case w.IntrinsicColumn != "":
		// Allowlist intrinsic columns we permit grouping by.
		allowed := map[string]bool{
			"SpanName": true, "SpanKind": true, "StatusCode": true, "ServiceNamespace": true,
		}
		if !allowed[w.IntrinsicColumn] {
			return nil, fmt.Errorf("breakdown: intrinsic column %q not allowed", w.IntrinsicColumn)
		}
		keyExpr = w.IntrinsicColumn
		// No placeholder in an intrinsic column reference, keyArgs stays nil.
	case w.Source == AttrSourceResource:
		keyExpr = "ResourceAttributes[?]"
		keyArgs = []any{w.Attribute}
	default:
		keyExpr = "SpanAttributes[?]"
		keyArgs = []any{w.Attribute}
	}

	limit := w.TopN
	if limit <= 0 {
		limit = 10
	}

	sql := fmt.Sprintf(`
		SELECT %s AS k,
		       toUInt64(count()) AS total,
		       toUInt64(countIf(StatusCode = 'Error')) AS errors
		FROM traces
		WHERE %s AND %s != ''
		GROUP BY k
		ORDER BY total DESC
		LIMIT ?
	`, keyExpr, clause, keyExpr)

	// Bind args in document order to match positional `?`s:
	//   1. keyExpr's `?` in SELECT          (if any)
	//   2. clause's `?`s in WHERE
	//   3. keyExpr's `?` in WHERE k != ''   (if any)
	//   4. LIMIT's `?`
	args := make([]any, 0, len(keyArgs)*2+len(clauseArgs)+1)
	args = append(args, keyArgs...)
	args = append(args, clauseArgs...)
	args = append(args, keyArgs...)
	args = append(args, limit)

	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("breakdown query: %w", err)
	}
	defer rows.Close()
	out := make([]BreakdownRow, 0)
	for rows.Next() {
		var r BreakdownRow
		if err := rows.Scan(&r.Key, &r.Total, &r.Errors); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- CounterWidget ---

// CounterAggregation determines what the counter measures.
type CounterAggregation string

const (
	CounterSpans  CounterAggregation = "spans"
	CounterErrors CounterAggregation = "errors"
	CounterTraces CounterAggregation = "traces"
)

type CounterWidget struct {
	WName        string
	WDescription string
	Subtitle     string
	Filter       SpanFilter
	Aggregation  CounterAggregation
}

func (w CounterWidget) Kind() string        { return "counter" }
func (w CounterWidget) Name() string        { return w.WName }
func (w CounterWidget) Description() string { return w.WDescription }

type CounterValue struct {
	Value    uint64 `json:"value"`
	Subtitle string `json:"subtitle,omitempty"`
}

func (w CounterWidget) Compute(ctx context.Context, conn driver.Conn, resolver facetmappings.Resolver, serviceName string, from, to time.Time) (any, error) {
	clause, args := composeFilters(w.Filter, resolver, serviceName, from, to)
	var expr string
	switch w.Aggregation {
	case CounterErrors:
		expr = "toUInt64(countIf(StatusCode = 'Error'))"
	case CounterTraces:
		expr = "toUInt64(uniqExact(TraceId))"
	default:
		expr = "toUInt64(count())"
	}
	sql := fmt.Sprintf(`SELECT %s FROM traces WHERE %s`, expr, clause)
	row := conn.QueryRow(ctx, sql, args...)
	var v uint64
	if err := row.Scan(&v); err != nil {
		return nil, fmt.Errorf("counter query: %w", err)
	}
	return CounterValue{Value: v, Subtitle: w.Subtitle}, nil
}
