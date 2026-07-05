// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Metadata fields admin page. Defines the typed key/value schema that
// integrations and services can fill in — like tags but structured.
// Field definitions are org-scoped; values are attached per integration
// or per service on the respective detail pages.

import { FormEvent, useEffect, useState } from "react";
import { api } from "../api/client";
import type {
  MetadataField,
  MetadataFieldInput,
  MetadataFieldType,
} from "../api/types";
import { usePageTitle } from "../lib/usePageTitle";
import { useCurrentUser } from "../lib/useCurrentUser";
import { EditDrawer } from "../components/primitives";

const TYPES: { value: MetadataFieldType; label: string; hint: string }[] = [
  { value: "text", label: "text", hint: 'Free-form string. Use for names, emails, URLs…' },
  { value: "boolean", label: "boolean", hint: "True / false toggle." },
  { value: "number", label: "number", hint: "Any decimal number." },
  { value: "select", label: "select", hint: "Pick one value from a fixed list of options." },
];

const EMPTY: MetadataFieldInput = {
  key: "",
  label: "",
  type: "text",
  options: [],
  description: "",
  applies_to_integration: true,
  applies_to_service: false,
  applies_to_system: false,
  system_type_key: "",
  required: false,
};

export default function MetadataFieldsPage() {
  usePageTitle("Metadata fields");
  // Defining the metadata schema is an editor+ action (same as tags):
  // viewers can read the fields but can't create/edit/delete. The
  // cell-api enforces the same with RequireRole(CanWrite).
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  const canDelete = can("integration.delete");

  const [fields, setFields] = useState<MetadataField[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Editor state. null = closed; "new" = creating; otherwise editing an
  // existing field by id.
  const [editing, setEditing] = useState<null | "new" | MetadataField>(null);

  const refresh = () => {
    setLoading(true);
    setError(null);
    api
      .listMetadataFields()
      .then((r) => setFields(r.fields ?? []))
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  };

  useEffect(refresh, []);

  const onSaved = (saved: MetadataField, isNew: boolean) => {
    setFields((curr) =>
      isNew
        ? [...curr, saved].sort((a, b) => a.label.localeCompare(b.label))
        : curr.map((f) => (f.id === saved.id ? saved : f)),
    );
    setEditing(null);
  };

  const onDelete = async (f: MetadataField) => {
    if (!confirm(`Delete "${f.label}"? Any saved values on integrations and services will be removed.`)) return;
    try {
      await api.deleteMetadataField(f.id);
      setFields((curr) => curr.filter((x) => x.id !== f.id));
    } catch (e) {
      setError(`Delete failed: ${String((e as Error).message ?? e)}`);
    }
  };

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Metadata fields</h1>
          <p className="page__subtitle">
            Define typed key/value fields that decorate your integrations and services —
            things like <span className="mono">Contact Person</span> (text) or{" "}
            <span className="mono">Handles GDPR data?</span> (boolean). Tags answer "which
            buckets does this belong to"; metadata answers "what do I know about it".
          </p>
        </div>
        <div>
          {canWrite && editing === null && (
            <button type="button" className="btn btn--primary" onClick={() => setEditing("new")}>
              + New field
            </button>
          )}
        </div>
      </div>

      {error && <div className="alert alert--error">{error}</div>}
      {!canWrite && (
        <div className="muted" style={{ fontSize: 13, marginBottom: 12 }}>
          You have read-only access to metadata fields. Ask an{" "}
          <strong>integration contributor</strong> or <strong>org admin</strong> to add or change them.
        </div>
      )}

      {canWrite && editing && (
        <EditDrawer
          title={editing === "new" ? "New metadata field" : `Edit · ${editing.label}`}
          width="medium"
          onClose={() => setEditing(null)}
        >
          <FieldEditor
            initial={editing === "new" ? null : editing}
            onCancel={() => setEditing(null)}
            onSaved={onSaved}
            existingKeys={new Set(
              fields
                .filter((f) => editing === "new" || f.id !== editing.id)
                .map((f) => f.key),
            )}
          />
        </EditDrawer>
      )}

      {loading ? (
        <div className="placeholder">Loading…</div>
      ) : fields.length === 0 ? (
        <div className="placeholder">
          {canWrite ? (
            <>
              No metadata fields yet. Click <b>+ New field</b> to define one — pick a key,
              a label, a type, and the scopes it applies to.
            </>
          ) : (
            <>No metadata fields have been defined yet.</>
          )}
        </div>
      ) : (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th>Label</th>
                <th>Key</th>
                <th>Type</th>
                <th>Applies to</th>
                <th>Required</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {fields.map((f) => (
                <tr key={f.id}>
                  <td>
                    <div className="font-medium">{f.label}</div>
                    {f.description && (
                      <div className="muted" style={{ fontSize: 12 }}>
                        {f.description}
                      </div>
                    )}
                  </td>
                  <td className="mono">{f.key}</td>
                  <td>
                    {f.type}
                    {f.type === "select" && f.options && f.options.length > 0 && (
                      <span className="muted"> · {f.options.length} option{f.options.length === 1 ? "" : "s"}</span>
                    )}
                  </td>
                  <td>
                    {[
                      f.applies_to_integration && "integration",
                      f.applies_to_service && "service",
                      f.applies_to_system && (f.system_type_key ? `system:${f.system_type_key}` : "system"),
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </td>
                  <td>{f.required ? "yes" : "—"}</td>
                  <td className="num">
                    {canWrite && (
                      <button className="btn btn--link" onClick={() => setEditing(f)}>
                        Edit
                      </button>
                    )}
                    {canDelete && (
                      <button
                        className="btn btn--link"
                        style={{ color: "var(--err)" }}
                        onClick={() => onDelete(f)}
                      >
                        Delete
                      </button>
                    )}
                    {!canWrite && !canDelete && <span className="muted">—</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

interface EditorProps {
  initial: MetadataField | null;
  existingKeys: Set<string>;
  onCancel: () => void;
  onSaved: (saved: MetadataField, isNew: boolean) => void;
}

function FieldEditor({ initial, existingKeys, onCancel, onSaved }: EditorProps) {
  const isNew = !initial;
  const [form, setForm] = useState<MetadataFieldInput>(
    initial
      ? {
          key: initial.key,
          label: initial.label,
          type: initial.type,
          options: initial.options ?? [],
          description: initial.description,
          applies_to_integration: initial.applies_to_integration,
          applies_to_service: initial.applies_to_service,
          applies_to_system: initial.applies_to_system,
          system_type_key: initial.system_type_key,
          required: initial.required,
        }
      : { ...EMPTY },
  );
  const [systemTypes, setSystemTypes] = useState<{ key: string; label: string }[]>([]);
  useEffect(() => {
    api.listSystemTypes().then((r) => setSystemTypes((r.system_types ?? []).map((t) => ({ key: t.key, label: t.label })))).catch(() => {});
  }, []);
  const [optionsText, setOptionsText] = useState<string>((initial?.options ?? []).join("\n"));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Live key suggestion while typing the label (only when creating).
  const onLabelChange = (label: string) => {
    setForm((curr) => {
      const next = { ...curr, label };
      if (isNew) {
        // Only auto-update key if the user hasn't customised it.
        const auto = slugifyKey(curr.label);
        if (curr.key === "" || curr.key === auto) {
          next.key = slugifyKey(label);
        }
      }
      return next;
    });
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);

    const key = form.key.trim();
    const label = form.label.trim();
    if (!key) return setError("Key is required.");
    if (!label) return setError("Label is required.");
    if (!form.applies_to_integration && !form.applies_to_service && !form.applies_to_system)
      return setError("Pick at least one scope (integration, service, and/or system).");
    if (isNew && existingKeys.has(key))
      return setError(`Key "${key}" is already used.`);

    const options = form.type === "select"
      ? optionsText
          .split("\n")
          .map((s) => s.trim())
          .filter((s) => s !== "")
      : undefined;
    if (form.type === "select" && (!options || options.length === 0))
      return setError("Select fields need at least one option (one per line).");

    const payload: MetadataFieldInput = {
      ...form,
      key,
      label,
      options,
      system_type_key: form.applies_to_system ? form.system_type_key : "",
    };

    setSubmitting(true);
    try {
      const saved = isNew
        ? await api.createMetadataField(payload)
        : await api.updateMetadataField(initial!.id, payload);
      onSaved(saved, isNew);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    // The drawer header provides title + close button + surface chrome;
    // the editor is just the form now.
    <>
      {error && <div className="alert alert--error">{error}</div>}
      <form onSubmit={submit} className="form">
        <div className="form__row">
          <label className="form__label">
            Label
            <input
              className="search__input"
              value={form.label}
              onChange={(e) => onLabelChange(e.target.value)}
              placeholder="Contact Person"
              required
              autoFocus
            />
            <span className="form__hint">Shown to users in forms and on detail pages.</span>
          </label>
          <label className="form__label">
            Key
            <input
              className="search__input"
              value={form.key}
              onChange={(e) => setForm({ ...form, key: e.target.value })}
              placeholder="contact_person"
              pattern="[a-z0-9_]+"
              required
              disabled={!isNew}
              title={isNew ? undefined : "Key is immutable once a field is created."}
            />
            <span className="form__hint">
              Machine identifier — lowercase letters, digits, underscores.
              {!isNew && " (cannot be changed)"}
            </span>
          </label>
        </div>

        <label className="form__label">
          Description
          <textarea
            className="svc-textarea"
            rows={2}
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            placeholder="Optional hint shown next to the field on edit forms."
          />
        </label>

        <div className="form__row">
          <label className="form__label">
            Type
            <select
              className="toolbar__select"
              value={form.type}
              onChange={(e) => setForm({ ...form, type: e.target.value as MetadataFieldType })}
              disabled={!isNew}
              title={isNew ? undefined : "Type is immutable once a field is created."}
            >
              {TYPES.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </select>
            <span className="form__hint">{TYPES.find((t) => t.value === form.type)?.hint}</span>
          </label>
          <div className="form__label">
            Applies to
            <div style={{ display: "flex", gap: 12, paddingTop: 6 }}>
              <label className="inline-flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={form.applies_to_integration}
                  onChange={(e) =>
                    setForm({ ...form, applies_to_integration: e.target.checked })
                  }
                />
                integrations
              </label>
              <label className="inline-flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={form.applies_to_service}
                  onChange={(e) =>
                    setForm({ ...form, applies_to_service: e.target.checked })
                  }
                />
                services
              </label>
              <label className="inline-flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={form.applies_to_system}
                  onChange={(e) =>
                    setForm({ ...form, applies_to_system: e.target.checked })
                  }
                />
                systems
              </label>
            </div>
            {form.applies_to_system && (
              <label className="form__label" style={{ marginTop: 8 }}>
                System type
                <select
                  className="search__input"
                  value={form.system_type_key}
                  onChange={(e) => setForm({ ...form, system_type_key: e.target.value })}
                >
                  <option value="">All systems</option>
                  {systemTypes.map((t) => (
                    <option key={t.key} value={t.key}>{t.label}</option>
                  ))}
                </select>
                <span className="form__hint">Limit this field to systems of one type, or apply it to all.</span>
              </label>
            )}
          </div>
        </div>

        {form.type === "select" && (
          <label className="form__label">
            Options
            <textarea
              className="svc-textarea mono"
              rows={4}
              value={optionsText}
              onChange={(e) => setOptionsText(e.target.value)}
              placeholder={"one per line — e.g.\nlow\nmedium\nhigh"}
            />
            <span className="form__hint">One value per line.</span>
          </label>
        )}

        <label className="inline-flex items-center gap-2" style={{ marginTop: 6 }}>
          <input
            type="checkbox"
            checked={form.required}
            onChange={(e) => setForm({ ...form, required: e.target.checked })}
          />
          Required — every applicable integration / service must set a value before save.
        </label>

        <div className="form__actions">
          <button type="button" className="btn" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="btn btn--primary" disabled={submitting}>
            {submitting ? "Saving…" : isNew ? "Create field" : "Save changes"}
          </button>
        </div>
      </form>
    </>
  );
}

// slugifyKey turns "Contact Person" into "contact_person", clamping to the
// pattern the form accepts. Best-effort — the user can override.
function slugifyKey(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
}

// Reusable elsewhere if needed.
export const __slugifyKey = slugifyKey;
