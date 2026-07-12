// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import SearchableSelect from "../components/SearchableSelect";
import type { Integration, MetadataField, Tag } from "../api/types";
import { SortableTh } from "../components/primitives";
import ColumnPicker, { type ColumnDef } from "../components/integrations/ColumnPicker";
import IntegrationFilterBar, {
  matchesFilter,
  parseFilters,
  serializeFilters,
  type IntegrationFilter,
} from "../components/integrations/IntegrationFilterBar";
import TagChip from "../components/tags/TagChip";
import { formatNumber, formatRelative, statusLabel } from "../lib/format";
import { useCurrentUser } from "../lib/useCurrentUser";
import { useAccess } from "../lib/useAccess";
import { usePageTitle } from "../lib/usePageTitle";
import { useTableSort } from "../lib/useTableSort";
import { useTimeWindow } from "../lib/useTimeWindow";

const INTEGRATION_STATUS_RANK: Record<string, number> = {
  unhealthy: 4,
  errors: 3,
  ok: 2,
  quiet: 1,
};

// Persists the user's visible-column choice across sessions and fresh
// navigations to /integrations (where there's no ?cols= query string).
// Mirrors the useTheme / Health last-dashboard localStorage pattern.
const COLS_STORAGE_KEY = "im.integrations.cols";
function readStoredCols(): string | null {
  try {
    return window.localStorage.getItem(COLS_STORAGE_KEY);
  } catch {
    return null; // private mode / storage disabled
  }
}

type IntegrationSortKey =
  | "name"
  | "slug"
  | "description"
  | "tags"
  | "service_count"
  | "trace_count"
  | "error_trace_count"
  | "status"
  | "updated_at"
  // Dynamic metadata column keys, encoded as "meta:<field_key>". The
  // useTableSort lookup is a Record, so we widen via a templated
  // string literal type.
  | `meta:${string}`;

// Multi-tag filtering uses AND semantics — selecting "prod" and "hr"
// narrows the table to integrations carrying both tags. That matches
// how teams typically use facets (each tag is an axis, intersect to
// drill down). If we ever need OR, the right place to add it is a
// small toggle near the chip strip rather than a separate page.

export default function Integrations() {
  usePageTitle("Integrations");
  const [windowVal] = useTimeWindow();
  const { can } = useCurrentUser();
  const access = useAccess();
  // Group-editors may create integrations (scoped server-side by
  // matcher containment), so creation follows write-anywhere.
  const canCreate = can("integration.write") || access.writeAnywhere;
  const [items, setItems] = useState<Integration[] | null>(null);
  const [allTags, setAllTags] = useState<Tag[]>([]);
  // User-defined metadata fields that apply to integrations — one
  // column is rendered per field. The empty default keeps existing
  // behaviour if the backend isn't yet returning the field list.
  const [metadataFields, setMetadataFields] = useState<MetadataField[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  // URL is the source of truth for the active filter. That makes the
  // back button work, makes the filter shareable as a link, and lets
  // the page survive a hard reload without re-clicking chips. Slugs
  // (not ids) are used because they're human-readable in the URL and
  // stable across renames.
  const [searchParams, setSearchParams] = useSearchParams();
  const activeSlugs = useMemo<string[]>(() => {
    const raw = searchParams.get("tags");
    if (!raw) return [];
    return raw
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
  }, [searchParams]);

  const setActiveSlugs = (next: string[]) => {
    const params = new URLSearchParams(searchParams);
    if (next.length === 0) {
      params.delete("tags");
    } else {
      params.set("tags", next.join(","));
    }
    setSearchParams(params, { replace: true });
  };

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
      .listIntegrations(windowVal)
      .then((d) => {
        setItems(d.integrations ?? []);
        setMetadataFields(d.metadata_fields ?? []);
      })
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
    api
      .listTags()
      .then((d) => setAllTags(d.tags ?? []))
      .catch(() => setAllTags([]));
  };

  useEffect(refresh, [windowVal]);

  // Resolve the active slugs against the loaded vocabulary. Unknown
  // slugs (e.g. a tag that was deleted after a link was copied) are
  // dropped silently — we don't want to filter on a slug nobody has.
  const activeTags = useMemo(
    () => allTags.filter((t) => activeSlugs.includes(t.slug)),
    [allTags, activeSlugs],
  );
  const activeTagIds = useMemo(
    () => new Set(activeTags.map((t) => t.id)),
    [activeTags],
  );

  // Filter rows live in the URL as ?filter=field:op:value,field:op:value
  // so the whole query is shareable. parseFilters is lenient — malformed
  // chunks are dropped.
  const filters = useMemo<IntegrationFilter[]>(
    () => parseFilters(searchParams.get("filter")),
    [searchParams],
  );
  const setFilters = (next: IntegrationFilter[]) => {
    const params = new URLSearchParams(searchParams);
    const enc = serializeFilters(next);
    if (enc) params.set("filter", enc);
    else params.delete("filter");
    setSearchParams(params, { replace: true });
  };

  // Visible columns: ?cols=name,description,... takes precedence so a
  // shared link / bookmark still pins a specific view, falling back to
  // the user's persisted choice in localStorage. Absent in both = "all"
  // so we don't hide things on first visit. localStorage is read inside
  // the memo (not tracked by React), which is fine because the only
  // writer is setVisibleCols and it always bumps searchParams too,
  // re-running this memo.
  const visibleCols = useMemo<Set<string> | null>(() => {
    const raw = searchParams.get("cols") ?? readStoredCols();
    return raw ? new Set(raw.split(",").filter(Boolean)) : null;
  }, [searchParams]);
  const isColVisible = (id: string) => (visibleCols ? visibleCols.has(id) : true);
  const setVisibleCols = (next: Set<string>) => {
    const raw = next.size === 0 ? "" : [...next].join(",");
    // Persist as this user's default, then reflect into the URL so the
    // current view stays shareable. Persisting survives a fresh nav to
    // /integrations (no query string) — the memo above falls back to it.
    try {
      window.localStorage.setItem(COLS_STORAGE_KEY, raw);
    } catch {
      /* private mode — the URL still carries it for this session */
    }
    const params = new URLSearchParams(searchParams);
    params.set("cols", raw);
    setSearchParams(params, { replace: true });
  };

  // Pull the comparable cell value out of an integration for filtering.
  const cellValueFor = (i: Integration, field: string): string | number | null => {
    switch (field) {
      case "name": return i.name;
      case "description": return i.description ?? "";
      case "slug": return i.slug;
      case "status": return i.status ?? "";
      default:
        if (field.startsWith("meta:")) return i.metadata_values?.[field.slice(5)] ?? "";
        return "";
    }
  };

  // Multi-level grouping by metadata fields: ?group=country,business-unit
  // nests level 2 inside level 1. Groups aggregate traffic so the list
  // answers "where is the most traffic" per country / business unit / …
  const groupKeys = useMemo(() => {
    const raw = (searchParams.get("group") ?? "").split(",").filter(Boolean);
    return raw.filter((k) => metadataFields.some((f) => f.key === k)).slice(0, 2);
  }, [searchParams, metadataFields]);
  const setGroupKeys = (keys: string[]) => {
    const params = new URLSearchParams(searchParams);
    const clean = keys.filter(Boolean);
    if (clean.length > 0) params.set("group", clean.join(","));
    else params.delete("group");
    setSearchParams(params, { replace: true });
  };
  const fieldLabelFor = (key: string) => metadataFields.find((f) => f.key === key)?.label ?? key;

  // Health-status filter from the URL (?status=unhealthy), set by the
  // dashboard KPI drill-in. "unhealthy" spans both problem states —
  // errors and unhealthy — to match the dashboard's unhealthy count.
  const statusFilter = searchParams.get("status") ?? "";
  const clearStatus = () => {
    const p = new URLSearchParams(searchParams);
    p.delete("status");
    setSearchParams(p, { replace: true });
  };

  // AND filter: every active tag must be present on the integration,
  // every filter row must hold.
  const visibleItems = useMemo(() => {
    const base = items ?? [];
    return base.filter((i) => {
      // Health-status filter (from the dashboard KPI).
      if (statusFilter) {
        const s = i.status ?? "";
        const hit = statusFilter === "unhealthy" ? s === "unhealthy" || s === "errors" : s === statusFilter;
        if (!hit) return false;
      }
      // Tag filter (existing chip-strip semantics).
      if (activeTagIds.size > 0) {
        const ids = new Set((i.tags ?? []).map((t) => t.id));
        for (const need of activeTagIds) {
          if (!ids.has(need)) return false;
        }
      }
      // Field filters.
      for (const f of filters) {
        if (!matchesFilter(f, cellValueFor(i, f.field))) return false;
      }
      return true;
    });
  }, [items, activeTagIds, filters, statusFilter]);

  const totalCount = items?.length ?? 0;
  const visibleCount = visibleItems.length;
  const filterActive = activeTagIds.size > 0 || filters.length > 0;

  // Distinct values seen for each filterable field across the loaded
  // integrations. Used to back the "equals" value SearchableSelect so
  // the user picks from what actually exists. Recomputed when items
  // change; sets are kept small (one per field, deduplicated, sorted).
  const distinctValuesByField = useMemo<Record<string, string[]>>(() => {
    const sets = new Map<string, Set<string>>();
    const push = (field: string, value: string) => {
      const v = (value ?? "").trim();
      if (v === "") return;
      let set = sets.get(field);
      if (!set) {
        set = new Set();
        sets.set(field, set);
      }
      set.add(v);
    };
    for (const i of items ?? []) {
      push("name", i.name);
      if (i.description) push("description", i.description);
      if (i.slug) push("slug", i.slug);
      for (const [k, v] of Object.entries(i.metadata_values ?? {})) {
        push(`meta:${k}`, v);
      }
    }
    const out: Record<string, string[]> = {};
    for (const [field, set] of sets) {
      out[field] = [...set].sort((a, b) => a.localeCompare(b));
    }
    return out;
  }, [items]);

  // Catalogue of all columns the user can toggle in the picker. Order
  // here is the order the table headers render in. Metadata fields
  // come after the operational columns by default.
  const columnDefs: ColumnDef[] = useMemo(() => {
    const base: ColumnDef[] = [
      { id: "name", label: "Name", group: "Identity" },
      { id: "description", label: "Description", group: "Identity" },
      { id: "slug", label: "Slug", group: "Identity" },
      { id: "tags", label: "Tags", group: "Identity" },
      { id: "service_count", label: "Services", group: "Operational" },
      { id: "trace_count", label: "Traces", group: "Operational" },
      { id: "error_trace_count", label: "Errors", group: "Operational" },
      { id: "status", label: "Status", group: "Operational" },
      { id: "updated_at", label: "Updated", group: "Operational" },
    ];
    for (const f of metadataFields) {
      base.push({ id: `meta:${f.key}`, label: f.label, group: "Metadata" });
    }
    return base;
  }, [metadataFields]);

  // Sort runs after the tag filter so users can sort within the
  // current selection. Counts may be undefined on rows where the
  // backend didn't compute them; the comparator pushes those to the
  // bottom either way.
  // Build the sort lookup with one comparator per static column plus one
  // per metadata field. Metadata comparators key off the value-by-key
  // map so the column sorts alphabetically (booleans collate as the
  // literal "true"/"false" — good enough for an inventory view).
  const sortLookup = useMemo(() => {
    const lookup: Record<string, (i: Integration) => string | number | null> = {
      name: (i) => i.name,
      slug: (i) => i.slug,
      description: (i) => i.description ?? "",
      tags: (i) =>
        (i.tags?.length ?? 0) * 1000 +
        (i.tags?.[0]?.name?.charCodeAt(0) ?? 0),
      service_count: (i) => i.service_count ?? null,
      trace_count: (i) => i.trace_count ?? null,
      error_trace_count: (i) => i.error_trace_count ?? null,
      status: (i) => (i.status ? INTEGRATION_STATUS_RANK[i.status] ?? 0 : null),
      updated_at: (i) => i.updated_at,
    };
    for (const f of metadataFields) {
      lookup[`meta:${f.key}`] = (i) => i.metadata_values?.[f.key] ?? "";
    }
    return lookup;
  }, [metadataFields]);

  const { sortedRows, sort, toggleSort } = useTableSort<Integration, IntegrationSortKey>(
    visibleItems,
    sortLookup as Record<IntegrationSortKey, (i: Integration) => string | number | null>,
  );

  interface IntegrationGroup {
    value: string;
    rows: Integration[];
    traces: number;
    errors: number;
    children: IntegrationGroup[] | null;
  }
  type RenderEntry =
    | { kind: "header"; level: number; fieldKey: string; group: IntegrationGroup }
    | { kind: "row"; i: Integration };

  // Group rows by the selected metadata keys, biggest traffic first at
  // every level — the in-group row order keeps the table's sort.
  const renderEntries = useMemo<RenderEntry[]>(() => {
    if (groupKeys.length === 0) return sortedRows.map((i) => ({ kind: "row", i }));
    const build = (rows: Integration[], keys: string[]): IntegrationGroup[] => {
      const buckets = new Map<string, Integration[]>();
      for (const r of rows) {
        const v = r.metadata_values?.[keys[0]] ?? "";
        const list = buckets.get(v);
        if (list) list.push(r);
        else buckets.set(v, [r]);
      }
      const groups = [...buckets.entries()].map(([value, rws]) => ({
        value,
        rows: rws,
        traces: rws.reduce((sum, x) => sum + (x.trace_count ?? 0), 0),
        errors: rws.reduce((sum, x) => sum + (x.error_trace_count ?? 0), 0),
        children: keys.length > 1 ? build(rws, keys.slice(1)) : null,
      }));
      // Traffic first; on ties, real values before the "not set" bucket.
      groups.sort(
        (a, b) =>
          b.traces - a.traces ||
          Number(a.value === "") - Number(b.value === "") ||
          a.value.localeCompare(b.value),
      );
      return groups;
    };
    const out: RenderEntry[] = [];
    for (const g of build(sortedRows, groupKeys)) {
      out.push({ kind: "header", level: 0, fieldKey: groupKeys[0], group: g });
      if (g.children) {
        for (const c of g.children) {
          out.push({ kind: "header", level: 1, fieldKey: groupKeys[1], group: c });
          for (const i of c.rows) out.push({ kind: "row", i });
        }
      } else {
        for (const i of g.rows) out.push({ kind: "row", i });
      }
    }
    return out;
  }, [sortedRows, groupKeys]);

  const renderGroupHeader = (entry: Extract<RenderEntry, { kind: "header" }>) => {
    const { level, fieldKey, group } = entry;
    return (
      <tr
        key={`g${level}-${fieldKey}-${group.value}`}
        className="integration-group-row"
        style={{ background: level === 0 ? "var(--surface-3)" : "var(--surface)", borderTop: "1px solid var(--border)" }}
      >
        <td colSpan={99} style={{ paddingLeft: level === 0 ? 12 : 28, padding: "7px 12px", paddingInlineStart: level === 0 ? 12 : 28 }}>
          <span className="muted" style={{ fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5 }}>
            {fieldLabelFor(fieldKey)}:{" "}
          </span>
          <span style={{ fontWeight: 600, fontSize: 13 }}>
            {group.value || <span className="muted">not set</span>}
          </span>
          <span className="muted" style={{ fontSize: 12, marginLeft: 10 }}>
            {group.rows.length} integration{group.rows.length === 1 ? "" : "s"} ·{" "}
            {formatNumber(group.traces)} traces
            {group.errors > 0 && (
              <>
                {" "}· <span style={{ color: "var(--err)" }}>{formatNumber(group.errors)} errors</span>
              </>
            )}
          </span>
        </td>
      </tr>
    );
  };

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Integrations</h1>
          <p className="page__subtitle">
            Group your services into the integrations they belong to. Anything matching
            the rules below shows up under that integration on the Services view.
          </p>
        </div>
        <div className="toolbar">
          <ColumnPicker
            columns={columnDefs}
            visible={visibleCols ?? new Set(columnDefs.map((c) => c.id))}
            onChange={setVisibleCols}
          />
          {canCreate ? (
            <Link className="btn btn--primary" to="/integrations/new">
              New integration
            </Link>
          ) : (
            <button
              type="button"
              className="btn btn--primary"
              disabled
              title="Your role doesn't allow creating integrations"
            >
              New integration
            </button>
          )}
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
                    // Dim un-selected chips when at least one filter is
                    // active so the focus rests on the selection.
                    opacity: filterActive && !active ? 0.55 : 1,
                    transition: "opacity 0.12s ease",
                  }}
                >
                  <span
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      borderRadius: 999,
                      // A 2px ring on active chips reads as "selected"
                      // against the chip's own background color.
                      boxShadow: active ? "0 0 0 2px var(--ink)" : "none",
                    }}
                  >
                    <TagChip tag={t} />
                  </span>
                </button>
              );
            })}
          </div>
          {filterActive && (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 8,
                marginLeft: "auto",
              }}
            >
              <span className="muted" style={{ fontSize: 12 }}>
                {visibleCount} of {totalCount} match{" "}
                {activeTags.length === 1
                  ? activeTags[0].name
                  : `all of ${activeTags.map((t) => t.name).join(", ")}`}
              </span>
              <button
                type="button"
                className="btn btn--link"
                onClick={() => setActiveSlugs([])}
              >
                Clear
              </button>
            </div>
          )}
        </div>
      )}

      <div style={{ margin: "0 0 16px" }}>
        <IntegrationFilterBar
          filters={filters}
          onChange={setFilters}
          metadataFields={metadataFields}
          distinctValues={distinctValuesByField}
        />
      </div>

      {metadataFields.length > 0 && (
        <div style={{ display: "flex", alignItems: "center", gap: 8, margin: "0 0 16px", flexWrap: "wrap" }}>
          <span className="muted" style={{ fontSize: 12, textTransform: "uppercase", letterSpacing: 0.5 }}>
            group by
          </span>
          <SearchableSelect
            value={groupKeys[0] ?? ""}
            allLabel="No grouping"
            options={metadataFields.map((f) => f.key)}
            labelFor={fieldLabelFor}
            onChange={(k) => setGroupKeys(k ? [k, ...(groupKeys[1] && groupKeys[1] !== k ? [groupKeys[1]] : [])] : [])}
          />
          {groupKeys[0] && (
            <>
              <span className="muted" style={{ fontSize: 12 }}>then by</span>
              <SearchableSelect
                value={groupKeys[1] ?? ""}
                allLabel="—"
                options={metadataFields.map((f) => f.key).filter((k) => k !== groupKeys[0])}
                labelFor={fieldLabelFor}
                onChange={(k) => setGroupKeys(k ? [groupKeys[0], k] : [groupKeys[0]])}
              />
            </>
          )}
        </div>
      )}

      {statusFilter && (
        <div style={{ display: "flex", alignItems: "center", gap: 8, margin: "0 0 12px" }}>
          <span className="chip">Showing {statusFilter === "unhealthy" ? "unhealthy" : statusFilter} integrations</span>
          <button type="button" className="btn btn--link" onClick={clearStatus}>Clear filter</button>
        </div>
      )}

      {error && <div className="alert alert--error">Failed to load: {error}</div>}

      {!error && items && items.length === 0 && !loading && (
        <div className="placeholder">
          No integrations yet. Click <strong>New integration</strong> to define one —
          for example, group everything with a name starting with <code>order-</code>
          into "Order Sync".
        </div>
      )}

      {items && items.length > 0 && visibleItems.length === 0 && statusFilter && (
        <div className="placeholder">
          No integrations are currently {statusFilter === "unhealthy" ? "unhealthy" : statusFilter}.{" "}
          <button type="button" className="btn btn--link" onClick={clearStatus}>Show all</button>
        </div>
      )}

      {items && items.length > 0 && visibleItems.length === 0 && !statusFilter && (
        <div className="placeholder">
          No integrations carry{" "}
          {activeTags.length === 1 ? (
            <>the tag <strong>{activeTags[0].name}</strong></>
          ) : (
            <>all of <strong>{activeTags.map((t) => t.name).join(", ")}</strong></>
          )}
          . Loosen the filter, or attach a tag from an integration's detail page.
        </div>
      )}

      {items && visibleItems.length > 0 && (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                {isColVisible("name") && <SortableTh sortKey="name" state={sort} onSort={toggleSort}>Name</SortableTh>}
                {isColVisible("description") && <SortableTh sortKey="description" state={sort} onSort={toggleSort}>Description</SortableTh>}
                {isColVisible("slug") && <SortableTh sortKey="slug" state={sort} onSort={toggleSort}>Slug</SortableTh>}
                {isColVisible("tags") && <SortableTh sortKey="tags" state={sort} onSort={toggleSort}>Tags</SortableTh>}
                {isColVisible("service_count") && <SortableTh sortKey="service_count" state={sort} onSort={toggleSort} className="num">Services</SortableTh>}
                {isColVisible("trace_count") && <SortableTh sortKey="trace_count" state={sort} onSort={toggleSort} className="num">Traces</SortableTh>}
                {isColVisible("error_trace_count") && <SortableTh sortKey="error_trace_count" state={sort} onSort={toggleSort} className="num">Errors</SortableTh>}
                {isColVisible("status") && <SortableTh sortKey="status" state={sort} onSort={toggleSort}>Status</SortableTh>}
                {isColVisible("updated_at") && <SortableTh sortKey="updated_at" state={sort} onSort={toggleSort}>Updated</SortableTh>}
                {metadataFields.map((f) =>
                  isColVisible(`meta:${f.key}`) ? (
                    <SortableTh key={f.id} sortKey={`meta:${f.key}` as IntegrationSortKey} state={sort} onSort={toggleSort}>
                      <span title={f.description || undefined}>{f.label}</span>
                    </SortableTh>
                  ) : null,
                )}
              </tr>
            </thead>
            <tbody>
              {renderEntries.map((entry) => {
                if (entry.kind === "header") return renderGroupHeader(entry);
                const i = entry.i;
                return (
                <tr key={i.id}>
                  {isColVisible("name") && (
                    <td>
                      <Link to={`/integrations/${i.id}`}>{i.name}</Link>
                    </td>
                  )}
                  {isColVisible("description") && (
                    <td>
                      {i.description ? i.description : <span className="muted">—</span>}
                    </td>
                  )}
                  {isColVisible("slug") && <td className="muted mono">{i.slug}</td>}
                  {isColVisible("tags") && (
                    <td>
                      {i.tags && i.tags.length > 0 ? (
                        <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
                          {i.tags.map((t) => (
                            <button
                              key={t.id}
                              type="button"
                              onClick={(e) => {
                                e.preventDefault();
                                toggleSlug(t.slug);
                              }}
                              title={
                                activeTagIds.has(t.id)
                                  ? `Remove ${t.name} from filter`
                                  : `Filter by ${t.name}`
                              }
                              style={{
                                background: "transparent",
                                border: 0,
                                padding: 0,
                                cursor: "pointer",
                              }}
                            >
                              <TagChip tag={t} />
                            </button>
                          ))}
                        </div>
                      ) : (
                        <span className="muted">—</span>
                      )}
                    </td>
                  )}
                  {isColVisible("service_count") && (
                    <td className="num">
                      {typeof i.service_count === "number" ? (
                        <>
                          {formatNumber(i.service_count)}
                          {i.unhealthy_count ? (
                            <span className="muted">
                              {" "}
                              · {i.unhealthy_count} unhealthy
                            </span>
                          ) : null}
                        </>
                      ) : (
                        <span className="muted">—</span>
                      )}
                    </td>
                  )}
                  {isColVisible("trace_count") && (
                    <td className="num">
                      {typeof i.trace_count === "number" ? (
                        formatNumber(i.trace_count)
                      ) : (
                        <span className="muted">—</span>
                      )}
                    </td>
                  )}
                  {isColVisible("error_trace_count") && (
                    <td className="num">
                      {typeof i.error_trace_count === "number" && i.error_trace_count > 0 ? (
                        <span className="pill pill--errors">{formatNumber(i.error_trace_count)}</span>
                      ) : (
                        <span className="muted">—</span>
                      )}
                    </td>
                  )}
                  {isColVisible("status") && (
                    <td>
                      {i.status ? (
                        <span className={`pill pill--${i.status}`}>{statusLabel(i.status)}</span>
                      ) : (
                        <span className="muted">—</span>
                      )}
                    </td>
                  )}
                  {isColVisible("updated_at") && (
                    <td className="muted">{formatRelative(i.updated_at)}</td>
                  )}
                  {metadataFields.map((f) =>
                    isColVisible(`meta:${f.key}`) ? (
                      <td key={f.id}>{renderMetadataCell(f, i.metadata_values?.[f.key])}</td>
                    ) : null,
                  )}
                </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// renderMetadataCell formats a saved metadata value against its field's
// declared type. Empty / missing -> em-dash; booleans render as ✓/✗ for
// quick scanning across rows; numbers monospaced.
function renderMetadataCell(field: MetadataField, raw: string | undefined) {
  if (raw === undefined || raw === "") {
    return <span className="muted">—</span>;
  }
  if (field.type === "boolean") {
    return raw === "true" ? "✓ yes" : "✗ no";
  }
  if (field.type === "number") {
    return <span className="mono">{raw}</span>;
  }
  return raw;
}
