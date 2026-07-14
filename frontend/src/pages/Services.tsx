// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import ServicesTable from "../components/ServicesTable";
import TagChip from "../components/tags/TagChip";
import IntegrationFilterBar, {
  matchesFilter,
  parseFilters,
  serializeFilters,
  type FieldSpec,
} from "../components/integrations/IntegrationFilterBar";
import type { MetadataField, ServiceSummary, ServicesResponse, Tag } from "../api/types";
import { formatNumber } from "../lib/format";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";

// Static (non-metadata) filter fields for services — name, namespace, status.
// (No slug/description like integrations have.)
const SERVICE_STATIC_FIELDS: FieldSpec[] = [
  { field: "name", label: "Name", kind: "text" },
  { field: "namespace", label: "Namespace", kind: "text" },
  { field: "status", label: "Status", kind: "status", options: ["ok", "errors", "unhealthy", "quiet"] },
  // System kind — filter to a specific system type (equals), all systems
  // (is set), or non-systems (is empty).
  { field: "system_kind", label: "System", kind: "text" },
];

// Dependency facet — find edge nodes in the window's service flow graph.
// Evaluated only for services that had traffic in-window (a quiet service has
// no edges and would otherwise read as "isolated").
type DepFilter =
  | ""
  | "has-upstream"
  | "has-downstream"
  | "has-any"
  | "no-upstream"
  | "no-downstream"
  | "isolated";
const DEP_OPTIONS: { value: DepFilter; label: string }[] = [
  { value: "", label: "Any dependencies" },
  { value: "has-upstream", label: "Has upstream callers" },
  { value: "has-downstream", label: "Has downstream callees" },
  { value: "has-any", label: "Has a dependency (either)" },
  { value: "no-upstream", label: "No upstream callers" },
  { value: "no-downstream", label: "No downstream callees" },
  { value: "isolated", label: "Isolated (no callers or callees)" },
];

// Group-by dimension for the list. Static dimensions below + one per service
// metadata field (built in the component). Extensible: add a case to
// groupKeysFor and an entry here (or it comes from metadata automatically).
type GroupBy = "" | "integration" | "namespace" | "status" | "tag" | `meta:${string}`;
const STATIC_GROUPS: { value: GroupBy; label: string }[] = [
  { value: "", label: "No grouping" },
  { value: "integration", label: "Integration" },
  { value: "namespace", label: "Namespace" },
  { value: "status", label: "Status" },
  { value: "tag", label: "Tag" },
];
// Bucket for services with no value on the chosen dimension.
const UNGROUPED = "— none —";

// The group label(s) a service belongs to on the chosen dimension. Many-to-many
// dimensions (integration, tag) can return several — the service then appears
// under each. Empty → the UNGROUPED bucket.
function groupKeysFor(s: ServiceSummary, by: GroupBy): string[] {
  switch (by) {
    case "integration": return (s.integrations ?? []).map((i) => i.name);
    case "namespace": return s.service_namespace ? [s.service_namespace] : [];
    case "status": return [s.status];
    case "tag": return (s.tags ?? []).map((t) => t.name);
    default:
      if (by.startsWith("meta:")) {
        const v = s.metadata_values?.[by.slice(5)];
        return v ? [v] : [];
      }
      return [];
  }
}

// Multi-tag filtering uses AND semantics — selecting two tags narrows
// the table to services carrying both — matching the integrations list.
export default function Services() {
  usePageTitle("Services");
  const [windowVal] = useTimeWindow();
  const [data, setData] = useState<ServicesResponse | null>(null);
  const [allTags, setAllTags] = useState<Tag[]>([]);
  const [metadataFields, setMetadataFields] = useState<MetadataField[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  // URL is the source of truth for tag / metadata / dependency filters so the
  // whole query is shareable and survives reload.
  const [searchParams, setSearchParams] = useSearchParams();
  const activeSlugs = useMemo<string[]>(() => {
    const raw = searchParams.get("tags");
    if (!raw) return [];
    return raw.split(",").map((s) => s.trim()).filter(Boolean);
  }, [searchParams]);
  const filters = useMemo(() => parseFilters(searchParams.get("filter")), [searchParams]);
  const dep = (searchParams.get("dep") ?? "") as DepFilter;

  // Each setter preserves the other params.
  const patchParams = (mut: (p: URLSearchParams) => void) => {
    const params = new URLSearchParams(searchParams);
    mut(params);
    setSearchParams(params, { replace: true });
  };
  const setActiveSlugs = (next: string[]) =>
    patchParams((p) => (next.length === 0 ? p.delete("tags") : p.set("tags", next.join(","))));
  const setFilters = (next: ReturnType<typeof parseFilters>) =>
    patchParams((p) => {
      const enc = serializeFilters(next);
      enc ? p.set("filter", enc) : p.delete("filter");
    });
  const setDep = (next: DepFilter) => patchParams((p) => (next ? p.set("dep", next) : p.delete("dep")));
  const groupBy = (searchParams.get("group") ?? "") as GroupBy;
  const setGroupBy = (next: GroupBy) => patchParams((p) => (next ? p.set("group", next) : p.delete("group")));

  // Collapsed group sections (by label). Empty = all expanded.
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const toggleCollapse = (label: string) =>
    setCollapsed((cur) => {
      const next = new Set(cur);
      next.has(label) ? next.delete(label) : next.add(label);
      return next;
    });

  const toggleSlug = (slug: string) => {
    setActiveSlugs(
      activeSlugs.includes(slug)
        ? activeSlugs.filter((s) => s !== slug)
        : [...activeSlugs, slug],
    );
  };

  const refresh = () => {
    setLoading(true);
    setError(null);
    api
      .listServices(windowVal)
      .then((d) => setData(d))
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
    api
      .listTags()
      .then((d) => setAllTags(d.tags ?? []))
      .catch(() => setAllTags([]));
    api
      .listMetadataFields()
      .then((r) => setMetadataFields((r.fields ?? []).filter((f) => f.applies_to_service)))
      .catch(() => setMetadataFields([]));
  };

  useEffect(() => {
    refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [windowVal]);

  const services: ServiceSummary[] = useMemo(() => data?.services ?? [], [data]);
  const okCount = services.filter((s) => s.status === "ok").length;
  const errorCount = services.filter((s) => s.status === "errors" || s.status === "unhealthy").length;
  const totalTraces = services.reduce((acc, s) => acc + s.trace_count, 0);
  // Services not matched by any integration — org-level (whole catalog, not the
  // filtered view), so it's a stable "you have N orphans" report.
  const noIntegrationCount = services.filter((s) => (s.integrations?.length ?? 0) === 0).length;

  const activeTags = useMemo(
    () => allTags.filter((t) => activeSlugs.includes(t.slug)),
    [allTags, activeSlugs],
  );
  const activeTagIds = useMemo(() => new Set(activeTags.map((t) => t.id)), [activeTags]);

  // Pull the comparable cell value out of a service for a filter field.
  const cellValueFor = (s: ServiceSummary, field: string): string => {
    switch (field) {
      case "name": return s.service_name;
      case "namespace": return s.service_namespace ?? "";
      case "status": return s.status ?? "";
      case "system_kind": return s.system_kind ?? "";
      default:
        if (field.startsWith("meta:")) return s.metadata_values?.[field.slice(5)] ?? "";
        return "";
    }
  };

  // Distinct values per field across the loaded services — backs the value
  // typeahead in the filter bar.
  const distinctValuesByField = useMemo<Record<string, string[]>>(() => {
    const sets = new Map<string, Set<string>>();
    const push = (field: string, value: string) => {
      const v = (value ?? "").trim();
      if (!v) return;
      let set = sets.get(field);
      if (!set) sets.set(field, (set = new Set()));
      set.add(v);
    };
    for (const s of services) {
      push("name", s.service_name);
      if (s.service_namespace) push("namespace", s.service_namespace);
      for (const [k, v] of Object.entries(s.metadata_values ?? {})) push(`meta:${k}`, v);
    }
    const out: Record<string, string[]> = {};
    for (const [field, set] of sets) out[field] = [...set].sort((a, b) => a.localeCompare(b));
    return out;
  }, [services]);

  const depMatches = (s: ServiceSummary): boolean => {
    if (!dep) return true;
    // (b): only evaluate dependency for services active in the window — a
    // quiet service has no edges and isn't meaningfully "isolated".
    if (s.trace_count === 0) return false;
    const up = s.upstream_count ?? 0;
    const down = s.downstream_count ?? 0;
    switch (dep) {
      case "has-upstream": return up > 0;
      case "has-downstream": return down > 0;
      case "has-any": return up > 0 || down > 0;
      case "no-upstream": return up === 0;
      case "no-downstream": return down === 0;
      case "isolated": return up === 0 && down === 0;
      default: return true;
    }
  };

  const visibleServices = useMemo(() => {
    return services.filter((s) => {
      if (activeTagIds.size > 0) {
        const ids = new Set((s.tags ?? []).map((t) => t.id));
        for (const need of activeTagIds) if (!ids.has(need)) return false;
      }
      for (const f of filters) {
        if (!matchesFilter(f, cellValueFor(s, f.field))) return false;
      }
      if (!depMatches(s)) return false;
      return true;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [services, activeTagIds, filters, dep]);

  const filterActive = activeTagIds.size > 0 || filters.length > 0 || dep !== "";

  // Group-by options: static dimensions + one per service metadata field.
  const groupOptions = useMemo(
    () => [
      ...STATIC_GROUPS,
      ...metadataFields.map((f) => ({ value: `meta:${f.key}` as GroupBy, label: f.label })),
    ],
    [metadataFields],
  );

  // Grouped view: label → services. Many-to-many dimensions duplicate a service
  // across the groups it belongs to. Sorted alphabetically, UNGROUPED last.
  const grouped = useMemo(() => {
    if (!groupBy) return null;
    const m = new Map<string, ServiceSummary[]>();
    for (const s of visibleServices) {
      const keys = groupKeysFor(s, groupBy);
      for (const label of keys.length ? keys : [UNGROUPED]) {
        const arr = m.get(label) ?? [];
        arr.push(s);
        m.set(label, arr);
      }
    }
    return [...m.entries()].sort((a, b) =>
      a[0] === UNGROUPED ? 1 : b[0] === UNGROUPED ? -1 : a[0].localeCompare(b[0]),
    );
  }, [groupBy, visibleServices]);

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Services</h1>
          <p className="page__subtitle">
            Everything that has sent telemetry in the selected time window.
          </p>
        </div>
        <div className="toolbar">
          <button className="btn" onClick={refresh} disabled={loading}>
            {loading ? "Loading…" : "Refresh"}
          </button>
        </div>
      </div>

      <div className="tiles">
        <Tile label="Services receiving data" value={formatNumber(okCount)} tone="ok" />
        <Tile label="Services with errors" value={formatNumber(errorCount)} tone="errors" />
        <Tile label="Traces in window" value={formatNumber(totalTraces)} tone="neutral" />
        <Tile
          label="Not in an integration"
          value={formatNumber(noIntegrationCount)}
          tone="neutral"
          onClick={() => setGroupBy("integration")}
          title="Group by integration to list them (under “— none —”)"
        />
      </div>

      {/* Metadata filter bar (field / op / value) + dependency facet. */}
      <div style={{ display: "flex", flexDirection: "column", gap: 8, margin: "8px 0 16px" }}>
        <IntegrationFilterBar
          filters={filters}
          onChange={setFilters}
          metadataFields={metadataFields}
          distinctValues={distinctValuesByField}
          staticFields={SERVICE_STATIC_FIELDS}
          noun="services"
        />
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 10,
            padding: "8px 12px",
            borderRadius: 8,
            border: "1px solid var(--border)",
            background: "var(--surface-2)",
          }}
        >
          <span className="muted" style={{ fontSize: 12, textTransform: "uppercase", letterSpacing: 0.5 }}>
            dependencies
          </span>
          <select
            className="toolbar__select"
            value={dep}
            onChange={(e) => setDep(e.target.value as DepFilter)}
            aria-label="Filter by service dependency"
          >
            {DEP_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
          <span className="muted" style={{ fontSize: 12 }}>
            (within the selected window)
          </span>

          <span
            className="muted"
            style={{ fontSize: 12, textTransform: "uppercase", letterSpacing: 0.5, marginLeft: "auto" }}
          >
            group by
          </span>
          <select
            className="toolbar__select"
            value={groupBy}
            onChange={(e) => setGroupBy(e.target.value as GroupBy)}
            aria-label="Group services by"
          >
            {groupOptions.map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
        </div>
      </div>

      {allTags.length > 0 && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 10,
            flexWrap: "wrap",
            margin: "8px 0 16px",
            padding: "10px 12px",
            borderRadius: 8,
            border: "1px solid var(--border)",
            background: "var(--surface-2)",
          }}
        >
          <span
            className="muted"
            style={{ fontSize: 12, textTransform: "uppercase", letterSpacing: 0.5 }}
          >
            filter by tag
          </span>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
            {allTags.map((t) => {
              const active = activeTagIds.has(t.id);
              return (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => toggleSlug(t.slug)}
                  aria-pressed={active}
                  title={active ? `Remove ${t.name} from filter` : `Add ${t.name} to filter`}
                  style={{
                    background: "transparent",
                    border: 0,
                    padding: 0,
                    cursor: "pointer",
                    opacity: activeTagIds.size > 0 && !active ? 0.55 : 1,
                    transition: "opacity 0.12s ease",
                  }}
                >
                  <span
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      borderRadius: 999,
                      boxShadow: active ? "0 0 0 2px var(--ink)" : "none",
                    }}
                  >
                    <TagChip tag={t} />
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      )}

      {filterActive && services.length > 0 && (
        <div style={{ display: "flex", alignItems: "center", gap: 8, margin: "0 0 12px" }}>
          <span className="muted" style={{ fontSize: 12 }}>
            {visibleServices.length} of {services.length} services match the active filters
          </span>
          <button
            type="button"
            className="btn btn--link"
            onClick={() => {
              patchParams((p) => {
                p.delete("tags");
                p.delete("filter");
                p.delete("dep");
              });
            }}
          >
            Clear all
          </button>
        </div>
      )}

      {error && <div className="alert alert--error">Failed to load services: {error}</div>}

      {!error && services.length === 0 && !loading && (
        <div className="placeholder">
          No services yet. Point an OpenTelemetry collector at{" "}
          <code>http://localhost:4318/v1/traces</code>.
        </div>
      )}

      {services.length > 0 && visibleServices.length === 0 && (
        <div className="placeholder">
          No services match the active filters. Loosen them, or{" "}
          <button
            type="button"
            className="btn btn--link"
            onClick={() => {
              patchParams((p) => {
                p.delete("tags");
                p.delete("filter");
                p.delete("dep");
              });
            }}
          >
            clear all
          </button>
          .
        </div>
      )}

      {visibleServices.length > 0 && (
        groupBy && grouped ? (
          // Grouped view: one collapsible card per group. When grouping by
          // integration, hide the Integrations column (the group is it).
          <div>
            {grouped.map(([label, svcs]) => {
              const isCollapsed = collapsed.has(label);
              return (
                <div className="card" key={label} style={{ marginBottom: 12, overflow: "hidden" }}>
                  <button
                    type="button"
                    onClick={() => toggleCollapse(label)}
                    aria-expanded={!isCollapsed}
                    style={{
                      display: "flex",
                      width: "100%",
                      alignItems: "center",
                      gap: 8,
                      padding: "10px 14px",
                      background: "var(--surface-2)",
                      border: 0,
                      borderBottom: isCollapsed ? "none" : "1px solid var(--border)",
                      cursor: "pointer",
                      font: "inherit",
                      textAlign: "left",
                    }}
                  >
                    <span aria-hidden style={{ color: "var(--muted)", width: 12 }}>
                      {isCollapsed ? "▸" : "▾"}
                    </span>
                    <span style={{ fontWeight: 600 }}>{label}</span>
                    <span className="muted" style={{ fontSize: 12 }}>· {svcs.length}</span>
                  </button>
                  {!isCollapsed && (
                    <ServicesTable
                      services={svcs}
                      onTagClick={toggleSlug}
                      activeTagIds={activeTagIds}
                      showIntegrations={groupBy !== "integration"}
                    />
                  )}
                </div>
              );
            })}
          </div>
        ) : (
          <div className="card">
            <ServicesTable
              services={visibleServices}
              onTagClick={toggleSlug}
              activeTagIds={activeTagIds}
            />
          </div>
        )
      )}
    </div>
  );
}

function Tile({
  label,
  value,
  tone,
  onClick,
  title,
}: {
  label: string;
  value: string;
  tone: "ok" | "errors" | "neutral";
  onClick?: () => void;
  title?: string;
}) {
  return (
    <div
      className={`tile tile--${tone}`}
      title={title}
      onClick={onClick}
      // Keep the same look as the static tiles; just make it actionable when a
      // handler is supplied (keyboard-accessible too).
      role={onClick ? "button" : undefined}
      tabIndex={onClick ? 0 : undefined}
      onKeyDown={
        onClick
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onClick();
              }
            }
          : undefined
      }
      style={onClick ? { cursor: "pointer" } : undefined}
    >
      <div className="tile__value">{value}</div>
      <div className="tile__label">{label}</div>
    </div>
  );
}
