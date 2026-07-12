// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// MetadataPanel shows the user-defined metadata values for an
// integration or a service, with an inline edit form per applicable
// field. The schema (which fields apply, what type they are) comes
// from the detail response — this component just renders + edits the
// values against that schema.

import { useEffect, useState } from "react";
import type { MetadataField } from "../api/types";
import { KVTable, type KVRow } from "./primitives";

interface Props {
  fields: MetadataField[];
  values: Record<string, string>;
  // onSave receives the full value map (only set keys; cleared keys are
  // sent as empty strings so the backend deletes them). Returns a
  // promise that resolves on save or rejects with an error message.
  onSave: (next: Record<string, string>) => Promise<void>;
  // Optional label override. Defaults to "Metadata".
  title?: string;
}

export default function MetadataPanel({ fields, values, onSave, title = "Metadata" }: Props) {
  // If no field applies in this scope, the panel renders an empty-state
  // pointer to the metadata-fields admin page. Hiding it entirely would
  // make the feature undiscoverable.
  const [editing, setEditing] = useState(false);

  if (fields.length === 0) {
    return (
      <section
        className="rounded-lg border bg-surface-2 p-4"
        style={{ borderColor: "var(--border)" }}
      >
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-base font-semibold">{title}</h2>
            <p className="text-xs text-muted">
              No metadata fields are defined yet. Go to{" "}
              <a href="/metadata-fields" className="hover:underline" style={{ color: "var(--primary)" }}>
                Metadata fields
              </a>{" "}
              to add things like "Contact Person" or "Handles GDPR data?".
            </p>
          </div>
        </div>
      </section>
    );
  }

  return (
    <section
      className="rounded-lg border bg-surface-2 p-4"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-base font-semibold">{title}</h2>
        {!editing && (
          <button type="button" className="btn btn--link" onClick={() => setEditing(true)}>
            ✎ Edit
          </button>
        )}
      </div>

      {editing ? (
        <Editor
          fields={fields}
          values={values}
          onCancel={() => setEditing(false)}
          onSave={async (next) => {
            await onSave(next);
            setEditing(false);
          }}
        />
      ) : (
        <Viewer fields={fields} values={values} />
      )}
    </section>
  );
}

// ── view mode ───────────────────────────────────────────────────────────
// Rendered as an aligned key/value list (KVTable) so multi-field
// metadata stays readable when the rail grows long. Matches the look
// of the resource/attribute lists on logs and traces — one consistent
// reading style for every "list of things" surface (issue #5).
//
// (Origin/main briefly switched this to an ad-hoc <table className="table">
// with Key / Value columns; PR #10 subsumes that by using the shared
// KVTable primitive, which gives the same readable rows plus copy
// affordances + consistent styling across MetadataPanel, TraceDrawer,
// SpanResultList and LogDetailsDrawer.)
function Viewer({ fields, values }: { fields: MetadataField[]; values: Record<string, string> }) {
  const rows: KVRow[] = fields.map((f) => {
    const raw = values[f.key];
    const set = raw !== undefined && raw !== "";
    return {
      rowKey: f.id,
      keyTitle: f.description || undefined,
      k: (
        <>
          {f.label}
          {f.required && <span className="muted"> *</span>}
        </>
      ),
      v: displayValue(f, raw),
      // Copy the raw value when set; the muted "—" placeholder isn't
      // worth a copy button.
      copyValue: set ? raw : null,
    };
  });
  // columns="auto" tells KVTable to flow into 2 newspaper-style
  // columns when the container is wide enough — important on the
  // integration detail page where a service may carry 20+ metadata
  // entries that would otherwise stack into a long vertical list.
  // Narrow contexts (service rail, edit drawer) automatically
  // collapse back to a single column via CSS column-width.
  return <KVTable variant="prose" rows={rows} columns="auto" />;
}

function displayValue(f: MetadataField, raw: string | undefined): React.ReactNode {
  if (raw === undefined || raw === "") {
    return <span className="muted">—</span>;
  }
  if (f.type === "boolean") {
    return raw === "true" ? "✓ yes" : "✗ no";
  }
  if (f.type === "number") {
    return <span className="mono">{raw}</span>;
  }
  return raw;
}

// ── edit mode ───────────────────────────────────────────────────────────
interface EditorProps {
  fields: MetadataField[];
  values: Record<string, string>;
  onCancel: () => void;
  onSave: (next: Record<string, string>) => Promise<void>;
}

function Editor({ fields, values, onCancel, onSave }: EditorProps) {
  // Local draft state. Use a function initialiser so re-opening the
  // editor after a save picks up the freshest values from props.
  const [draft, setDraft] = useState<Record<string, string>>(() => ({ ...values }));
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Keep draft in sync if parent values change while editing
  // (e.g. another save landed). We re-seed only when the editor opens.
  useEffect(() => {
    setDraft({ ...values });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const setField = (key: string, val: string) =>
    setDraft((curr) => ({ ...curr, [key]: val }));

  const submit = async () => {
    setError(null);
    // Required-field validation client-side; server enforces it again.
    for (const f of fields) {
      if (f.required && !(draft[f.key] ?? "").trim()) {
        setError(`"${f.label}" is required.`);
        return;
      }
    }
    setSaving(true);
    try {
      // Ensure all known field keys are present so the server can
      // delete unset ones (empty strings == clear).
      const payload: Record<string, string> = {};
      for (const f of fields) {
        payload[f.key] = (draft[f.key] ?? "").trim();
      }
      await onSave(payload);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col gap-3">
      {error && <div className="alert alert--error">{error}</div>}
      {fields.map((f) => (
        <FieldInput
          key={f.id}
          field={f}
          value={draft[f.key] ?? ""}
          onChange={(v) => setField(f.key, v)}
        />
      ))}
      <div className="flex items-center justify-end gap-2">
        <button type="button" className="btn" onClick={onCancel} disabled={saving}>
          Cancel
        </button>
        <button type="button" className="btn btn--primary" onClick={submit} disabled={saving}>
          {saving ? "Saving…" : "Save"}
        </button>
      </div>
    </div>
  );
}

interface InputProps {
  field: MetadataField;
  value: string;
  onChange: (v: string) => void;
}

// Exported: the integration-creation form reuses the same per-field
// inputs so metadata capture at create time matches the editor exactly.
export function FieldInput({ field, value, onChange }: InputProps) {
  const labelEl = (
    <>
      <span className="text-sm font-medium">
        {field.label}
        {field.required && <span className="muted"> *</span>}
      </span>
      {field.description && (
        <span className="block text-xs text-muted">{field.description}</span>
      )}
    </>
  );

  switch (field.type) {
    case "boolean":
      return (
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={value === "true"}
            onChange={(e) => onChange(e.target.checked ? "true" : "false")}
          />
          <span className="flex flex-col">{labelEl}</span>
        </label>
      );
    case "number":
      return (
        <label className="flex flex-col gap-1">
          {labelEl}
          <input
            type="number"
            step="any"
            className="search__input"
            value={value}
            onChange={(e) => onChange(e.target.value)}
          />
        </label>
      );
    case "select":
      return (
        <label className="flex flex-col gap-1">
          {labelEl}
          <select
            className="toolbar__select"
            value={value}
            onChange={(e) => onChange(e.target.value)}
          >
            <option value="">— not set —</option>
            {(field.options ?? []).map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
        </label>
      );
    case "text":
    default:
      return (
        <label className="flex flex-col gap-1">
          {labelEl}
          <input
            type="text"
            className="search__input"
            value={value}
            onChange={(e) => onChange(e.target.value)}
          />
        </label>
      );
  }
}
