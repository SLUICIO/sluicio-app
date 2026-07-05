// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Schemas admin page. Each schema describes the shape of a message a
// service consumes / produces; services pick from this catalogue via
// their In-Schema / Out-Schema fields. The right-hand column also
// shows which services point at each schema, so a delete is informed.

import { FormEvent, lazy, Suspense, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { Schema, SchemaInput, SchemaKind, SchemaUsage } from "../api/types";
import ContentViewerDrawer from "../components/ContentViewerDrawer";
import { EditDrawer } from "../components/primitives";
import { usePageTitle } from "../lib/usePageTitle";
import { useCurrentUser } from "../lib/useCurrentUser";

// CodeMirror is ~150 KB gzip — lazy-loaded so the schemas list page
// itself stays cheap; the bundle hit is paid only when the editor
// opens. The fallback below renders a plain textarea so authoring
// still works during the brief moment before CodeMirror arrives.
const CodeEditor = lazy(() => import("../components/CodeEditor"));

const KINDS: { value: SchemaKind; label: string; hint: string }[] = [
  { value: "schema", label: "schema", hint: "Describes a data shape (JSON Schema, OpenAPI, Avro, Protobuf…)." },
  { value: "example", label: "example", hint: "A sample document. Useful for tests / docs." },
  { value: "other", label: "other", hint: "Anything that doesn't fit the buckets above." },
];

// Schemas is now strictly a shape catalogue, so transformation
// formats (xslt, liquid) belong on the Maps page. We keep the
// shape-description set here.
const FORMATS = ["json", "yaml", "xml", "protobuf", "avro", "openapi", "text", "other"];

const EMPTY: SchemaInput = {
  name: "",
  kind: "schema",
  version: "",
  description: "",
  format: "json",
  content: "",
};

export default function SchemasPage() {
  usePageTitle("Schemas");
  // Editing the schema catalogue is an editor+ action; viewers are
  // read-only (the cell-api enforces the same with RequireRole(CanWrite)).
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  const canDelete = can("integration.delete");

  const [schemas, setSchemas] = useState<Schema[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // Lifted from SchemaEditor so EditDrawer can guard close with a
  // confirm prompt when the user has typed unsaved content — Schemas
  // editors hold CodeMirror bodies that are expensive to retype.
  const [editorDirty, setEditorDirty] = useState(false);
  const [editing, setEditing] = useState<null | "new" | Schema>(null);
  // The schema whose content is being viewed in the read-only drawer.
  // Independent of `editing`; the table's name button opens this, the
  // row's Edit button opens the editor.
  const [viewing, setViewing] = useState<Schema | null>(null);

  const refresh = () => {
    setLoading(true);
    setError(null);
    api
      .listSchemas()
      .then((r) => setSchemas(r.schemas ?? []))
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  };

  useEffect(refresh, []);

  const onSaved = (saved: Schema, isNew: boolean) => {
    setSchemas((curr) =>
      isNew
        ? [...curr, saved].sort((a, b) => a.name.localeCompare(b.name))
        : curr.map((s) => (s.id === saved.id ? { ...saved, usage_count: s.usage_count } : s)),
    );
    setEditing(null);
  };

  const onDelete = async (s: Schema) => {
    const used = s.usage_count ?? 0;
    const msg =
      used > 0
        ? `Delete "${s.name}"? ${used} service link${used === 1 ? "" : "s"} will also be removed.`
        : `Delete "${s.name}"?`;
    if (!confirm(msg)) return;
    try {
      await api.deleteSchema(s.id);
      setSchemas((curr) => curr.filter((x) => x.id !== s.id));
    } catch (e) {
      setError(`Delete failed: ${String((e as Error).message ?? e)}`);
    }
  };

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Schemas</h1>
          <p className="page__subtitle">
            Data schemas describing the shape of messages flowing between
            services. Each service can declare an <b>In-Schema</b> (what it
            consumes) and an <b>Out-Schema</b> (what it produces). When two
            services share a schema — one's Out matches another's In — that's
            a dependency you can see at a glance.
          </p>
        </div>
        <div>
          {canWrite && editing === null && (
            <button type="button" className="btn btn--primary" onClick={() => setEditing("new")}>
              + New schema
            </button>
          )}
        </div>
      </div>

      {error && <div className="alert alert--error">{error}</div>}
      {!canWrite && (
        <div className="muted" style={{ fontSize: 13, marginBottom: 12 }}>
          You have read-only access to schemas. Ask an{" "}
          <strong>integration contributor</strong> or <strong>org admin</strong> to add or change them.
        </div>
      )}

      {canWrite && editing && (
        <EditDrawer
          title={editing === "new" ? "New schema" : `Edit · ${editing.name}`}
          width="wide"
          onClose={() => setEditing(null)}
          dirty={editorDirty}
        >
          <SchemaEditor
            initial={editing === "new" ? null : editing}
            existingNames={new Set(
              schemas
                .filter((s) => editing === "new" || s.id !== editing.id)
                .map((s) => `${s.name} ${s.version}`),
            )}
            onCancel={() => setEditing(null)}
            onSaved={onSaved}
            onDirtyChange={setEditorDirty}
          />
        </EditDrawer>
      )}

      {viewing && (
        <ContentViewerDrawer
          title={viewing.version ? `${viewing.name} · ${viewing.version}` : viewing.name}
          content={viewing.content || ""}
          format={viewing.format}
          onClose={() => setViewing(null)}
        />
      )}

      {loading ? (
        <div className="placeholder">Loading…</div>
      ) : schemas.length === 0 ? (
        <div className="placeholder">
          {canWrite ? (
            <>
              No schemas yet. Click <b>+ New schema</b> to define one — for example,
              an order JSON Schema, an OpenAPI doc, or a proto definition.
            </>
          ) : (
            <>No schemas have been defined yet.</>
          )}
        </div>
      ) : (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Version</th>
                <th>Kind</th>
                <th>Format</th>
                <th>Description</th>
                <th>Used by</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {schemas.map((s) => (
                <SchemaRow
                  key={s.id}
                  schema={s}
                  canWrite={canWrite}
                  canDelete={canDelete}
                  onView={() => setViewing(s)}
                  onEdit={() => setEditing(s)}
                  onDelete={() => onDelete(s)}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ── one row ────────────────────────────────────────────────────────────
// Clicking the name opens the content in a read-only drawer (see
// ContentViewerDrawer) rather than expanding an inline row, so the
// table stays compact and the body is easy to copy whole. The "Used
// by" column stays inline.
interface RowProps {
  schema: Schema;
  canWrite: boolean;
  canDelete: boolean;
  onView: () => void;
  onEdit: () => void;
  onDelete: () => void;
}

function SchemaRow({ schema, canWrite, canDelete, onView, onEdit, onDelete }: RowProps) {
  const usage = schema.usage ?? [];
  const kindLabel = KINDS.find((k) => k.value === schema.kind)?.label ?? schema.kind;

  return (
    <tr>
      <td>
        <button
          type="button"
          className="font-medium"
          onClick={onView}
          style={{ background: "transparent", border: 0, padding: 0, cursor: "pointer" }}
          title="View schema content"
        >
          {schema.name} <span className="muted" style={{ fontSize: 11 }}>⤢</span>
        </button>
      </td>
      <td className="mono">
        {schema.version || <span className="muted">—</span>}
      </td>
      <td>
        <span
          className="badge"
          style={{
            background: kindBadgeBg(schema.kind),
            color: "var(--ink-2)",
          }}
          title={KINDS.find((k) => k.value === schema.kind)?.hint}
        >
          {kindLabel}
        </span>
      </td>
      <td className="mono">{schema.format}</td>
      <td>
        {schema.description ? schema.description : <span className="muted">—</span>}
      </td>
      <td>
        {usage.length === 0 ? (
          <span className="muted">unused</span>
        ) : (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
            {usage.map((u) => (
              <ServiceChip key={`${u.service_name}|${u.direction}`} usage={u} />
            ))}
          </div>
        )}
      </td>
      <td className="num">
        {canWrite && <button className="btn btn--link" onClick={onEdit}>Edit</button>}
        {canDelete && <button className="btn btn--link" style={{ color: "var(--err)" }} onClick={onDelete}>Delete</button>}
        {!canWrite && !canDelete && <span className="muted">—</span>}
      </td>
    </tr>
  );
}

// ServiceChip renders one (service, direction) link as a colored chip
// — green for "in" (this service consumes the schema), blue for "out"
// (this service produces it). Both are clickable.
function ServiceChip({ usage }: { usage: SchemaUsage }) {
  const isIn = usage.direction === "in";
  return (
    <Link
      to={`/services/${encodeURIComponent(usage.service_name)}`}
      className="badge"
      title={`${usage.service_name} · ${isIn ? "consumes (In-Schema)" : "produces (Out-Schema)"}`}
      style={{
        textDecoration: "none",
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        background: isIn
          ? "color-mix(in oklab, var(--ok) 18%, transparent)"
          : "color-mix(in oklab, var(--primary) 18%, transparent)",
        borderColor: isIn
          ? "color-mix(in oklab, var(--ok) 30%, transparent)"
          : "color-mix(in oklab, var(--primary) 30%, transparent)",
      }}
    >
      <span style={{ fontSize: 10, fontWeight: 600, textTransform: "uppercase", letterSpacing: 0.4 }}>
        {usage.direction}
      </span>
      <span className="mono">{usage.service_name}</span>
    </Link>
  );
}

// Tint the kind badge so the table is scannable at a glance.
function kindBadgeBg(kind: string): string {
  switch (kind) {
    case "schema": return "var(--primary-soft)";
    case "example": return "var(--surface-3)";
    default: return "var(--surface-3)";
  }
}

// ── editor (create / update) ───────────────────────────────────────────
interface EditorProps {
  initial: Schema | null;
  existingNames: Set<string>;
  onCancel: () => void;
  onSaved: (saved: Schema, isNew: boolean) => void;
  // Called whenever the editor's form drifts from / returns to the
  // initial snapshot, so the wrapping EditDrawer can guard close with
  // a confirm prompt when unsaved changes exist.
  onDirtyChange?: (dirty: boolean) => void;
}

function SchemaEditor({ initial, existingNames, onCancel, onSaved, onDirtyChange }: EditorProps) {
  const isNew = !initial;
  // initialSnapshot is the JSON we compare current form state against
  // for the dirty signal. For a new-record editor that's EMPTY; for
  // an edit-record it's the row we opened with. Captured once on
  // mount via useRef so a re-render with new initial doesn't reset
  // the baseline mid-edit (the drawer remounts when editing changes,
  // so a stable baseline matches the user's intuition).
  const initialSnapshot = useRef<string>("");
  if (initialSnapshot.current === "") {
    initialSnapshot.current = JSON.stringify(
      initial
        ? {
            name: initial.name,
            kind: initial.kind,
            version: initial.version,
            description: initial.description,
            format: initial.format,
            content: initial.content,
          }
        : EMPTY,
    );
  }
  const [form, setForm] = useState<SchemaInput>(
    initial
      ? {
          name: initial.name,
          kind: initial.kind,
          version: initial.version,
          description: initial.description,
          format: initial.format,
          content: initial.content,
        }
      : { ...EMPTY },
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Push the dirty signal up to the drawer whenever the form drifts
  // from / returns to its initial snapshot.
  useEffect(() => {
    onDirtyChange?.(JSON.stringify(form) !== initialSnapshot.current);
  }, [form, onDirtyChange]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    const name = form.name.trim();
    const version = form.version.trim();
    if (!name) return setError("Name is required.");
    // Uniqueness is on (name, version) so versioning a name is fine.
    const key = `${name} ${version}`;
    if (existingNames.has(key))
      return setError(
        version
          ? `${name} ${version} already exists. Pick a different version.`
          : `${name} already exists. Add a version or rename.`,
      );

    setSubmitting(true);
    try {
      const saved = isNew
        ? await api.createSchema({ ...form, name, version })
        : await api.updateSchema(initial!.id, { ...form, name, version });
      onSaved(saved, isNew);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    // Title + surface chrome live on the wrapping EditDrawer now;
    // this component is just the form body.
    <>
      {error && <div className="alert alert--error">{error}</div>}
      <form onSubmit={submit} className="form">
        <div className="form__row">
          <label className="form__label">
            Name
            <input
              className="search__input"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="CreateOrderRequest"
              required
              autoFocus
            />
            <span className="form__hint">Together with version, must be unique in the org.</span>
          </label>
          <label className="form__label">
            Version
            <input
              className="search__input"
              value={form.version}
              onChange={(e) => setForm({ ...form, version: e.target.value })}
              placeholder="v1, 1.0.0, …"
            />
            <span className="form__hint">Optional. Two schemas with the same name + different versions can co-exist.</span>
          </label>
        </div>

        <div className="form__row">
          <label className="form__label">
            Type
            <select
              className="toolbar__select"
              value={form.kind}
              onChange={(e) => setForm({ ...form, kind: e.target.value as SchemaKind })}
            >
              {KINDS.map((k) => (
                <option key={k.value} value={k.value}>{k.label}</option>
              ))}
            </select>
            <span className="form__hint">{KINDS.find((k) => k.value === form.kind)?.hint}</span>
          </label>
          <label className="form__label">
            Format
            <select
              className="toolbar__select"
              value={form.format}
              onChange={(e) => setForm({ ...form, format: e.target.value })}
            >
              {FORMATS.map((f) => (
                <option key={f} value={f}>{f}</option>
              ))}
            </select>
            <span className="form__hint">Drives syntax highlighting and how the UI labels the content.</span>
          </label>
        </div>

        <label className="form__label">
          Description
          <textarea
            className="svc-textarea"
            rows={2}
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            placeholder="What this schema / template represents and where it's used."
          />
        </label>

        <div className="form__label">
          <span>Content</span>
          <Suspense
            fallback={
              <textarea
                className="svc-textarea mono"
                rows={14}
                value={form.content}
                onChange={(e) => setForm({ ...form, content: e.target.value })}
                placeholder="Loading editor…"
                spellCheck={false}
                style={{ whiteSpace: "pre", overflowWrap: "normal", overflowX: "auto" }}
              />
            }
          >
            <CodeEditor
              value={form.content}
              onChange={(content) => setForm({ ...form, content })}
              format={form.format}
              height={420}
            />
          </Suspense>
          <span className="form__hint">
            Live syntax highlighting via CodeMirror — supports JSON, YAML, XML/XSLT,
            and OpenAPI. Anything else stays plain text. Line numbers, code folding,
            bracket matching, and basic autocomplete are on.
          </span>
        </div>

        <div className="form__actions">
          <button type="button" className="btn" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="btn btn--primary" disabled={submitting}>
            {submitting ? "Saving…" : isNew ? "Create schema" : "Save changes"}
          </button>
        </div>
      </form>
    </>
  );
}
