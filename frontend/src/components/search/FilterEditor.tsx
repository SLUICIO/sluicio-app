// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// FilterEditor — the friendly sentence-style filter UI from the
// Sluicio handoff (Message search variant A). Each filter is a row
// of three pills (field / operator / value); each pill is clickable
// and opens a small inline picker. A plain-English summary sentence
// regenerates live as filters change.

import { useEffect, useMemo, useRef, useState } from "react";
import type { MessageAttributeKey, MessageFieldDescriptor } from "../../api/types";

export type Field = "payload" | "time" | "integration" | "status" | "service" | "errorType" | "traceId" | "spanId";
export type Operator = "equals" | "contains" | "is" | "in" | "matches";

// The fields the add-field picker offers by default (global Message Search).
// Scoped surfaces can pass a narrower `fields` prop — e.g. the integration
// Messages tab drops "integration" since the page is already one integration.
const DEFAULT_PICKER_FIELDS: Field[] = ["payload", "integration", "status", "service", "errorType", "traceId", "spanId"];

export interface Filter {
  id: string;
  field: Field;
  fieldPath?: string; // for "payload" rows, e.g. "orderId"
  op: Operator;
  value: string;
  removable: boolean;
  // locked: the row's value is fixed by the page's scope. Renders as
  // a read-only pill with a lock icon and "locked · scope of this
  // page" instead of the remove affordance. Clicking does nothing.
  locked?: boolean;
  // optional: the user has muted the row but kept it visible as a
  // reminder of what filters are available. Renders dashed/faded and
  // the search engine treats it as a no-op.
  optional?: boolean;
}

interface Props {
  filters: Filter[];
  onChange: (filters: Filter[]) => void;
  knownIntegrations?: { id: string; name: string }[];
  recentValues?: string[];
  // The /messages/fields catalog for the current window: drives the
  // by-name service picker and the list of live attributes that can be
  // filtered on.
  fieldCatalog?: MessageFieldDescriptor[];
  // Which fields the add-field picker offers. Defaults to the full set;
  // scoped pages pass a subset (the integration Messages tab omits
  // "integration").
  fields?: Field[];
  // Explicit payload attribute keys for the field typeahead. When set, this
  // wins over fieldCatalog — the integration Messages tab passes the keys
  // scoped to that integration's traffic.
  attributeKeys?: MessageAttributeKey[];
  // When set, the payload value picker becomes a typeahead: it calls this to
  // load the top-N observed values for the chosen attribute key (e.g.
  // integration-scoped). A free-text fallback always remains for exact values
  // outside the top-N. Absent → the legacy text + recent-values picker.
  fetchAttrValues?: (key: string) => Promise<AttrValueSuggestion[]>;
}

export interface AttrValueSuggestion {
  value: string;
  count: number;
}

const FIELD_LABELS: Record<Field, string> = {
  payload: "payload",
  time: "time",
  integration: "integration",
  status: "status",
  service: "service",
  errorType: "error type",
  traceId: "trace ID",
  spanId: "span ID",
};

const OP_LABELS: Record<Operator, string> = {
  equals: "equals",
  contains: "contains",
  is: "is",
  in: "in",
  matches: "matches",
};

export default function FilterEditor({
  filters,
  onChange,
  knownIntegrations,
  recentValues = [],
  fieldCatalog,
  fields = DEFAULT_PICKER_FIELDS,
  attributeKeys: attributeKeysProp,
  fetchAttrValues,
}: Props) {
  const summary = buildSummary(filters);

  // Service names + live attribute keys pulled out of the field catalog
  // for the value/field pickers (filter by service NAME, never id; offer
  // every attribute present in the window as a filterable field).
  const knownServices = useMemo(
    () => fieldCatalog?.find((f) => f.field === "service")?.enumValues ?? [],
    [fieldCatalog],
  );
  // Explicit keys (integration-scoped) win over the catalog's org-wide list.
  const attributeKeys = useMemo(
    () => attributeKeysProp ?? fieldCatalog?.find((f) => f.field === "payload")?.attributeKeys ?? [],
    [attributeKeysProp, fieldCatalog],
  );

  const addFilter = () => {
    // No seeded field path: the row defaults to a bare "payload" and the
    // field pill opens straight onto the attribute typeahead, so we never
    // pin a made-up example like "orderId".
    const next: Filter = {
      id: crypto.randomUUID(),
      field: "payload",
      fieldPath: "",
      op: "equals",
      value: "",
      removable: true,
    };
    onChange([...filters, next]);
  };

  const updateFilter = (id: string, patch: Partial<Filter>) => {
    onChange(filters.map((f) => (f.id === id ? { ...f, ...patch } : f)));
  };

  const removeFilter = (id: string) => {
    onChange(filters.filter((f) => f.id !== id));
  };

  const hasLocked = filters.some((f) => f.locked);

  return (
    <div
      className="rounded-xl border bg-surface-2 p-4 shadow-sm"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="mb-3 flex items-baseline justify-between">
        <h3 className="text-base font-semibold">Filters</h3>
        <span className="text-xs text-muted">
          click any pill to change
          {hasLocked ? " · scope is locked to this page" : " · sentence updates live"}
        </span>
      </div>

      {/* Plain-English summary */}
      <div
        className="mb-3 rounded-md border-2 border-dashed px-3 py-2 text-sm leading-relaxed"
        style={{ borderColor: "var(--border)", background: "var(--surface-3)" }}
      >
        {summary}
      </div>

      {/* Filter rows */}
      <div className="space-y-2">
        {filters.map((f, i) => (
          <FilterRow
            key={f.id}
            conjunction={i === 0 ? "where" : "and"}
            filter={f}
            onUpdate={(patch) => updateFilter(f.id, patch)}
            onRemove={() => removeFilter(f.id)}
            recentValues={recentValues}
            knownIntegrations={knownIntegrations}
            knownServices={knownServices}
            attributeKeys={attributeKeys}
            pickerFields={fields}
            fetchAttrValues={fetchAttrValues}
          />
        ))}
      </div>

      <div className="mt-3 flex items-center gap-3 text-sm">
        <button
          type="button"
          onClick={addFilter}
          className="underline-offset-2 hover:underline"
          style={{ color: "var(--primary)" }}
        >
          + add a filter
        </button>
        <button
          type="button"
          disabled={filters.length === 0}
          className="text-muted underline-offset-2 hover:underline disabled:cursor-not-allowed"
          title='Coming soon'
        >
          + "OR" group
        </button>
        <div className="ml-auto text-xs text-muted">
          group by: none ▾ · sort: time ↓
        </div>
      </div>
    </div>
  );
}

interface RowProps {
  conjunction: string;
  filter: Filter;
  onUpdate: (patch: Partial<Filter>) => void;
  onRemove: () => void;
  recentValues: string[];
  knownIntegrations?: { id: string; name: string }[];
  knownServices?: string[];
  attributeKeys?: MessageAttributeKey[];
  pickerFields: Field[];
  fetchAttrValues?: (key: string) => Promise<AttrValueSuggestion[]>;
}

function FilterRow({
  conjunction,
  filter,
  onUpdate,
  onRemove,
  recentValues,
  knownIntegrations,
  knownServices,
  attributeKeys,
  pickerFields,
  fetchAttrValues,
}: RowProps) {
  const fieldLabel =
    filter.field === "payload" && filter.fieldPath
      ? `payload.${filter.fieldPath}`
      : FIELD_LABELS[filter.field];

  const locked = !!filter.locked;
  const optional = !!filter.optional;

  // A locked row's three pills are read-only — only the value pill
  // shows the lock icon, but the field+op pills should also be frozen
  // since they describe the locked dimension. Optional rows render
  // faded but stay interactive.
  return (
    <div
      className="flex items-center gap-2 text-sm"
      aria-disabled={locked || undefined}
      aria-description={
        locked
          ? "locked to this page's scope"
          : optional
            ? "optional · ignored unless a value is set"
            : undefined
      }
      style={{ opacity: optional ? 0.55 : 1 }}
    >
      <span className="w-14 text-xs text-muted">
        {conjunction}
        {optional && (
          <span className="ml-1 text-[10px] uppercase">opt.</span>
        )}
      </span>
      <Pill
        kind="field"
        label={fieldLabel}
        locked={locked}
        dashed={optional}
        editor={({ close }) => (
          <FieldPicker
            current={filter.field}
            fieldPath={filter.fieldPath}
            attributeKeys={attributeKeys}
            fields={pickerFields}
            onPick={(f, path) => {
              // trace/span id only support exact match — pin the op so the
              // pill never shows a stale operator the picker won't offer.
              const idField = f === "traceId" || f === "spanId";
              onUpdate({ field: f, fieldPath: path, ...(idField ? { op: "is" as Operator } : {}) });
              close();
            }}
          />
        )}
      />
      <Pill
        kind="op"
        label={OP_LABELS[filter.op]}
        locked={locked}
        dashed={optional}
        editor={({ close }) => (
          <OpPicker
            current={filter.op}
            field={filter.field}
            onPick={(op) => {
              onUpdate({ op });
              close();
            }}
          />
        )}
      />
      <Pill
        kind="value"
        label={filter.value || "—"}
        accent={!locked}
        locked={locked}
        dashed={optional}
        showLockIcon={locked}
        editor={({ close }) => (
          <ValuePicker
            current={filter.value}
            field={filter.field}
            fieldPath={filter.fieldPath}
            recentValues={recentValues}
            knownIntegrations={knownIntegrations}
            knownServices={knownServices}
            fetchAttrValues={fetchAttrValues}
            onPick={(v) => {
              onUpdate({ value: v });
              close();
            }}
          />
        )}
      />
      {locked ? (
        <span
          className="ml-auto text-xs text-muted"
          title="This filter is part of the page's scope and can't be removed here."
        >
          locked · scope of this page
        </span>
      ) : (
        filter.removable && (
          <button
            type="button"
            onClick={onRemove}
            className="ml-auto text-xs text-muted hover:text-foreground"
            aria-label="Remove filter"
          >
            ✕ remove
          </button>
        )
      )}
    </div>
  );
}

interface PillProps {
  kind: "field" | "op" | "value";
  label: string;
  accent?: boolean;
  locked?: boolean;
  dashed?: boolean;
  showLockIcon?: boolean;
  editor: (props: { close: () => void }) => React.ReactNode;
}

function Pill({
  kind,
  label,
  accent,
  locked,
  dashed,
  showLockIcon,
  editor,
}: PillProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open || locked) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, locked]);

  // Locked pills get --surface-3 background, solid border (not dashed),
  // no dropdown arrow, default cursor, and clicking does nothing. The
  // tabIndex of -1 keeps keyboard nav from focusing the read-only
  // control.
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => {
          if (locked) return;
          setOpen((o) => !o);
        }}
        tabIndex={locked ? -1 : 0}
        aria-disabled={locked || undefined}
        className="inline-flex items-center gap-1 rounded-full border px-3 py-1 text-sm transition-colors"
        style={{
          borderColor: locked
            ? "var(--border)"
            : accent
              ? "color-mix(in oklab, var(--primary) 35%, transparent)"
              : "var(--border)",
          borderStyle: dashed ? "dashed" : "solid",
          background: locked
            ? "var(--surface-3)"
            : accent
              ? "var(--primary-soft)"
              : "var(--surface-3)",
          color: locked
            ? "var(--ink)"
            : accent
              ? "var(--primary-ink)"
              : "var(--ink)",
          fontFamily: kind === "value" ? "'JetBrains Mono', ui-monospace, monospace" : undefined,
          cursor: locked ? "default" : "pointer",
        }}
      >
        {showLockIcon && (
          <span aria-hidden="true" className="text-[10px] text-muted">
            🔒
          </span>
        )}
        <span>{label}</span>
        {!locked && <span className="text-muted">▾</span>}
      </button>
      {open && !locked && (
        <div
          className="absolute left-0 top-full z-20 mt-1 min-w-[260px] rounded-lg border p-3 shadow"
          style={{ background: "var(--surface-2)", borderColor: "var(--border)" }}
        >
          {editor({ close: () => setOpen(false) })}
        </div>
      )}
    </div>
  );
}

interface FieldPickerProps {
  current: Field;
  fieldPath?: string;
  attributeKeys?: MessageAttributeKey[];
  // The fields offered in this picker (see DEFAULT_PICKER_FIELDS). "time" is
  // never offered: the time range is controlled by the page-wide header
  // selector (useTimeWindow), so the search reuses that rather than a
  // redundant in-filter time row.
  fields: Field[];
  onPick: (f: Field, path?: string) => void;
}

function FieldPicker({ current, fieldPath, attributeKeys, fields, onPick }: FieldPickerProps) {
  const [path, setPath] = useState(fieldPath ?? "");
  const [attrFilter, setAttrFilter] = useState("");
  return (
    <div className="space-y-2 text-sm">
      <div className="text-xs text-muted">Pick a field</div>
      <div className="space-y-1">
        {fields.map((f) => (
          <button
            key={f}
            type="button"
            onClick={() => {
              // Commit payload with whatever path is set (may be empty —
              // the user then picks a key from the typeahead below). No
              // made-up "orderId" default.
              if (f === "payload") onPick(f, path);
              else onPick(f);
            }}
            className="block w-full rounded px-2 py-1 text-left hover:bg-surface-elevated"
            style={{ background: f === current ? "var(--surface-3)" : undefined }}
          >
            {FIELD_LABELS[f]}
          </button>
        ))}
      </div>
      {current === "payload" && (
        <div className="border-t pt-2" style={{ borderColor: "var(--border)" }}>
          <div className="text-xs text-muted">attribute / payload field</div>
          <input
            type="text"
            value={attributeKeys && attributeKeys.length > 0 ? attrFilter : path}
            onChange={(e) =>
              attributeKeys && attributeKeys.length > 0
                ? setAttrFilter(e.target.value)
                : setPath(e.target.value)
            }
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                const v = attributeKeys && attributeKeys.length > 0 ? attrFilter : path;
                if (v.trim()) onPick("payload", v.trim());
              }
            }}
            placeholder={attributeKeys && attributeKeys.length > 0 ? "filter attributes… (or type a custom field)" : "attribute key (e.g. http.route)"}
            className="mt-1 w-full rounded border px-2 py-1 font-mono text-sm"
            style={{
              borderColor: "var(--border)",
              background: "var(--surface-3)",
              color: "var(--ink)",
            }}
          />
          {attributeKeys && attributeKeys.length > 0 && (
            <div className="mt-2 max-h-48 space-y-0.5 overflow-auto">
              {attributeKeys
                .filter((k) => k.key.toLowerCase().includes(attrFilter.trim().toLowerCase()))
                .slice(0, 100)
                .map((k) => (
                  <button
                    key={`${k.source}:${k.key}`}
                    type="button"
                    onClick={() => onPick("payload", k.key)}
                    className="flex w-full items-center justify-between gap-2 rounded px-2 py-1 text-left font-mono text-xs hover:bg-surface-elevated"
                    style={{ background: k.key === fieldPath ? "var(--surface-3)" : undefined }}
                  >
                    <span className="truncate">{k.key}</span>
                    <span className="shrink-0 text-[10px] uppercase text-muted">{k.source}</span>
                  </button>
                ))}
            </div>
          )}
          <button
            type="button"
            onClick={() => {
              const v = attributeKeys && attributeKeys.length > 0 ? attrFilter : path;
              if (v.trim()) onPick("payload", v.trim());
            }}
            className="mt-2 rounded px-2 py-1 text-xs"
            style={{ background: "var(--primary)", color: "var(--on-primary)" }}
          >
            use custom field
          </button>
        </div>
      )}
    </div>
  );
}

interface OpPickerProps {
  current: Operator;
  field: Field;
  onPick: (op: Operator) => void;
}

function OpPicker({ current, field, onPick }: OpPickerProps) {
  const ops: Operator[] =
    field === "time" || field === "traceId" || field === "spanId"
      ? ["is"]
      : field === "status" || field === "integration" || field === "service"
        ? ["is", "in"]
        : ["equals", "contains", "matches", "in"];
  return (
    <div className="space-y-1 text-sm">
      <div className="text-xs text-muted">Operator</div>
      {ops.map((op) => (
        <button
          key={op}
          type="button"
          onClick={() => onPick(op)}
          className="block w-full rounded px-2 py-1 text-left hover:bg-surface-elevated"
          style={{ background: op === current ? "var(--surface-3)" : undefined }}
        >
          {OP_LABELS[op]}
        </button>
      ))}
    </div>
  );
}

interface ValuePickerProps {
  current: string;
  field: Field;
  fieldPath?: string;
  recentValues: string[];
  knownIntegrations?: { id: string; name: string }[];
  knownServices?: string[];
  fetchAttrValues?: (key: string) => Promise<AttrValueSuggestion[]>;
  onPick: (v: string) => void;
}

function ValuePicker({
  current,
  field,
  fieldPath,
  recentValues,
  knownIntegrations,
  knownServices,
  fetchAttrValues,
  onPick,
}: ValuePickerProps) {
  const [draft, setDraft] = useState(current);
  const [svcFilter, setSvcFilter] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  // Payload value typeahead: when the host wired a value fetcher and the row
  // targets a concrete attribute key, offer the top-N observed values (its own
  // component so its hooks stay unconditional) with a free-text fallback.
  if (field === "payload" && fieldPath && fetchAttrValues) {
    return (
      <AttrValuePicker
        attrKey={fieldPath}
        current={current}
        fetchAttrValues={fetchAttrValues}
        onPick={onPick}
      />
    );
  }

  // Field-specific value pickers.
  if (field === "time") {
    const presets = ["last 15 minutes", "last 1 hour", "last 24 hours", "last 7 days"];
    return (
      <div className="space-y-1 text-sm">
        <div className="text-xs text-muted">Time range</div>
        {presets.map((p) => (
          <button
            key={p}
            type="button"
            onClick={() => onPick(p)}
            className="block w-full rounded px-2 py-1 text-left hover:bg-surface-elevated"
            style={{ background: p === current ? "var(--surface-3)" : undefined }}
          >
            {p}
          </button>
        ))}
      </div>
    );
  }

  if (field === "status") {
    const choices = ["any (ok, warn, err)", "ok only", "err only", "warn or err"];
    return (
      <div className="space-y-1 text-sm">
        {choices.map((c) => (
          <button
            key={c}
            type="button"
            onClick={() => onPick(c)}
            className="block w-full rounded px-2 py-1 text-left hover:bg-surface-elevated"
          >
            {c}
          </button>
        ))}
      </div>
    );
  }

  if (field === "integration" && knownIntegrations) {
    return (
      <div className="max-h-64 space-y-1 overflow-auto text-sm">
        <button
          type="button"
          onClick={() => onPick("any")}
          className="block w-full rounded px-2 py-1 text-left hover:bg-surface-elevated"
        >
          any
        </button>
        {knownIntegrations.map((it) => (
          <button
            key={it.id}
            type="button"
            onClick={() => onPick(it.name)}
            className="block w-full rounded px-2 py-1 text-left hover:bg-surface-elevated"
          >
            {it.name}
          </button>
        ))}
      </div>
    );
  }

  // Service is picked by NAME from the catalog — a service id is
  // meaningless to the user. Typeahead-filtered list of service names.
  if (field === "service" && knownServices && knownServices.length > 0) {
    const matches = knownServices.filter((s) =>
      s.toLowerCase().includes(svcFilter.trim().toLowerCase()),
    );
    return (
      <div className="space-y-2 text-sm">
        <div className="text-xs text-muted">Pick a service (by name)</div>
        <input
          ref={inputRef}
          type="text"
          value={svcFilter}
          onChange={(e) => setSvcFilter(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && matches.length > 0) onPick(matches[0]);
          }}
          placeholder="filter services…"
          className="w-full rounded border px-2 py-1 text-sm"
          style={{ borderColor: "var(--primary)", background: "var(--primary-soft)", color: "var(--primary-ink)" }}
        />
        <div className="max-h-56 space-y-0.5 overflow-auto">
          {matches.slice(0, 200).map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => onPick(s)}
              className="block w-full rounded px-2 py-1 text-left hover:bg-surface-elevated"
              style={{ background: s === current ? "var(--surface-3)" : undefined }}
            >
              {s}
            </button>
          ))}
          {matches.length === 0 && (
            <div className="px-2 py-1 text-xs text-muted">no matching service</div>
          )}
        </div>
      </div>
    );
  }

  // Default: text value with recent suggestions
  return (
    <div className="space-y-2 text-sm">
      <div className="text-xs text-muted">Set value</div>
      <input
        ref={inputRef}
        type="text"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") onPick(draft);
        }}
        placeholder="e.g. 1323"
        className="w-full rounded border px-2 py-1 font-mono text-sm"
        style={{
          borderColor: "var(--primary)",
          background: "var(--primary-soft)",
          color: "var(--primary-ink)",
        }}
      />
      <button
        type="button"
        onClick={() => onPick(draft)}
        className="rounded px-2 py-1 text-xs font-medium"
        style={{ background: "var(--primary)", color: "var(--on-primary)" }}
      >
        apply
      </button>
      {recentValues.length > 0 && (
        <div className="border-t pt-2" style={{ borderColor: "var(--border)" }}>
          <div className="mb-1 text-xs text-muted">recent values</div>
          <div className="space-y-0.5">
            {recentValues.slice(0, 5).map((v) => (
              <button
                key={v}
                type="button"
                onClick={() => onPick(v)}
                className="block w-full rounded px-2 py-0.5 text-left font-mono text-xs hover:bg-surface-elevated"
              >
                {v}
              </button>
            ))}
          </div>
        </div>
      )}
      <div className="border-t pt-2 text-xs text-muted" style={{ borderColor: "var(--border)" }}>
        tip: try a list — <code>1323, 1419, 0991</code>
      </div>
    </div>
  );
}

// AttrValuePicker — the payload value typeahead. Loads the top-N observed
// values for one attribute key (via the host's fetcher, e.g. integration-
// scoped) on open, filters them client-side as the user types, and keeps a
// free-text "use exact value" fallback so a high-cardinality value outside the
// top-N is still selectable. Replaces the old static recent-values list.
function AttrValuePicker({
  attrKey,
  current,
  fetchAttrValues,
  onPick,
}: {
  attrKey: string;
  current: string;
  fetchAttrValues: (key: string) => Promise<AttrValueSuggestion[]>;
  onPick: (v: string) => void;
}) {
  const [draft, setDraft] = useState(current);
  const [all, setAll] = useState<AttrValueSuggestion[]>([]);
  const [loading, setLoading] = useState(true);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
    let alive = true;
    setLoading(true);
    fetchAttrValues(attrKey)
      .then((vals) => {
        if (alive) setAll(vals);
      })
      .catch(() => {
        if (alive) setAll([]);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [attrKey, fetchAttrValues]);

  const q = draft.trim().toLowerCase();
  const matches = q ? all.filter((v) => v.value.toLowerCase().includes(q)) : all;
  const exactInList = all.some((v) => v.value === draft.trim());

  return (
    <div className="space-y-2 text-sm">
      <div className="text-xs text-muted">
        Value of <span className="font-mono">{attrKey}</span>
      </div>
      <input
        ref={inputRef}
        type="text"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && draft.trim()) onPick(draft.trim());
        }}
        placeholder="search values… (or type an exact value)"
        className="w-full rounded border px-2 py-1 font-mono text-sm"
        style={{ borderColor: "var(--primary)", background: "var(--primary-soft)", color: "var(--primary-ink)" }}
      />
      <div className="max-h-56 space-y-0.5 overflow-auto">
        {loading && <div className="px-2 py-1 text-xs text-muted">Loading values…</div>}
        {!loading && matches.length === 0 && (
          <div className="px-2 py-1 text-xs text-muted">
            {all.length === 0 ? "No values seen for this attribute in the window." : "No matching value — type it exactly below."}
          </div>
        )}
        {!loading &&
          matches.slice(0, 100).map((v) => (
            <button
              key={v.value}
              type="button"
              onClick={() => onPick(v.value)}
              className="flex w-full items-center justify-between gap-2 rounded px-2 py-1 text-left font-mono text-xs hover:bg-surface-elevated"
              style={{ background: v.value === current ? "var(--surface-3)" : undefined }}
            >
              <span className="truncate">{v.value}</span>
              <span className="shrink-0 text-[10px] text-muted">{v.count.toLocaleString()}</span>
            </button>
          ))}
      </div>
      {draft.trim() && !exactInList && (
        <button
          type="button"
          onClick={() => onPick(draft.trim())}
          className="rounded px-2 py-1 text-xs font-medium"
          style={{ background: "var(--primary)", color: "var(--on-primary)" }}
        >
          use “{draft.trim()}”
        </button>
      )}
    </div>
  );
}

// ── Plain-English summary builder ───────────────────────────────────
// Locked rows are surfaced first — "showing messages in <X> where …" —
// so the user always sees the page's scope before the rest of the
// sentence. Optional rows are stripped out (they don't restrict the
// query).
function buildSummary(filters: Filter[]): React.ReactNode {
  const effective = filters.filter((f) => !f.optional);
  if (effective.length === 0) {
    return (
      <span className="text-muted">
        showing all messages in the selected time range, across any integration, with any status.
      </span>
    );
  }

  // Pull locked filters to the front and render them as "in <value>"
  // phrases. Multiple locks chain with ", " for readability.
  const locked = effective.filter((f) => f.locked);
  const free = effective.filter((f) => !f.locked);

  return (
    <span>
      <span className="text-muted">showing messages </span>
      {locked.map((f, i) => (
        <span key={f.id}>
          {i === 0 ? "in " : <span className="text-muted">, </span>}
          <b>{f.value || "—"}</b>
        </span>
      ))}
      {free.length > 0 && (
        <>
          {locked.length > 0 ? (
            <span className="text-muted"> where </span>
          ) : (
            <span className="text-muted">where </span>
          )}
          {free.map((f, i) => (
            <span key={f.id}>
              {i === 0 ? null : <span className="text-muted">, </span>}
              <b>
                {f.field === "payload" && f.fieldPath
                  ? `payload.${f.fieldPath}`
                  : FIELD_LABELS[f.field]}
              </b>{" "}
              <span className="text-muted">{OP_LABELS[f.op]}</span>{" "}
              <b>{f.value || "—"}</b>
            </span>
          ))}
        </>
      )}
      <span className="text-muted">.</span>
    </span>
  );
}
