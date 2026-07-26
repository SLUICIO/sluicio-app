// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// NotificationTemplateEditor — the message-template set editor (issue
// #5), shared by the org-default card (Settings → System) and the team
// card (group drawer). Four Liquid fields (email subject/body, Slack
// title/body); empty = inherit down the ladder, and the inherited value
// shows as the placeholder. The variable palette is served by the
// backend (reflected from AlertContext) — inserting appends
// {{ path }} to the focused field. Preview renders the CANDIDATE text
// against a sample firing via the existing preview endpoint.

import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import type { NotificationTemplateSet, TemplateVariable } from "../../api/types";

// CodeMirror is a heavy chunk — pay for it only when this card renders.
// Deliberately a CODE editor, not a WYSIWYG: these fields are Liquid
// over email-safe HTML (table layout, inline styles) and Slack mrkdwn,
// all of which a rich-text editor would rewrite or escape. The "what
// will it look like" half is the live preview below.
const CodeEditor = lazy(() => import("../CodeEditor"));

type Field = "email_subject" | "email_body" | "slack_title" | "slack_body";

const FIELDS: { key: Field; label: string; multiline: boolean; lang?: string; height?: number }[] = [
  { key: "email_subject", label: "Email subject", multiline: false },
  { key: "email_body", label: "Email body (HTML)", multiline: true, lang: "html", height: 260 },
  { key: "slack_title", label: "Slack title", multiline: false },
  { key: "slack_body", label: "Slack body (mrkdwn)", multiline: true, lang: "text", height: 140 },
];

const EMPTY: Record<Field, string> = { email_subject: "", email_body: "", slack_title: "", slack_body: "" };

export default function NotificationTemplateEditor({
  scope,
  groupId,
}: {
  scope: "org" | "group";
  groupId?: string;
}) {
  const [values, setValues] = useState<Record<Field, string>>({ ...EMPTY });
  const [inherited, setInherited] = useState<Record<Field, string>>({ ...EMPTY });
  const [canEdit, setCanEdit] = useState(scope === "org");
  const [variables, setVariables] = useState<TemplateVariable[]>([]);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [focused, setFocused] = useState<Field>("slack_body");
  const [preview, setPreview] = useState<{ kind: "email" | "slack"; body: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [savedAt, setSavedAt] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const loadedFor = useRef<string>("");
  // Live editor handles, so the palette inserts at the CURSOR rather
  // than appending to the end of the document.
  const views = useRef<Partial<Record<Field, { dispatch: (t: unknown) => void; state: { selection: { main: { from: number; to: number } } }; focus: () => void }>>>({});

  useEffect(() => {
    const key = scope + (groupId ?? "");
    if (loadedFor.current === key) return;
    loadedFor.current = key;
    const fromSet = (s: NotificationTemplateSet | undefined): Record<Field, string> => ({
      email_subject: s?.email_subject ?? "",
      email_body: s?.email_body ?? "",
      slack_title: s?.slack_title ?? "",
      slack_body: s?.slack_body ?? "",
    });
    if (scope === "org") {
      api.getOrgNotificationTemplates().then((s) => setValues(fromSet(s))).catch((e) => setError(String((e as Error).message ?? e)));
    } else if (groupId) {
      api
        .getGroupNotificationTemplate(groupId)
        .then((r) => {
          setValues(fromSet(r.template));
          setInherited(fromSet(r.org_default));
          setCanEdit(r.can_edit);
        })
        .catch((e) => setError(String((e as Error).message ?? e)));
    }
    api.templateContextSchema().then((r) => setVariables(r.variables ?? [])).catch(() => {});
  }, [scope, groupId]);

  // Completion entries: the path, its sample rendering as the inline
  // detail, and the description + availability in the info panel.
  const completions = variables.map((v) => ({
    label: v.path.replace(".<key>", ".yourKey"),
    detail: v.sample ? `→ ${v.sample}` : v.type,
    info: v.available && v.available !== "always" ? `${v.description} · ${v.available}` : v.description,
  }));

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      if (scope === "org") {
        await api.putOrgNotificationTemplates(values);
      } else if (groupId) {
        await api.putGroupNotificationTemplate(groupId, values);
      }
      setSavedAt(Date.now());
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const runPreview = async (kind: "email" | "slack", quiet = false) => {
    if (!quiet) setBusy(true);
    try {
      const r = await api.previewAlertTemplate(kind, {
        service: true,
        integration: true,
        service_metadata: true,
        integration_metadata: true,
        check: true,
        email_subject: values.email_subject || inherited.email_subject || undefined,
        email_body: values.email_body || inherited.email_body || undefined,
        slack_title: values.slack_title || inherited.slack_title || undefined,
        slack_body: values.slack_body || inherited.slack_body || undefined,
      });
      setPreview({ kind, body: r.body });
    } catch (e) {
      // A mid-edit template is often invalid — during a quiet refresh
      // keep the last good preview rather than flashing the error.
      if (!quiet) setPreview({ kind, body: String((e as Error).message ?? e) });
    } finally {
      if (!quiet) setBusy(false);
    }
  };

  // A preview that follows you: once opened, it re-renders ~600ms after
  // typing stops. Closing it (Hide preview) stops the refresh.
  useEffect(() => {
    if (!preview) return;
    const kind = preview.kind;
    const t = window.setTimeout(() => {
      void runPreview(kind, true);
    }, 600);
    return () => window.clearTimeout(t);
    // Deliberately keyed on the template text, not the preview object —
    // the refresh itself must not retrigger the effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [values.email_subject, values.email_body, values.slack_title, values.slack_body]);

  const insertVar = (path: string) => {
    const token = `{{ ${path.replace(".<key>", ".yourKey")} }}`;
    const view = views.current[focused];
    if (view) {
      // Replace the selection (or insert at the caret) and put the caret
      // after the token — the editor keeps focus so typing continues.
      const { from, to } = view.state.selection.main;
      view.dispatch({
        changes: { from, to, insert: token },
        selection: { anchor: from + token.length },
      });
      view.focus();
      return;
    }
    setValues((v) => ({ ...v, [focused]: v[focused] + token }));
  };

  return (
    // Divider styling only in the group drawer, where the editor sits
    // below the group's other sections; at org scope it fills its own
    // System sub-tab.
    <section style={scope === "group" ? { marginTop: 24, borderTop: "1px solid var(--border)", paddingTop: 18 } : undefined}>
      <h3 style={{ fontSize: 14, fontWeight: 600, margin: "0 0 4px" }}>Notification templates</h3>
      <p className="muted" style={{ fontSize: 13, lineHeight: 1.55, margin: "0 0 12px" }}>
        {scope === "org"
          ? "The org's default look for alert emails and Slack messages. Teams can override any field on their group; a rule can override inline. Empty fields inherit the cell/built-in defaults."
          : "This team's look for alert notifications on rules it owns. Empty fields inherit the org default (shown greyed). Liquid syntax; a saved template that fails to render falls back — it never blocks an alert."}
      </p>

      {error && <div className="alert alert--error" style={{ marginBottom: 10 }}>{error}</div>}

      <div style={{ display: "flex", flexDirection: "column", gap: 8, maxWidth: 680 }}>
        {FIELDS.map((f) =>
          f.multiline ? (
            <div key={f.key} className="form__label" onFocus={() => setFocused(f.key)}>
              {f.label}
              <div className="muted" style={{ fontSize: 11.5, margin: "2px 0 4px" }}>
                {values[f.key]
                  ? " "
                  : inherited[f.key]
                    ? `Empty — inherits the org default: ${inherited[f.key].slice(0, 80)}…`
                    : "Empty — inherits the built-in default."}
              </div>
              <Suspense
                fallback={
                  <textarea
                    className="svc-textarea"
                    style={{ fontSize: 12.5, minHeight: f.height, fontFamily: "var(--font-mono, monospace)" }}
                    value={values[f.key]}
                    readOnly
                  />
                }
              >
                <CodeEditor
                  value={values[f.key]}
                  onChange={(v) => setValues((prev) => ({ ...prev, [f.key]: v }))}
                  format={f.lang ?? "text"}
                  height={f.height ?? 160}
                  readOnly={!canEdit}
                  showToolbar={false}
                  liquidCompletions={completions}
                  onReady={(view) => {
                    views.current[f.key] = view as unknown as (typeof views.current)[Field];
                  }}
                />
              </Suspense>
            </div>
          ) : (
            <label key={f.key} className="form__label">
              {f.label}
              <input
                className="search__input"
                style={{ fontSize: 13 }}
                placeholder={inherited[f.key] ? `Inherited: ${inherited[f.key].slice(0, 120)}` : "Inherits the default"}
                value={values[f.key]}
                disabled={!canEdit}
                onFocus={() => setFocused(f.key)}
                onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
              />
            </label>
          ),
        )}

        <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
          {canEdit && (
            <button type="button" className="btn btn--primary" onClick={save} disabled={busy}>
              {busy ? "Saving…" : "Save templates"}
            </button>
          )}
          <button type="button" className="btn" onClick={() => (preview?.kind === "slack" ? setPreview(null) : runPreview("slack"))} disabled={busy}>
            {preview?.kind === "slack" ? "Hide preview" : "Preview Slack"}
          </button>
          <button type="button" className="btn" onClick={() => (preview?.kind === "email" ? setPreview(null) : runPreview("email"))} disabled={busy}>
            {preview?.kind === "email" ? "Hide preview" : "Preview email"}
          </button>
          {preview && <span className="muted" style={{ fontSize: 11.5 }}>live — updates as you type</span>}
          <button type="button" className="btn btn--link" onClick={() => setPaletteOpen((o) => !o)}>
            {paletteOpen ? "Hide variables" : "Variables…"}
          </button>
          {savedAt > 0 && <span className="muted" style={{ fontSize: 12 }}>Saved ✓</span>}
        </div>

        {paletteOpen && (
          <div style={{ border: "1px solid var(--border)", borderRadius: 6, padding: 8, maxHeight: 220, overflow: "auto" }}>
            <div className="muted" style={{ fontSize: 11.5, marginBottom: 6 }}>
              Click to append to the focused field ({FIELDS.find((f) => f.key === focused)?.label}).
            </div>
            {variables.map((v) => (
              <div key={v.path} style={{ display: "flex", gap: 8, alignItems: "baseline", padding: "2px 0" }}>
                <button type="button" className="btn btn--link mono" style={{ padding: 0, fontSize: 12 }} onClick={() => insertVar(v.path)}>
                  {v.path}
                </button>
                <span className="muted" style={{ fontSize: 11.5 }}>
                  {v.sample && <span className="mono" style={{ color: "var(--ink-2)" }}>→ {v.sample}</span>}
                  {v.sample ? " · " : ""}
                  {v.description}
                  {v.available !== "always" ? ` · ${v.available}` : ""}
                </span>
              </div>
            ))}
          </div>
        )}

        {preview?.kind === "email" && (
          <iframe title="email preview" sandbox="" style={{ width: "100%", height: 320, border: "1px solid var(--border)", borderRadius: 6, background: "#fff" }} srcDoc={preview.body} />
        )}
        {preview?.kind === "slack" && (
          <pre style={{ border: "1px solid var(--border)", borderRadius: 6, padding: 10, fontSize: 12.5, whiteSpace: "pre-wrap", background: "var(--surface-2)" }}>
            {preview.body}
          </pre>
        )}
      </div>
    </section>
  );
}
