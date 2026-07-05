// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Maps admin page. A Map describes a data transformation — XSLT, jq,
// JSONata, Liquid, Mustache, Handlebars, etc. — and optionally pins
// the input ("from") and output ("to") schemas it operates against,
// so the table can show the relationship at a glance and the editor
// can offer the right schemas to read against. The list page reads
// from /api/v1/maps; the editor opens a CodeMirror surface for the
// transformation body.

import { FormEvent, lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { MapDoc, MapExecuteResponse, MapInput, Schema, SchemaRef } from "../api/types";
import ContentViewerDrawer from "../components/ContentViewerDrawer";
import { EditDrawer } from "../components/primitives";
import { usePageTitle } from "../lib/usePageTitle";
import { useCurrentUser } from "../lib/useCurrentUser";

// CodeMirror is ~150 KB gzip — lazy-loaded so the maps list itself
// stays cheap; the bundle hit is paid only when the editor opens.
const CodeEditor = lazy(() => import("../components/CodeEditor"));

// Transformation languages we offer in the format dropdown. Free-text
// on the wire — anything else is accepted and falls back to plain
// text + no highlighting in the editor.
const FORMATS = ["xslt", "jq", "jsonata", "liquid", "mustache", "handlebars", "other"];

const EMPTY: MapInput = {
  name: "",
  version: "",
  description: "",
  format: "xslt",
  content: "",
  from_schema_id: "",
  to_schema_id: "",
};

export default function MapsPage() {
  usePageTitle("Maps");
  // Editing maps is an editor+ action; viewers are read-only (they can
  // still open the read-only content viewer). The cell-api enforces the
  // same with RequireRole(CanWrite).
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  const canDelete = can("integration.delete");

  const [maps, setMaps] = useState<MapDoc[]>([]);
  // Schemas are needed by the editor so the from/to pickers can list
  // candidates. We load them alongside the maps list and pass into
  // the editor as a prop rather than refetching per-edit.
  const [schemas, setSchemas] = useState<Schema[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<null | "new" | MapDoc>(null);
  // The map whose content is being viewed in the read-only drawer.
  // Independent of `editing`; the table's name button opens this, the
  // row's Edit button opens the editor.
  const [viewing, setViewing] = useState<MapDoc | null>(null);
  // Lifted from MapEditor so the wrapping EditDrawer can guard close
  // with a confirm prompt when the user has typed unsaved content.
  // Maps editors hold a CodeMirror Content body plus a Test panel
  // sample-input editor — both expensive to retype on a stray Esc.
  const [editorDirty, setEditorDirty] = useState(false);

  const refresh = () => {
    setLoading(true);
    setError(null);
    Promise.all([api.listMaps(), api.listSchemas()])
      .then(([m, s]) => {
        setMaps(m.maps ?? []);
        setSchemas(s.schemas ?? []);
      })
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  };

  useEffect(refresh, []);

  const onSaved = (saved: MapDoc, isNew: boolean) => {
    setMaps((curr) =>
      isNew
        ? [...curr, saved].sort((a, b) => a.name.localeCompare(b.name))
        : curr.map((m) => (m.id === saved.id ? saved : m)),
    );
    setEditing(null);
  };

  const onDelete = async (m: MapDoc) => {
    if (!confirm(`Delete "${m.name}${m.version ? " " + m.version : ""}"?`)) return;
    try {
      await api.deleteMap(m.id);
      setMaps((curr) => curr.filter((x) => x.id !== m.id));
    } catch (e) {
      setError(`Delete failed: ${String((e as Error).message ?? e)}`);
    }
  };

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Maps</h1>
          <p className="page__subtitle">
            Data transformations between schemas — XSLT, jq, JSONata,
            Liquid, Mustache, Handlebars. Each map can pin an input
            ("from") and output ("to") schema, so you can see at a
            glance which shape gets turned into which.
          </p>
        </div>
        <div>
          {canWrite && editing === null && (
            <button type="button" className="btn btn--primary" onClick={() => setEditing("new")}>
              + New map
            </button>
          )}
        </div>
      </div>

      {error && <div className="alert alert--error">{error}</div>}
      {!canWrite && (
        <div className="muted" style={{ fontSize: 13, marginBottom: 12 }}>
          You have read-only access to maps. Ask an{" "}
          <strong>integration contributor</strong> or <strong>org admin</strong> to add or change them.
        </div>
      )}

      {canWrite && editing && (
        <EditDrawer
          title={editing === "new" ? "New map" : `Edit · ${editing.name}`}
          width="wide"
          onClose={() => setEditing(null)}
          dirty={editorDirty}
        >
          <MapEditor
            initial={editing === "new" ? null : editing}
            schemas={schemas}
            existingNames={new Set(
              maps
                .filter((m) => editing === "new" || m.id !== editing.id)
                .map((m) => `${m.name} ${m.version}`),
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
      ) : maps.length === 0 ? (
        <div className="placeholder">
          {canWrite ? (
            <>
              No maps yet. Click <b>+ New map</b> to define one — for example,
              an XSLT that converts CreateOrderRequest to a downstream OrderEvent.
            </>
          ) : (
            <>No maps have been defined yet.</>
          )}
        </div>
      ) : (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Version</th>
                <th>Format</th>
                <th>From → To</th>
                <th>Description</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {maps.map((m) => (
                <MapRow
                  key={m.id}
                  map={m}
                  canWrite={canWrite}
                  canDelete={canDelete}
                  onView={() => setViewing(m)}
                  onEdit={() => setEditing(m)}
                  onDelete={() => onDelete(m)}
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
// table stays compact and the body is easy to copy whole.
interface RowProps {
  map: MapDoc;
  canWrite: boolean;
  canDelete: boolean;
  onView: () => void;
  onEdit: () => void;
  onDelete: () => void;
}

function MapRow({ map, canWrite, canDelete, onView, onEdit, onDelete }: RowProps) {
  return (
    <tr>
      <td>
        <button
          type="button"
          className="font-medium"
          onClick={onView}
          style={{ background: "transparent", border: 0, padding: 0, cursor: "pointer" }}
          title="View map content"
        >
          {map.name} <span className="muted" style={{ fontSize: 11 }}>⤢</span>
        </button>
      </td>
      <td className="mono">
        {map.version || <span className="muted">—</span>}
      </td>
      <td className="mono">{map.format}</td>
      <td>
        <SchemaPair from={map.from_schema} to={map.to_schema} />
      </td>
      <td>
        {map.description ? map.description : <span className="muted">—</span>}
      </td>
      <td className="num">
        {canWrite && <button className="btn btn--link" onClick={onEdit}>Edit</button>}
        {canDelete && <button className="btn btn--link" style={{ color: "var(--err)" }} onClick={onDelete}>Delete</button>}
        {!canWrite && !canDelete && <span className="muted">—</span>}
      </td>
    </tr>
  );
}

// SchemaPair renders the from → to relationship as two clickable chips
// separated by an arrow. Either side may be missing; in that case we
// show a muted "—" so the column still aligns.
function SchemaPair({ from, to }: { from?: SchemaRef | null; to?: SchemaRef | null }) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
      <SchemaPill schema={from ?? null} side="from" />
      <span className="muted" aria-hidden>→</span>
      <SchemaPill schema={to ?? null} side="to" />
    </div>
  );
}

function SchemaPill({ schema, side }: { schema: SchemaRef | null; side: "from" | "to" }) {
  if (!schema) {
    return (
      <span className="muted" style={{ fontSize: 11 }} title={`No ${side}-schema set`}>—</span>
    );
  }
  const label = schema.version ? `${schema.name} · ${schema.version}` : schema.name;
  return (
    <Link
      to={`/schemas`}
      className="badge"
      title={`${label} · ${schema.format}`}
      style={{
        textDecoration: "none",
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        background:
          side === "from"
            ? "color-mix(in oklab, var(--ok) 14%, transparent)"
            : "color-mix(in oklab, var(--primary) 14%, transparent)",
        borderColor:
          side === "from"
            ? "color-mix(in oklab, var(--ok) 30%, transparent)"
            : "color-mix(in oklab, var(--primary) 30%, transparent)",
      }}
    >
      <span style={{ fontSize: 10, fontWeight: 600, textTransform: "uppercase", letterSpacing: 0.4 }}>
        {side}
      </span>
      <span className="mono">{label}</span>
    </Link>
  );
}

// ── editor (create / update) ──────────────────────────────────────────
interface EditorProps {
  initial: MapDoc | null;
  schemas: Schema[];
  existingNames: Set<string>;
  onCancel: () => void;
  onSaved: (saved: MapDoc, isNew: boolean) => void;
  // Called whenever the editor's form drifts from / returns to the
  // initial snapshot, so the wrapping EditDrawer can guard close.
  onDirtyChange?: (dirty: boolean) => void;
}

function MapEditor({ initial, schemas, existingNames, onCancel, onSaved, onDirtyChange }: EditorProps) {
  const isNew = !initial;
  // See SchemasPage for the same captured-once-on-mount pattern. The
  // drawer remounts MapEditor when `editing` changes, so a stable
  // baseline matches the user's intuition of "this form opened in
  // this state".
  const initialSnapshot = useRef<string>("");
  if (initialSnapshot.current === "") {
    initialSnapshot.current = JSON.stringify(
      initial
        ? {
            name: initial.name,
            version: initial.version,
            description: initial.description,
            format: initial.format,
            content: initial.content,
            from_schema_id: initial.from_schema_id ?? "",
            to_schema_id: initial.to_schema_id ?? "",
          }
        : EMPTY,
    );
  }
  const [form, setForm] = useState<MapInput>(
    initial
      ? {
          name: initial.name,
          version: initial.version,
          description: initial.description,
          format: initial.format,
          content: initial.content,
          from_schema_id: initial.from_schema_id ?? "",
          to_schema_id: initial.to_schema_id ?? "",
        }
      : { ...EMPTY },
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Push the dirty signal up to the drawer whenever the form deviates
  // from / matches the initial snapshot.
  useEffect(() => {
    onDirtyChange?.(JSON.stringify(form) !== initialSnapshot.current);
  }, [form, onDirtyChange]);

  // Schemas sorted for the dropdown — alphabetical by name, then
  // version for stable ordering inside same-named groups.
  const schemaOptions = useMemo(
    () =>
      [...schemas].sort((a, b) => {
        const n = a.name.localeCompare(b.name);
        return n !== 0 ? n : a.version.localeCompare(b.version);
      }),
    [schemas],
  );

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    const name = form.name.trim();
    const version = form.version.trim();
    if (!name) return setError("Name is required.");
    const key = `${name} ${version}`;
    if (existingNames.has(key))
      return setError(
        version
          ? `${name} ${version} already exists. Pick a different version.`
          : `${name} already exists. Add a version or rename.`,
      );

    setSubmitting(true);
    try {
      const payload: MapInput = { ...form, name, version };
      const saved = isNew
        ? await api.createMap(payload)
        : await api.updateMap(initial!.id, payload);
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
              placeholder="OrderToOrderEvent"
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
            <span className="form__hint">Optional. Two maps with the same name + different versions can co-exist.</span>
          </label>
        </div>

        <div className="form__row">
          <label className="form__label">
            From schema
            <select
              className="toolbar__select"
              value={form.from_schema_id}
              onChange={(e) => setForm({ ...form, from_schema_id: e.target.value })}
            >
              <option value="">— none —</option>
              {schemaOptions.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}{s.version ? ` · ${s.version}` : ""} ({s.format})
                </option>
              ))}
            </select>
            <span className="form__hint">Input shape this map reads from. Optional.</span>
          </label>
          <label className="form__label">
            To schema
            <select
              className="toolbar__select"
              value={form.to_schema_id}
              onChange={(e) => setForm({ ...form, to_schema_id: e.target.value })}
            >
              <option value="">— none —</option>
              {schemaOptions.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}{s.version ? ` · ${s.version}` : ""} ({s.format})
                </option>
              ))}
            </select>
            <span className="form__hint">Output shape this map produces. Optional.</span>
          </label>
        </div>

        <div className="form__row">
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
            <span className="form__hint">Drives syntax highlighting and the Format-document button.</span>
          </label>
          <div /> {/* spacer to keep the row's grid alignment */}
        </div>

        <label className="form__label">
          Description
          <textarea
            className="svc-textarea"
            rows={2}
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            placeholder="What this transformation does and when it's used."
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
            Live syntax highlighting via CodeMirror. XSLT lights up as XML;
            JSON-flavoured transformations (jq, JSONata) and templating
            languages stay plain text. Use Format document above the
            editor to canonicalise indentation.
          </span>
        </div>

        <MapTestPanel mapId={initial?.id ?? null} format={form.format} />

        <div className="form__actions">
          <button type="button" className="btn" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="btn btn--primary" disabled={submitting}>
            {submitting ? "Saving…" : isNew ? "Create map" : "Save changes"}
          </button>
        </div>
      </form>
    </>
  );
}

// ── Test panel ─────────────────────────────────────────────────────────
//
// Sits inside the editor below the Content area. Lets the user paste a
// sample input, click Run, and see the transformation output plus
// validation status for both input and output against the pinned
// schemas. Runs against the saved version of the map — the user must
// save before re-testing edits.
//
// XSLT and Liquid are supported on the backend in v1. Other formats
// disable the Run button and explain why.

interface TestPanelProps {
  mapId: string | null;
  format: string;
}

const RUNTIME_FORMATS = new Set(["xslt", "liquid"]);

// testIOFormat derives the input + output document formats for the
// Test panel from the map's transformation format, so each CodeEditor
// in the panel gets the right syntax-highlighting language.
//
//   xslt   : input = XML,  output = XML
//   liquid : input = JSON, output = plain text (Liquid renders to text)
//   other  : both fall back to plain text (no highlighting)
//
// Returning "" for unsupported formats tells CodeEditor to render
// plain text — matches its existing default-case behaviour.
function testIOFormat(mapFormat: string): { input: string; output: string } {
  switch (mapFormat.toLowerCase()) {
    case "xslt":
      return { input: "xml", output: "xml" };
    case "liquid":
      return { input: "json", output: "" };
    default:
      return { input: "", output: "" };
  }
}

function MapTestPanel({ mapId, format }: TestPanelProps) {
  const [input, setInput] = useState("");
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<MapExecuteResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  const isNew = mapId === null;
  const formatSupported = RUNTIME_FORMATS.has(format.toLowerCase());
  const canRun = !isNew && formatSupported && !running;
  const { input: inputFormat, output: outputFormat } = testIOFormat(format);

  const disabledReason = isNew
    ? "Save the map first to enable testing."
    : !formatSupported
      ? `Test runtime not available for ${format} yet — supported: xslt, liquid.`
      : null;

  const run = async () => {
    if (!mapId) return;
    setRunning(true);
    setError(null);
    setResult(null);
    try {
      const r = await api.executeMap(mapId, { input });
      setResult(r);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setRunning(false);
    }
  };

  return (
    <fieldset
      style={{
        border: "1px solid var(--border)",
        borderRadius: 8,
        padding: 12,
        margin: 0,
      }}
    >
      <legend
        className="muted"
        style={{
          fontSize: 11,
          padding: "0 6px",
          textTransform: "uppercase",
          letterSpacing: 0.6,
        }}
      >
        Test
      </legend>

      {disabledReason && (
        <div className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
          {disabledReason}
        </div>
      )}

      {/* NB: NOT wrapped in <div className="form__row">. That class is
          a 2-column grid used to pair fields like Name + Version;
          dropping a single full-width editor into it constrains the
          editor to one column (half the panel width) which made the
          CodeMirror surface render tiny.
          Also: a plain <div> rather than <label> wraps the editor —
          wrapping CodeMirror in <label> makes a click on the editor
          surface trigger the label's default-focus behaviour, which
          fights with CodeMirror's own click-to-position-cursor
          handler and deselects the editor on every click. The
          Content editor higher up in this file uses the same
          full-width, div-not-label pattern for both reasons. */}
      <div className="form__label">
        <span>
          Sample input <span className="muted" style={{ fontWeight: 400 }}>· {inputFormat || "text"}</span>
        </span>
        <Suspense
          fallback={
            <textarea
              className="svc-textarea mono"
              rows={8}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              spellCheck={false}
              style={{ whiteSpace: "pre", overflowWrap: "normal", overflowX: "auto" }}
            />
          }
        >
          <CodeEditor
            value={input}
            onChange={setInput}
            format={inputFormat}
            height={220}
          />
        </Suspense>
        <span className="form__hint">
          {format === "liquid"
            ? "JSON object whose top-level keys become Liquid variables, e.g. {\"name\":\"Robert\",\"orders\":[1,2,3]}."
            : format === "xslt"
              ? "XML document the XSLT will transform."
              : "Sample input for the transformation."}
          {" "}Tests run against the saved version of this map — save your edits before re-testing.
        </span>
      </div>

      <div style={{ display: "flex", gap: 8, marginTop: 6 }}>
        <button
          type="button"
          className="btn btn--primary"
          onClick={run}
          disabled={!canRun}
          title={disabledReason ?? "Run the map against the sample input"}
        >
          {running ? "Running…" : "Run"}
        </button>
        {result && (
          <button
            type="button"
            className="btn"
            onClick={() => {
              setResult(null);
              setError(null);
            }}
          >
            Clear result
          </button>
        )}
      </div>

      {error && (
        <div className="alert alert--error" style={{ marginTop: 8 }}>
          {error}
        </div>
      )}

      {result && (
        <div style={{ marginTop: 12, display: "flex", flexDirection: "column", gap: 12 }}>
          {result.engine_error && (
            <div className="alert alert--error">
              <div style={{ fontWeight: 600, marginBottom: 4 }}>Runtime error</div>
              <pre
                className="mono"
                style={{
                  whiteSpace: "pre-wrap",
                  margin: 0,
                  fontSize: 12,
                }}
              >
                {result.engine_error}
              </pre>
            </div>
          )}

          <div>
            <div
              className="muted"
              style={{
                fontSize: 11,
                marginBottom: 4,
                textTransform: "uppercase",
                letterSpacing: 0.6,
              }}
            >
              Output <span style={{ textTransform: "none", letterSpacing: 0 }}>· {outputFormat || "text"}</span>
            </div>
            <Suspense
              fallback={
                <textarea
                  className="svc-textarea mono"
                  rows={Math.min(20, Math.max(6, result.output.split("\n").length))}
                  value={result.output}
                  readOnly
                  spellCheck={false}
                  style={{ whiteSpace: "pre", overflowWrap: "normal", overflowX: "auto" }}
                />
              }
            >
              <CodeEditor
                value={result.output}
                onChange={() => { /* read-only */ }}
                format={outputFormat}
                height={Math.min(420, Math.max(120, result.output.split("\n").length * 18 + 24))}
                readOnly
              />
            </Suspense>
          </div>

          <ValidationBadge label="Input" v={result.input_validation} />
          <ValidationBadge label="Output" v={result.output_validation} />
        </div>
      )}
    </fieldset>
  );
}

// ValidationBadge renders one validation summary as a coloured strip:
// green for valid, red with bullets for invalid, muted for skipped.
function ValidationBadge({ label, v }: { label: string; v: MapExecuteResponse["input_validation"] }) {
  let status: "pass" | "fail" | "skip";
  let title: string;
  if (v.skipped) {
    status = "skip";
    title = v.skip_reason ?? "skipped";
  } else if (v.valid) {
    status = "pass";
    title = `valid against ${v.schema_name ?? "schema"}`;
  } else {
    status = "fail";
    title = `invalid against ${v.schema_name ?? "schema"}`;
  }

  const bg =
    status === "pass"
      ? "color-mix(in oklab, var(--ok) 14%, transparent)"
      : status === "fail"
        ? "color-mix(in oklab, var(--err-ink, #ef4444) 14%, transparent)"
        : "var(--surface-3)";
  const border =
    status === "pass"
      ? "color-mix(in oklab, var(--ok) 30%, transparent)"
      : status === "fail"
        ? "color-mix(in oklab, var(--err-ink, #ef4444) 30%, transparent)"
        : "var(--border)";

  return (
    <div
      style={{
        background: bg,
        border: `1px solid ${border}`,
        borderRadius: 6,
        padding: 8,
        fontSize: 12,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span style={{ fontWeight: 600 }}>{label}</span>
        <span className="muted" style={{ fontSize: 11 }}>·</span>
        <span>{status === "pass" ? "✓" : status === "fail" ? "✗" : "—"}&nbsp;{title}</span>
      </div>
      {status === "fail" && v.errors && v.errors.length > 0 && (
        <ul style={{ margin: "6px 0 0 0", paddingLeft: 18 }}>
          {v.errors.slice(0, 8).map((e, i) => (
            <li key={i} className="mono" style={{ fontSize: 11 }}>
              {e}
            </li>
          ))}
          {v.errors.length > 8 && (
            <li className="muted" style={{ fontSize: 11 }}>
              … and {v.errors.length - 8} more
            </li>
          )}
        </ul>
      )}
    </div>
  );
}
