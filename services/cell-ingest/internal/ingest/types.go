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
}

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
