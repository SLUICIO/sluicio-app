// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// Tiny typed fetch client for the cell-api. The Vite dev server
// proxies /api to localhost:8081 (see vite.config.ts) so callers can
// use relative paths from the browser.

import type {
  CreateDashboardRequest,
  CreateIntegrationRequest,
  CreateMessageViewRequest,
  CreateTagRequest,
  CreateFacetMappingRequest,
  Dashboard,
  DashboardsResponse,
  FacetMapping,
  FacetMappingsResponse,
  FacetOverridesResponse,
  FlowResponse,
  IngestKey,
  IngestKeyCreated,
  Integration,
  IntegrationDetail,
  LogAttrFilter,
  LogAttrValuesResponse,
  LogCursor,
  LogEntry,
  LogFieldsResponse,
  LogsResponse,
  LogAttrValue,
  LogVolumeResponse,
  LogServicesResponse,
  Matcher,
  MessageAttributeKey,
  MessageFieldsResponse,
  MessageSearchRequest,
  MessageView,
  MessageViewsResponse,
  MetadataField,
  MetadataFieldInput,
  Schema,
  SchemaInput,
  SchemaUsage,
  MapDoc,
  MapInput,
  MapExecuteRequest,
  MapExecuteResponse,
  AuthLoginResponse,
  AuthOrg,
  AuthUser,
  RetentionRequest,
  RetentionResponse,
  SystemSettings,
  SystemSettingsRequest,
  TraceCompletionCounts,
  TraceCompletionFiring,
  TraceCompletionRule,
  TraceCompletionRuleInput,
  MeResponse,
  MeAccess,
  AuthRole,
  OperatorOrg,
  OperatorUser,
  OperatorOrgMembersResponse,
  CreateTokenResponse,
  ListMembersResponse,
  ResourceGroup,
  ResourceShare,
  ListTokensResponse,
  ServiceAccount,
  Group,
  GroupInput,
  ListGroupsResponse,
  AuthProvider,
  ClaimMapping,
  SsoProviderButton,
  MetaGraphResponse,
  ListGroupMembersResponse,
  ServiceGroupsResponse,
  AccessPolicy,
  AccessPolicyInput,
  AlertDelivery,
  AlertInstance,
  AlertPreview,
  AlertRule,
  AlertRuleInput,
  ChannelInput,
  LogGroupsResponse,
  MetricCatalogResponse,
  MetricCatalogRichResponse,
  MetricGroupsResponse,
  MetricRuleSpec,
  NotificationContent,
  MetricSeriesByServiceResponse,
  NotificationChannel,
  NotificationProfile,
  NotificationProfileInput,
  ServiceMetadata,
  NeighborsResponse,
  SearchResponse,
  GlobalSearchResponse,
  ServiceDetailResponse,
  ServiceErrorAck,
  ServiceLogsResponse,
  ServiceMetricNamesResponse,
  ServiceMetricSeriesResponse,
  ServiceReadingsResponse,
  ServicesResponse,
  ErrorsFeedResponse,
  LicenseStatus,
  AuditLogResponse,
  AuditVerifyResult,
  SMTPSettingsResponse,
  SMTPSettingsRequest,
  MFAStatusResponse,
  MFASetupResponse,
  MFAEnableResponse,
  SecuritySettingsResponse,
  ServiceTracesResponse,
  ServiceFacetDetailResponse,
  ServiceFacetsListResponse,
  ServiceWidgetsResponse,
  Tag,
  TagWithUsage,
  TraceDetailResponse,
  UpdateDashboardRequest,
  UpdateFacetOverridesRequest,
  UpdateMessageViewRequest,
  UpdateTagRequest,
  MonitoringTemplate,
  MonitoringTemplateCheck,
  SystemType,
  System,
  SystemDetailResponse,
  SystemMetadataResponse,
  DigestResponse,
  UsageVolumeResponse,
} from "./types";
import { getActiveOrgSlug } from "../lib/activeOrg";

const BASE = "/api/v1";

async function request<T>(
  path: string,
  init: RequestInit = {}
): Promise<T> {
  // Multi-org users pin their active org client-side; the cell-api
  // resolves scope per request from this header (membership-checked
  // server-side). Absent header = first membership.
  const activeOrg = getActiveOrgSlug();
  const res = await fetch(BASE + path, {
    // Explicit "same-origin" so the session cookie set by the cell-api
    // (via the Vite proxy in dev, or directly in prod) is included on
    // every request. The fetch default IS same-origin in modern
    // browsers, but writing it out keeps the auth contract obvious.
    credentials: "same-origin",
    headers: {
      Accept: "application/json",
      ...(activeOrg ? { "X-Sluicio-Org": activeOrg } : {}),
      ...(init.body ? { "Content-Type": "application/json" } : {}),
      ...(init.headers ?? {}),
    },
    ...init,
  });
  if (!res.ok) {
    let detail = res.statusText;
    try {
      const body = await res.json();
      if (body?.error?.message) detail = body.error.message;
    } catch {
      /* ignore */
    }
    throw new Error(`${res.status} ${detail}`);
  }
  if (res.status === 204) return undefined as unknown as T;
  // Defensive: a 200 with no body (e.g. a backend that failed to encode
  // and silently dropped the response) used to surface here as a cryptic
  // "JSON.parse: unexpected end of data". Read as text first so we can
  // tell the difference between empty and parseable.
  const text = await res.text();
  if (text === "") {
    throw new Error(`${res.status} empty response body`);
  }
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new Error(`${res.status} invalid JSON in response body`);
  }
}

const get = <T,>(p: string) => request<T>(p);
const post = <T,>(p: string, body: unknown) =>
  request<T>(p, { method: "POST", body: JSON.stringify(body) });
// postEmpty is for POSTs that take no body and return 204 — used by
// the idempotent tag-attach endpoints.
const postEmpty = (p: string) => request<void>(p, { method: "POST" });
const put = <T,>(p: string, body: unknown) =>
  request<T>(p, { method: "PUT", body: JSON.stringify(body) });
const patch = <T,>(p: string, body: unknown) =>
  request<T>(p, { method: "PATCH", body: JSON.stringify(body) });
const del = (p: string) => request<void>(p, { method: "DELETE" });

export const api = {
  listServices: (window: string = "1h") =>
    get<ServicesResponse>(`/services?range=${encodeURIComponent(window)}`),

  // Services flagged as monitored "systems" (RabbitMQ, SQL Server, …).
  // System entities (phase 2). The list returns entities with visible members.
  listSystems: () => get<{ systems: System[] }>(`/systems`),
  getSystem: (id: string, window: string = "1h") =>
    get<SystemDetailResponse>(`/systems/${encodeURIComponent(id)}?range=${encodeURIComponent(window)}`),
  createSystem: (body: { name: string; type_key: string; description?: string }) =>
    post<System>(`/systems`, body),
  updateSystem: (id: string, body: { name: string; type_key: string; description?: string }) =>
    put<System>(`/systems/${encodeURIComponent(id)}`, body),
  deleteSystem: (id: string) => del(`/systems/${encodeURIComponent(id)}`),
  attachSystemService: (id: string, serviceName: string) =>
    post<{ attached: boolean }>(`/systems/${encodeURIComponent(id)}/services`, { service_name: serviceName }),
  detachSystemService: (id: string, serviceName: string) =>
    del(`/systems/${encodeURIComponent(id)}/services/${encodeURIComponent(serviceName)}`),
  getSystemMetadata: (id: string) =>
    get<SystemMetadataResponse>(`/systems/${encodeURIComponent(id)}/metadata`),
  putSystemMetadata: (id: string, values: Record<string, string>) =>
    put<SystemMetadataResponse>(`/systems/${encodeURIComponent(id)}/metadata`, values),
  applySystemTemplateAll: (id: string, channelIds: string[] = []) =>
    post<{ type_key: string; members: number; created: number; updated: number; skipped: number; message?: string }>(
      `/systems/${encodeURIComponent(id)}/apply-template`,
      { channel_ids: channelIds },
    ),

  // Mark/unmark a service as a system (+ its kind). Clearing is_system blanks
  // the kind server-side.
  setServiceSystem: (name: string, isSystem: boolean, systemKind: string) =>
    put<{ service_name: string; is_system: boolean; system_kind: string }>(
      `/services/${encodeURIComponent(name)}/system`,
      { is_system: isSystem, system_kind: systemKind },
    ),

  // Opt an entity into (or out of) its public status badge. `id` is the
  // integration/system id, or the service name.
  setBadgePublic: (kind: "integration" | "system" | "service", id: string, isPublic: boolean) =>
    put<{ badge_public: boolean }>(
      kind === "integration"
        ? `/integrations/${encodeURIComponent(id)}/badge`
        : kind === "system"
          ? `/systems/${encodeURIComponent(id)}/badge`
          : `/services/${encodeURIComponent(id)}/badge`,
      { public: isPublic },
    ),

  // Apply the built-in monitoring template for the service's system kind —
  // creates its health checks (skipping any already present). channelIds, if
  // given, are routed onto every template check (new + existing) so they alert.
  applySystemTemplate: (name: string, channelIds: string[] = []) =>
    post<{ kind: string; created: number; updated: number; skipped: number; message?: string }>(
      `/services/${encodeURIComponent(name)}/system/apply-template`,
      { channel_ids: channelIds },
    ),

  // Apply any monitoring template kind to a service (not limited to systems).
  applyTemplate: (name: string, kind: string, channelIds: string[] = []) =>
    post<{ kind: string; created: number; updated: number; skipped: number; message?: string }>(
      `/services/${encodeURIComponent(name)}/apply-template`,
      { kind, channel_ids: channelIds },
    ),

  // Auto-detected template kinds for a service, from its emitted metric names.
  templateSuggestions: (name: string) =>
    get<{ suggestions: { kind: string; label: string; system: boolean; check_count: number; applied: boolean }[] }>(
      `/services/${encodeURIComponent(name)}/template-suggestions`,
    ),

  // Remove a template's checks from a service (by built-in kind or custom id).
  removeTemplate: (name: string, opts: { kind?: string; templateId?: string }) =>
    post<{ removed: number }>(`/services/${encodeURIComponent(name)}/remove-template`, {
      kind: opts.kind,
      template_id: opts.templateId,
    }),

  // Apply a custom (user-defined) template by id to a service.
  applyCustomTemplate: (name: string, templateId: string, channelIds: string[] = []) =>
    post<{ kind: string; created: number; updated: number; skipped: number; message?: string }>(
      `/services/${encodeURIComponent(name)}/apply-template`,
      { template_id: templateId, channel_ids: channelIds },
    ),

  // User-defined monitoring templates (custom + forks).
  listMonitoringTemplates: () =>
    get<{ templates: MonitoringTemplate[] }>(`/monitoring-templates`),
  createMonitoringTemplate: (body: {
    name: string;
    description?: string;
    from_service?: string;
    fork_kind?: string;
    checks?: MonitoringTemplateCheck[];
  }) => post<MonitoringTemplate>(`/monitoring-templates`, body),
  updateMonitoringTemplate: (id: string, body: { name: string; description?: string; checks: MonitoringTemplateCheck[] }) =>
    put<MonitoringTemplate>(`/monitoring-templates/${encodeURIComponent(id)}`, body),
  deleteMonitoringTemplate: (id: string) => del(`/monitoring-templates/${encodeURIComponent(id)}`),

  // System-types catalog (built-ins + org custom/overrides).
  listSystemTypes: () => get<{ system_types: SystemType[] }>(`/system-types`),
  createSystemType: (body: {
    key: string;
    label: string;
    is_system: boolean;
    detect_prefixes: string[];
    checks: MonitoringTemplateCheck[];
  }) => post<SystemType>(`/system-types`, body),
  updateSystemType: (
    id: string,
    body: { label: string; is_system: boolean; detect_prefixes: string[]; checks: MonitoringTemplateCheck[] },
  ) => put<SystemType>(`/system-types/${encodeURIComponent(id)}`, body),
  deleteSystemType: (id: string) => del(`/system-types/${encodeURIComponent(id)}`),

  errorsFeed: (window: string = "1h") =>
    get<ErrorsFeedResponse>(`/errors?range=${encodeURIComponent(window)}`),

  // "Since last visit" activity digest (RBAC-filtered) + mark-as-seen.
  getDigest: () => get<DigestResponse>(`/digest`),
  markDigestSeen: () => postEmpty(`/digest/seen`),

  serviceDetail: (name: string, window: string = "1h") =>
    get<ServiceDetailResponse>(
      `/services/${encodeURIComponent(name)}?range=${encodeURIComponent(window)}`
    ),

  search: (
    q: string,
    window: string = "1h",
    opts: { integrationId?: string; serviceName?: string; onlyFailed?: boolean } = {}
  ) => {
    const params = new URLSearchParams({ q, range: window });
    if (opts.serviceName) params.set("service", opts.serviceName);
    else if (opts.integrationId) params.set("integration", opts.integrationId);
    if (opts.onlyFailed) params.set("only_failed", "true");
    return get<SearchResponse>(`/search?${params.toString()}`);
  },

  // Global search — the top-navbar "search everything" finder (#28).
  globalSearch: (q: string, window: string = "1h") =>
    get<GlobalSearchResponse>(
      `/global-search?q=${encodeURIComponent(q)}&range=${encodeURIComponent(window)}`,
    ),

  traceDetail: (traceId: string) =>
    get<TraceDetailResponse>(`/traces/${encodeURIComponent(traceId)}`),

  // Integrations. Pass { series: true } to include each integration's
  // traffic sparkline series (the dashboard); the plain list omits it to
  // avoid the extra per-row query.
  listIntegrations: (window: string = "1h", opts: { series?: boolean } = {}) =>
    get<{ integrations: Integration[]; metadata_fields?: MetadataField[] }>(
      `/integrations?range=${encodeURIComponent(window)}${opts.series ? "&series=1" : ""}`
    ),

  getIntegration: (id: string, window: string = "1h") =>
    get<IntegrationDetail>(
      `/integrations/${encodeURIComponent(id)}?range=${encodeURIComponent(window)}`
    ),

  createIntegration: (req: CreateIntegrationRequest) =>
    post<IntegrationDetail>(`/integrations`, req),

  updateIntegration: (id: string, body: { name: string; description: string }) =>
    put<Integration>(`/integrations/${encodeURIComponent(id)}`, body),

  deleteIntegration: (id: string) =>
    del(`/integrations/${encodeURIComponent(id)}`),

  addMatcher: (id: string, body: { operator: string; value: string; attribute?: string; match_group?: number }) =>
    post<Matcher>(`/integrations/${encodeURIComponent(id)}/matchers`, body),

  removeMatcher: (id: string, matcherId: string) =>
    del(`/integrations/${encodeURIComponent(id)}/matchers/${encodeURIComponent(matcherId)}`),

  // Remove a service's direct (equals) link to an integration. Returns the
  // number of matchers removed; 0 means the service is matched by a broader
  // rule that this endpoint won't touch.
  removeServiceFromIntegration: (id: string, serviceName: string) =>
    request<{ removed: number }>(
      `/integrations/${encodeURIComponent(id)}/services/${encodeURIComponent(serviceName)}`,
      { method: "DELETE" }
    ),

  // Org-wide topology graph. view = "services" (default) | "integrations".
  getTopology: (window: string = "24h", view: "services" | "integrations" = "services") =>
    get<FlowResponse>(`/topology?range=${encodeURIComponent(window)}&view=${view}`),
  // Metadata relationship graph: integrations ↔ metadata values + tags.
  getMetadataGraph: () => get<MetaGraphResponse>(`/metadata-graph`),
  integrationFlow: (id: string, window: string = "1h") =>
    get<FlowResponse>(
      `/integrations/${encodeURIComponent(id)}/flow?range=${encodeURIComponent(window)}`
    ),

  // Service facets — multi-facet classification of a service.
  listServiceFacets: () => get<ServiceFacetsListResponse>(`/service-facets`),

  getServiceFacet: (slug: string, window: string = "1h") =>
    get<ServiceFacetDetailResponse>(
      `/service-facets/${encodeURIComponent(slug)}?range=${encodeURIComponent(window)}`
    ),
  // Custom facet management (editor+).
  createServiceFacet: (body: { name: string; description?: string }) =>
    post<{ slug: string; name: string; description: string }>(`/service-facets`, body),
  updateServiceFacet: (slug: string, body: { name: string; description?: string }) =>
    put<{ slug: string; name: string; description: string }>(`/service-facets/${encodeURIComponent(slug)}`, body),
  deleteServiceFacet: (slug: string) => del(`/service-facets/${encodeURIComponent(slug)}`),

  serviceWidgets: (name: string, window: string = "1h") =>
    get<ServiceWidgetsResponse>(
      `/services/${encodeURIComponent(name)}/widgets?range=${encodeURIComponent(window)}`
    ),

  serviceTraces: (name: string, window: string = "1h", opts: { onlyFailed?: boolean } = {}) =>
    get<ServiceTracesResponse>(
      `/services/${encodeURIComponent(name)}/traces?range=${encodeURIComponent(window)}${
        opts.onlyFailed ? "&only_failed=1" : ""
      }`
    ),

  // serviceNeighbors returns the direct callers + callees of a service
  // in the trace graph over the supplied window. Used by the integration
  // builder to suggest dependent services when the user pins an `equals`
  // matcher to a known service.
  serviceNeighbors: (name: string, window: string = "1h") =>
    get<NeighborsResponse>(
      `/services/${encodeURIComponent(name)}/neighbors?range=${encodeURIComponent(window)}`
    ),

  // Editable service metadata (description/owner/on-call/team/repo/runbook).
  // GET also returns the org's user-defined metadata fields applicable
  // to services plus this service's saved values.
  getServiceMetadata: (name: string) =>
    get<ServiceMetadata>(`/services/${encodeURIComponent(name)}/metadata`),
  updateServiceMetadata: (name: string, body: Partial<ServiceMetadata>) =>
    put<ServiceMetadata>(`/services/${encodeURIComponent(name)}/metadata`, body),

  // User-defined metadata fields (the schema, org-scoped).
  listMetadataFields: () => get<{ fields: MetadataField[] }>("/metadata-fields"),
  createMetadataField: (body: MetadataFieldInput) =>
    post<MetadataField>("/metadata-fields", body),
  updateMetadataField: (id: string, body: MetadataFieldInput) =>
    patch<MetadataField>(`/metadata-fields/${encodeURIComponent(id)}`, body),
  deleteMetadataField: (id: string) =>
    del(`/metadata-fields/${encodeURIComponent(id)}`),

  // Per-target value setters. Body is a key → value map (boolean/number
  // values are accepted; the server coerces to TEXT for storage).
  setIntegrationMetadata: (id: string, values: Record<string, string | boolean | number | null>) =>
    put<{ metadata_values: Record<string, string> }>(
      `/integrations/${encodeURIComponent(id)}/metadata`, values),
  setServiceMetadataExtras: (name: string, values: Record<string, string | boolean | number | null>) =>
    put<{ metadata_values: Record<string, string> }>(
      `/services/${encodeURIComponent(name)}/metadata-extras`, values),

  // ── Data schemas ──────────────────────────────────────────────────
  listSchemas: () => get<{ schemas: Schema[] }>("/schemas"),
  getSchema: (id: string) =>
    get<{ schema: Schema; usage: SchemaUsage[] }>(`/schemas/${encodeURIComponent(id)}`),
  createSchema: (body: SchemaInput) => post<Schema>("/schemas", body),
  updateSchema: (id: string, body: SchemaInput) =>
    patch<Schema>(`/schemas/${encodeURIComponent(id)}`, body),
  deleteSchema: (id: string) => del(`/schemas/${encodeURIComponent(id)}`),
  // Set / clear a service's In-Schema and Out-Schema. Either id may be
  // null to clear that direction.
  setServiceSchemas: (name: string, body: { in_schema_id?: string | null; out_schema_id?: string | null }) =>
    put<{ in?: Schema; out?: Schema }>(`/services/${encodeURIComponent(name)}/schemas`, body),

  // ── Maps (data transformations) ───────────────────────────────────
  listMaps: () => get<{ maps: MapDoc[] }>("/maps"),
  getMap: (id: string) => get<{ map: MapDoc }>(`/maps/${encodeURIComponent(id)}`),
  createMap: (body: MapInput) => post<MapDoc>("/maps", body),
  updateMap: (id: string, body: MapInput) =>
    patch<MapDoc>(`/maps/${encodeURIComponent(id)}`, body),
  deleteMap: (id: string) => del(`/maps/${encodeURIComponent(id)}`),
  executeMap: (id: string, body: MapExecuteRequest) =>
    post<MapExecuteResponse>(`/maps/${encodeURIComponent(id)}/execute`, body),

  // Facet attribute mappings — user-defined rules that classify a
  // service into an I/O facet when io.kind / io.role aren't set on
  // its spans. CRUD only; the rules are applied implicitly inside
  // the service-widgets and facet-classification endpoints.
  listFacetMappings: (name: string) =>
    get<FacetMappingsResponse>(
      `/services/${encodeURIComponent(name)}/facet-mappings`,
    ),

  createFacetMapping: (name: string, req: CreateFacetMappingRequest) =>
    post<FacetMapping>(
      `/services/${encodeURIComponent(name)}/facet-mappings`,
      req,
    ),

  deleteFacetMapping: (name: string, id: string) =>
    del(
      `/services/${encodeURIComponent(name)}/facet-mappings/${encodeURIComponent(id)}`,
    ),

  // Manual facet overrides — direct include/exclude decisions for a
  // service's facets, layered on top of auto-detection. GET returns the
  // full facet vocabulary annotated for the editor; PUT replaces the
  // whole override set and returns the recomputed resolution.
  getServiceFacetOverrides: (name: string) =>
    get<FacetOverridesResponse>(
      `/services/${encodeURIComponent(name)}/facet-overrides`,
    ),

  updateServiceFacetOverrides: (
    name: string,
    req: UpdateFacetOverridesRequest,
  ) =>
    put<FacetOverridesResponse>(
      `/services/${encodeURIComponent(name)}/facet-overrides`,
      req,
    ),

  // Service-page value tiles: the "show on service page" health checks
  // bound to a service, each with its latest reading + breach state.
  serviceReadings: (name: string) =>
    get<ServiceReadingsResponse>(`/services/${encodeURIComponent(name)}/readings`),

  // Push an external observation to a pushed-source health check.
  pushHealthCheckValue: (name: string, id: string, value: number) =>
    post<void>(
      `/services/${encodeURIComponent(name)}/health-checks/${encodeURIComponent(id)}/value`,
      { value }
    ),

  // "Clear errors": mark a service's current failures reviewed (sets a
  // watermark + optional comment), or undo it.
  clearServiceErrors: (name: string, comment?: string) =>
    post<ServiceErrorAck>(`/services/${encodeURIComponent(name)}/clear-errors`, {
      comment: comment ?? "",
    }),
  unclearServiceErrors: (name: string) =>
    del(`/services/${encodeURIComponent(name)}/clear-errors`),

  // Ingested OTLP logs + metrics — the raw telemetry browse endpoints,
  // distinct from the custom-metrics (threshold) endpoints above.
  listServiceLogs: (
    name: string,
    window: string = "1h",
    opts: { q?: string; minSeverity?: number; traceId?: string; limit?: number; cursor?: LogCursor; attrs?: LogAttrFilter[] } = {}
  ) => {
    const params = new URLSearchParams({ range: window });
    if (opts.q) params.set("q", opts.q);
    if (opts.minSeverity) params.set("min_severity", String(opts.minSeverity));
    if (opts.traceId) params.set("trace_id", opts.traceId);
    if (opts.limit) params.set("limit", String(opts.limit));
    if (opts.cursor) {
      params.set("before_ts", opts.cursor.ts);
      params.set("before_ord", opts.cursor.ord);
    }
    (opts.attrs ?? []).forEach((a) => params.append("attr", JSON.stringify(a)));
    return get<ServiceLogsResponse>(
      `/services/${encodeURIComponent(name)}/logs?${params.toString()}`
    );
  },

  listServiceMetricNames: (name: string, window: string = "1h") =>
    get<ServiceMetricNamesResponse>(
      `/services/${encodeURIComponent(name)}/metric-names?range=${encodeURIComponent(window)}`
    ),

  serviceMetricSeries: (name: string, metric: string, window: string = "1h", transform?: string, stepSeconds?: number) => {
    const params = new URLSearchParams({ metric, range: window });
    if (transform) params.set("transform", transform);
    if (stepSeconds) params.set("step", String(stepSeconds));
    return get<ServiceMetricSeriesResponse>(
      `/services/${encodeURIComponent(name)}/metric-series?${params.toString()}`
    );
  },

  // Global logs + metrics — across all services, the dedicated Logs and
  // Metrics pages (not scoped to one service).
  searchLogs: (
    window: string = "1h",
    opts: { q?: string; minSeverity?: number; service?: string; integration?: string; traceId?: string; limit?: number; cursor?: LogCursor; attrs?: LogAttrFilter[] } = {}
  ) => {
    const params = new URLSearchParams({ range: window });
    if (opts.q) params.set("q", opts.q);
    if (opts.minSeverity) params.set("min_severity", String(opts.minSeverity));
    if (opts.service) params.set("service", opts.service);
    if (opts.integration) params.set("integration", opts.integration);
    if (opts.traceId) params.set("trace_id", opts.traceId);
    if (opts.limit) params.set("limit", String(opts.limit));
    if (opts.cursor) {
      params.set("before_ts", opts.cursor.ts);
      params.set("before_ord", opts.cursor.ord);
    }
    (opts.attrs ?? []).forEach((a) => params.append("attr", JSON.stringify(a)));
    return get<LogsResponse>(`/logs?${params.toString()}`);
  },

  // Fetch a single log by its LogId — backs the drawer deep link, which
  // re-opens the exact log regardless of the current window / filters.
  getLog: (id: string) => get<LogEntry>(`/logs/${encodeURIComponent(id)}`),

  logServices: (window: string = "1h") =>
    get<LogServicesResponse>(`/log-services?range=${encodeURIComponent(window)}`),

  logFields: (window: string = "1h") =>
    get<LogFieldsResponse>(`/log-fields?range=${encodeURIComponent(window)}`),

  logAttrValues: (key: string, window: string = "1h", limit = 50) =>
    get<LogAttrValuesResponse>(
      `/log-attributes/${encodeURIComponent(key)}/values?range=${encodeURIComponent(window)}&limit=${limit}`,
    ),

  logVolume: (
    window: string = "1h",
    opts: { q?: string; minSeverity?: number; service?: string; integration?: string; attrs?: LogAttrFilter[]; buckets?: number } = {},
  ) => {
    const params = new URLSearchParams({ range: window });
    if (opts.q) params.set("q", opts.q);
    if (opts.minSeverity) params.set("min_severity", String(opts.minSeverity));
    if (opts.service) params.set("service", opts.service);
    if (opts.integration) params.set("integration", opts.integration);
    if (opts.buckets) params.set("buckets", String(opts.buckets));
    (opts.attrs ?? []).forEach((a) => params.append("attr", JSON.stringify(a)));
    return get<LogVolumeResponse>(`/logs/volume?${params.toString()}`);
  },

  listMetricCatalog: (window: string = "1h") =>
    get<MetricCatalogResponse>(`/metric-names?range=${encodeURIComponent(window)}`),

  // Metric explorer: rich catalog (sparkline table) + attribute picker,
  // mirroring the Logs filter endpoints.
  metricCatalog: (
    window: string = "1h",
    opts: { q?: string; type?: string; attrs?: LogAttrFilter[]; service?: string; integration?: string; limit?: number } = {},
  ) => {
    const params = new URLSearchParams({ range: window });
    if (opts.q) params.set("q", opts.q);
    if (opts.type && opts.type !== "all") params.set("type", opts.type);
    if (opts.service) params.set("service", opts.service);
    if (opts.integration) params.set("integration", opts.integration);
    if (opts.limit) params.set("limit", String(opts.limit));
    (opts.attrs ?? []).forEach((a) => params.append("attr", JSON.stringify(a)));
    return get<MetricCatalogRichResponse>(`/metric-catalog?${params.toString()}`);
  },

  metricGroups: (
    window: string = "1h",
    by: string,
    opts: { key?: string; q?: string; type?: string; attrs?: LogAttrFilter[] } = {},
  ) => {
    const params = new URLSearchParams({ range: window, by });
    if (opts.key) params.set("key", opts.key);
    if (opts.q) params.set("q", opts.q);
    if (opts.type && opts.type !== "all") params.set("type", opts.type);
    (opts.attrs ?? []).forEach((a) => params.append("attr", JSON.stringify(a)));
    return get<MetricGroupsResponse>(`/metric-groups?${params.toString()}`);
  },

  logGroups: (
    window: string = "1h",
    by: string,
    opts: { key?: string; q?: string; minSeverity?: number; service?: string; integration?: string; attrs?: LogAttrFilter[] } = {},
  ) => {
    const params = new URLSearchParams({ range: window, by });
    if (opts.key) params.set("key", opts.key);
    if (opts.q) params.set("q", opts.q);
    if (opts.minSeverity) params.set("min_severity", String(opts.minSeverity));
    if (opts.service) params.set("service", opts.service);
    if (opts.integration) params.set("integration", opts.integration);
    (opts.attrs ?? []).forEach((a) => params.append("attr", JSON.stringify(a)));
    return get<LogGroupsResponse>(`/logs/groups?${params.toString()}`);
  },

  usageVolume: (window: string = "1h", windowed = false) =>
    get<UsageVolumeResponse>(
      `/usage/volume?range=${encodeURIComponent(window)}${windowed ? "&windowed=1" : ""}`,
    ),

  metricFields: (window: string = "1h", metric?: string) =>
    get<LogFieldsResponse>(
      `/metric-fields?range=${encodeURIComponent(window)}${metric ? `&metric=${encodeURIComponent(metric)}` : ""}`,
    ),

  metricAttributeValues: (key: string, window: string = "1h", limit = 50, metric?: string, q?: string) =>
    get<LogAttrValuesResponse>(
      `/metric-attributes/${encodeURIComponent(key)}/values?range=${encodeURIComponent(window)}&limit=${limit}${
        metric ? `&metric=${encodeURIComponent(metric)}` : ""
      }${q ? `&q=${encodeURIComponent(q)}` : ""}`,
    ),

  // Alerting: metric rules, would-fire preview, channels, instances ----
  listAlertRules: (opts: { service?: string; integration?: string } = {}) => {
    const p = new URLSearchParams();
    if (opts.service) p.set("service", opts.service);
    if (opts.integration) p.set("integration", opts.integration);
    const qs = p.toString();
    return get<{ rules: AlertRule[] }>(`/alert-rules${qs ? `?${qs}` : ""}`);
  },
  createAlertRule: (body: AlertRuleInput) => post<AlertRule>(`/alert-rules`, body),
  updateAlertRule: (id: string, body: AlertRuleInput) =>
    put<AlertRule>(`/alert-rules/${encodeURIComponent(id)}`, body),
  deleteAlertRule: (id: string) => del(`/alert-rules/${encodeURIComponent(id)}`),
  previewAlertRule: (spec: MetricRuleSpec, serviceName?: string) =>
    post<AlertPreview>(`/alert-rules/preview`, { spec, service_name: serviceName }),
  // Render a notification (email HTML / webhook JSON) against a sample firing
  // context for the given content config — preview, no send.
  previewAlertTemplate: (kind: string, content: NotificationContent) =>
    post<{ subject: string; body: string }>(`/alert-templates/preview`, { kind, content }),
  getAlertEmailTemplate: () =>
    get<{ subject: string; body: string; default_subject: string; default_body: string }>(`/alert-email-template`),
  putAlertEmailTemplate: (subject: string, body: string) =>
    put<void>(`/alert-email-template`, { subject, body }),
  listAlertInstances: (limit = 100) => get<{ instances: AlertInstance[] }>(`/alert-instances?limit=${limit}`),

  // Delivery history — what notifications have been sent, to which
  // channel, and whether they succeeded. Server-side filtered by the time
  // range plus optional service / integration / system / health-check name.
  listAlertDeliveries: (
    opts: { range?: string; service?: string; integration?: string; system?: string; name?: string; limit?: number } = {},
  ) => {
    const p = new URLSearchParams();
    if (opts.range) p.set("range", opts.range);
    if (opts.service) p.set("service", opts.service);
    if (opts.integration) p.set("integration", opts.integration);
    if (opts.system) p.set("system", opts.system);
    if (opts.name) p.set("name", opts.name);
    p.set("limit", String(opts.limit ?? 500));
    return get<{ deliveries: AlertDelivery[] }>(`/alert-deliveries?${p.toString()}`);
  },
  // Acknowledge ("being worked on") / resolve ("closed") a firing alert.
  acknowledgeAlertInstance: (id: string) =>
    postEmpty(`/alert-instances/${encodeURIComponent(id)}/acknowledge`),
  resolveAlertInstance: (id: string) =>
    postEmpty(`/alert-instances/${encodeURIComponent(id)}/resolve`),
  // Per-org OTLP ingest keys. createIngestKey returns the full secret
  // once (IngestKeyCreated.key) — surface it immediately, then it's gone.
  listIngestKeys: () => get<{ keys: IngestKey[] }>(`/ingest-keys`),
  createIngestKey: (name: string) => post<IngestKeyCreated>(`/ingest-keys`, { name }),
  revokeIngestKey: (id: string) => del(`/ingest-keys/${encodeURIComponent(id)}`),

  listChannels: () => get<{ channels: NotificationChannel[] }>(`/notification-channels`),

  // Notification profiles: per-team (or org-wide) bundles of behaviour +
  // channels. An alert/error resolves to one profile, most-specific-first:
  // the integration's assigned profile → the owning team's default → the
  // org-wide default.
  listNotificationProfiles: () =>
    get<{ profiles: NotificationProfile[] }>(`/notification-profiles`),
  createNotificationProfile: (body: NotificationProfileInput) =>
    post<NotificationProfile>(`/notification-profiles`, body),
  updateNotificationProfile: (id: string, body: NotificationProfileInput) =>
    put<NotificationProfile>(`/notification-profiles/${encodeURIComponent(id)}`, body),
  deleteNotificationProfile: (id: string) =>
    del(`/notification-profiles/${encodeURIComponent(id)}`),
  setNotificationProfileChannels: (id: string, channelIDs: string[]) =>
    put<void>(`/notification-profiles/${encodeURIComponent(id)}/channels`, {
      channel_ids: channelIDs,
    }),
  getIntegrationProfile: (integrationID: string) =>
    get<{ profile_id: string | null }>(
      `/integrations/${encodeURIComponent(integrationID)}/notification-profile`,
    ),
  assignIntegrationProfile: (integrationID: string, profileID: string | null) =>
    put<void>(
      `/integrations/${encodeURIComponent(integrationID)}/notification-profile`,
      { profile_id: profileID ?? "" },
    ),
  createChannel: (body: ChannelInput) => post<NotificationChannel>(`/notification-channels`, body),
  updateChannel: (id: string, body: ChannelInput) =>
    put<NotificationChannel>(`/notification-channels/${encodeURIComponent(id)}`, body),
  deleteChannel: (id: string) => del(`/notification-channels/${encodeURIComponent(id)}`),

  metricSeriesByService: (metric: string, window: string = "1h", services?: string[], transform?: string, stepSeconds?: number) => {
    const params = new URLSearchParams({ metric, range: window });
    (services ?? []).forEach((s) => params.append("service", s));
    if (transform) params.set("transform", transform);
    if (stepSeconds) params.set("step", String(stepSeconds));
    return get<MetricSeriesByServiceResponse>(`/metric-series?${params.toString()}`);
  },

  // Messages: structured search + saved views ---------------------------
  //
  // The Messages page uses these endpoints in place of /search. The
  // body of /messages/search mirrors the FilterEditor's filter list
  // 1:1, and /message-views persists saved searches in Postgres.
  searchMessages: (req: MessageSearchRequest) =>
    post<SearchResponse>(`/messages/search`, req),

  messageFields: (window: string = "24h") =>
    get<MessageFieldsResponse>(
      `/messages/fields?range=${encodeURIComponent(window)}`
    ),

  listMessageViews: () => get<MessageViewsResponse>(`/message-views`),

  createMessageView: (req: CreateMessageViewRequest) =>
    post<MessageView>(`/message-views`, req),

  updateMessageView: (id: string, req: UpdateMessageViewRequest) =>
    put<MessageView>(`/message-views/${encodeURIComponent(id)}`, req),

  deleteMessageView: (id: string) =>
    del(`/message-views/${encodeURIComponent(id)}`),

  // Tags ----------------------------------------------------------------
  //
  // Org-scoped vocabulary. Tag attach/detach is idempotent on the
  // server (ON CONFLICT DO NOTHING) so the UI can fire requests
  // freely without tracking local state.
  listTags: () => get<{ tags: Tag[] }>(`/tags`),

  // Same endpoint, richer payload — used by the management page so
  // every row can show how many integrations / services it touches
  // without N+1.
  listTagsWithUsage: () =>
    get<{ tags: TagWithUsage[] }>(`/tags?include=usage`),

  createTag: (req: CreateTagRequest) => post<Tag>(`/tags`, req),

  updateTag: (id: string, req: UpdateTagRequest) =>
    patch<Tag>(`/tags/${encodeURIComponent(id)}`, req),

  deleteTag: (id: string) => del(`/tags/${encodeURIComponent(id)}`),

  listIntegrationTags: (id: string) =>
    get<{ tags: Tag[] }>(`/integrations/${encodeURIComponent(id)}/tags`),

  attachIntegrationTag: (id: string, tagId: string) =>
    postEmpty(
      `/integrations/${encodeURIComponent(id)}/tags/${encodeURIComponent(tagId)}`,
    ),

  detachIntegrationTag: (id: string, tagId: string) =>
    del(
      `/integrations/${encodeURIComponent(id)}/tags/${encodeURIComponent(tagId)}`,
    ),

  listServiceTags: (name: string) =>
    get<{ tags: Tag[] }>(`/services/${encodeURIComponent(name)}/tags`),

  attachServiceTag: (name: string, tagId: string) =>
    postEmpty(
      `/services/${encodeURIComponent(name)}/tags/${encodeURIComponent(tagId)}`,
    ),

  detachServiceTag: (name: string, tagId: string) =>
    del(
      `/services/${encodeURIComponent(name)}/tags/${encodeURIComponent(tagId)}`,
    ),

  // Dashboards ----------------------------------------------------------
  //
  // Per-user, named layouts for the Home page. The list call returns
  // every dashboard visible to the active org with items inlined, so a
  // single request gives the picker its menu *and* the currently-active
  // dashboard's contents.
  listDashboards: () => get<DashboardsResponse>(`/dashboards`),

  getDashboard: (id: string) =>
    get<Dashboard>(`/dashboards/${encodeURIComponent(id)}`),

  createDashboard: (req: CreateDashboardRequest) =>
    post<Dashboard>(`/dashboards`, req),

  updateDashboard: (id: string, req: UpdateDashboardRequest) =>
    put<Dashboard>(`/dashboards/${encodeURIComponent(id)}`, req),

  deleteDashboard: (id: string) =>
    del(`/dashboards/${encodeURIComponent(id)}`),

  // ── Auth ──────────────────────────────────────────────────────────
  // login mints a session cookie on success; the browser holds it
  // HTTP-only. The response carries the user + memberships so the
  // SPA can render its first frame post-login without a /me round-trip.
  login: (body: { email: string; password: string }) =>
    post<AuthLoginResponse>(`/auth/login`, body),
  logout: () => post<void>(`/auth/logout`, {}),

  // Self-service password reset (both public). forgotPassword always
  // resolves 200 regardless of whether the email exists.
  forgotPassword: (email: string) =>
    post<{ status: string }>(`/auth/forgot-password`, { email }),
  resetPassword: (token: string, newPassword: string) =>
    post<{ status: string }>(`/auth/reset-password`, { token, new_password: newPassword }),

  // MFA (TOTP). Account endpoints manage the current user's own 2FA;
  // mfaVerify is the public login second step.
  mfaStatus: () => get<MFAStatusResponse>(`/account/mfa`),
  mfaSetup: () => post<MFASetupResponse>(`/account/mfa/setup`, {}),
  mfaEnable: (code: string) => post<MFAEnableResponse>(`/account/mfa/enable`, { code }),
  mfaDisable: (code: string) => post<{ enabled: boolean }>(`/account/mfa/disable`, { code }),
  mfaVerify: (mfaToken: string, code: string) =>
    post<AuthLoginResponse>(`/auth/mfa-verify`, { mfa_token: mfaToken, code }),

  // Global SMTP settings (admin). Password is write-only — getSMTP never
  // returns it, only password_set. updateSMTP omits password to keep it.
  getSMTP: () => get<SMTPSettingsResponse>(`/cell-settings/smtp`),
  updateSMTP: (body: SMTPSettingsRequest) =>
    patch<SMTPSettingsResponse>(`/cell-settings/smtp`, body),
  testSMTP: (to?: string) =>
    post<{ sent: boolean; to: string }>(`/cell-settings/smtp/test`, to ? { to } : {}),

  // Org security policy (admin). Toggling MFA enforcement needs the
  // Enterprise mfa_policy entitlement (server returns 402 otherwise).
  getSecuritySettings: () => get<SecuritySettingsResponse>(`/cell-settings/security`),
  updateSecuritySettings: (mfaRequired: boolean) =>
    patch<SecuritySettingsResponse>(`/cell-settings/security`, { mfa_required: mfaRequired }),
  // meAccess mirrors scoped-manage capabilities for UI affordances.
  meAccess: () => get<MeAccess>(`/me/access`),

  // me is the bootstrap call on app open. 401 → "show login page";
  // 200 → render the app with this principal + memberships.
  me: () => get<MeResponse>(`/me`),

  // installState is public — the Login page calls it before anyone
  // has a session, to decide whether to show the "ships with a
  // default admin" hint. fresh=true on a first-boot install.
  installState: () => get<{ fresh: boolean }>(`/auth/install-state`),

  // Self-service profile + password. updateMe returns the fresh
  // users row; the caller re-renders state. changePassword returns
  // 204 (or 401 if the current password is wrong).
  updateMe: (body: { name?: string; email?: string }) =>
    patch<AuthUser>(`/me`, body),
  changePassword: (body: { current_password: string; new_password: string }) =>
    post<void>(`/me/password`, body),

  // Organization profile. Any member can read; admins can edit /
  // delete. The active org id comes from useCurrentUser().
  getOrg: (id: string) => get<AuthOrg>(`/orgs/${encodeURIComponent(id)}`),
  updateOrg: (id: string, body: { name?: string; slug?: string }) =>
    patch<AuthOrg>(`/orgs/${encodeURIComponent(id)}`, body),

  // Cell-wide settings. Read is open to any signed-in user; PATCH is
  // Enterprise license status — drives feature gating + upsell in the UI.
  license: () => get<LicenseStatus>(`/license`),

  // Enterprise audit log (admin + audit_log entitlement gated server-side).
  // All filters optional: actor is a name/email substring, action a prefix
  // ("member." matches member.added …), from/to are RFC3339 bounds.
  listAuditLog: (
    opts: {
      limit?: number;
      before?: number;
      actor?: string;
      actorId?: string;
      action?: string;
      from?: string;
      to?: string;
    } = {},
  ) => {
    const p = new URLSearchParams({ limit: String(opts.limit ?? 100) });
    if (opts.before) p.set("before", String(opts.before));
    if (opts.actor) p.set("actor", opts.actor);
    if (opts.actorId) p.set("actor_id", opts.actorId);
    if (opts.action) p.set("action", opts.action);
    if (opts.from) p.set("from", opts.from);
    if (opts.to) p.set("to", opts.to);
    return get<AuditLogResponse>(`/audit-log?${p.toString()}`);
  },
  // Walk the org's tamper-evidence hash chain and report integrity.
  verifyAuditChain: () => get<AuditVerifyResult>(`/audit-log/verify`),

  // admin-only (server enforces). Retention is the only knob today.
  getRetention: () => get<RetentionResponse>(`/cell-settings/retention`),
  updateRetention: (body: RetentionRequest) =>
    patch<RetentionResponse>(`/cell-settings/retention`, body),
  // System settings (environment label). Read open; PATCH admin-only.
  getSystemSettings: () => get<SystemSettings>(`/cell-settings/system`),
  updateSystemSettings: (body: SystemSettingsRequest) =>
    patch<SystemSettings>(`/cell-settings/system`, body),

  // Trace completion rules per integration. Mutations are admin-only;
  // reads (rules + counts) are open to any signed-in member.
  listTraceCompletionRules: (integrationID: string) =>
    get<{ rules: TraceCompletionRule[] }>(
      `/integrations/${encodeURIComponent(integrationID)}/completion-rules`,
    ),
  // Distinct span names across the integration's traces — suggestions for
  // the start/stage span pickers in the rule editor.
  integrationSpanNames: (integrationID: string, range = "24h") =>
    get<{ span_names: string[] }>(
      `/integrations/${encodeURIComponent(integrationID)}/span-names?range=${encodeURIComponent(range)}`,
    ),
  // Distinct payload attribute keys seen within this integration's traffic —
  // powers the payload-field typeahead on the integration Messages tab so the
  // user only sees attributes that actually appear in this integration.
  integrationAttributeKeys: (integrationID: string, range = "24h") =>
    get<{ attribute_keys: MessageAttributeKey[] }>(
      `/integrations/${encodeURIComponent(integrationID)}/attribute-keys?range=${encodeURIComponent(range)}`,
    ),
  // Top-N values for one attribute key within this integration's traffic —
  // powers the value typeahead on the integration Messages tab.
  integrationAttributeValues: (integrationID: string, key: string, range = "24h") =>
    get<{ key: string; values: LogAttrValue[] }>(
      `/integrations/${encodeURIComponent(integrationID)}/attribute-values?key=${encodeURIComponent(key)}&range=${encodeURIComponent(range)}`,
    ),
  createTraceCompletionRule: (integrationID: string, body: TraceCompletionRuleInput) =>
    post<TraceCompletionRule>(
      `/integrations/${encodeURIComponent(integrationID)}/completion-rules`,
      body,
    ),
  updateTraceCompletionRule: (integrationID: string, ruleID: string, body: TraceCompletionRuleInput) =>
    patch<TraceCompletionRule>(
      `/integrations/${encodeURIComponent(integrationID)}/completion-rules/${encodeURIComponent(ruleID)}`,
      body,
    ),
  deleteTraceCompletionRule: (integrationID: string, ruleID: string) =>
    del(
      `/integrations/${encodeURIComponent(integrationID)}/completion-rules/${encodeURIComponent(ruleID)}`,
    ),
  getCompletionCounts: (integrationID: string) =>
    get<TraceCompletionCounts>(
      `/integrations/${encodeURIComponent(integrationID)}/completion-counts`,
    ),
  listCompletionFirings: (integrationID: string, window: string = "1h") =>
    get<{ firings: TraceCompletionFiring[] }>(
      `/integrations/${encodeURIComponent(integrationID)}/completion-firings?range=${encodeURIComponent(window)}`,
    ),
  // Per-trace lookup — drives the trace-level StatusPip (warn/err)
  // on the TraceDetail header. Returns ALL firings (any state)
  // matching this trace_id across rules in the org.
  listCompletionFiringsForTrace: (traceID: string) =>
    get<{ firings: TraceCompletionFiring[] }>(
      `/traces/${encodeURIComponent(traceID)}/completion-firings`,
    ),

  // Send a sample notification to a channel to verify it works. Resolves
  // on 204; rejects with the delivery error (e.g. SMTP failure) otherwise.
  testChannel: (id: string) =>
    postEmpty(`/notification-channels/${encodeURIComponent(id)}/test`),

  // Mark a delayed trace's firing as operator-handled. It stays open
  // (never re-fires) but stops counting as delayed and renders benign.
  markCompletionFiringHandled: (integrationID: string, instanceID: string) =>
    postEmpty(
      `/integrations/${encodeURIComponent(integrationID)}/completion-firings/${encodeURIComponent(instanceID)}/handle`,
    ),

  // ── Settings → Members (admin) ─────────────────────────────────
  listMembers: () => get<ListMembersResponse>(`/settings/members`),
  addMember: (body: { email: string; name: string; password: string; role: AuthRole }) =>
    post<{ user: unknown; role: AuthRole }>(`/settings/members`, body),
  updateMemberRole: (userId: string, role: AuthRole) =>
    patch<void>(`/settings/members/${encodeURIComponent(userId)}`, { role }),
  adminResetMemberPassword: (userId: string, newPassword: string, requireChange: boolean) =>
    post<void>(`/settings/members/${encodeURIComponent(userId)}/password`, {
      new_password: newPassword,
      require_change: requireChange,
    }),
  removeMember: (userId: string) =>
    del(`/settings/members/${encodeURIComponent(userId)}`),

  // ── Settings → Personal access tokens ───────────────────────────
  listTokens: () => get<ListTokensResponse>(`/settings/tokens`),
  createToken: (name: string, scopeRole = "", expiresInDays = 0) =>
    post<CreateTokenResponse>(`/settings/tokens`, { name, scope_role: scopeRole, expires_in_days: expiresInDays }),
  revokeToken: (id: string) => del(`/settings/tokens/${encodeURIComponent(id)}`),

  // ── Settings → Service accounts (machine identities + their tokens) ──
  listServiceAccounts: () => get<{ service_accounts: ServiceAccount[] }>(`/settings/service-accounts`),
  createServiceAccount: (body: { name: string; description?: string; role: string }) =>
    post<ServiceAccount>(`/settings/service-accounts`, body),
  updateServiceAccount: (id: string, body: { name: string; description?: string; role: string }) =>
    put<ServiceAccount>(`/settings/service-accounts/${encodeURIComponent(id)}`, body),
  deleteServiceAccount: (id: string) => del(`/settings/service-accounts/${encodeURIComponent(id)}`),
  listServiceAccountTokens: (id: string) =>
    get<ListTokensResponse>(`/settings/service-accounts/${encodeURIComponent(id)}/tokens`),
  createServiceAccountToken: (id: string, name: string, scopeRole = "", expiresInDays = 0) =>
    post<CreateTokenResponse>(`/settings/service-accounts/${encodeURIComponent(id)}/tokens`, { name, scope_role: scopeRole, expires_in_days: expiresInDays }),
  revokeServiceAccountToken: (id: string, tid: string) =>
    del(`/settings/service-accounts/${encodeURIComponent(id)}/tokens/${encodeURIComponent(tid)}`),

  // ── Settings → SSO / OIDC (EE) ────────────────────────────────────
  listSsoProviders: () => get<{ providers: SsoProviderButton[] }>(`/auth/sso/providers`),
  listAuthProviders: () => get<{ providers: AuthProvider[] }>(`/settings/auth-providers`),
  createAuthProvider: (body: Partial<AuthProvider> & { client_secret?: string }) =>
    post<AuthProvider>(`/settings/auth-providers`, body),
  updateAuthProvider: (id: string, body: Partial<AuthProvider> & { client_secret?: string }) =>
    put<AuthProvider>(`/settings/auth-providers/${encodeURIComponent(id)}`, body),
  deleteAuthProvider: (id: string) => del(`/settings/auth-providers/${encodeURIComponent(id)}`),
  listClaimMappings: (id: string) =>
    get<{ mappings: ClaimMapping[] }>(`/settings/auth-providers/${encodeURIComponent(id)}/mappings`),
  createClaimMapping: (
    id: string,
    body: { claim_value: string; org_role?: string; group_id?: string | null; group_role?: string },
  ) => post<ClaimMapping>(`/settings/auth-providers/${encodeURIComponent(id)}/mappings`, body),
  deleteClaimMapping: (id: string, mid: string) =>
    del(`/settings/auth-providers/${encodeURIComponent(id)}/mappings/${encodeURIComponent(mid)}`),

  // ── Settings → Groups (access-control axis under org) ─────────────
  listGroups: () => get<ListGroupsResponse>(`/settings/groups`),
  getGroup: (id: string) => get<Group>(`/settings/groups/${encodeURIComponent(id)}`),
  createGroup: (body: GroupInput) => post<Group>(`/settings/groups`, body),
  updateGroup: (id: string, body: GroupInput) =>
    patch<void>(`/settings/groups/${encodeURIComponent(id)}`, body),
  deleteGroup: (id: string) => del(`/settings/groups/${encodeURIComponent(id)}`),
  listGroupMembers: (groupId: string) =>
    get<ListGroupMembersResponse>(`/settings/groups/${encodeURIComponent(groupId)}/members`),
  addGroupMember: (groupId: string, body: { user_id: string; role: AuthRole }) =>
    post<void>(`/settings/groups/${encodeURIComponent(groupId)}/members`, body),
  updateGroupMemberRole: (groupId: string, userId: string, role: AuthRole) =>
    patch<void>(`/settings/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}`, { role }),
  removeGroupMember: (groupId: string, userId: string) =>
    del(`/settings/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}`),

  // Per-service group assignment surface.
  listServiceGroups: (name: string) =>
    get<ServiceGroupsResponse>(`/services/${encodeURIComponent(name)}/groups`),
  putServiceGroups: (name: string, groupIDs: string[]) =>
    put<void>(`/services/${encodeURIComponent(name)}/groups`, { group_ids: groupIDs }),

  // ── Access policies (the ABAC layer under groups) ───────────────
  listGroupPolicies: (groupId: string) =>
    get<{ policies: AccessPolicy[] }>(`/settings/groups/${encodeURIComponent(groupId)}/policies`),
  createGroupPolicy: (groupId: string, body: AccessPolicyInput) =>
    post<AccessPolicy>(`/settings/groups/${encodeURIComponent(groupId)}/policies`, body),
  deleteGroupPolicy: (groupId: string, policyId: string) =>
    del(`/settings/groups/${encodeURIComponent(groupId)}/policies/${encodeURIComponent(policyId)}`),

  // ── Resource ⇄ group attachment (RBAC v2 phase 1) ────────────────
  // "Which groups can view this integration/system" — the CE-facing
  // visibility grant. Reads open to members; PUT is org-admin.
  listIntegrationGroups: (id: string) =>
    get<{ groups: ResourceGroup[] }>(`/integrations/${encodeURIComponent(id)}/groups`),
  setIntegrationGroups: (id: string, groupIDs: string[]) =>
    put<{ group_ids: string[] }>(`/integrations/${encodeURIComponent(id)}/groups`, {
      group_ids: groupIDs,
    }),
  listSystemGroups: (id: string) =>
    get<{ groups: ResourceGroup[] }>(`/systems/${encodeURIComponent(id)}/groups`),
  setSystemGroups: (id: string, groupIDs: string[]) =>
    put<{ group_ids: string[] }>(`/systems/${encodeURIComponent(id)}/groups`, {
      group_ids: groupIDs,
    }),

  // ── Resource sharing (RBAC v2 phase 3, EE) ───────────────────────
  listResourceShares: (kind: "integrations" | "systems", id: string) =>
    get<{ shares: ResourceShare[] }>(`/${kind}/${encodeURIComponent(id)}/shares`),
  createResourceShare: (
    kind: "integrations" | "systems",
    id: string,
    body: { grantee_kind: "user" | "group"; grantee_email?: string; grantee_group_id?: string },
  ) => post<{ id: string }>(`/${kind}/${encodeURIComponent(id)}/shares`, body),
  deleteResourceShare: (kind: "integrations" | "systems", id: string, shareId: string) =>
    del(`/${kind}/${encodeURIComponent(id)}/shares/${encodeURIComponent(shareId)}`),

  // ── Operator surface (cell super-admin) ─────────────────────────
  // Org lifecycle + cross-org member assignment + operator management.
  // All operator-gated on the backend.
  operatorListOrgs: () => get<{ orgs: OperatorOrg[] }>(`/operator/orgs`),
  operatorCreateOrg: (body: { name: string; slug: string }) =>
    post<OperatorOrg>(`/operator/orgs`, body),
  operatorUpdateOrg: (id: string, body: { name?: string; slug?: string }) =>
    patch<OperatorOrg>(`/operator/orgs/${encodeURIComponent(id)}`, body),
  operatorDeleteOrg: (id: string) => del(`/operator/orgs/${encodeURIComponent(id)}`),
  operatorListOrgMembers: (id: string) =>
    get<OperatorOrgMembersResponse>(`/operator/orgs/${encodeURIComponent(id)}/members`),
  operatorAddOrgMember: (
    id: string,
    body: { email: string; name?: string; password?: string; role: AuthRole },
  ) => post<void>(`/operator/orgs/${encodeURIComponent(id)}/members`, body),
  operatorUpdateOrgMemberRole: (id: string, userId: string, role: AuthRole) =>
    patch<void>(
      `/operator/orgs/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}`,
      { role },
    ),
  operatorRemoveOrgMember: (id: string, userId: string) =>
    del(`/operator/orgs/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}`),
  operatorListUsers: (q = "", offset = 0, limit = 50) =>
    get<{ users: OperatorUser[]; total: number; limit: number; offset: number }>(
      `/operator/users?q=${encodeURIComponent(q)}&limit=${limit}&offset=${offset}`,
    ),
  operatorSetUserDemo: (userId: string, isDemo: boolean) =>
    put<void>(`/operator/users/${encodeURIComponent(userId)}/demo`, { is_demo: isDemo }),
  operatorSetUserOperator: (userId: string, isOperator: boolean) =>
    put<void>(`/operator/users/${encodeURIComponent(userId)}/operator`, {
      is_operator: isOperator,
    }),
};
