// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// AlertEmailTemplateSettings — the org-default alert email template editor on
// Settings → System settings. Edits the Liquid subject + HTML body that every
// alert email uses unless a rule overrides it inline, with a live preview
// rendered against a sample firing. Read-only for non-admins.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import { useCurrentUser } from "../lib/useCurrentUser";

export default function AlertEmailTemplateSettings() {
  const { can } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [defaults, setDefaults] = useState({ subject: "", body: "" });
  const [preview, setPreview] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savedAt, setSavedAt] = useState(0);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .getAlertEmailTemplate()
      .then((r) => {
        setSubject(r.subject);
        setBody(r.body);
        setDefaults({ subject: r.default_subject, body: r.default_body });
      })
      .catch(() => {});
  }, []);

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      await api.putAlertEmailTemplate(subject, body);
      setSavedAt(Date.now());
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  const doPreview = async () => {
    setLoading(true);
    try {
      const r = await api.previewAlertTemplate("email", {
        service: true,
        integration: true,
        service_metadata: true,
        integration_metadata: true,
        check: true,
        email_subject: subject || undefined,
        email_body: body || undefined,
      });
      setPreview(r.body);
    } catch (e) {
      setPreview(String((e as Error).message ?? e));
    } finally {
      setLoading(false);
    }
  };

  // Flat section styling — matches the Email (SMTP) and Security policy
  // sections on the System tab (top-border divider, h3 title, muted intro).
  return (
    <section style={{ marginTop: 28, borderTop: "1px solid var(--border)", paddingTop: 20 }}>
      <h3 style={{ fontSize: 14, fontWeight: 600, margin: "0 0 4px" }}>Alert email template</h3>
      <p className="muted" style={{ fontSize: 13, lineHeight: 1.55, margin: "0 0 14px" }}>
        The default Liquid email for alert notifications. Individual alerts can
        override it. Leave a field blank to use the built-in default.
      </p>
      <div style={{ display: "flex", flexDirection: "column", gap: 10, maxWidth: 720 }}>
        {error && <div className="alert alert--error">{error}</div>}
        <label className="form__label">
          Subject (Liquid)
          <input
            className="search__input"
            value={subject}
            placeholder={defaults.subject}
            onChange={(e) => setSubject(e.target.value)}
            disabled={!isAdmin}
          />
        </label>
        <label className="form__label">
          HTML body (Liquid)
          <textarea
            className="svc-textarea"
            style={{ minHeight: 200, fontFamily: "var(--font-mono, monospace)", fontSize: 12.5 }}
            value={body}
            placeholder="Blank uses the built-in default layout"
            onChange={(e) => setBody(e.target.value)}
            disabled={!isAdmin}
          />
        </label>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          {isAdmin && (
            <button className="btn btn--primary" type="button" onClick={save} disabled={saving}>
              {saving ? "Saving…" : "Save"}
            </button>
          )}
          {isAdmin && (
            <button
              className="btn"
              type="button"
              onClick={() => { setSubject(defaults.subject); setBody(defaults.body); }}
            >
              Load built-in default
            </button>
          )}
          <button className="btn" type="button" onClick={doPreview} disabled={loading}>
            {loading ? "Rendering…" : "Preview"}
          </button>
          {savedAt > 0 && <span className="muted" style={{ fontSize: 12 }}>Saved</span>}
        </div>
        {preview && (
          <iframe
            title="default email preview"
            sandbox=""
            style={{ width: "100%", height: 380, border: "1px solid var(--border)", borderRadius: 6, background: "#fff" }}
            srcDoc={preview}
          />
        )}
      </div>
    </section>
  );
}
