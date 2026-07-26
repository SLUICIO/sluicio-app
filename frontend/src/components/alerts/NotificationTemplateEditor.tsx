// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// NotificationTemplateEditor — the message-template set editor (issue
// #5), shared by the org-default card (Settings → System) and the team
// card (group drawer). Four Liquid fields (email subject/body, Slack
// title/body); empty = inherit down the ladder, and the inherited value
// shows as the placeholder. The variable palette is served by the
// backend (reflected from AlertContext) — inserting writes {{ path }}
// at the cursor. Preview renders the CANDIDATE text against a sample
// firing via the existing preview endpoint.
//
// LAYOUT: one channel at a time (Email / Slack tabs), with preview and
// the variable palette sharing a sticky SIDE pane. Stacking all four
// fields put the palette a full email body below the subject line — on a
// 14" screen you edited one thing and scrolled to consult another. Two
// fields plus a side pane fits without scrolling.
//
// Save still writes all four fields, so the hidden channel must not go
// unnoticed: its tab carries a warning marker when it has unknown
// variables, and the pre-save confirm lists issues from BOTH channels.

import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import type { NotificationTemplateSet, TemplateVariable } from "../../api/types";
import { unknownVariables } from "../../lib/liquidVars";

// CodeMirror is a heavy chunk — pay for it only when this card renders.
// Deliberately a CODE editor, not a WYSIWYG: these fields are Liquid
// over email-safe HTML (table layout, inline styles) and Slack mrkdwn,
// all of which a rich-text editor would rewrite or escape. The "what
// will it look like" half is the live preview below.
const CodeEditor = lazy(() => import("../CodeEditor"));

type Field = "email_subject" | "email_body" | "slack_title" | "slack_body";
type Channel = "email" | "slack";

const FIELDS: { key: Field; channel: Channel; label: string; multiline: boolean; lang?: string; height?: number }[] = [
  { key: "email_subject", channel: "email", label: "Email subject", multiline: false },
  { key: "email_body", channel: "email", label: "Email body (HTML)", multiline: true, lang: "html", height: 320 },
  { key: "slack_title", channel: "slack", label: "Slack title", multiline: false },
  { key: "slack_body", channel: "slack", label: "Slack body (mrkdwn)", multiline: true, lang: "text", height: 220 },
];

const CHANNELS: { key: Channel; label: string; body: Field }[] = [
  { key: "email", label: "Email template", body: "email_body" },
  { key: "slack", label: "Slack template", body: "slack_body" },
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
  // The built-in templates, so an empty field can be seeded instead of
  // written from scratch.
  const [defaults, setDefaults] = useState<Record<string, string>>({});
  const [channel, setChannel] = useState<Channel>("email");
  // The side pane shows one helper at a time; null closes it and the
  // fields take the full width. Preview is the DEFAULT — a template
  // editor whose output you have to ask to see is just a text box, and
  // the refresh effect below self-starts from this state. Variables
  // borrows the pane; closing it entirely stays available for anyone who
  // wants the full width to edit in.
  const [pane, setPane] = useState<"preview" | "variables" | null>("preview");
  const [focused, setFocused] = useState<Field>("email_body");
  // Preview body only — its kind always follows the active channel, so
  // switching tabs re-renders rather than showing the other channel.
  const [previewBody, setPreviewBody] = useState<string | null>(null);
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
    api
      .templateContextSchema()
      .then((r) => {
        setVariables(r.variables ?? []);
        setDefaults(r.defaults ?? {});
      })
      .catch(() => {});
  }, [scope, groupId]);

  // Completion entries: the path, its sample rendering as the inline
  // detail, and the description + availability in the info panel.
  const completions = variables.map((v) => ({
    label: v.path.replace(".<key>", ".yourKey"),
    detail: v.sample ? `→ ${v.sample}` : v.type,
    info: v.available && v.available !== "always" ? `${v.description} · ${v.available}` : v.description,
  }));

  // Unknown variable references per field, checked against the schema.
  // Warnings only: Liquid renders an unknown path as empty, and the
  // delivery ladder falls through on a render error — so this catches
  // typos without ever standing between someone and their save.
  const knownPaths = variables.map((v) => v.path);
  const issuesByField = Object.fromEntries(
    FIELDS.map((f) => [f.key, unknownVariables(values[f.key], knownPaths)]),
  ) as Record<Field, ReturnType<typeof unknownVariables>>;
  const issues = FIELDS.map((f) => ({ field: f, found: issuesByField[f.key] })).filter((i) => i.found.length > 0);
  // Per-channel issue counts drive the tab markers — the point is that a
  // typo on the tab you're NOT looking at still gets saved.
  const issuesByChannel = (c: Channel) =>
    FIELDS.filter((f) => f.channel === c).reduce((n, f) => n + (issuesByField[f.key] ?? []).length, 0);

  const save = async (force = false) => {
    if (!force && issues.length > 0) {
      const detail = issues
        .flatMap((i) => i.found.map((u) => `• ${i.field.label} line ${u.line}: {{ ${u.path} }}${u.suggestion ? ` — did you mean ${u.suggestion}?` : ""}`))
        .join("\n");
      const ok = window.confirm(
        `These variables aren't in the schema and will render as empty:\n\n${detail}\n\n` +
          `That's fine if they come from a {% for %} or {% assign %} this check can't see.\n\nSave anyway?`,
      );
      if (!ok) return;
    }
    await doSave();
  };

  const doSave = async () => {
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

  const runPreview = async (kind: Channel, quiet = false) => {
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
      setPreviewBody(r.body);
    } catch (e) {
      // A mid-edit template is often invalid — during a quiet refresh
      // keep the last good preview rather than flashing the error.
      if (!quiet) setPreviewBody(String((e as Error).message ?? e));
    } finally {
      if (!quiet) setBusy(false);
    }
  };

  // A preview that follows you: once opened, it re-renders ~600ms after
  // typing stops, and immediately when you switch channel tabs. Closing
  // the pane stops the refresh.
  useEffect(() => {
    if (pane !== "preview") return;
    const t = window.setTimeout(() => {
      void runPreview(channel, true);
    }, 600);
    return () => window.clearTimeout(t);
    // Deliberately keyed on the template text + channel, not on the
    // preview body — the refresh itself must not retrigger the effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [values.email_subject, values.email_body, values.slack_title, values.slack_body, channel, pane]);

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

      <div className="svc-tabs" style={{ marginBottom: 12 }}>
        {CHANNELS.map((c) => {
          const n = issuesByChannel(c.key);
          return (
            <button
              key={c.key}
              type="button"
              className={`svc-tab ${channel === c.key ? "on" : ""}`}
              onClick={() => {
                setChannel(c.key);
                // Insertions target this channel's body from the moment
                // you land, before anything has been focused.
                setFocused(c.body);
                // Drop the other channel's render rather than showing it
                // for the ~600ms until the refresh lands (an email body
                // in the Slack <pre> is a confusing flash).
                setPreviewBody(null);
              }}
            >
              {c.label}
              {n > 0 && (
                <span
                  title={`${n} unknown variable${n === 1 ? "" : "s"} in this template`}
                  style={{ marginLeft: 6, color: "var(--warn, #b26a00)" }}
                >
                  ⚠
                </span>
              )}
            </button>
          );
        })}
      </div>

      <div className={pane ? "tmpl-split" : undefined}>
      <div style={{ display: "flex", flexDirection: "column", gap: 8, maxWidth: pane ? undefined : 680, minWidth: 0 }}>
        {FIELDS.filter((f) => f.channel === channel).map((f) =>
          f.multiline ? (
            <div key={f.key} className="form__label" onFocus={() => setFocused(f.key)}>
              {f.label}
              <div className="muted" style={{ fontSize: 11.5, margin: "2px 0 4px", display: "flex", gap: 8, alignItems: "baseline", flexWrap: "wrap" }}>
                <span>
                  {values[f.key]
                    ? " "
                    : inherited[f.key]
                      ? `Empty — inherits the org default: ${inherited[f.key].slice(0, 80)}…`
                      : "Empty — inherits the built-in default."}
                </span>
                {canEdit && defaults[f.key] !== undefined && (
                  <button
                    type="button"
                    className="btn btn--link"
                    style={{ padding: 0, fontSize: 11.5 }}
                    title="Load the built-in template into this field as a starting point"
                    onClick={() => {
                      if (values[f.key] && !window.confirm("Replace what's in this field with the built-in template?")) return;
                      setValues((v) => ({ ...v, [f.key]: defaults[f.key] }));
                    }}
                  >
                    Start from the built-in template
                  </button>
                )}
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
              {(issuesByField[f.key] ?? []).length > 0 && (
                <div className="alert alert--warn" style={{ marginTop: 4, fontSize: 12, padding: "6px 8px" }}>
                  {(issuesByField[f.key] ?? []).map((u) => (
                    <div key={`${u.path}-${u.line}`}>
                      Line {u.line}: <code>{`{{ ${u.path} }}`}</code> isn't a known variable — renders empty
                      {u.suggestion ? (
                        <>
                          {" · "}
                          <button
                            type="button"
                            className="btn btn--link"
                            style={{ padding: 0, fontSize: 12 }}
                            onClick={() =>
                              setValues((v) => ({ ...v, [f.key]: v[f.key].split(u.path).join(u.suggestion!) }))
                            }
                          >
                            use {u.suggestion}
                          </button>
                        </>
                      ) : null}
                    </div>
                  ))}
                </div>
              )}
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
            <button type="button" className="btn btn--primary" onClick={() => save()} disabled={busy}>
              {busy ? "Saving…" : "Save templates"}
            </button>
          )}
          <button
            type="button"
            className="btn"
            onClick={() => {
              if (pane === "preview") {
                setPane(null);
                return;
              }
              setPane("preview");
              void runPreview(channel);
            }}
            disabled={busy}
          >
            {pane === "preview" ? "Hide preview" : "Preview"}
          </button>
          <button type="button" className="btn" onClick={() => setPane((p) => (p === "variables" ? null : "variables"))}>
            {pane === "variables" ? "Hide variables" : "Variables…"}
          </button>
          {savedAt > 0 && <span className="muted" style={{ fontSize: 12 }}>Saved ✓</span>}
        </div>
      </div>

      {pane && (
        <div className="tmpl-split__side">
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 6, gap: 8 }}>
            {/* Flip between the two helpers without going back to the
                toolbar — they occupy the same pane. */}
            <span style={{ display: "flex", gap: 10, alignItems: "baseline" }}>
              {(["preview", "variables"] as const).map((p) => (
                <button
                  key={p}
                  type="button"
                  className="btn btn--link"
                  style={{ padding: 0, fontSize: 12, fontWeight: pane === p ? 600 : 400 }}
                  onClick={() => {
                    setPane(p);
                    if (p === "preview" && previewBody === null) void runPreview(channel);
                  }}
                >
                  {p === "preview" ? (channel === "email" ? "Email preview" : "Slack preview") : "Variables"}
                </button>
              ))}
              <span className="muted" style={{ fontSize: 11.5 }}>
                {pane === "preview" ? "sample firing · live" : `insert into ${FIELDS.find((f) => f.key === focused)?.label}`}
              </span>
            </span>
            <button type="button" className="btn btn--link" style={{ padding: 0, fontSize: 12 }} onClick={() => setPane(null)}>
              Close
            </button>
          </div>

          {pane === "variables" ? (
            <div className="tmpl-preview-body" style={{ border: "1px solid var(--border)", borderRadius: 6, padding: 8 }}>
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
          ) : channel === "email" ? (
            <iframe
              title="email preview"
              sandbox=""
              className="tmpl-preview-body"
              style={{ width: "100%", minHeight: 320, border: "1px solid var(--border)", borderRadius: 6, background: "#fff" }}
              srcDoc={previewBody ?? ""}
            />
          ) : (
            <pre
              className="tmpl-preview-body"
              style={{ border: "1px solid var(--border)", borderRadius: 6, padding: 10, fontSize: 12.5, whiteSpace: "pre-wrap", background: "var(--surface-2)", margin: 0 }}
            >
              {previewBody ?? ""}
            </pre>
          )}
        </div>
      )}
      </div>
    </section>
  );
}
