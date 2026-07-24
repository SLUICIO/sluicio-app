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

import { useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import type { NotificationTemplateSet, TemplateVariable } from "../../api/types";

type Field = "email_subject" | "email_body" | "slack_title" | "slack_body";

const FIELDS: { key: Field; label: string; multiline: boolean }[] = [
  { key: "email_subject", label: "Email subject", multiline: false },
  { key: "email_body", label: "Email body (HTML)", multiline: true },
  { key: "slack_title", label: "Slack title", multiline: false },
  { key: "slack_body", label: "Slack body (mrkdwn)", multiline: true },
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

  const runPreview = async (kind: "email" | "slack") => {
    setBusy(true);
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
      setPreview({ kind, body: String((e as Error).message ?? e) });
    } finally {
      setBusy(false);
    }
  };

  const insertVar = (path: string) => {
    const token = `{{ ${path.replace(".<key>", ".yourKey")} }}`;
    setValues((v) => ({ ...v, [focused]: v[focused] + token }));
  };

  return (
    <section style={{ marginTop: 24, borderTop: "1px solid var(--border)", paddingTop: 18 }}>
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
            <label key={f.key} className="form__label">
              {f.label}
              <textarea
                className="svc-textarea"
                style={{ fontSize: 12.5, minHeight: f.key === "email_body" ? 100 : 72, fontFamily: "var(--font-mono, monospace)" }}
                placeholder={inherited[f.key] ? `Inherited: ${inherited[f.key].slice(0, 120)}…` : "Inherits the default"}
                value={values[f.key]}
                disabled={!canEdit}
                onFocus={() => setFocused(f.key)}
                onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
              />
            </label>
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
          <button type="button" className="btn" onClick={() => runPreview("slack")} disabled={busy}>
            Preview Slack
          </button>
          <button type="button" className="btn" onClick={() => runPreview("email")} disabled={busy}>
            Preview email
          </button>
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
