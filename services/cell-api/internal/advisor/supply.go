// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Supply-side queries: what the cell actually ingests, per metric, per
// log stream, per span attribute. The counterweight to the demand
// ledger.
//
// Every query here carries a first-seen timestamp, because the
// quarantine guardrail needs it: telemetry younger than the observation
// window is never judged. A metric added last Tuesday has no consumption
// history to be absent from, and proposing its removal because "nobody
// looked in 30 days" would be an accusation the data cannot support.
//
// Byte figures are ESTIMATES and named as such throughout. ClickHouse
// knows compressed part sizes per table, not per key, so per-key cost is
// derived from average uncompressed lengths. Good enough to rank
// suggestions and to say "roughly this much"; not good enough to put on
// an invoice, and the UI must not imply otherwise.
package advisor

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// MetricSupply is one metric name's ingest footprint.
type MetricSupply struct {
	Name      string
	Rows      uint64
	Series    uint64
	Services  []string
	FirstSeen time.Time
	LastSeen  time.Time
	// EstBytesPerDay is a rough uncompressed estimate — see file comment.
	EstBytesPerDay uint64
}

// LogStreamSupply is one (service, severity band) log stream.
type LogStreamSupply struct {
	Service        string
	SeverityNumber int32
	SeverityText   string
	Rows           uint64
	FirstSeen      time.Time
	LastSeen       time.Time
	EstBytesPerDay uint64
}

// SpanAttrSupply is one attribute key on one service's spans.
type SpanAttrSupply struct {
	Service string
	Key     string
	// Spans carrying the key, and the service's total, so the evaluator
	// can require the attribute be near-universal before calling it
	// dead weight — a key on 2% of spans is a debug flag, not a cost.
	SpansWithKey   uint64
	SpansTotal     uint64
	DistinctValues uint64
	AvgValueBytes  float64
	FirstSeen      time.Time
	EstBytesPerDay uint64
}

// perDay scales a window total to a daily rate. Windows shorter than a
// day still report their own total rather than being extrapolated
// upward, which would make a fresh cell look far more expensive than it
// is.
func perDay(total uint64, from, to time.Time) uint64 {
	days := to.Sub(from).Hours() / 24
	if days < 1 {
		return total
	}
	return uint64(float64(total) / days)
}

// MetricsSupply lists every metric name ingested in the window.
func MetricsSupply(ctx context.Context, conn driver.Conn, orgID uuid.UUID, from, to time.Time) ([]MetricSupply, error) {
	rows, err := conn.Query(ctx, `
		SELECT MetricName,
		       count()                                   AS rows,
		       uniqExact((ServiceName, mapSort(MetricAttributes))) AS series,
		       groupUniqArray(10)(ServiceName)            AS services,
		       min(Timestamp)                             AS firstSeen,
		       max(Timestamp)                             AS lastSeen,
		       -- Rough per-row cost: the name, the value, and the
		       -- attribute map as stored.
		       sum(length(MetricName) + 8 +
		           length(arrayStringConcat(mapKeys(MetricAttributes), '')) +
		           length(arrayStringConcat(mapValues(MetricAttributes), ''))) AS bytes
		FROM metrics
		WHERE OrganizationId = ? AND Timestamp >= ? AND Timestamp < ?
		GROUP BY MetricName`, orgID.String(), from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricSupply{}
	for rows.Next() {
		var m MetricSupply
		var bytes uint64
		if err := rows.Scan(&m.Name, &m.Rows, &m.Series, &m.Services, &m.FirstSeen, &m.LastSeen, &bytes); err != nil {
			return nil, err
		}
		m.EstBytesPerDay = perDay(bytes, from, to)
		out = append(out, m)
	}
	return out, rows.Err()
}

// LogStreamsSupply groups log volume by service and severity band.
//
// Bands, not exact severity numbers: emitters use the whole 1-24 range
// inconsistently, and a suggestion has to map onto the one control an
// operator actually has — a severity FLOOR in the collector.
func LogStreamsSupply(ctx context.Context, conn driver.Conn, orgID uuid.UUID, from, to time.Time) ([]LogStreamSupply, error) {
	rows, err := conn.Query(ctx, `
		SELECT ServiceName,
		       -- Collapse to the band floor: 1 trace, 5 debug, 9 info,
		       -- 13 warn, 17 error, 21 fatal.
		       --
		       -- Cast explicitly: arithmetic on an Int32 column widens to
		       -- Int64 in ClickHouse, and the driver refuses to narrow it
		       -- on scan. Doing it in SQL keeps the Go struct field the
		       -- same type as SeverityNumber everywhere else.
		       --
		       -- 0 (unspecified) stays 0: a record without a severity
		       -- is not a trace record, and may well be an error the
		       -- emitter never labelled.
		       toInt32(if(SeverityNumber = 0, 0, intDiv(SeverityNumber - 1, 4) * 4 + 1)) AS band,
		       any(SeverityText)                                  AS severityText,
		       count()                                            AS rows,
		       min(Timestamp)                                     AS firstSeen,
		       max(Timestamp)                                     AS lastSeen,
		       sum(length(Body) +
		           length(arrayStringConcat(mapKeys(LogAttributes), '')) +
		           length(arrayStringConcat(mapValues(LogAttributes), ''))) AS bytes
		FROM logs
		WHERE OrganizationId = ? AND Timestamp >= ? AND Timestamp < ?
		GROUP BY ServiceName, band`, orgID.String(), from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogStreamSupply{}
	for rows.Next() {
		var s LogStreamSupply
		var band int32
		var bytes uint64
		if err := rows.Scan(&s.Service, &band, &s.SeverityText, &s.Rows, &s.FirstSeen, &s.LastSeen, &bytes); err != nil {
			return nil, err
		}
		s.SeverityNumber = band
		s.EstBytesPerDay = perDay(bytes, from, to)
		out = append(out, s)
	}
	return out, rows.Err()
}

// SpanAttrsSupply lists span attribute keys per service.
//
// Sampled: walking every span's attribute map across a month is the most
// expensive query in the advisor by an order of magnitude, and the
// evaluator only needs proportions. The sample is per-service so a
// low-traffic service is not drowned out by a busy one.
func SpanAttrsSupply(ctx context.Context, conn driver.Conn, orgID uuid.UUID, from, to time.Time, sampleRows int) ([]SpanAttrSupply, error) {
	if sampleRows <= 0 {
		sampleRows = 200_000
	}
	rows, err := conn.Query(ctx, `
		WITH sampled AS (
			SELECT ServiceName, SpanAttributes, Timestamp
			FROM traces
			WHERE OrganizationId = ? AND Timestamp >= ? AND Timestamp < ?
			LIMIT ?
		),
		totals AS (
			SELECT ServiceName, count() AS spansTotal FROM sampled GROUP BY ServiceName
		)
		SELECT s.ServiceName,
		       kv.1                        AS attrKey,
		       count()                     AS spansWithKey,
		       any(t.spansTotal)           AS spansTotal,
		       uniqExact(kv.2)             AS distinctValues,
		       avg(length(kv.2))           AS avgValueBytes,
		       min(s.Timestamp)            AS firstSeen,
		       sum(length(kv.1) + length(kv.2)) AS bytes
		FROM sampled AS s
		ARRAY JOIN SpanAttributes AS kv
		INNER JOIN totals AS t ON t.ServiceName = s.ServiceName
		GROUP BY s.ServiceName, attrKey`,
		orgID.String(), from, to, sampleRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpanAttrSupply{}
	for rows.Next() {
		var a SpanAttrSupply
		var bytes uint64
		if err := rows.Scan(&a.Service, &a.Key, &a.SpansWithKey, &a.SpansTotal,
			&a.DistinctValues, &a.AvgValueBytes, &a.FirstSeen, &bytes); err != nil {
			return nil, err
		}
		a.EstBytesPerDay = perDay(bytes, from, to)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SampleAttrValues pulls a few values for one key, for the PII scan
// (T6). Values never leave the process — only the VERDICT is stored, so
// a matched personal number does not end up copied into the suggestion
// table that made the finding.
func SampleAttrValues(ctx context.Context, conn driver.Conn, orgID uuid.UUID, service, key string, from, to time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := conn.Query(ctx, `
		SELECT DISTINCT SpanAttributes[?] AS v
		FROM traces
		WHERE OrganizationId = ? AND ServiceName = ? AND Timestamp >= ? AND Timestamp < ?
		  AND has(mapKeys(SpanAttributes), ?) AND v != ''
		LIMIT ?`, key, orgID.String(), service, from, to, key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SpanNameSupply is one span name on one service, with what decides
// whether its spans can go without anything in Sluicio losing them (T7).
type SpanNameSupply struct {
	Service string
	Name    string
	Spans   uint64
	// NotInternal counts spans of another kind than Internal; Foreign
	// counts spans whose parent is not a span of the same service in the
	// same trace (a root, a call from another service, or a parent not
	// seen); Parents counts spans that are the parent of another span;
	// Linked counts spans with span links. Any of them rules a name out.
	NotInternal uint64
	Foreign     uint64
	Parents     uint64
	Linked      uint64
	// Keys are the attribute keys its spans carry (up to 50).
	Keys           []string
	FirstSeen      time.Time
	EstBytesPerDay uint64
}

// SpanNamesSupply reads span names per service over a sample of whole
// traces in the last day of the window. Whole traces, so a span's
// children and parent are always in the sample with it: a sample cut
// through a trace could make a parent look like a leaf. The first-seen
// time comes from the whole window, for the quarantine.
func SpanNamesSupply(ctx context.Context, conn driver.Conn, orgID uuid.UUID, from, to time.Time, sampleTraces int) ([]SpanNameSupply, error) {
	if sampleTraces <= 0 {
		sampleTraces = 20_000
	}
	day := to.Add(-24 * time.Hour)
	if day.Before(from) {
		day = from
	}
	rows, err := conn.Query(ctx, `
		WITH
		  sampled AS (
		    SELECT TraceId FROM traces
		    WHERE OrganizationId = ? AND Timestamp >= ? AND Timestamp < ?
		    GROUP BY TraceId LIMIT ?
		  ),
		  s AS (
		    SELECT TraceId, SpanId, ParentSpanId, ServiceName, SpanName, SpanKind, LinksTotal,
		           mapKeys(SpanAttributes) AS keys,
		           length(SpanName) + length(arrayStringConcat(mapKeys(SpanAttributes), '')) +
		           length(arrayStringConcat(mapValues(SpanAttributes), '')) + 60 AS bytes
		    FROM traces
		    WHERE OrganizationId = ? AND Timestamp >= ? AND Timestamp < ? AND TraceId IN (SELECT TraceId FROM sampled)
		  ),
		  parents AS (SELECT DISTINCT TraceId, ParentSpanId AS SpanId FROM s WHERE ParentSpanId != ''),
		  firsts AS (
		    SELECT ServiceName, SpanName, min(Timestamp) AS firstSeen FROM traces
		    WHERE OrganizationId = ? AND Timestamp >= ? AND Timestamp < ?
		    GROUP BY ServiceName, SpanName
		  )
		SELECT c.ServiceName, c.SpanName,
		       count() AS spans,
		       countIf(c.SpanKind != 'Internal') AS notInternal,
		       countIf(p.ServiceName != c.ServiceName) AS foreign,
		       countIf((c.TraceId, c.SpanId) IN (SELECT TraceId, SpanId FROM parents)) AS parentsOf,
		       countIf(c.LinksTotal > 0) AS linked,
		       groupUniqArrayArray(50)(c.keys) AS keys,
		       any(f.firstSeen) AS firstSeen,
		       sum(c.bytes) AS bytes
		FROM s AS c
		LEFT JOIN s AS p ON p.TraceId = c.TraceId AND p.SpanId = c.ParentSpanId
		LEFT JOIN firsts AS f ON f.ServiceName = c.ServiceName AND f.SpanName = c.SpanName
		GROUP BY c.ServiceName, c.SpanName`,
		orgID.String(), day, to, sampleTraces,
		orgID.String(), day.Add(-time.Hour), to,
		orgID.String(), from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpanNameSupply{}
	sampleDay := to.Sub(day)
	for rows.Next() {
		var s SpanNameSupply
		var bytes uint64
		if err := rows.Scan(&s.Service, &s.Name, &s.Spans, &s.NotInternal, &s.Foreign, &s.Parents, &s.Linked, &s.Keys, &s.FirstSeen, &bytes); err != nil {
			return nil, err
		}
		// A sample of traces, not of the day: the estimate is per sampled
		// day, which undercounts on a busy cell. It only has to clear the
		// floor, not be a bill.
		s.EstBytesPerDay = perDay(bytes, day, day.Add(sampleDay))
		out = append(out, s)
	}
	return out, rows.Err()
}
