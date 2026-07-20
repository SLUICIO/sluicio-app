// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// TypeScript types matching the JSON shapes served by cell-api.

export interface Window {
  from: string;
  to: string;
}

// Usage report: ingested telemetry volume over a window.
export interface UsageVolumeCounts {
  spans: number;
  metric_points: number;
  metric_series: number;
  logs: number;
}
export interface UsageVolumeService extends UsageVolumeCounts {
  service: string;
}
// Actual on-disk (compressed) footprint of one signal's table, plus its
// row count — used to derive a bytes-per-row for per-service size estimates.
export interface UsageStorageSignal {
  bytes: number;
  rows: number;
}
export interface UsageStorage {
  spans: UsageStorageSignal;
  metric_points: UsageStorageSignal;
  logs: UsageStorageSignal;
}
export interface UsageVolumeResponse {
  window: Window;
  // false = totals at rest across the whole database; true = bounded to window.
  windowed: boolean;
  totals: UsageVolumeCounts;
  storage: UsageStorage;
  integrations: IntegrationUsage;
  services: UsageVolumeService[];
}

// Usage report (Settings → Reports): how much ingested data is actually
// watched by alert rules, and what the unused share costs in storage.
export interface UsageServiceRow {
  service_name: string;
  rows: number;
  est_bytes: number;
  covered: boolean;
}
export interface UsageSignalReport {
  total: number;
  unused: number;
  unused_rows: number;
  est_bytes_per_day: number;
  est_bytes_per_30d: number;
  services?: UsageServiceRow[];
}
export interface UsageReportResponse {
  window: Window;
  metrics: UsageSignalReport;
  logs: UsageSignalReport;
  traces: UsageSignalReport;
}

export type ServiceStatus = "ok" | "errors" | "quiet" | "unhealthy";

export interface IntegrationRef {
  id: string;
  slug: string;
  name: string;
}

// One facet a service plays — file-input, queue-output, http-input,
// db-output, email-output, etc. A service can carry many of these;
// the UI renders a chip per facet and the dashboard concatenates
// widgets from every matched facet.
export interface ServiceFacetRef {
  slug: string;
  name: string;
  // Why the facet is on the service: "auto" (detected from telemetry)
  // or "manual" (assigned via a facet override). Optional so older
  // payloads without the field still type-check.
  source?: FacetSource;
}

// FacetSource distinguishes telemetry-detected facets from ones a user
// assigned manually via a facet override.
export type FacetSource = "auto" | "manual";

export interface ServiceSummary {
  service_name: string;
  service_namespace: string;
  // first_seen is the all-time earliest timestamp for the service
  // (when present); last_seen is bounded by the listing's window.
  first_seen?: string;
  last_seen: string;
  // Counts are at trace granularity — one trace = one unit of work,
  // regardless of how many spans the service emitted within it.
  trace_count: number;
  error_trace_count: number;
  integrations: IntegrationRef[];
  // Every facet currently matching the service. Always present; the
  // always-on `core` facet means this is never empty for a service
  // that has any spans at all.
  service_facets: ServiceFacetRef[];
  // Tags attached to the service. Present on the services list (always,
  // possibly empty); other endpoints that build a service summary may
  // omit it, so it's optional here.
  tags?: Tag[];
  status: ServiceStatus;
  // Custom metadata (field key → value), for the Services-list metadata
  // filter. Populated by the services list endpoint.
  metadata_values?: Record<string, string>;
  // In-/out-degree in the window's service dependency graph: how many
  // distinct services called this one (upstream callers) and how many it
  // called (downstream callees). Drives the dependency filter. Window-scoped.
  upstream_count?: number;
  downstream_count?: number;
  // Flagged as a monitored "system" (RabbitMQ, SQL Server, …) + which kind.
  // Drives the System badge in the services list and the Systems view.
  is_system?: boolean;
  system_kind?: string;
}

export interface ServicesResponse {
  window: Window;
  services: ServiceSummary[];
}

// One firing health check on the Errors feed, attributed to the service
// or integration it guards. target_kind === "global" means the rule is
// org-wide (bound to no specific entity).
export interface FailingCheck {
  id: string;
  rule_id: string;
  rule_name: string;
  severity: AlertSeverity;
  started_at: string;
  handled_at?: string;
  summary?: string;
  target_kind: "service" | "integration" | "global";
  service_name?: string;
  integration_id?: string;
  integration_name?: string;
}

// OpenServiceError — a persisted, unacknowledged error: a service that has
// produced error traces since it was last cleared. Surfaced regardless of
// the page time window so an error stays visible until acknowledged
// (acknowledging = clearing the service's errors, which bumps the
// watermark; new errors after that re-open it).
export interface OpenServiceError {
  service_name: string;
  error_traces: number;
  first_error_at: string;
  last_error_at: string;
  sample_trace_id?: string;
}

// The Errors feed: failing health checks + unacknowledged errors +
// affected services, already scoped to the caller's rights and respecting
// the "clear errors" acknowledgements.
export interface ErrorsFeedResponse {
  window: Window;
  failing_checks: FailingCheck[];
  open_errors: OpenServiceError[];
  services: ServiceSummary[];
  counts: {
    failing_checks: number;
    services_unhealthy: number;
    services_errors: number;
    open_errors: number;
  };
}

export interface ServiceStats {
  trace_count: number;
  error_trace_count: number;
  error_rate: number;
  p50_duration_ms: number;
  p95_duration_ms: number;
}

// ServiceStatsSeries — bucketed time series behind the golden-signal
// sparklines. error_rate is a fraction (0..1); latencies are ms.
export interface ServiceStatsSeries {
  traces: number[];
  error_rate: number[];
  p50_ms: number[];
  p95_ms: number[];
}

export interface SpanSummary {
  timestamp: string;
  trace_id: string;
  span_id: string;
  parent_span_id?: string;
  service_name: string;
  span_name: string;
  span_kind: string;
  status_code: string;
  status_message?: string;
  duration_ms: number;
  // Merged view (resource + span attributes) — what most UI should render.
  attributes?: Record<string, string>;
  // Split views for the advanced panel, when the source matters.
  resource_attributes?: Record<string, string>;
  span_attributes?: Record<string, string>;
}

export interface ServiceMetadata {
  service_name: string;
  description: string;
  owner: string;
  on_call: string;
  team: string;
  repository: string;
  runbook_url: string;
  updated_at?: string;
  // User-defined metadata: definitions applicable to services
  // alongside the saved values (key → string). Returned by GET; the
  // PUT for builtins only touches the fixed fields.
  metadata_fields?: MetadataField[];
  metadata_values?: Record<string, string>;
  // Linked data schemas: what shape this service consumes (in) and
  // produces (out). Either may be null. Set via
  // PUT /services/{name}/schemas.
  in_schema?: Schema | null;
  out_schema?: Schema | null;
}

// ── Data schemas ────────────────────────────────────────────────────────
// Transformations (mapping / template) used to live in this enum but
// have their own first-class entity now — see Map below.
export type SchemaKind = "schema" | "example" | "other";

export interface Schema {
  id: string;
  organization_id?: string;
  name: string;
  kind: SchemaKind;
  version: string;
  description: string;
  format: string;
  content: string;
  created_at?: string;
  updated_at?: string;
  // Number of (service, direction) rows pointing at this schema.
  // Populated on the list endpoint.
  usage_count?: number;
  // Full per-service link list, populated on the list endpoint so the
  // table can render clickable service chips without a per-row fetch.
  usage?: SchemaUsage[];
}

export interface SchemaInput {
  name: string;
  kind: SchemaKind;
  version: string;
  description: string;
  format: string;
  content: string;
}

export interface SchemaUsage {
  service_name: string;
  direction: "in" | "out";
}

// ── Maps (data transformations) ─────────────────────────────────────────
// A Map is a transformation from one schema shape to another: XSLT, jq,
// JSONata, Liquid, Mustache, Handlebars, …  The optional `from_schema`
// and `to_schema` references describe the input and output shape; the
// `content` field holds the transformation source itself.

// SchemaRef is the thin handle hydrated alongside a Map — just enough
// to render a chip and link back to /schemas without paying for the
// full content body.
export interface SchemaRef {
  id: string;
  name: string;
  version: string;
  format: string;
  kind: SchemaKind;
}

// Named `MapDoc` (not `Map`) so importing this type doesn't shadow the
// built-in `Map<K, V>` generic in consumer files. The UI label is still
// "Map" / "Maps"; only the TS identifier carries the suffix.
export interface MapDoc {
  id: string;
  organization_id?: string;
  name: string;
  version: string;
  description: string;
  format: string;
  content: string;
  created_at?: string;
  updated_at?: string;
  from_schema_id?: string | null;
  to_schema_id?: string | null;
  // Hydrated by the API on list / get responses; null when the
  // corresponding *_id is null.
  from_schema?: SchemaRef | null;
  to_schema?: SchemaRef | null;
}

export interface MapInput {
  name: string;
  version: string;
  description: string;
  format: string;
  content: string;
  // Empty string clears the link in that direction.
  from_schema_id: string;
  to_schema_id: string;
}

// ── Map execution / testing ─────────────────────────────────────────────
// Backed by POST /api/v1/maps/{id}/execute. The endpoint runs the map's
// transformation against the sample input and optionally validates both
// sides against the pinned from / to schemas.

export interface MapExecuteRequest {
  input: string;
}

export interface MapValidationResult {
  skipped: boolean;
  skip_reason?: string;
  valid: boolean;
  errors?: string[];
  schema_name?: string;
}

export interface MapExecuteResponse {
  output: string;
  // Runtime-level diagnostic (libxslt parse error, Liquid syntax error).
  // Empty when execution succeeded.
  engine_error: string;
  input_validation: MapValidationResult;
  output_validation: MapValidationResult;
}

export interface ServiceDetailResponse {
  visible_signals?: string[];
  service_name: string;
  service_namespace: string;
  status?: ServiceStatus;
  window: Window;
  stats: ServiceStats;
  // Bucketed series behind the golden-signal sparklines. Absent if the
  // backend couldn't compute it (sparklines then render flat).
  stats_series?: ServiceStatsSeries;
  integrations: IntegrationRef[];
  // Tags attached to the service. Always present (possibly empty).
  tags?: Tag[];
  recent_spans: SpanSummary[];
  // Current "clear errors" acknowledgement, if the team has cleared
  // this service's errors. Absent when not cleared.
  error_ack?: ServiceErrorAck;
  // Whether this service is flagged as a monitored "system" + which kind.
  is_system?: boolean;
  system_kind?: string;
  badge_public?: boolean; // public status-badge opt-in
  // Persisted, unacknowledged error traces behind the built-in "error
  // span → unhealthy" check. >0 means that check is firing (the service
  // is unhealthy for this reason); the health-check view surfaces it and
  // links to the offending traces. 0 when none / acknowledged.
  open_error_count?: number;
}

// A service's "clear errors" acknowledgement: errors at or before
// acknowledged_until don't count toward health until new ones arrive.
export interface ServiceErrorAck {
  service_name: string;
  acknowledged_until: string;
  comment?: string;
  acknowledged_by?: string;
  acknowledged_by_name?: string;
  acknowledged_at: string;
}

export interface TraceDetailResponse {
  trace_id: string;
  spans: SpanSummary[];
  // truncated=true when the trace had more spans than the server-side
  // cap. UI renders a banner so a 50K-span runaway trace doesn't look
  // identical to a complete 5K-span trace.
  truncated?: boolean;
}

// Flow graph types (used for the integration-level service map and the
// trace-level "where did it fail" view).

export interface FlowNode {
  service_name: string;
  trace_count: number;
  error_trace_count: number;
  // Health ("ok" / "unhealthy"), driven by the service's configured health
  // checks. The integration graph colours nodes by this; absent in the
  // single-trace flow (which colours by that trace's own span errors).
  status?: ServiceStatus;
}

export interface FlowEdge {
  source: string;
  target: string;
  call_count: number;
  error_count: number;
}

// FlowSchemaRef — a schema pinned to a service with its direction
// ("in" = incoming/consumed, "out" = outgoing/produced).
export interface FlowSchemaRef {
  schema_id: string;
  name: string;
  direction: "in" | "out";
}

// FlowMap — a transformation relevant to the integration (its input or
// output schema is used by a member service).
export interface FlowMap {
  id: string;
  name: string;
  from_schema?: string;
  to_schema?: string;
  format?: string;
}

export interface FlowResponse {
  window: Window;
  nodes: FlowNode[];
  edges: FlowEdge[];
  // Data-shape overlay: schemas pinned per member service (in/out) and
  // the maps that transform between them. Absent when nothing is linked.
  service_schemas?: Record<string, FlowSchemaRef[]>;
  maps?: FlowMap[];
  // True when the user's selected window had no matching services
  // and the topology was filled from a wider historical window so the
  // graph still renders. Per-node counts reflect that historical
  // window in that case — the UI badges the panel accordingly.
  historical?: boolean;
}

// ServiceNeighbor is one direct caller or callee of a focal service
// in the trace graph. The neighbors endpoint groups them by direction;
// these rows are what the integration-builder UI iterates over when
// suggesting dependent services to add to a new or existing integration.
//
// Counts are at trace granularity, matching FlowEdge.
export interface ServiceNeighbor {
  service_name: string;
  trace_count: number;
  error_count: number;
}

// NeighborsResponse is the body of GET /services/{name}/neighbors.
// Upstream are services that called into the focal service; downstream
// are services it called. Both lists are pre-sorted by trace_count
// descending and may be empty (orphan service, leaf, or quiet window).
export interface NeighborsResponse {
  service_name: string;
  window: Window;
  upstream: ServiceNeighbor[];
  downstream: ServiceNeighbor[];
}

// FacetMappingOperator mirrors the closed set the backend enforces.
// "exists" matches any span where the attribute is set and non-empty
// — for everything else the value is compared lexically.
export type FacetMappingOperator =
  | "equals"
  | "prefix"
  | "suffix"
  | "contains"
  | "exists";

// FacetMappingAttributeSource picks SpanAttributes vs ResourceAttributes
// on the underlying span. Both are populated by the OTel collector;
// resource attributes apply to every span the service emits while span
// attributes are per-operation.
export type FacetMappingAttributeSource = "span" | "resource";

// FacetMappingIOKind / IORole mirror the closed set the built-in
// facets recognize. A mapping classifies matching spans as carrying
// the chosen (kind, role) pair, which then drives facet matching and
// per-facet widget filtering.
export type FacetMappingIOKind = "file" | "queue" | "stream" | "http" | "db" | "email";
export type FacetMappingIORole = "input" | "output";

// FacetMapping is one user-defined rule for a service: "treat spans
// where attribute X satisfies condition Y as having io.kind=K and
// io.role=R." Lets users bring services without io.kind/io.role
// attributes into the facet classification without re-instrumenting.
export interface FacetMapping {
  id: string;
  organization_id: string;
  service_name: string;
  attribute_source: FacetMappingAttributeSource;
  attribute_key: string;
  match_operator: FacetMappingOperator;
  // Empty string when match_operator === "exists".
  match_value: string;
  set_io_kind: FacetMappingIOKind;
  set_io_role: FacetMappingIORole;
  created_at: string;
  updated_at: string;
}

export interface FacetMappingsResponse {
  service_name: string;
  mappings: FacetMapping[];
}

export interface CreateFacetMappingRequest {
  attribute_source: FacetMappingAttributeSource;
  attribute_key: string;
  match_operator: FacetMappingOperator;
  match_value: string;
  set_io_kind: FacetMappingIOKind;
  set_io_role: FacetMappingIORole;
}

export interface TraceSummary {
  trace_id: string;
  trace_start: string;
  duration_ms: number;
  has_error: boolean;
  total_spans: number;
  service_count: number;
  first_span_name: string;
  attributes?: Record<string, string>;
}

export interface ServiceTracesResponse {
  service_name: string;
  window: Window;
  traces: TraceSummary[];
}

export type MatcherOperator = "equals" | "prefix" | "suffix" | "contains" | "regex";

export interface Integration {
  id: string;
  organization_id: string;
  slug: string;
  name: string;
  description: string;
  // Public status-badge opt-in. Present on single-integration (detail)
  // responses; absent on the list endpoint.
  badge_public?: boolean;
  created_at: string;
  updated_at: string;
  // Present on the list endpoint (IntegrationSummary), absent on
  // single-integration responses where the status field appears at
  // the top level of the wrapper instead.
  status?: ServiceStatus;
  service_count?: number;
  // Member service names (persisted catalog membership) — present on the list
  // endpoint, independent of window traffic.
  services?: string[];
  unhealthy_count?: number;
  trace_count?: number;
  error_trace_count?: number;
  // Distinct traces that breached a trace-completion SLA in the window.
  // A missed-SLA failure, disjoint from error_trace_count; counts
  // against success rate and pulls status to at least "errors".
  delayed_trace_count?: number;
  // Per-integration traffic sparkline: distinct trace counts bucketed
  // across the window. Only present when the list is requested with
  // ?series=1 (the dashboard). All-zeros for a quiet integration.
  traffic_series?: number[];
  // User-defined metadata values for this integration on the list
  // endpoint, keyed by field key. Only includes saved values; the
  // schema lives at the top-level metadata_fields on the response
  // wrapper.
  metadata_values?: Record<string, string>;
  // Tags are returned by the list endpoint (always present, possibly
  // empty). On single-integration responses they live at the top
  // level of the wrapper (see IntegrationDetail.tags).
  tags?: Tag[];
}

export interface Matcher {
  id: string;
  integration_id: string;
  attribute: string;
  operator: MatcherOperator;
  value: string;
  match_group: number;
  created_at: string;
}

export interface IntegrationDetail {
  can_manage?: boolean;
  integration: Integration;
  matchers: Matcher[];
  // services, status, and window are returned by GET; absent on the
  // POST (create) response. The detail page is the canonical place
  // to see the matched services and aggregate health.
  services?: ServiceSummary[];
  status?: ServiceStatus;
  // tags attached to the integration. Always present on GET, omitted
  // on the create response (which the frontend follows up with an
  // explicit attach for any preselected tags).
  tags?: Tag[];
  window?: Window;
  // Integration-level distinct trace counts over the window. Unlike
  // summing per-service trace_count, a trace spanning two of the
  // integration's services is counted once — so this matches the
  // Messages tab (which groups by TraceId). Present on GET only.
  message_count?: number;
  error_message_count?: number;
  // Distinct traces that breached a trace-completion SLA in the window
  // (a missed-SLA failure, disjoint from error_message_count). Present
  // on GET only; zero when the integration has no completion rule.
  delayed_message_count?: number;
  // Firing health checks scoped to this integration's members (or the
  // integration itself), and unacknowledged errors on its members. Present
  // on GET only — these let the Errors tab badge reflect failing checks /
  // open errors, not just failed traces.
  failing_check_count?: number;
  open_error_count?: number;
  // User-defined metadata: definitions applicable to integrations
  // alongside the saved values (key → string).
  metadata_fields?: MetadataField[];
  metadata_values?: Record<string, string>;
}

// ── User-defined metadata fields ────────────────────────────────────────
export type MetadataFieldType = "text" | "boolean" | "number" | "select";

export interface MetadataField {
  id: string;
  key: string;
  label: string;
  type: MetadataFieldType;
  options?: string[]; // for type=select
  description: string;
  applies_to_integration: boolean;
  applies_to_service: boolean;
  applies_to_system: boolean;
  system_type_key: string; // "" = all systems; else only that type
  required: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface MetadataFieldInput {
  key: string;
  label: string;
  type: MetadataFieldType;
  options?: string[];
  description: string;
  applies_to_integration: boolean;
  applies_to_service: boolean;
  applies_to_system: boolean;
  system_type_key: string;
  required: boolean;
}

export interface SystemMetadataResponse {
  fields: MetadataField[];
  metadata_values: Record<string, string>;
}

// Tags — flat, org-scoped vocabulary attachable to integrations and
// services. v1 carries only name + slug + color; categories may be
// added later as a separate field. See docs/tags.md.
export interface Tag {
  id: string;
  organization_id: string;
  slug: string;
  name: string;
  // Lowercase hex color, "#rgb" or "#rrggbb".
  color: string;
  created_at: string;
  updated_at: string;
}

export interface CreateTagRequest {
  slug: string;
  name: string;
  color: string;
}

export interface UpdateTagRequest {
  name: string;
  color: string;
}

// TagWithUsage is what /api/v1/tags?include=usage returns: a Tag
// plus the number of integrations and services it's attached to.
// The management page uses the counts to render informed delete
// confirmations.
export interface TagWithUsage extends Tag {
  integration_count: number;
  service_count: number;
}

export interface CreateIntegrationRequest {
  slug: string;
  name: string;
  description: string;
  matchers: { attribute?: string; operator: MatcherOperator; value: string; match_group?: number }[];
}

// Service facets and widgets --------------------------------------------

export type WidgetKind = "counter" | "throughput" | "error_rate" | "latency" | "breakdown";

// A facet's definition without widget data — name, description, the
// widget descriptors (kind/name/description, not their values), and
// the attribute keys the UI should highlight on every span tied to a
// service that has this facet.
// How Sluicio detects a facet — the OTel instrumentation a producer
// must emit for the facet to be applied to its service.
export interface FacetMatch {
  // (key=value) span-attribute pairs that must all be present on the
  // same span, e.g. io.kind=file AND io.role=input.
  span_attributes?: { key: string; value: string }[];
  // OTel span kinds that drive the match (e.g. Internal for Worker).
  span_kinds?: string[];
  // True for the baseline facet applied to every service.
  always?: boolean;
  // Optional human clarification (e.g. Worker's "and no I/O spans").
  note?: string;
}

export interface ServiceFacetShape {
  slug: string;
  name: string;
  description: string;
  widgets: { kind: WidgetKind; name: string; description: string }[];
  // Attribute keys the UI should highlight on every span belonging to
  // a service that has this facet. For file-input this is file.name +
  // transfer.source.host etc.
  key_attributes: string[];
  // What a service must emit for Sluicio to apply this facet.
  match: FacetMatch;
  // True for org-defined ("custom") facets — editable/deletable in the UI.
  // Built-in facets are read-only.
  custom?: boolean;
}

export interface ServiceFacetsListResponse {
  facets: ServiceFacetShape[];
}

export interface ServiceFacetDetailResponse {
  facet: ServiceFacetShape;
  services: ServiceSummary[];
  window: Window;
}

// Widget data shapes returned by /services/{name}/widgets ---------------

export interface CounterData {
  value: number;
  subtitle?: string;
}

export interface TimePointData {
  ts: string;
  value: number;
}

export interface ErrorRatePointData {
  ts: string;
  total: number;
  errors: number;
  rate: number;
}

export interface LatencyPointData {
  ts: string;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
}

export interface BreakdownRowData {
  key: string;
  total: number;
  errors: number;
}

// A widget result. Data shape varies by kind; `data` is null if
// computation failed for that widget. UI should render an empty state.
export interface WidgetResult {
  kind: WidgetKind;
  name: string;
  description: string;
  data:
    | CounterData
    | TimePointData[]
    | ErrorRatePointData[]
    | LatencyPointData[]
    | BreakdownRowData[]
    | null;
}

// One facet section on a service's dashboard: the facet identity plus
// the computed widget values that belong to it.
export interface FacetWidgetsResult {
  slug: string;
  name: string;
  description: string;
  // "auto" or "manual" — see ServiceFacetRef.source. A "manual" section
  // was assigned via a facet override and may have empty widgets when
  // the service emits no matching telemetry.
  source?: FacetSource;
  widgets: WidgetResult[];
}

// Facet overrides — manual include/exclude decisions layered on top of
// auto-detection. See docs and the ServiceFacetsEditor component.
export type FacetOverrideAction = "include" | "exclude";

// FacetOverrideRow is one facet in the editor's view of a service: its
// identity plus how it currently resolves. `effective` is the checkbox
// state; `removable` is false for the always-on core facet.
export interface FacetOverrideRow {
  slug: string;
  name: string;
  description: string;
  auto_detected: boolean;
  override: FacetOverrideAction | null;
  effective: boolean;
  removable: boolean;
}

export interface FacetOverridesResponse {
  service_name: string;
  window: Window;
  facets: FacetOverrideRow[];
}

// UpdateFacetOverridesRequest replaces a service's entire override set.
// Both arrays hold facet slugs; the server keeps them disjoint and
// ignores the core facet.
export interface UpdateFacetOverridesRequest {
  include: string[];
  exclude: string[];
}

export interface ServiceWidgetsResponse {
  service_name: string;
  window: Window;
  // Facet sections in the order returned by the cell, with the
  // always-on `core` facet last. Stack them top-to-bottom in the UI.
  facets: FacetWidgetsResult[];
}

// Custom metrics --------------------------------------------------------

export type MetricOperator = "gt" | "gte" | "lt" | "lte";

// One attribute predicate on a query-backed custom metric (AND-combined).
// ServiceReading is one "show on service page" health check with its
// latest value tile. Custom metrics are unified into health checks: a
// telemetry check's value is the one the evaluator computed; a pushed
// check's value is the last one POSTed in.
export interface ServiceReading {
  rule_id: string;
  name: string;
  unit: string;
  source: "telemetry" | "pushed";
  operator: AlertOperator;
  threshold: number;
  value?: number;
  observed_at?: string;
  has_value: boolean;
  breached: boolean;
}

export interface ServiceReadingsResponse {
  service_name: string;
  readings: ServiceReading[];
}

export interface TraceSearchResult {
  trace_id: string;
  trace_start: string;
  duration_ms: number;
  has_error: boolean;
  total_spans: number;
  service_count: number;
  matched_service: string;
  matched_span_name: string;
  attributes?: Record<string, string>;
}

// Keyset cursor for the next page of message search results. Both
// fields are opaque strings the client round-trips unchanged.
export interface MessageCursor {
  ts: string;
  id: string;
}

export interface SearchResponse {
  // The free-text query that produced this result set. The
  // structured /messages/search endpoint emits an empty string here
  // since the request has no single needle — the filter list does
  // the work and is echoed back to the client through context.
  query: string;
  window: Window;
  total: number;
  results: TraceSearchResult[];
  // Set by /messages/search when more rows may follow; absent on the
  // free-text /search endpoint.
  next_cursor?: MessageCursor;
}

// Global search (top-navbar "search everything", #28) ------------------
//
// GET /global-search?q= returns hits grouped by source. Phase 1 covers
// integrations, services, messages (trace spans), logs (body contains)
// and metrics (by name); each group is capped, with has_more + a
// see_all_href driving the "click for the full list" affordance.
export type GlobalSearchType =
  | "integration"
  | "service"
  | "message"
  | "log"
  | "metric"
  | "facet"
  | "tag"
  | "metadata"
  | "map"
  | "schema";

export interface GlobalSearchHit {
  type: GlobalSearchType;
  label: string;
  sublabel?: string;
  href: string;
}

export interface GlobalSearchGroup {
  type: GlobalSearchType;
  label: string;
  hits: GlobalSearchHit[];
  has_more: boolean;
  see_all_href?: string;
}

export interface GlobalSearchResponse {
  query: string;
  groups: GlobalSearchGroup[];
}

// Messages search & saved views ---------------------------------------
//
// The Messages page in the UI is driven by these endpoints. A
// MessageFilter is the wire-shape that mirrors the FilterEditor's
// Filter type — same field names, same op vocabulary — so the
// frontend can serialize its in-memory state directly.

export type MessageField =
  | "payload"
  | "time"
  | "integration"
  | "status"
  | "service"
  | "errorType"
  | "traceId"
  | "spanId";

export type MessageOperator =
  | "equals"
  | "contains"
  | "is"
  | "in"
  | "matches";

export interface MessageFilter {
  id?: string;
  field: MessageField;
  fieldPath?: string;
  op: MessageOperator;
  value: string;
  removable?: boolean;
  // locked: the row is fixed by the page's scope (e.g. the integration
  // pill on /integrations/:id/messages). The editor renders it as a
  // read-only pill and the server ignores attempts to change it.
  locked?: boolean;
  // optional: the user has muted this row but kept it as a reminder.
  // The search engine treats optional rows as a no-op.
  optional?: boolean;
}

// MessageViewScope pins a saved view to a specific entity. The frontend
// uses it to show "in <integration>" badges in the global search rail
// and to route the user back to the entity's Messages tab when they
// open a scoped view. nil/empty fields = global view.
export interface MessageViewScope {
  integrationId?: string;
  serviceId?: string;
}

export interface MessageView {
  id: string;
  name: string;
  description?: string;
  mine: boolean;
  pinned: boolean;
  shared: boolean;
  filters: MessageFilter[];
  // Always emitted by the server (even when empty) so the UI can do
  // `view.scope.integrationId` without a presence check.
  scope: MessageViewScope;
  resultCount?: number;
  lastEditedAt: string;
  createdAt: string;
  updatedAt: string;
}

export interface MessageViewsResponse {
  views: MessageView[];
}

export interface CreateMessageViewRequest {
  name: string;
  description?: string;
  pinned: boolean;
  shared: boolean;
  filters: MessageFilter[];
  scope?: MessageViewScope;
}

export interface UpdateMessageViewRequest extends CreateMessageViewRequest {}

export interface MessageAttributeKey {
  key: string;
  source: "span" | "resource";
  useCount: number;
}

export interface MessageFieldDescriptor {
  field: MessageField;
  label: string;
  description: string;
  operators: MessageOperator[];
  enumValues?: string[];
  attributeKeys?: MessageAttributeKey[];
}

export interface MessageFieldsResponse {
  window: Window;
  fields: MessageFieldDescriptor[];
}

export interface MessageSearchRequest {
  range?: string;
  limit?: number;
  filters: MessageFilter[];
  cursor?: MessageCursor;
}

// Identity, organizations, roles --------------------------------------
//
// A user belongs to one or more organizations, and within each org
// they hold one or more roles. Roles — not users — carry permissions.
// The list below is the closed set for now; new roles should be added
// here and given a permission mapping in lib/useCurrentUser.ts.
//
//   org-admin               manage members, roles, billing, org settings
//   integration-contributor create/edit/archive integrations, service
//                           types, topology, alerts
//   operator                ack/replay/retry stuck messages, mute alerts
//   viewer                  read-only across everything
//
// Server-side the same role check must be enforced on the API — the
// UI gate is convenience, not security.

export type RoleSlug =
  | "org-admin"
  | "integration-contributor"
  | "operator"
  | "viewer";

export interface Role {
  slug: RoleSlug;
  name: string;
}

export interface Organization {
  id: string;
  slug: string;
  name: string;
}

export interface OrganizationMembership {
  organization: Organization;
  roles: Role[];
}

export interface User {
  id: string;
  email: string;
  name: string;
  // initials are precomputed so the avatar doesn't have to guess at
  // names like "Mary-Anne O'Connor".
  initials: string;
  // Cell operator (super-admin): gates the Operator surface + cell-wide
  // settings in the UI. The cell-api enforces the same on every route.
  isOperator: boolean;
  isDemo?: boolean;
  mustResetPassword?: boolean;
  memberships: OrganizationMembership[];
}

// ── Operator surface (cell super-admin) ────────────────────────────
// Org lifecycle + cross-org member assignment + operator management.
// Every endpoint is operator-gated on the backend (RequireOperator).

export interface OperatorOrg extends Organization {
  member_count: number;
  created_at?: string;
  updated_at?: string;
}

export interface OperatorUser {
  id: string;
  email: string;
  name: string;
  is_operator?: boolean;
  is_demo?: boolean;
}

// A member row as returned by the operator members endpoint — the same
// shape as the org-settings MemberRow (email/role/activity), reused.
export interface OperatorOrgMembersResponse {
  members: MemberRow[];
}

// Permission strings are stable identifiers checked from the UI and
// (eventually) the API. Keep them coarse — page-level, not field-level
// — and grow the list as features land.
export type Permission =
  | "integration.read"
  | "integration.write"
  | "integration.delete"
  | "stuck.replay"
  | "alert.mute"
  | "org.manage";

export interface CurrentUserResponse {
  user: User;
  // The currently active org. The avatar dropdown's org switcher
  // updates this; everything else reads it.
  active_organization_id: string;
}

// Dashboards — per-user, named layouts for the Home page. The
// shape mirrors the Go dashboards.Dashboard / dashboards.Item types
// 1:1 so the wire format is a direct round-trip of the editor's
// in-memory state.
//
// autoIncludeAll = true reproduces the legacy "show every integration"
// behaviour and treats items[] as widget-type *overrides*.
// autoIncludeAll = false renders only integrations explicitly listed
// in items[].

export type DashboardWidgetType =
  | "traffic_sparkline"
  | "error_count"
  | "latency_p95"
  | "system_health";

export type DashboardEntityKind = "integration" | "system";

// Picker labels — kept beside the type so adding a new widget is a
// one-place change in the UI. system_health is omitted here: it's not an
// integration-card widget choice (system items always use it).
export const DASHBOARD_WIDGET_LABELS: Record<DashboardWidgetType, string> = {
  traffic_sparkline: "Traffic sparkline",
  error_count: "Error count",
  latency_p95: "Latency p95",
  system_health: "System health",
};

// The picker set — what a user may CHOOSE for an integration card or as
// the dashboard default. system_health is display-only (system items
// always use it; the backend rejects it everywhere else with a 400).
export const DASHBOARD_WIDGET_PICKER: DashboardWidgetType[] = [
  "traffic_sparkline",
  "error_count",
  "latency_p95",
];

export interface DashboardItem {
  id: string;
  entityKind: DashboardEntityKind;
  integrationId: string;
  systemName?: string;
  widgetType: DashboardWidgetType;
  position: number;
  createdAt: string;
}

export interface Dashboard {
  canManage?: boolean;
  groupId?: string | null;
  id: string;
  name: string;
  isDefault: boolean;
  autoIncludeAll: boolean;
  defaultWidgetType: DashboardWidgetType;
  position: number;
  mine: boolean;
  // Always emitted (possibly empty) so callers never have to check
  // for presence.
  items: DashboardItem[];
  createdAt: string;
  updatedAt: string;
}

export interface DashboardsResponse {
  dashboards: Dashboard[];
}

export interface DashboardItemRequest {
  entityKind?: DashboardEntityKind;
  integrationId?: string;
  systemName?: string;
  widgetType: DashboardWidgetType;
  position: number;
}

export interface CreateDashboardRequest {
  groupId?: string | null;
  name: string;
  isDefault?: boolean;
  autoIncludeAll?: boolean;
  defaultWidgetType?: DashboardWidgetType;
  position?: number;
  items?: DashboardItemRequest[];
}

export interface UpdateDashboardRequest {
  name: string;
  isDefault: boolean;
  autoIncludeAll: boolean;
  defaultWidgetType: DashboardWidgetType;
  position: number;
  items: DashboardItemRequest[];
}

// Ingested OTLP logs --------------------------------------------------
//
// The raw telemetry browse shapes, distinct from the custom-metrics
// (threshold) shapes above. attributes is the merged resource + log
// attribute view, matching SpanSummary.attributes.
export interface LogEntry {
  // Stable per-row id (ClickHouse LogId). Used to deep-link to a single
  // log — see client.getLog and the Logs `?log=` query param.
  log_id?: string;
  timestamp: string;
  observed_timestamp: string;
  trace_id?: string;
  span_id?: string;
  severity_number: number;
  severity_text: string;
  service_name: string;
  scope_name?: string;
  body: string;
  attributes?: Record<string, string>;
  resource_attributes?: Record<string, string>;
  log_attributes?: Record<string, string>;
}

// Keyset cursor for the next page of logs. ord is a string because the
// backing hash is a full uint64 that a JS number would round off.
export interface LogCursor {
  ts: string;
  ord: string;
}

export interface ServiceLogsResponse {
  service_name: string;
  window: Window;
  logs: LogEntry[];
  next_cursor?: LogCursor;
}

// Global Logs page.
export interface LogsResponse {
  window: Window;
  logs: LogEntry[];
  next_cursor?: LogCursor;
}

export interface LogServicesResponse {
  window: Window;
  services: string[];
}

// Attribute filtering on logs. Text ops apply to string attributes;
// the comparison ops (gt/gte/lt/lte) apply to numeric ones.
export type LogAttrOp =
  | "eq"
  | "neq"
  | "contains"
  | "not_contains"
  | "starts_with"
  | "exists"
  | "gt"
  | "gte"
  | "lt"
  | "lte";

export interface LogAttrFilter {
  key: string;
  op: LogAttrOp;
  value: string;
}

// One attribute key in the Logs filter catalog. type is "number" when
// every observed value parsed as a float, else "string".
export interface LogFieldEntry {
  key: string;
  type: "number" | "string";
  use_count: number;
  cardinality: number;
}

export interface LogFieldsResponse {
  window: Window;
  fields: LogFieldEntry[];
}

export interface LogAttrValue {
  value: string;
  events: number;
}

export interface LogAttrValuesResponse {
  key: string;
  window: Window;
  values: LogAttrValue[];
}

export interface LogVolumeBucket {
  start: string;
  info: number;
  warn: number;
  err: number;
  fatal: number;
}

export interface LogVolumeResponse {
  window: Window;
  step_seconds: number;
  buckets: LogVolumeBucket[];
}

// Ingested OTLP metrics ----------------------------------------------
export interface MetricNameEntry {
  name: string;
  type: string; // OTLP metric type: "gauge" | "sum" | "histogram"
  unit?: string;
  point_count: number;
  service_count: number;
  last_seen: string;
}

export interface ServiceMetricNamesResponse {
  service_name: string;
  window: Window;
  metrics: MetricNameEntry[];
}

// Global metric catalog (across all services).
export interface MetricCatalogResponse {
  window: Window;
  metrics: MetricNameEntry[];
}

// Metric explorer catalog (GET /metric-catalog): one row per metric with
// a type-aware headline value, a sparkline, distinct-series count, and —
// joined from the alert engine — how many rules watch it + the tightest
// threshold (for the dashed sparkline line).
export interface MetricCatalogEntry {
  name: string;
  type: string; // "gauge" | "sum" | "histogram"
  unit?: string;
  aggregation: string; // "latest" | "rate" | "mean"
  value: number;
  spark: number[];
  series_count: number;
  point_count: number;
  last_seen: string;
  rule_count: number;
  threshold?: number;
  severity?: string;
}

export interface MetricCatalogRichResponse {
  window: Window;
  step_seconds: number;
  total_series: number;
  rule_count: number;
  metrics: MetricCatalogEntry[];
}

export interface MetricSeriesPoint {
  bucket: string;
  value: number;
}

export interface ServiceMetricSeriesResponse {
  service_name: string;
  metric: string;
  type: string;
  unit?: string;
  aggregation: string; // "avg" | "increase"
  step_seconds: number;
  window: Window;
  points: MetricSeriesPoint[];
}

// Global metric chart: one series per emitting service.
export interface MetricServiceSeries {
  service_name: string;
  points: MetricSeriesPoint[];
}

export interface MetricSeriesByServiceResponse {
  metric: string;
  type: string;
  unit?: string;
  aggregation: string; // "avg" | "increase"
  step_seconds: number;
  window: Window;
  series: MetricServiceSeries[];
}

// Alerting --------------------------------------------------------------
export type AlertSeverity = "info" | "warning" | "critical";
export type AlertAggregation = "last" | "max" | "avg" | "min" | "sum" | "p95" | "increase" | "rate" | "age";
export type AlertOperator = "gt" | "gte" | "lt" | "lte" | "eq" | "neq";

export interface RuleAttrFilter {
  key: string;
  op: string;
  value: string;
}

export interface MetricRuleSpec {
  metric_name: string;
  aggregation: AlertAggregation;
  operator: AlertOperator;
  threshold: number;
  for_window: string; // Go duration, e.g. "5m"
  attrs?: RuleAttrFilter[];
  // split_by, when set, breaks the rule down by the distinct values of
  // this metric attribute: each value is compared to the threshold on its
  // own and the firing enumerates every breaching value (e.g. "DLQ depth
  // > 0 split by queue_name" → the alert lists each backed-up queue).
  split_by?: string;
}

// LogRuleSpec is the rule_spec for a log-signal rule: fire when the
// count of logs matching {min_severity, body_contains, attrs} within the
// trailing window reaches threshold. threshold=1 = "alert on any match".
export interface LogRuleSpec {
  min_severity: number; // OTLP SeverityNumber floor; 0 = any (info≈9, warn≈13, error≈17, fatal≈21)
  body_contains: string; // case-insensitive substring of the body; "" = no text filter
  attrs?: RuleAttrFilter[];
  threshold: number; // fire when match count crosses this; min 1
  window_seconds: number; // trailing window matches are counted over
  // Direction the threshold is compared in. "at_least" (default) fires on a
  // flood (count >= threshold); "fewer_than" fires on a drought (count <
  // threshold), where zero matching logs is the canonical breach.
  comparison?: "at_least" | "fewer_than";
}

// trace_error rule spec: fire when the bound integration accumulates
// >= threshold failed traces (a trace with an error span) over the window.
export interface TraceErrorRuleSpec {
  threshold: number; // fire when failed-trace count >= this; min 1
  window_seconds: number; // trailing window failed traces are counted over
  // Optional predicates narrowing WHICH error spans make a trace count as
  // failed (AND-ed, same key/op vocabulary as log rules).
  attrs?: LogAttrFilter[];
}

// trace_latency rule spec: fire when the bound scope's windowed quantile
// span latency (p95 or max) reaches threshold_ms. Shares signal "trace"
// with the failed-trace spec; only one of the two is present on a rule.
export interface TraceLatencyRuleSpec {
  threshold_ms: number; // fire when latency_ms >= this; min 1
  window_seconds: number; // trailing window latency is aggregated over
  aggregation: "p95" | "max"; // "p95" (default) or worst-case "max"
}

// trace_volume rule spec: fire when the bound scope produces FEWER than
// threshold distinct traces over the window — a low-traffic / dead-man's-
// switch check. Zero traces counts as below. Shares signal "trace" with the
// failed-trace and latency specs; only one of the three is present on a rule.
export interface TraceVolumeRuleSpec {
  threshold: number; // fire when total trace count < this; min 1
  window_seconds: number; // trailing window traces are counted over
}

// NotificationContent controls which enrichment blocks an alert's email +
// webhook include, plus an optional inline Liquid email override. All flags
// default off (back-compat). Mirrors the backend alerting.NotificationContent.
export interface NotificationContent {
  service?: boolean;
  integration?: boolean;
  service_metadata?: boolean;
  integration_metadata?: boolean;
  check?: boolean;
  email_subject?: string;
  email_body?: string;
}

export interface AlertRule {
  id: string;
  organization_id: string;
  integration_id?: string;
  service_name?: string;
  group_id?: string; // owning team; absent = org-wide
  name: string;
  description: string;
  signal: string; // "metric" | "log" | "trace" (failed-trace)
  spec: MetricRuleSpec;
  log_spec?: LogRuleSpec; // present when signal === "log"
  trace_error_spec?: TraceErrorRuleSpec; // signal "trace" + failed-trace flavour
  trace_latency_spec?: TraceLatencyRuleSpec; // signal "trace" + response-time flavour
  trace_volume_spec?: TraceVolumeRuleSpec; // signal "trace" + low-traffic flavour
  severity: AlertSeverity;
  evaluation_seconds: number;
  enabled: boolean;
  // "telemetry" (aggregate an OTLP metric, default) or "pushed" (value
  // fed in by an external scraper). Metric rules only.
  source: "telemetry" | "pushed";
  // When set, the check's latest reading shows as a value tile on its
  // bound service page; unit is that tile's display unit.
  display_on_service: boolean;
  unit?: string;
  // "auto" = self-recovering; "manual" = stays firing until acknowledged.
  resolve_mode: "auto" | "manual";
  channel_ids: string[];
  title_template?: string; // Go text/template; empty = built-in summary
  body_template?: string;
  notification_config?: NotificationContent;
  created_at: string;
  updated_at: string;
}

// One row of the delivery history ("what's been sent"): a notification
// job joined to its channel + the rule/instance it notified about.
export interface AlertDelivery {
  job_id: string;
  channel_name: string;
  channel_kind: string;
  rule_name: string;
  severity: AlertSeverity;
  alert_state: string; // instance state: "firing" | "resolved"
  job_state: string; // "pending" | "running" | "succeeded" | "failed"
  attempts: number;
  last_error?: string;
  subject?: string; // rendered, exactly as sent
  body?: string;
  summary: string;
  created_at: string;
  updated_at: string; // delivery / last-attempt time
  group_id?: string;
}

export interface AlertRuleInput {
  name: string;
  description?: string;
  severity: AlertSeverity;
  enabled?: boolean;
  channel_ids: string[];
  signal?: "metric" | "log" | "trace"; // default "metric"; "trace" = failed-trace rule
  spec?: MetricRuleSpec; // required for metric rules
  log_spec?: LogRuleSpec; // required for log rules
  // for a "trace" rule, supply exactly one: trace_error_spec (fire on
  // >= threshold failed traces over the window), trace_latency_spec (fire
  // when windowed p95/max response time reaches threshold_ms), or
  // trace_volume_spec (fire when fewer than threshold traces in the window).
  trace_error_spec?: TraceErrorRuleSpec;
  trace_latency_spec?: TraceLatencyRuleSpec;
  trace_volume_spec?: TraceVolumeRuleSpec;
  service_name?: string; // when set, the rule defines that service's health
  integration_id?: string; // bind to an integration's health
  group_id?: string; // owning team; omit / "" = org-wide
  title_template?: string; // Go text/template for the notification title
  body_template?: string; // Go text/template for the notification body
  notification_config?: NotificationContent;
  source?: "telemetry" | "pushed"; // default "telemetry"; "pushed" = value fed in externally
  display_on_service?: boolean; // show the latest reading as a tile on the service page
  unit?: string; // display unit for the value tile
  // "auto" = self-recovering; "manual" = stays firing until acknowledged.
  // Omit to let the server default by signal (metric→auto, log/trace→manual).
  resolve_mode?: "auto" | "manual";
}

export interface NotificationChannel {
  id: string;
  organization_id: string;
  name: string;
  kind: string; // "webhook" | "slack" | "pagerduty"
  config: Record<string, string>;
  created_at: string;
  updated_at: string;
}

export interface ChannelInput {
  name: string;
  kind: string;
  config: Record<string, string>;
}

// NotificationProfile — a per-team (group_id set) or org-wide (group_id
// null) bundle of delivery behaviour + channels. An alert/error resolves
// to one profile: integration's assigned → team default → org default.
export type ProfileGrouping = "per_check" | "per_integration";

export interface NotificationProfile {
  id: string;
  organization_id: string;
  group_id?: string | null; // null = org-wide
  name: string;
  grouping: ProfileGrouping;
  renotify_minutes: number; // 0 = no recurring re-notification
  is_default: boolean; // the default for its scope (team or org)
  channel_ids: string[];
  created_at: string;
  updated_at: string;
}

export interface NotificationProfileInput {
  group_id?: string | null;
  name: string;
  grouping: ProfileGrouping;
  renotify_minutes: number;
  is_default: boolean;
  channel_ids?: string[];
}

// ConfigImportReport is what config-import returns (dry-run and real).
export interface ConfigImportReport {
  mode: "strict" | "replace";
  dry_run: boolean;
  sections: Record<string, { created: number; updated: number; skipped: number }>;
  needs_credentials?: string[];
  warnings?: string[];
}

// Announcement is one persistent banner (org-scoped, or cell-wide when
// org_id is absent). See docs/maintenance-and-announcements-design.md.
export interface Announcement {
  id: string;
  org_id?: string;
  message: string;
  severity: "info" | "warning" | "critical";
  starts_at: string;
  ends_at?: string;
  dismissible: boolean;
  created_at: string;
}

export interface AnnouncementInput {
  message: string;
  severity?: "info" | "warning" | "critical";
  ends_at?: string;
  dismissible?: boolean;
}

// MaintenanceWindowScope says which alert rules a window silences:
// everything, an explicit entity list, or one team's rules.
export interface MaintenanceWindowScope {
  kind: "all_org" | "entities" | "group";
  integration_ids?: string[];
  system_ids?: string[];
  service_names?: string[];
  // Write-time snapshot of the systems' member services (server-set).
  service_names_expanded?: string[];
  group_id?: string;
}

export interface MaintenanceWindow {
  id: string;
  org_id: string;
  name: string;
  reason?: string;
  starts_at: string;
  ends_at: string;
  scope: MaintenanceWindowScope;
  announcement_id?: string;
  created_at: string;
  active: boolean;
}

export interface MaintenanceWindowInput {
  name: string;
  reason?: string;
  starts_at?: string;
  ends_at: string;
  scope: MaintenanceWindowScope;
  announce?: boolean;
}

export interface AlertInstance {
  id: string;
  alert_rule_id: string;
  rule_name: string;
  severity: AlertSeverity;
  state: string; // "firing" | "resolved" | "pending"
  started_at: string;
  ended_at?: string;
  summary: string;
  // Set once a user has acknowledged the alert ("being worked on").
  handled_at?: string;
}

export interface AlertPreview {
  value: number;
  samples: number;
  has_data: boolean;
  breached: boolean;
  threshold: number;
  window: Window;
  // Present only for split-by rules: the per-value breakdown plus how
  // many of those values currently breach the threshold.
  split_by?: string;
  breach_count?: number;
  groups?: AlertPreviewGroup[];
}

// AlertPreviewGroup is one split-by value's reading in a preview.
export interface AlertPreviewGroup {
  label: string;
  value: number;
  breached: boolean;
}

// Group-by rollups ------------------------------------------------------
export interface MetricGroup {
  key: string;
  metric_count: number;
  series_count: number;
  point_count: number;
}
export interface MetricGroupsResponse {
  window: Window;
  by: string;
  groups: MetricGroup[];
}
export interface LogGroup {
  key: string;
  count: number;
  error_count: number;
}
export interface LogGroupsResponse {
  window: Window;
  by: string;
  groups: LogGroup[];
}

// ── Auth (mirrors cell-api's internal/identity types) ─────────────────
// These are the wire shapes Sluicio's own login + me endpoints return.
// Distinct from the user / org / RoleSlug types higher in this file
// which the UI uses internally — the adapter in UserProvider.tsx
// converts between the two.

export type AuthRole = "admin" | "editor" | "viewer";

// A per-org OTLP ingest API key. The full secret is only returned once,
// at creation (see IngestKeyCreated); listing shows the masked prefix.
export interface IngestKey {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  last_used_at?: string;
}

export interface IngestKeyCreated {
  key: string; // full secret — shown once, never retrievable again
  meta: IngestKey;
}

// ── Trace completion rules ──────────────────────────────────────────
//
// Per-integration SLA rules: "a trace is done when a span with name X
// appears; if not within timeout, flip the trace to 'delayed' and fire
// the integration's alert channels." Stored server-side as alert_rules
// with signal='trace'; the UI works against a typed projection.

// One hop in a completion pipeline: the trace must emit a span whose
// name is in span_names within timeout_seconds of the previous stage
// (or the start span, for the first stage). timeout_seconds omitted /
// 0 → inherit the rule's default_timeout_seconds.
export interface TraceCompletionStage {
  span_names: string[];
  timeout_seconds?: number;
}

export interface TraceCompletionRule {
  id: string;
  integration_id: string;
  name: string;
  description: string;
  severity: "info" | "warning" | "critical";
  enabled: boolean;
  // start_span_name gates the rule: only traces containing this span are
  // evaluated and counted as the integration's messages. Empty = ungated
  // (legacy). stages is the ordered chain of hops after the start span.
  start_span_name?: string;
  stages?: TraceCompletionStage[];
  default_timeout_seconds?: number;
  // Legacy: flat OR-list of "done" span names + single timeout. The
  // server mirrors the final stage's names here for back-compat.
  closing_span_names: string[];
  timeout_seconds: number;
  lookback_seconds: number;
  channel_ids: string[];
  created_at: string;
  updated_at: string;
}

export interface TraceCompletionRuleInput {
  name: string;
  description: string;
  severity: "info" | "warning" | "critical";
  enabled: boolean;
  start_span_name?: string;
  stages?: TraceCompletionStage[];
  default_timeout_seconds?: number;
  // Legacy fields still accepted; the server folds them into a single
  // stage when stages is empty.
  closing_span_names?: string[];
  timeout_seconds?: number;
  lookback_seconds?: number;
  channel_ids: string[];
}

export interface TraceCompletionCounts {
  completed: number;
  pending: number;
  delayed: number;
}

// One firing — an alert_instance opened because a trace breached
// the completion SLA. Sticky: a 'firing' state never auto-resolves
// just because the closing span shows up late.
export interface TraceCompletionFiring {
  instance_id: string;
  rule_id: string;
  rule_name: string;
  // Integration the firing belongs to. Used by the TraceDetail page
  // to scope status to the integration context the user is browsing
  // in — a trace late in one integration may be fine in another.
  integration_id: string;
  // Severity inherited from the rule that opened the firing.
  // Drives the trace's StatusPip kind: 'warning' → warn (yellow),
  // 'critical' → err (red, same as a real error span).
  severity: "info" | "warning" | "critical";
  trace_id: string;
  state: "firing" | "resolved" | "pending";
  started_at: string;          // when the firing opened
  last_evaluated_at: string;
  ended_at?: string;
  summary: string;
  trace_started_at?: string;   // when the trace itself began
  // Set when an operator marked this delayed trace as handled (e.g. the
  // message was manually resent). A handled firing no longer counts as
  // delayed and is rendered benign (not warning/error).
  handled_at?: string;
}

// Cell-wide settings — telemetry retention today, more knobs as they
// land. min_days / max_days are echoed by the server so the UI's
// input bounds match the validation floor / ceiling without hard-
// coding it on the client.
export interface RetentionEntry {
  days: number;
  last_applied_at?: string;
}

// One Enterprise feature key, mirrored from ee/license.Feature.
export type LicenseFeature =
  | "sso"
  | "rbac_advanced"
  | "audit_log"
  | "retention_long"
  | "mfa_policy";

// The /api/v1/license read model. `features` always carries every gate so
// the UI can branch without optional-chaining. `licensed` is true only for a
// valid, in-force license (signature good, not past the grace period).
// Monitored-entity count vs the licensed cap. A monitored entity is an
// integration flow or a first-class system (broker, DB, …) — both count, so
// used = integration_count + system_count. limit 0 / unlimited = no cap
// (Community, Enterprise, or no in-force license). over_limit = cap reached.
export interface IntegrationUsage {
  used: number;
  integration_count: number;
  system_count: number;
  limit: number;
  unlimited: boolean;
  over_limit: boolean;
}

export interface LicenseStatus {
  licensed: boolean;
  plan?: string;
  customer?: string;
  license_id?: string;
  expires_at?: string;
  expired: boolean;
  in_grace: boolean;
  entitlements: string[];
  features: Record<LicenseFeature, boolean>;
  limits?: { max_retention_days?: number; max_integrations?: number };
  integration_usage?: IntegrationUsage;
  warning?: string;
}

export interface RetentionResponse {
  traces: RetentionEntry;
  logs: RetentionEntry;
  metrics: RetentionEntry;
  min_days: number;
  // Effective ceiling: the free-tier cap unless long_retention is unlocked.
  max_days: number;
  // Whether the Enterprise long-retention entitlement is active. When false,
  // max_days is the free cap and the UI shows an upgrade prompt.
  long_retention?: boolean;
  // Optional — populated only on PATCH responses when the Postgres
  // write succeeded but the synchronous ClickHouse ALTER didn't. UI
  // surfaces this as a yellow banner so the user knows the change
  // will land at the next enforcer tick rather than instantly.
  apply_warning?: string;
  // Audit-log retention (Postgres prune, not a ClickHouse TTL).
  // audit_configurable mirrors the audit_log entitlement; without it the
  // field is pinned to the free cap and the UI shows the upgrade prompt.
  audit_days: number;
  audit_max_days: number;
  audit_configurable?: boolean;
}

export interface RetentionRequest {
  traces_days?: number;
  logs_days?: number;
  metrics_days?: number;
  audit_days?: number;
}

// AuditVerifyResult is the outcome of a tamper-evidence chain walk
// (GET /api/v1/audit-log/verify).
export interface AuditVerifyResult {
  ok: boolean;
  entries_checked: number;
  legacy_unhashed: number;
  first_broken_id?: number;
  detail?: string;
}

// One Enterprise audit-log entry (GET /api/v1/audit-log).
export interface AuditEntry {
  id: number;
  actor_user_id?: string;
  actor_name: string;
  actor_email?: string;
  action: string;
  target_type?: string;
  target_id?: string;
  metadata?: Record<string, unknown>;
  ip?: string;
  created_at: string;
}

export interface AuditLogResponse {
  entries: AuditEntry[];
}

// Global SMTP transport (Settings → System). The password is never
// returned — only password_set. `configured` reflects the effective
// transport (env + settings), i.e. whether email will actually send.
export interface SMTPSettingsResponse {
  host: string;
  port: string;
  username: string;
  from: string;
  from_name: string;
  password_set: boolean;
  configured: boolean;
}

// PATCH body. Omit `password` to keep the stored one; send "" to clear it.
export interface SMTPSettingsRequest {
  host: string;
  port: string;
  username: string;
  password?: string;
  from: string;
  from_name: string;
}

// Cell-wide system settings. environment is the label shown in the top
// nav (e.g. "production", "staging") — set by an org/system admin.
export interface SystemSettings {
  environment: string;
  // External OTLP/HTTP base URL of this cell's ingest endpoint, e.g.
  // "https://ingest.acme.example.com". "" when unset — the UI then falls
  // back to the browser origin for the ready-to-paste exporter snippets.
  ingest_base_url: string;
  // "env" = deployment-managed (SLUICIO_INGEST_URL, read-only here),
  // "setting" = admin-editable cell setting, "unset" = origin fallback.
  ingest_url_source?: "env" | "setting" | "unset";
  // Normalize span status at ingest: spans carrying an HTTP 5xx attribute
  // but a non-Error span status are stored as error spans.
  map_http_5xx_to_error?: boolean;
  // Compliance posture: refuse org-wide service accounts — every SA must
  // resolve visibility through group memberships (scoped).
  forbid_org_wide_service_accounts?: boolean;
}
export interface SystemSettingsRequest {
  environment?: string;
  ingest_base_url?: string;
  map_http_5xx_to_error?: boolean;
  forbid_org_wide_service_accounts?: boolean;
}

export interface AuthUser {
  id: string;
  email: string;
  name: string;
  // Cell operator (super-admin above the org roles). Omitted when false.
  is_operator?: boolean;
  is_demo?: boolean;
  must_reset_password: boolean;
  last_login_at?: string | null;
  created_at?: string;
  updated_at?: string;
  // Per-user activity stats (Settings → Members). Omitted by the API when
  // zero/false, so treat absent as 0 / false.
  login_count?: number;
  failed_login_count?: number;
  last_active_at?: string | null;
  mfa_enabled?: boolean;
}

export interface AuthOrg {
  id: string;
  slug: string;
  name: string;
  created_at?: string;
  updated_at?: string;
}

export interface AuthMembership {
  org: AuthOrg;
  role: AuthRole;
  joined_at?: string;
}

export interface AuthPrincipal {
  kind: "user" | "service_account";
  user_id?: string;
  service_account_id?: string;
  org_id: string;
  role: AuthRole;
  email?: string;
  name?: string;
}

export interface AuthLoginResponse {
  // When MFA is enabled the first /auth/login response carries no user —
  // just mfa_required + a short-lived mfa_token to pass to /auth/mfa-verify.
  user?: AuthUser;
  memberships?: AuthMembership[];
  must_reset_password?: boolean;
  mfa_required?: boolean;
  mfa_token?: string;
}

// Per-user MFA (TOTP).
export interface MFAStatusResponse {
  enabled: boolean;
  pending: boolean;
  available: boolean; // server has an encryption key configured
}
export interface MFASetupResponse {
  secret: string;
  otpauth_uri: string;
}
export interface MFAEnableResponse {
  enabled: boolean;
  backup_codes: string[];
}

export interface MeResponse {
  user: AuthUser;
  memberships: AuthMembership[];
  principal: AuthPrincipal;
  // True when org-wide MFA enforcement is on (Enterprise) and this user
  // hasn't enabled MFA yet — the UI nudges them to enrol.
  mfa_enrollment_required?: boolean;
}

// Org security policy (Settings → System). mfa_policy_entitled reflects the
// Enterprise entitlement that gates toggling enforcement.
export interface SecuritySettingsResponse {
  mfa_required: boolean;
  mfa_policy_entitled: boolean;
}

// ── Settings → Members + Tokens ──────────────────────────────────────

export interface MemberRow {
  user: AuthUser;
  role: AuthRole;
  joined_at: string;
  /** True when the user can sign in with a local password. */
  has_password: boolean;
  /** Names of the IdPs the user signs in through (empty = password-only). */
  sso_providers: string[];
}

export interface ListMembersResponse {
  members: MemberRow[];
}

export interface ApiToken {
  id: string;
  owner_type: "user" | "service_account";
  owner_id: string;
  name: string;
  prefix: string;
  last_used_at?: string | null;
  created_at: string;
  revoked_at?: string | null;
  // Optional role cap (least-privilege): "" / absent = full owner role.
  scope_role?: string;
  // Optional expiry; absent/null = never expires.
  expires_at?: string | null;
}

export interface ListTokensResponse {
  tokens: ApiToken[];
}

// A service account — a machine identity with its own role, owning
// service-account tokens (owner_type='service_account').
export interface ServiceAccount {
  id: string;
  org_id: string;
  name: string;
  description: string;
  role: "admin" | "editor" | "viewer";
  // Visibility model: "scoped" (default) resolves what the SA can see
  // from its group memberships, deny-by-default like a user; "org_wide"
  // is the explicit, audited opt-in to read the whole org.
  scope: "scoped" | "org_wide";
  created_by?: string;
  created_at?: string;
}

// One group a service account belongs to (its visibility scope).
export interface ServiceAccountGroup {
  group: Group;
  role: AuthRole;
  joined_at: string;
}

export interface CreateTokenResponse {
  token: ApiToken;
  // Returned ONCE by the cell-api at mint time. Frontend must
  // surface this in a copy-to-clipboard dialog and never persist it.
  plaintext: string;
}

// ── Groups (access-control axis under org) ────────────────────────────

export interface Group {
  id: string;
  org_id: string;
  slug: string;
  name: string;
  description: string;
  created_at?: string;
  updated_at?: string;
  member_count: number;
  service_count: number;
}

// ── Metadata relationship graph ────────────────────────────────────────
export interface MetaGraphNode {
  id: string;
  kind: "integration" | "value" | "tag";
  label: string;
  integration_id?: string;
  field?: string;
  field_label?: string;
  value?: string;
}
export interface MetaGraphEdge {
  source: string;
  target: string;
}
export interface MetaGraphResponse {
  nodes: MetaGraphNode[];
  edges: MetaGraphEdge[];
  fields: { key: string; label: string }[];
}

// ── SSO / OIDC (EE) ────────────────────────────────────────────────────
export type OrgRole = "admin" | "editor" | "viewer";

export interface AuthProvider {
  id: string;
  org_id: string;
  name: string;
  kind: string;
  issuer_url: string;
  client_id: string;
  claim_email: string;
  claim_name: string;
  claim_sub: string;
  claim_groups: string;
  scopes: string;
  default_role: OrgRole;
  jit_provisioning: boolean;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface ClaimMapping {
  id: string;
  provider_id: string;
  claim_value: string;
  org_role?: OrgRole | "";
  group_id?: string | null;
  group_role?: OrgRole;
  created_at?: string;
}

// SsoProviderButton is the pre-auth login-page shape (no secrets).
export interface SsoProviderButton {
  id: string;
  name: string;
}

export interface GroupInput {
  name: string;
  slug: string;
  description: string;
}

// Exactly one of user / service_account is set — group memberships are
// polymorphic (service accounts join groups to gain scoped visibility).
export interface GroupMember {
  user?: AuthUser;
  service_account?: ServiceAccount;
  role: AuthRole;
  joined_at: string;
}

export interface ListGroupsResponse {
  groups: Group[];
}

export interface ListGroupMembersResponse {
  members: GroupMember[];
}

export interface ServiceGroupsResponse {
  groups: Group[];
}

// ── Access policies (ABAC layer on groups) ───────────────────────────

// User-defined monitoring template — a named, org-owned bundle of health-check
// specs (metric + log), created from a service / forked from a built-in / built
// by hand, and applied like a built-in.
export interface MonitoringTemplateCheck {
  name: string;
  description?: string;
  signal?: string; // "" | "metric" | "log" | "trace_error" | "trace_latency" | "trace_volume"
  metric?: string;
  agg?: string;
  op?: string;
  threshold?: number;
  attrs?: { key: string; op: string; value: string }[];
  min_severity?: number;
  body_contains?: string;
  log_threshold?: number;
  split_by?: string; // metric checks: evaluate per distinct attribute value
  // trace-signal fields
  trace_threshold?: number; // trace_error / trace_volume
  threshold_ms?: number; // trace_latency (p95)
  window_seconds?: number; // trace checks
  severity?: string;
  unit?: string;
  display?: boolean;
}

export interface MonitoringTemplate {
  id: string;
  name: string;
  description: string;
  source: string; // 'custom' | 'fork:<kind>' | 'service:<name>'
  checks: MonitoringTemplateCheck[];
  created_at: string;
  updated_at: string;
}

// A system type in the managed catalog: detection prefixes + starter checks per
// kind. Built-ins are read-only (built_in=true, id=""); org rows are editable.
export interface SystemType {
  id: string; // "" for a pure built-in (read-only)
  key: string;
  label: string;
  is_system: boolean;
  detect_prefixes: string[];
  checks: MonitoringTemplateCheck[];
  built_in: boolean;
}

// A system instance (phase 2): an entity of a given type spanning member
// services. `members` are service names; on the list endpoint they're the
// visible members.
export interface System {
  id: string;
  name: string;
  type_key: string;
  description: string;
  members: string[];
  member_count: number;
  status?: string; // rollup of member health: ok/errors/unhealthy/quiet
  badge_public?: boolean; // public status-badge opt-in (detail response)
}

export interface SystemDetailResponse {
  can_manage?: boolean;
  window: string;
  system: System;
  members: ServiceSummary[];
}

// "Since last visit" digest — RBAC-filtered per user.
export interface DigestNewService {
  service_name: string;
  namespace?: string;
  first_seen_at: string;
  suggested_kind?: string;
  suggested_label?: string;
}
export interface DigestFailure {
  service_name?: string;
  integration_id?: string;
  integration_name?: string;
  rule_name: string;
  severity: string;
  state: string;
  started_at: string;
}
export interface DigestShared {
  resource_kind: "integration" | "system";
  resource_id: string;
  resource_name: string;
  shared_by?: string;
  shared_at: string;
}

export interface DigestResponse {
  since: string;
  new_services: DigestNewService[];
  failures: DigestFailure[];
  shared?: DigestShared[];
  counts: { new_services: number; failures: number; shared?: number };
}

// One viewer-only share of an integration/system (RBAC v2 phase 3).
export interface ResourceShare {
  id: string;
  resource_kind: "integration" | "system";
  resource_id: string;
  grantee_kind: "user" | "group";
  grantee_id: string;
  grantee_name: string;
  created_by?: string;
  created_at: string;
}

export type PolicyKind =
  | "service"
  | "integration"
  | "attributes"
  | "compound"
  | "all_org"
  | "system"
  | "expression";

// PolicyExpr is one node of an expression policy's boolean tree. A node
// is an operator (op + children); a leaf carries a match op. A leaf with
// no `attr` matches the service name, else the named resource attribute.
export type PolicyExprOp = "and" | "or" | "not";
export type PolicyExprMatch =
  | "equals"
  | "not_equals"
  | "prefix"
  | "suffix"
  | "contains"
  | "regex"
  | "in"
  | "exists"
  | "not_exists";

export interface PolicyExpr {
  op?: PolicyExprOp;
  children?: PolicyExpr[];
  attr?: string;
  match?: PolicyExprMatch;
  value?: string;
  values?: string[];
}

export interface AccessPolicy {
  id: string;
  group_id: string;
  kind: PolicyKind;
  target_service_name?: string | null;
  target_integration_id?: string | null;
  target_system_kind?: string | null;
  target_system_id?: string | null;
  attribute_match: Record<string, string>;
  conditions?: PolicyExpr | null;
  signals?: ("traces" | "logs" | "metrics" | "messages")[];
  created_at?: string;
}

// MeAccess mirrors the caller's scoped-manage capabilities (RBAC v2 §5,
// GET /api/v1/me/access). Server gates stay authoritative.
export interface MeAccess {
  write_anywhere: boolean;
  manage_all: boolean;
  managed_services: string[];
  editor_groups: { id: string; slug: string; name: string }[];
}

// ResourceGroup is one group attached to an integration/system via the
// "Group access" card (RBAC v2 phase 1 — the CE visibility grant).
export interface ResourceGroup {
  group_id: string;
  slug: string;
  name: string;
}

export interface AccessPolicyInput {
  kind: PolicyKind;
  target_service_name: string;
  target_integration_id: string;
  target_system_kind: string;
  attribute_match: Record<string, string>;
  conditions?: PolicyExpr;
  signals?: ("traces" | "logs" | "metrics" | "messages")[];
}
