// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ServiceSchemasPanel — shows a service's In-Schema / Out-Schema and
// lets the user pick from the org's schema catalogue. View mode renders
// the linked schemas (clickable to the admin page); edit mode is a
// pair of SearchableSelects backed by the catalogue.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import SearchableSelect from "./SearchableSelect";
import type { Schema } from "../api/types";

interface Props {
  in?: Schema | null;
  out?: Schema | null;
  onSave: (next: { in_schema_id: string | null; out_schema_id: string | null }) => Promise<void>;
  title?: string;
}

export default function ServiceSchemasPanel({ in: inSchema, out: outSchema, onSave, title = "Schemas" }: Props) {
  const [editing, setEditing] = useState(false);

  return (
    <section
      className="rounded-lg border bg-surface-2 p-4"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="flex items-center justify-between mb-3">
        <div>
          <h2 className="text-base font-semibold">{title}</h2>
          <p className="text-xs text-muted">
            Data shapes this service consumes (In) and produces (Out). Pick from{" "}
            <a href="/schemas" className="hover:underline" style={{ color: "var(--primary)" }}>
              the schema catalogue
            </a>
            .
          </p>
        </div>
        {!editing && (
          <button type="button" className="btn btn--link" onClick={() => setEditing(true)}>
            ✎ Edit
          </button>
        )}
      </div>

      {editing ? (
        <SchemasEditor
          inSchema={inSchema ?? null}
          outSchema={outSchema ?? null}
          onCancel={() => setEditing(false)}
          onSave={async (next) => {
            await onSave(next);
            setEditing(false);
          }}
        />
      ) : (
        // Rendered as a proper table — one row per direction, Key /
        // Value columns — to match the new metadata-table treatment
        // and stay readable when sat next to it on the rail.
        <table className="table">
          <thead>
            <tr>
              <th style={{ width: "40%" }}>Key</th>
              <th>Value</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td>In</td>
              <td style={{ wordBreak: "break-word" }}>
                {inSchema ? (
                  <a href="/schemas" style={{ color: "var(--primary)" }}>
                    {inSchema.name}{" "}
                    <span className="muted">· {inSchema.format}</span>
                  </a>
                ) : (
                  <span className="muted">—</span>
                )}
              </td>
            </tr>
            <tr>
              <td>Out</td>
              <td style={{ wordBreak: "break-word" }}>
                {outSchema ? (
                  <a href="/schemas" style={{ color: "var(--primary)" }}>
                    {outSchema.name}{" "}
                    <span className="muted">· {outSchema.format}</span>
                  </a>
                ) : (
                  <span className="muted">—</span>
                )}
              </td>
            </tr>
          </tbody>
        </table>
      )}
    </section>
  );
}

// ── edit mode ───────────────────────────────────────────────────────────
function SchemasEditor({
  inSchema,
  outSchema,
  onCancel,
  onSave,
}: {
  inSchema: Schema | null;
  outSchema: Schema | null;
  onCancel: () => void;
  onSave: (next: { in_schema_id: string | null; out_schema_id: string | null }) => Promise<void>;
}) {
  const [catalog, setCatalog] = useState<Schema[]>([]);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [inId, setInId] = useState<string>(inSchema?.id ?? "");
  const [outId, setOutId] = useState<string>(outSchema?.id ?? "");
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .listSchemas()
      .then((r) => setCatalog(r.schemas ?? []))
      .catch((e) => setLoadErr(String(e.message ?? e)));
  }, []);

  // SearchableSelect needs string options; we'll show the label via labelFor.
  const options = catalog.map((s) => s.id);
  const labelFor = (id: string) => {
    const s = catalog.find((x) => x.id === id);
    return s ? `${s.name}  ·  ${s.format}` : id;
  };

  const submit = async () => {
    setErr(null);
    setSaving(true);
    try {
      await onSave({
        in_schema_id: inId ? inId : null,
        out_schema_id: outId ? outId : null,
      });
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col gap-3">
      {loadErr && <div className="alert alert--error">Couldn't load catalogue: {loadErr}</div>}
      {err && <div className="alert alert--error">{err}</div>}
      <label className="flex flex-col gap-1">
        <span className="text-sm font-medium">In-Schema</span>
        <SearchableSelect
          value={inId}
          onChange={setInId}
          options={options}
          labelFor={labelFor}
          placeholder="Filter schemas…"
          allLabel="— none —"
        />
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-sm font-medium">Out-Schema</span>
        <SearchableSelect
          value={outId}
          onChange={setOutId}
          options={options}
          labelFor={labelFor}
          placeholder="Filter schemas…"
          allLabel="— none —"
        />
      </label>
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
