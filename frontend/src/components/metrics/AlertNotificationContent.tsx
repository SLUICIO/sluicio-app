// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// AlertNotificationContent — the "Notification content" editor shared by the
// alert builders. Lets an author choose which enrichment blocks (failing
// check, service, integration, and each metadata map) the alert's email +
// webhook include, optionally override the email with an inline Liquid
// template, and preview the rendered email HTML / webhook JSON against a
// sample firing — all backed by POST /alert-templates/preview.

import { useState } from "react";
import { api } from "../../api/client";
import type { NotificationContent } from "../../api/types";

type Key = keyof NotificationContent;

const BLOCKS: { key: Key; label: string; parent?: Key }[] = [
  { key: "check", label: "Failing check — metric, value vs threshold" },
  { key: "service", label: "Service details" },
  { key: "service_metadata", label: "Service metadata", parent: "service" },
  { key: "integration", label: "Integration details" },
  { key: "integration_metadata", label: "Integration metadata", parent: "integration" },
];

export default function AlertNotificationContent({
  value,
  onChange,
}: {
  value: NotificationContent;
  onChange: (v: NotificationContent) => void;
}) {
  const [kind, setKind] = useState<"email" | "webhook">("email");
  const [preview, setPreview] = useState<{ subject: string; body: string } | null>(null);
  const [loading, setLoading] = useState(false);
  const [custom, setCustom] = useState(!!(value.email_subject || value.email_body));

  const set = (patch: Partial<NotificationContent>) => onChange({ ...value, ...patch });

  const runPreview = async () => {
    setLoading(true);
    try {
      setPreview(await api.previewAlertTemplate(kind, value));
    } catch (e) {
      setPreview({ subject: "", body: String((e as Error).message ?? e) });
    } finally {
      setLoading(false);
    }
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div className="muted" style={{ fontSize: 12 }}>
        Choose what this alert's email + webhook include. Metadata sits under its parent block.
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        {BLOCKS.map((b) => {
          const disabled = b.parent ? !value[b.parent] : false;
          return (
            <label
              key={b.key as string}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 8,
                fontSize: 13,
                paddingLeft: b.parent ? 22 : 0,
                opacity: disabled ? 0.5 : 1,
              }}
            >
              <input
                type="checkbox"
                checked={!!value[b.key]}
                disabled={disabled}
                onChange={() => set({ [b.key]: !value[b.key] } as Partial<NotificationContent>)}
              />
              {b.label}
            </label>
          );
        })}
      </div>

      <details
        open={custom}
        style={{ border: "1px solid var(--border)", borderRadius: 6, padding: "8px 10px" }}
      >
        <summary style={{ cursor: "pointer", fontSize: 13 }} onClick={(e) => { e.preventDefault(); setCustom((c) => !c); }}>
          Customize email template (Liquid, optional)
        </summary>
        <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 8 }}>
          <input
            className="search__input"
            style={{ fontSize: 13 }}
            placeholder="Subject (Liquid) — blank uses the default"
            value={value.email_subject ?? ""}
            onChange={(e) => set({ email_subject: e.target.value })}
          />
          <textarea
            className="svc-textarea"
            style={{ fontSize: 12.5, minHeight: 120, fontFamily: "var(--font-mono, monospace)" }}
            placeholder="HTML body (Liquid) — blank uses the default layout"
            value={value.email_body ?? ""}
            onChange={(e) => set({ email_body: e.target.value })}
          />
          <span className="muted" style={{ fontSize: 11.5 }}>
            Liquid. Objects: <code>alert</code> <code>rule</code> <code>check</code>{" "}
            <code>service</code> <code>integration</code> <code>org</code> — e.g.{" "}
            <code>{"{{ service.name }}"}</code>,{" "}
            <code>{"{% for kv in service.metadata %}{{ kv.key }}{% endfor %}"}</code>.
          </span>
        </div>
      </details>

      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <select
          className="toolbar__select"
          value={kind}
          onChange={(e) => { setKind(e.target.value as "email" | "webhook"); setPreview(null); }}
        >
          <option value="email">Email</option>
          <option value="webhook">Webhook</option>
        </select>
        <button type="button" className="btn" onClick={runPreview} disabled={loading}>
          {loading ? "Rendering…" : "Preview"}
        </button>
        <span className="muted" style={{ fontSize: 11.5 }}>Rendered against a sample firing.</span>
      </div>
      {preview && kind === "email" && (
        <iframe
          title="email preview"
          sandbox=""
          style={{ width: "100%", height: 360, border: "1px solid var(--border)", borderRadius: 6, background: "#fff" }}
          srcDoc={preview.body}
        />
      )}
      {preview && kind === "webhook" && (
        <pre style={{ maxHeight: 360, overflow: "auto", border: "1px solid var(--border)", borderRadius: 6, padding: 10, fontSize: 12, background: "var(--surface-2)" }}>
          {preview.body}
        </pre>
      )}
    </div>
  );
}
