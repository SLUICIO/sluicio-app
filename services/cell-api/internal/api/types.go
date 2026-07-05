// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package api contains the cell-api HTTP handlers and the JSON shapes
// they return to the frontend.
package api

import (
	"time"

	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/erroracks"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/integrations"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/tags"
)

// IntegrationRef is a compact reference to an integration, attached
// to services and search results to show grouping.
type IntegrationRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// IntegrationSummary is the integration list row enriched with the
// aggregate health and traffic derived from the services currently
// matching it. integrations.Integration is embedded so its JSON
// fields stay flattened — the existing list shape consumers expect.
type IntegrationSummary struct {
	integrations.Integration
	Status       string `json:"status"`
	ServiceCount int    `json:"service_count"`
	// Services is the integration's MEMBER service names — the persisted
	// catalog membership, so members are always reported even with no traffic
	// in the window (a "quiet" integration still lists its services).
	// ServiceCount == len(Services).
	Services       []string `json:"services"`
	UnhealthyCount int      `json:"unhealthy_count"`
	// TraceCount is the total number of distinct traces seen across
	// every matched service in the listed window — i.e. the number
	// of messages / units of work that flowed through the
	// integration. ErrorTraceCount is the subset that contained at
	// least one error span.
	TraceCount      uint64 `json:"trace_count"`
	ErrorTraceCount uint64 `json:"error_trace_count"`
	// DelayedTraceCount is the subset of TraceCount that breached a
	// trace-completion SLA in the window (a missed-SLA failure, distinct
	// from an error span). Disjoint from ErrorTraceCount. Zero for
	// integrations with no completion rule.
	DelayedTraceCount uint64 `json:"delayed_trace_count"`
	// TrafficSeries is the per-integration traffic sparkline: distinct
	// trace counts bucketed evenly across the window. Only populated when
	// the caller passes ?series=1 (the dashboard); omitted otherwise. A
	// quiet integration returns all-zeros (a flat line), never fake data.
	TrafficSeries []int `json:"traffic_series,omitempty"`
	// Tags attached to the integration. Always present (possibly empty)
	// so the frontend can render an empty cell without branching.
	Tags []tags.Tag `json:"tags"`
	// User-defined metadata values for this integration, keyed by field
	// key. Only present on list / detail responses, and only includes
	// keys that have a saved value (the schema lives at the response
	// top level under metadata_fields).
	MetadataValues map[string]string `json:"metadata_values,omitempty"`
}

// ServiceFacetRef is a compact reference to a service facet, attached
// to service summaries so the UI can show how a service is classified
// without a second round-trip. A service may carry many facets.
//
// Source records why the facet is on the service: "auto" when it was
// detected from telemetry, "manual" when a user added it via a facet
// override that telemetry alone wouldn't have produced. The UI uses it
// to badge manually-assigned facets.
type ServiceFacetRef struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Facet source values for ServiceFacetRef.Source.
const (
	FacetSourceAuto   = "auto"
	FacetSourceManual = "manual"
)

// ServiceSummary is one row in the services list. Counts are at
// *trace* granularity — TraceCount is the number of distinct traces
// the service participated in over the window; ErrorTraceCount is
// the subset of those that contained at least one error span.
type ServiceSummary struct {
	ServiceName      string `json:"service_name"`
	ServiceNamespace string `json:"service_namespace"`
	// FirstSeen is the all-time earliest timestamp we have for this
	// service — useful as a "created" / "discovered at" signal.
	// Optional because not every endpoint that builds a
	// ServiceSummary has it (e.g. the integration-detail matched
	// services don't carry it).
	FirstSeen       *time.Time        `json:"first_seen,omitempty"`
	LastSeen        time.Time         `json:"last_seen"`
	TraceCount      uint64            `json:"trace_count"`
	ErrorTraceCount uint64            `json:"error_trace_count"`
	Integrations    []IntegrationRef  `json:"integrations"`
	ServiceFacets   []ServiceFacetRef `json:"service_facets"`
	// Tags attached to the service. Always present (possibly empty) so
	// the frontend can render an empty cell without branching, matching
	// IntegrationSummary.
	Tags []tags.Tag `json:"tags"`
	// Status is a coarse, human-readable health label computed by the
	// API. One of: "ok", "errors", "quiet", "unhealthy".
	Status string `json:"status"`
	// MetadataValues is the service's custom metadata (field key → value),
	// for the Services-list metadata filter. Only populated by the services
	// list (omitted elsewhere, e.g. the Errors feed).
	MetadataValues map[string]string `json:"metadata_values,omitempty"`
	// UpstreamCount / DownstreamCount are the service's in-/out-degree in the
	// window's service dependency graph: how many distinct services called it
	// (upstream callers) and how many it called (downstream callees). Drives
	// the "lacking upstream/downstream" dependency filter. Window-scoped.
	UpstreamCount   int `json:"upstream_count"`
	DownstreamCount int `json:"downstream_count"`
	// IsSystem flags this service as a monitored "system" (RabbitMQ, SQL
	// Server, …); SystemKind names which one. Drives the System badge + the
	// Systems view.
	IsSystem   bool   `json:"is_system"`
	SystemKind string `json:"system_kind,omitempty"`
}

// ServiceDetail is the response from /services/:name.
type ServiceDetail struct {
	ServiceName      string        `json:"service_name"`
	ServiceNamespace string        `json:"service_namespace"`
	Status           string        `json:"status"` // ok | errors | quiet | unhealthy
	Window           WindowSummary `json:"window"`
	Stats            ServiceStats  `json:"stats"`
	// StatsSeries is the bucketed time series behind the golden-signal
	// sparklines (traces / error rate / p50 / p95 per bucket). Omitted
	// if the series query fails; the sparklines then just render flat.
	StatsSeries  *ServiceStatsSeries `json:"stats_series,omitempty"`
	Integrations []IntegrationRef    `json:"integrations"`
	// Tags attached to the service. Always present (possibly empty).
	Tags        []tags.Tag    `json:"tags"`
	RecentSpans []SpanSummary `json:"recent_spans"`
	// ErrorAck is the current "clear errors" acknowledgement, if the
	// team has cleared this service's errors. nil when not cleared.
	ErrorAck *erroracks.Ack `json:"error_ack,omitempty"`
	// OpenErrorCount is the number of persisted, unacknowledged error
	// traces behind the built-in "error span → unhealthy" check. >0 means
	// that check is firing; the health-check view shows it as the reason
	// and links to the offending traces. 0 when none / acknowledged.
	OpenErrorCount uint64 `json:"open_error_count"`
	// VisibleSignals lists the telemetry signals THIS caller may see for
	// the service (RBAC v2 §7) — the UI hides tabs that aren't granted.
	VisibleSignals []string `json:"visible_signals,omitempty"`
	// IsSystem / SystemKind: whether this service is flagged as a monitored
	// system and which kind — drives the "Mark as system" control + badge.
	IsSystem   bool   `json:"is_system"`
	SystemKind string `json:"system_kind,omitempty"`
	// BadgePublic: this service exposes a public status badge.
	BadgePublic bool `json:"badge_public"`
}

// TraceDetail is the response from /traces/:id. Spans are returned as
// a flat list ordered by timestamp; the frontend reconstructs the
// parent-child tree from ParentSpanID.
//
// Truncated is true when the trace had more spans than the server-side
// cap (see store.spansForTraceDefault). The UI can then render a banner
// like "Showing first N of many spans" so a 50K-span runaway trace
// doesn't silently look like a 5K-span trace. There's no cursor here —
// fetching the remaining spans isn't useful in practice; if you hit the
// cap, the trace is interesting for a different reason than waterfall
// inspection.
type TraceDetail struct {
	TraceID   string        `json:"trace_id"`
	Spans     []SpanSummary `json:"spans"`
	Truncated bool          `json:"truncated,omitempty"`
}

// FlowNode is one service in the flow graph.
type FlowNode struct {
	ServiceName     string `json:"service_name"`
	TraceCount      uint64 `json:"trace_count"`
	ErrorTraceCount uint64 `json:"error_trace_count"`
	// Status is the service's health ("ok" / "unhealthy"), driven by its
	// configured health checks. The integration graph colours nodes by this
	// rather than by raw error traces; empty in the single-trace flow.
	Status string `json:"status,omitempty"`
}

// FlowEdge is one directed service→service hop, aggregated across
// the listed traces. CallCount is at trace granularity.
type FlowEdge struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	CallCount  uint64 `json:"call_count"`
	ErrorCount uint64 `json:"error_count"`
}

// FlowSchemaRef is one schema pinned to a service, with the direction
// it plays for that service ("in" = consumed/incoming, "out" =
// produced/outgoing). Surfaced on the flow graph as a node chip.
type FlowSchemaRef struct {
	SchemaID  string `json:"schema_id"`
	Name      string `json:"name"`
	Direction string `json:"direction"` // "in" | "out"
}

// FlowMap is one transformation (Maps catalogue) relevant to this
// integration — its input or output schema is used by a member
// service. Rendered as a labeled hop / data-shapes caption.
type FlowMap struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	FromSchema string `json:"from_schema,omitempty"` // schema name (may be empty)
	ToSchema   string `json:"to_schema,omitempty"`
	Format     string `json:"format,omitempty"`
}

// FlowResponse is the integration / trace flow graph payload.
type FlowResponse struct {
	Window WindowSummary `json:"window"`
	Nodes  []FlowNode    `json:"nodes"`
	Edges  []FlowEdge    `json:"edges"`
	// Historical is true when the selected window had no matching
	// services and the response was filled from a wider historical
	// window so the topology still renders. Per-node counts then
	// reflect that historical fallback, not the empty current window.
	Historical bool `json:"historical,omitempty"`
	// ServiceSchemas maps a member service name to the schemas pinned
	// to it (with in/out direction) — the data shapes flowing through
	// that node. Omitted when no member service has schema links.
	ServiceSchemas map[string][]FlowSchemaRef `json:"service_schemas,omitempty"`
	// Maps are the transformations whose input or output schema is used
	// by a member service — "schema 1 → map x → schema 2", scoped to
	// this integration. Omitted when none apply.
	Maps []FlowMap `json:"maps,omitempty"`
}

// ServiceNeighbor is one direct neighbor of a focal service in the
// service-call graph — either a caller (upstream) or a callee
// (downstream). Counts are at trace granularity, matching FlowEdge.
type ServiceNeighbor struct {
	ServiceName string `json:"service_name"`
	TraceCount  uint64 `json:"trace_count"`
	ErrorCount  uint64 `json:"error_count"`
}

// NeighborsResponse is the body of GET /services/{name}/neighbors.
//
// Upstream is the set of services that called into the focal service
// in the window; Downstream is the set of services it called. Both
// arrays are pre-sorted by trace_count descending so the UI can render
// them as a relevance-ranked list without re-sorting. Either list may
// be empty (an orphan service, a leaf, or simply a quiet window).
//
// The endpoint does NOT filter against any integration's existing
// matchers — that's the caller's job (and is integration-specific).
// Returning the full unfiltered list keeps the endpoint composable
// for future uses like dependency search outside the new-integration
// flow.
type NeighborsResponse struct {
	ServiceName string            `json:"service_name"`
	Window      WindowSummary     `json:"window"`
	Upstream    []ServiceNeighbor `json:"upstream"`
	Downstream  []ServiceNeighbor `json:"downstream"`
}

// TraceSummary is one row in the recent-traces table on the service
// detail page. The attributes map carries the merged resource + span
// attributes of the service's first span in the trace, so the UI can
// surface key-attribute chips (file.name, http.route, etc.) without
// loading the whole trace.
type TraceSummary struct {
	TraceID       string            `json:"trace_id"`
	TraceStart    time.Time         `json:"trace_start"`
	DurationMs    float64           `json:"duration_ms"`
	HasError      bool              `json:"has_error"`
	TotalSpans    uint64            `json:"total_spans"`
	ServiceCount  uint64            `json:"service_count"`
	FirstSpanName string            `json:"first_span_name"`
	Attributes    map[string]string `json:"attributes,omitempty"`
}

// ServiceTracesResponse is the body of GET /services/{name}/traces.
type ServiceTracesResponse struct {
	ServiceName string         `json:"service_name"`
	Window      WindowSummary  `json:"window"`
	Traces      []TraceSummary `json:"traces"`
}

// WindowSummary describes the time window a response covers.
type WindowSummary struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// ServiceStats holds aggregate stats for a service over the window.
// Counts are trace-granular; latency percentiles are span-granular
// (operation duration), which is the right grain for "how slow is
// this service".
type ServiceStats struct {
	TraceCount      uint64  `json:"trace_count"`
	ErrorTraceCount uint64  `json:"error_trace_count"`
	ErrorRate       float64 `json:"error_rate"`
	P50DurationMs   float64 `json:"p50_duration_ms"`
	P95DurationMs   float64 `json:"p95_duration_ms"`
}

// ServiceStatsSeries is the per-bucket time series behind the golden-
// signal sparklines. All arrays are the same length and zero-filled;
// error_rate is a fraction (0..1, matching ServiceStats.ErrorRate),
// latencies are milliseconds.
type ServiceStatsSeries struct {
	Traces    []int     `json:"traces"`
	ErrorRate []float64 `json:"error_rate"`
	P50Ms     []float64 `json:"p50_ms"`
	P95Ms     []float64 `json:"p95_ms"`
}

// SpanSummary is a compact span representation suitable for lists.
//
// Attributes are exposed both as a merged view (Attributes) and split
// by source (ResourceAttributes / SpanAttributes). Most UI surfaces
// should render the merged view because OpenTelemetry users think of
// "attributes on this span" uniformly. The split fields are kept for
// the advanced detail panel where the source can be useful.
type SpanSummary struct {
	Timestamp          time.Time         `json:"timestamp"`
	TraceID            string            `json:"trace_id"`
	SpanID             string            `json:"span_id"`
	ParentSpanID       string            `json:"parent_span_id,omitempty"`
	ServiceName        string            `json:"service_name"`
	SpanName           string            `json:"span_name"`
	SpanKind           string            `json:"span_kind"`
	StatusCode         string            `json:"status_code"`
	StatusMessage      string            `json:"status_message,omitempty"`
	DurationMs         float64           `json:"duration_ms"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
	SpanAttributes     map[string]string `json:"span_attributes,omitempty"`
}

// TraceSearchResult is one row in the search response. The "matched"
// fields capture the span that actually satisfied the query, so the
// UI can show why this trace came back — the matching service, the
// span name within that service, and the attribute snapshot of that
// span (used to surface key-attribute chips like file.name=…).
type TraceSearchResult struct {
	TraceID         string            `json:"trace_id"`
	TraceStart      time.Time         `json:"trace_start"`
	DurationMs      float64           `json:"duration_ms"`
	HasError        bool              `json:"has_error"`
	TotalSpans      uint64            `json:"total_spans"`
	ServiceCount    uint64            `json:"service_count"`
	MatchedService  string            `json:"matched_service"`
	MatchedSpanName string            `json:"matched_span_name"`
	Attributes      map[string]string `json:"attributes,omitempty"`
}

// MessageCursorJSON is the keyset cursor for the next page of message
// search results. TS is unix nanoseconds (string; exceeds JS safe int),
// ID is the last row's TraceId.
type MessageCursorJSON struct {
	TS string `json:"ts"`
	ID string `json:"id"`
}

// SearchResponse is the response from /search and /messages/search.
// Results are *traces* — one row per matching trace — not the
// underlying spans. The trace is the user's unit of work. NextCursor is
// set by /messages/search when more rows may follow (the free-text
// /search endpoint leaves it nil).
type SearchResponse struct {
	Query      string              `json:"query"`
	Window     WindowSummary       `json:"window"`
	Total      int                 `json:"total"`
	Results    []TraceSearchResult `json:"results"`
	NextCursor *MessageCursorJSON  `json:"next_cursor,omitempty"`
}

// LogEntry is one row in the service logs table. Attributes is the
// merged resource + log attribute view (log attributes win on
// conflict), matching how SpanSummary presents attributes.
type LogEntry struct {
	// LogID is the row's unique id (ClickHouse LogId). Stable per log
	// record — used for deep-linking to a single log.
	LogID             string    `json:"log_id,omitempty"`
	Timestamp         time.Time `json:"timestamp"`
	ObservedTimestamp time.Time `json:"observed_timestamp"`
	TraceID           string    `json:"trace_id,omitempty"`
	SpanID            string    `json:"span_id,omitempty"`
	SeverityNumber    int32     `json:"severity_number"`
	SeverityText      string    `json:"severity_text"`
	ServiceName       string    `json:"service_name"`
	ScopeName         string    `json:"scope_name,omitempty"`
	Body              string    `json:"body"`
	// Attributes is the merged resource + log view (log wins on
	// conflict) for the table's kv chips. The drawer needs them split,
	// so the two source maps are exposed separately too — the OTel
	// model treats resource attributes as dimensions a log is grouped
	// by, not properties of it.
	Attributes         map[string]string `json:"attributes,omitempty"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
	LogAttributes      map[string]string `json:"log_attributes,omitempty"`
}

// LogCursorJSON is the keyset cursor for the next page of logs. Both
// fields are opaque strings to the client: TS is unix nanoseconds
// (exceeds JS's safe-integer range, hence a string) and Ord is the last
// row's LogId.
type LogCursorJSON struct {
	TS  string `json:"ts"`
	Ord string `json:"ord"`
}

// ServiceLogsResponse is the body of GET /services/{name}/logs.
// NextCursor is non-null when more rows may follow.
type ServiceLogsResponse struct {
	ServiceName string         `json:"service_name"`
	Window      WindowSummary  `json:"window"`
	Logs        []LogEntry     `json:"logs"`
	NextCursor  *LogCursorJSON `json:"next_cursor,omitempty"`
}

// LogsResponse is the body of GET /logs — the global Logs page.
type LogsResponse struct {
	Window     WindowSummary  `json:"window"`
	Logs       []LogEntry     `json:"logs"`
	NextCursor *LogCursorJSON `json:"next_cursor,omitempty"`
}

// LogServicesResponse is the body of GET /log-services.
type LogServicesResponse struct {
	Window   WindowSummary `json:"window"`
	Services []string      `json:"services"`
}

// LogFieldEntry is one attribute key in the Logs filter catalog. Type
// is "number" when every observed value parsed as a float (so the UI
// can offer numeric operators), else "string". Cardinality is the
// approximate distinct-value count (sample-based) for the picker hint.
type LogFieldEntry struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	UseCount    uint64 `json:"use_count"`
	Cardinality uint64 `json:"cardinality"`
}

// LogFieldsResponse is the body of GET /log-fields.
type LogFieldsResponse struct {
	Window WindowSummary   `json:"window"`
	Fields []LogFieldEntry `json:"fields"`
}

// LogAttrValue is one top-N value for an attribute key, with how many
// log events carried it.
type LogAttrValue struct {
	Value  string `json:"value"`
	Events uint64 `json:"events"`
}

// LogAttrValuesResponse is the body of GET /log-attributes/{key}/values.
type LogAttrValuesResponse struct {
	Key    string         `json:"key"`
	Window WindowSummary  `json:"window"`
	Values []LogAttrValue `json:"values"`
}

// LogVolumeBucketJSON is one bar of the volume histogram: severity-band
// counts for one time bucket (stacked bottom→top info → warn → err →
// fatal; info folds in debug/trace).
type LogVolumeBucketJSON struct {
	Start time.Time `json:"start"`
	Info  uint64    `json:"info"`
	Warn  uint64    `json:"warn"`
	Err   uint64    `json:"err"`
	Fatal uint64    `json:"fatal"`
}

// LogVolumeResponse is the body of GET /logs/volume.
type LogVolumeResponse struct {
	Window      WindowSummary         `json:"window"`
	StepSeconds int                   `json:"step_seconds"`
	Buckets     []LogVolumeBucketJSON `json:"buckets"`
}

// MetricNameEntry is one metric in a metric catalog. Type is the OTLP
// metric type ("gauge", "sum", "histogram"); PointCount is how many
// data points landed in the window; ServiceCount is how many distinct
// services emitted it (1 for the per-service catalog).
type MetricNameEntry struct {
	Name         string    `json:"name"`
	Type         string    `json:"type"`
	Unit         string    `json:"unit,omitempty"`
	PointCount   uint64    `json:"point_count"`
	ServiceCount uint64    `json:"service_count"`
	LastSeen     time.Time `json:"last_seen"`
}

// ServiceMetricNamesResponse is the body of GET /services/{name}/metric-names.
type ServiceMetricNamesResponse struct {
	ServiceName string            `json:"service_name"`
	Window      WindowSummary     `json:"window"`
	Metrics     []MetricNameEntry `json:"metrics"`
}

// MetricCatalogResponse is the body of GET /metric-names — the global
// metric catalog across all services.
type MetricCatalogResponse struct {
	Window  WindowSummary     `json:"window"`
	Metrics []MetricNameEntry `json:"metrics"`
}

// MetricCatalogEntry is one metric in the explorer table (GET
// /metric-catalog): a type-aware headline value, a sparkline, the
// distinct-series count, and — joined from the alert engine — how many
// rules watch it plus the tightest threshold (for the sparkline's
// dashed line). Aggregation says how Value was derived ("latest",
// "rate", "mean").
type MetricCatalogEntry struct {
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Unit        string    `json:"unit,omitempty"`
	Aggregation string    `json:"aggregation"`
	Value       float64   `json:"value"`
	Spark       []float64 `json:"spark"`
	SeriesCount uint64    `json:"series_count"`
	PointCount  uint64    `json:"point_count"`
	LastSeen    time.Time `json:"last_seen"`
	RuleCount   int       `json:"rule_count"`
	Threshold   *float64  `json:"threshold,omitempty"`
	Severity    string    `json:"severity,omitempty"`
}

// MetricCatalogRichResponse is the body of GET /metric-catalog — the
// metric explorer table, with the headline stats the page shows.
type MetricCatalogRichResponse struct {
	Window      WindowSummary        `json:"window"`
	StepSeconds int                  `json:"step_seconds"`
	TotalSeries uint64               `json:"total_series"`
	RuleCount   int                  `json:"rule_count"`
	Metrics     []MetricCatalogEntry `json:"metrics"`
}

// MetricGroup is one rollup row of the metric catalog grouped by a
// dimension (GET /metric-groups).
type MetricGroup struct {
	Key         string `json:"key"`
	MetricCount uint64 `json:"metric_count"`
	SeriesCount uint64 `json:"series_count"`
	PointCount  uint64 `json:"point_count"`
}

// MetricGroupsResponse is the body of GET /metric-groups.
type MetricGroupsResponse struct {
	Window WindowSummary `json:"window"`
	By     string        `json:"by"`
	Groups []MetricGroup `json:"groups"`
}

// LogGroup is one rollup row of a log search grouped by a dimension
// (GET /logs/groups).
type LogGroup struct {
	Key        string `json:"key"`
	Count      uint64 `json:"count"`
	ErrorCount uint64 `json:"error_count"`
}

// LogGroupsResponse is the body of GET /logs/groups.
type LogGroupsResponse struct {
	Window WindowSummary `json:"window"`
	By     string        `json:"by"`
	Groups []LogGroup    `json:"groups"`
}

// MetricServiceSeries is one service's charted series of a metric, for
// the global one-line-per-service chart.
type MetricServiceSeries struct {
	ServiceName string              `json:"service_name"`
	Points      []MetricSeriesPoint `json:"points"`
}

// MetricSeriesByServiceResponse is the body of GET /metric-series — the
// metric charted as one series per emitting service.
type MetricSeriesByServiceResponse struct {
	Metric      string                `json:"metric"`
	Type        string                `json:"type"`
	Unit        string                `json:"unit,omitempty"`
	Aggregation string                `json:"aggregation"`
	StepSeconds int                   `json:"step_seconds"`
	Window      WindowSummary         `json:"window"`
	Series      []MetricServiceSeries `json:"series"`
}

// MetricSeriesPoint is one time bucket of a charted metric series.
type MetricSeriesPoint struct {
	Bucket time.Time `json:"bucket"`
	Value  float64   `json:"value"`
}

// ServiceMetricSeriesResponse is the body of GET /services/{name}/metric-series.
// Aggregation records which per-bucket aggregation was applied for this
// metric's type ("avg" or "increase") so the UI can label the axis.
type ServiceMetricSeriesResponse struct {
	ServiceName string              `json:"service_name"`
	Metric      string              `json:"metric"`
	Type        string              `json:"type"`
	Unit        string              `json:"unit,omitempty"`
	Aggregation string              `json:"aggregation"`
	StepSeconds int                 `json:"step_seconds"`
	Window      WindowSummary       `json:"window"`
	Points      []MetricSeriesPoint `json:"points"`
}
