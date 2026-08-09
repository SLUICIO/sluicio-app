// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import "time"

// SpanRow is one row in the ClickHouse `traces` table. It is the
// internal representation a converted OTLP span is held in before it
// is appended to a ClickHouse batch.
type SpanRow struct {
	Timestamp          time.Time
	TraceID            string
	SpanID             string
	ParentSpanID       string
	SpanName           string
	SpanKind           string
	ServiceName        string
	ServiceNamespace   string
	OrganizationID     string // resolved from the ingest API key
	ResourceAttributes map[string]string
	SpanAttributes     map[string]string
	DurationNs         uint64
	StatusCode         string
	StatusMessage      string
	// LinkTraceIDs / LinkSpanIDs are the spans this one links to, in
	// order, truncated to MaxSpanLinks. Links are how ASYNCHRONOUS
	// hand-offs are expressed under the OTel messaging conventions: a
	// queue, a scheduled retry, a delayed delivery. Without them a
	// message handed to a delayed second trace looks like a message that
	// stopped (issue #19).
	LinkTraceIDs []string
	LinkSpanIDs  []string
	// LinksTotal is how many links the span actually carried, before
	// truncation. Kept separately so a truncated span can say "linked to
	// 500, showing 32" instead of quietly presenting 32 as all of them.
	LinksTotal uint32
}

// MaxSpanLinks caps how many links are stored per span.
//
// Links are unbounded in the protocol, and the distribution is bimodal:
// almost every span carries zero or one, while a batch consumer links to
// every message in its batch. A cap tuned to the first mode would mangle
// the second.
//
// 32 rather than 8, and the reason is not cost. The decision is
// ASYMMETRIC: raising the cap later does not recover links already
// discarded, while lowering it is free. Eight would cover every hand-off
// shape known today, but that is calibrated against the cases we already
// understand. Thirty-two keeps a small batch whole, costs almost nothing
// because the median span has no links at all, and leaves room for a use
// nobody has thought of yet.
const MaxSpanLinks = 32

// LogRow is one row in the ClickHouse `logs` table — a converted OTLP
// LogRecord. Body holds the rendered log message.
type LogRow struct {
	Timestamp          time.Time
	ObservedTimestamp  time.Time
	TraceID            string
	SpanID             string
	SeverityNumber     int32
	SeverityText       string
	ServiceName        string
	ServiceNamespace   string
	OrganizationID     string // resolved from the ingest API key
	ScopeName          string
	Body               string
	ResourceAttributes map[string]string
	LogAttributes      map[string]string
}

// MetricRow is one row in the ClickHouse `metrics` table — one numeric
// data point of an OTLP metric. MetricType is "gauge", "sum", or
// "histogram"; for histograms Value is the bucket sum and Count is the
// observation count, otherwise Count is 0. IsMonotonic is 1 for
// monotonic sums (true counters), else 0.
type MetricRow struct {
	Timestamp          time.Time
	StartTimestamp     time.Time
	MetricName         string
	MetricType         string
	ServiceName        string
	ServiceNamespace   string
	OrganizationID     string // resolved from the ingest API key
	Value              float64
	Count              uint64
	IsMonotonic        uint8
	Unit               string
	ResourceAttributes map[string]string
	MetricAttributes   map[string]string
}
