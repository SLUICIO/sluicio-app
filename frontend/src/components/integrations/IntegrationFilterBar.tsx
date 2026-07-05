// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationFilterBar — a simple AND-joined filter row builder for the
// integrations list. Each row is { field, op, value }; operators are
// type-aware (text -> equals/contains, boolean -> is true/false, number
// -> = / > / <, status -> is). The whole thing is controlled — state
// lives in the parent (which encodes it in the URL).

import type { MetadataField } from "../../api/types";
import SearchableSelect from "../SearchableSelect";

export type StaticFilterField = "name" | "namespace" | "description" | "slug" | "status" | "system_kind";
export type FilterField = StaticFilterField | `meta:${string}`;

export type FilterOperator =
  | "equals"
  | "contains"
  | "is_empty"
  | "is_set"
  | "is_true"
  | "is_false"
  | "is"
  | "eq"
  | "gt"
  | "lt";

export interface IntegrationFilter {
  id: string;
  field: FilterField;
  op: FilterOperator;
  value: string;
}

export type FieldKind = "text" | "boolean" | "number" | "select" | "status";

export interface FieldSpec {
  field: FilterField;
  label: string;
  kind: FieldKind;
  options?: string[];
}

const STATUS_OPTIONS = ["ok", "errors", "unhealthy", "quiet"];

// Default static (non-metadata) fields — the integrations list set. Callers
// (e.g. the services list) can pass their own `staticFields` instead.
const DEFAULT_STATIC_FIELDS: FieldSpec[] = [
  { field: "name", label: "Name", kind: "text" },
  { field: "description", label: "Description", kind: "text" },
  { field: "slug", label: "Slug", kind: "text" },
  { field: "status", label: "Status", kind: "status", options: STATUS_OPTIONS },
];

// Operators offered per field kind. Order matters — the first one is
// the default when the user adds a filter on that field.
function opsFor(kind: FieldKind): { value: FilterOperator; label: string }[] {
  switch (kind) {
    case "text":
    case "select":
      return [
        { value: "contains", label: "contains" },
        { value: "equals", label: "equals" },
        { value: "is_set", label: "is set" },
        { value: "is_empty", label: "is empty" },
      ];
    case "boolean":
      return [
        { value: "is_true", label: "is true" },
        { value: "is_false", label: "is false" },
      ];
    case "number":
      return [
        { value: "eq", label: "=" },
        { value: "gt", label: ">" },
        { value: "lt", label: "<" },
        { value: "is_set", label: "is set" },
        { value: "is_empty", label: "is empty" },
      ];
    case "status":
      return [{ value: "is", label: "is" }];
  }
}

// Whether the operator needs a value pill at all. For is_true / is_false /
// is_set / is_empty the operator itself fully specifies the predicate.
export function opNeedsValue(op: FilterOperator): boolean {
  return !["is_true", "is_false", "is_set", "is_empty"].includes(op);
}

interface Props {
  filters: IntegrationFilter[];
  onChange: (next: IntegrationFilter[]) => void;
  metadataFields: MetadataField[];
  // Distinct values seen for each field across the loaded list, keyed
  // by FilterField. Used to back the SearchableSelect value picker
  // when the operator is "equals" (or "is" for status / select fields)
  // so the user picks from what actually exists instead of guessing.
  distinctValues?: Record<string, string[]>;
  // Static (non-metadata) fields offered. Defaults to the integrations set;
  // the services list passes name/namespace/status instead.
  staticFields?: FieldSpec[];
  // Noun shown in the "Showing all <noun>" hint. Defaults to "integrations".
  noun?: string;
}

export default function IntegrationFilterBar({ filters, onChange, metadataFields, distinctValues, staticFields, noun = "integrations" }: Props) {
  // Build the field catalogue: static columns + one entry per metadata
  // field, each with the right kind so opsFor can pick operators.
  const fieldSpecs: FieldSpec[] = [
    ...(staticFields ?? DEFAULT_STATIC_FIELDS),
    ...metadataFields.map<FieldSpec>((f) => ({
      field: `meta:${f.key}` as FilterField,
      label: f.label,
      kind: f.type as FieldKind,
      options: f.options,
    })),
  ];
  const byField = new Map(fieldSpecs.map((s) => [s.field, s]));

  const addFilter = () => {
    // Default first filter is "Name contains ___" — a sensible starter.
    const spec = fieldSpecs[0];
    const op = opsFor(spec.kind)[0].value;
    onChange([
      ...filters,
      { id: crypto.randomUUID(), field: spec.field, op, value: "" },
    ]);
  };

  const update = (id: string, patch: Partial<IntegrationFilter>) => {
    onChange(filters.map((f) => (f.id === id ? { ...f, ...patch } : f)));
  };

  const remove = (id: string) => onChange(filters.filter((f) => f.id !== id));
  const clear = () => onChange([]);

  return (
    <div
      className="rounded-md border bg-surface-2 p-3"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="flex items-baseline justify-between mb-2">
        <h3 className="text-sm font-semibold">Filters</h3>
        <div className="text-xs text-muted">
          {filters.length === 0
            ? `Showing all ${noun}. Click + add a filter to narrow.`
            : `${filters.length} active · AND-joined`}
        </div>
      </div>

      <div className="space-y-1.5">
        {filters.map((f, i) => {
          const spec = byField.get(f.field);
          if (!spec) return null;
          const ops = opsFor(spec.kind);
          const needsValue = opNeedsValue(f.op);
          return (
            <div key={f.id} className="flex flex-wrap items-center gap-2 text-sm">
              <span className="w-12 text-xs text-muted">{i === 0 ? "where" : "and"}</span>
              <select
                className="toolbar__select"
                value={f.field}
                onChange={(e) => {
                  const nextSpec = byField.get(e.target.value as FilterField);
                  const nextOp = nextSpec ? opsFor(nextSpec.kind)[0].value : "contains";
                  update(f.id, { field: e.target.value as FilterField, op: nextOp, value: "" });
                }}
              >
                {fieldSpecs.map((s) => (
                  <option key={s.field} value={s.field}>
                    {s.label}
                  </option>
                ))}
              </select>
              <select
                className="toolbar__select"
                value={f.op}
                onChange={(e) => update(f.id, { op: e.target.value as FilterOperator })}
              >
                {ops.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
              {needsValue && (
                <ValueInput
                  spec={spec}
                  op={f.op}
                  value={f.value}
                  onChange={(v) => update(f.id, { value: v })}
                  knownValues={distinctValues?.[f.field] ?? []}
                />
              )}
              <button
                type="button"
                className="ml-auto text-xs text-muted hover:text-foreground"
                onClick={() => remove(f.id)}
                aria-label="Remove filter"
              >
                ✕ remove
              </button>
            </div>
          );
        })}
      </div>

      <div className="mt-2 flex items-center gap-3 text-sm">
        <button
          type="button"
          onClick={addFilter}
          className="underline-offset-2 hover:underline"
          style={{ color: "var(--primary)" }}
        >
          + add a filter
        </button>
        {filters.length > 0 && (
          <button type="button" onClick={clear} className="text-muted hover:text-foreground text-xs">
            clear all
          </button>
        )}
      </div>
    </div>
  );
}

function ValueInput({
  spec,
  op,
  value,
  onChange,
  knownValues,
}: {
  spec: FieldSpec;
  op: FilterOperator;
  value: string;
  onChange: (v: string) => void;
  knownValues: string[];
}) {
  // status: a small fixed enum — pick from it via the standard typeahead
  // popover that the rest of the app uses for SearchableSelect.
  if (spec.kind === "status") {
    return (
      <SearchableSelect
        value={value}
        onChange={onChange}
        options={spec.options ?? STATUS_OPTIONS}
        placeholder="Filter values…"
        allLabel="— pick a status —"
      />
    );
  }

  // metadata select: the field's declared option list is the truth.
  if (spec.kind === "select" && spec.options && spec.options.length > 0) {
    return (
      <SearchableSelect
        value={value}
        onChange={onChange}
        options={spec.options}
        placeholder="Filter values…"
        allLabel="— pick a value —"
      />
    );
  }

  // text + equals: pick from what we've actually seen across integrations
  // so the user isn't guessing exact strings. (For "contains" / "is_set"
  // / "is_empty" the typeahead doesn't make sense — keep the free-text
  // input.)
  if (spec.kind === "text" && op === "equals" && knownValues.length > 0) {
    return (
      <SearchableSelect
        value={value}
        onChange={onChange}
        options={knownValues}
        placeholder="Filter values…"
        allLabel="— pick a value —"
      />
    );
  }

  if (spec.kind === "number") {
    return (
      <input
        type="number"
        step="any"
        className="search__input"
        style={{ minWidth: 120 }}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }

  return (
    <input
      type="text"
      className="search__input"
      style={{ minWidth: 200 }}
      placeholder="value…"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

// ── pure predicate used by the parent to filter rows ────────────────────
export function matchesFilter(
  f: IntegrationFilter,
  rowValue: string | number | null | undefined,
): boolean {
  const s = rowValue == null ? "" : String(rowValue).toLowerCase();
  const v = (f.value ?? "").toLowerCase();
  switch (f.op) {
    case "contains":
      return s.includes(v);
    case "equals":
      return s === v;
    case "is":
      return s === v;
    case "is_set":
      return s !== "";
    case "is_empty":
      return s === "";
    case "is_true":
      return s === "true";
    case "is_false":
      return s === "false";
    case "eq":
      return Number(rowValue) === Number(f.value);
    case "gt":
      return Number(rowValue) > Number(f.value);
    case "lt":
      return Number(rowValue) < Number(f.value);
  }
}

// ── URL serialisation: filter=field|op|value,field|op|value ────────────
// We pick "|" as the inner separator because field names can legitimately
// contain ":" (metadata keys are prefixed "meta:<key>"). The outer
// separator stays "," — field/op/value never contain commas in practice
// (the value can, so it's percent-encoded).
export function serializeFilters(filters: IntegrationFilter[]): string {
  return filters
    .map((f) => `${f.field}|${f.op}|${encodeURIComponent(f.value)}`)
    .join(",");
}

export function parseFilters(raw: string | null): IntegrationFilter[] {
  if (!raw) return [];
  return raw
    .split(",")
    .map((chunk, idx) => {
      const parts = chunk.split("|");
      if (parts.length < 2) return null;
      const [field, op, ...rest] = parts;
      // Deterministic id: position + field + op. Stable across re-parses
      // as long as the user is only editing a row's value — which is the
      // case while typing. Without this every keystroke would generate a
      // new React key and the input would lose focus after the first
      // character. (The value is excluded from the id on purpose.)
      return {
        id: `${idx}|${field}|${op}`,
        field: field as FilterField,
        op: op as FilterOperator,
        value: decodeURIComponent(rest.join("|") ?? ""),
      } as IntegrationFilter;
    })
    .filter((x): x is IntegrationFilter => x !== null);
}

