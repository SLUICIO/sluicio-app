// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// WebhookBodyEditor — authors the JSON body a webhook channel posts, for
// receivers that dictate their own shape (AhaSend, Twilio, a ticket API)
// rather than accepting ours.
//
// The template is JSON, not text: every value may be a reference, and a
// string that is EXACTLY "$path" keeps the referenced type (a number
// stays a number, a list stays a list) while a string that merely
// CONTAINS references interpolates as text. A text template producing
// JSON breaks the moment a summary contains a quote — the receiver
// answers 400, and nobody notices until an alert fails to arrive.
//
// The preview is rendered by the server through the delivery renderer,
// so what shows here is the payload. A local re-implementation would
// agree right up to the case that matters.

import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import type { TemplateVariable } from "../../api/types";
import type { EditorView } from "@codemirror/view";
import { caretIsInJSONString } from "../../lib/jsonCaret";

const CodeEditor = lazy(() => import("../CodeEditor"));

// A body that shows every rule worth knowing: a bare reference, an
// interpolated one, and a literal the receiver requires.
const STARTER_PLAIN = `{
  "from": { "email": "alerts@example.com" },
  "recipients": [{ "email": "oncall@example.com" }],
  "content": {
    "subject": "[\${alert.severity}] \${rule.name}",
    "text_body": "\${alert.summary}\\n\\n\${alert.link}"
  }
}`;

// The other half of the job: a receiver that is itself an email sender
// wants the message the org already designed, not a plainer one written
// again by hand here. email.* is the rendered mail, subject and both
// bodies, straight out of the template ladder.
const STARTER_EMAIL = `{
  "from": { "email": "alerts@example.com" },
  "recipients": [{ "email": "oncall@example.com" }],
  "content": {
    "subject": "$email.subject",
    "html_body": "$email.html",
    "text_body": "$email.text"
  }
}`;

const STARTERS: { label: string; title: string; body: string }[] = [
  {
    label: "Start from an example",
    title: "A body that writes the message itself, from the alert's own fields",
    body: STARTER_PLAIN,
  },
  {
    label: "Send the alert email",
    title: "For a receiver that sends mail: posts the rendered alert email, subject and both bodies",
    body: STARTER_EMAIL,
  },
];

export default function WebhookBodyEditor({
  value,
  onChange,
  disabled,
}: {
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
}) {
  const [variables, setVariables] = useState<TemplateVariable[]>([]);
  const [pane, setPane] = useState<"preview" | "variables" | null>("variables");
  const [preview, setPreview] = useState<string | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const view = useRef<EditorView | null>(null);

  useEffect(() => {
    void api
      // Webhook scope, so the palette also carries email.* - the rendered
      // alert mail, for a receiver that is itself an email sender.
      .templateContextSchema("webhook")
      .then((r) => setVariables(r.variables ?? []))
      .catch(() => setVariables([]));
  }, []);

  // Re-render ~600ms after typing stops. A mid-edit template is usually
  // invalid JSON, so the error is shown in place of the payload rather
  // than beside it — an author needs to know the body is broken now, not
  // when the alert doesn't arrive.
  useEffect(() => {
    if (pane !== "preview") return;
    const t = window.setTimeout(() => {
      api
        .previewAlertTemplate("webhook", {}, value)
        .then((r) => {
          setPreview(r.body);
          setPreviewError(null);
        })
        .catch((e) => setPreviewError(String((e as Error).message ?? e)));
    }, 600);
    return () => window.clearTimeout(t);
  }, [value, pane]);

  const insertVar = (path: string) => {
    const token = path.replace(".<key>", ".yourKey");
    const v = view.current;
    if (!v) {
      onChange(value + `"$${token}"`);
      return;
    }
    // Inside a string already? Then the quotes are the author's, and
    // adding ours would produce "" "$path" "". Scan the line up to the
    // caret, honouring backslash escapes; JSON strings can't span lines,
    // so the line is enough. Deliberately a scan and not a regex count:
    // a /(^|[^\\])"/g count swallows the character before each quote, so
    // an adjacent pair reads as one and flips the answer.
    const { from, to } = v.state.selection.main;
    const line = v.state.doc.lineAt(from);
    const head = v.state.doc.sliceString(line.from, from);
    const insert = caretIsInJSONString(head) ? `\${${token}}` : `"$${token}"`;
    v.dispatch({ changes: { from, to, insert }, selection: { anchor: from + insert.length } });
    v.focus();
  };

  return (
    <div className={pane ? "tmpl-split" : undefined} style={{ marginTop: 8 }}>
      <div style={{ display: "flex", flexDirection: "column", gap: 6, minWidth: 0 }}>
        <div className="muted" style={{ fontSize: 11.5, display: "flex", gap: 8, alignItems: "baseline", flexWrap: "wrap" }}>
          <span>
            JSON. Write <code>{'"$alert.summary"'}</code> for the value itself, or{" "}
            <code>{"\"[${alert.severity}] ${rule.name}\""}</code> to build a string.
          </span>
          {!disabled &&
            STARTERS.map((st) => (
              <button
                key={st.label}
                type="button"
                className="btn btn--link"
                style={{ padding: 0, fontSize: 11.5 }}
                title={st.title}
                onClick={() => {
                  if (value.trim() && !window.confirm(`Replace the body template with "${st.label}"?`)) return;
                  onChange(st.body);
                }}
              >
                {st.label}
              </button>
            ))}
        </div>
        <Suspense
          fallback={
            <textarea className="svc-textarea" style={{ fontSize: 12.5, minHeight: 200, fontFamily: "var(--font-mono, monospace)" }} value={value} readOnly />
          }
        >
          <CodeEditor
            value={value}
            onChange={onChange}
            format="json"
            height={220}
            readOnly={disabled}
            showToolbar={false}
            onReady={(v) => {
              view.current = v;
            }}
          />
        </Suspense>
        <div style={{ display: "flex", gap: 8 }}>
          <button type="button" className="btn" onClick={() => setPane((p) => (p === "preview" ? null : "preview"))}>
            {pane === "preview" ? "Hide preview" : "Preview payload"}
          </button>
          <button type="button" className="btn" onClick={() => setPane((p) => (p === "variables" ? null : "variables"))}>
            {pane === "variables" ? "Hide variables" : "Variables…"}
          </button>
        </div>
      </div>

      {pane && (
        <div className="tmpl-split__side">
          <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", marginBottom: 6, gap: 8 }}>
            <span style={{ display: "flex", gap: 10, alignItems: "baseline" }}>
              {(["preview", "variables"] as const).map((p) => (
                <button
                  key={p}
                  type="button"
                  className="btn btn--link"
                  style={{ padding: 0, fontSize: 12, fontWeight: pane === p ? 600 : 400 }}
                  onClick={() => setPane(p)}
                >
                  {p === "preview" ? "Payload" : "Variables"}
                </button>
              ))}
              <span className="muted" style={{ fontSize: 11.5 }}>
                {pane === "preview" ? "sample firing · live" : "click to insert"}
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
                  </span>
                </div>
              ))}
            </div>
          ) : previewError ? (
            <div className="alert alert--error" style={{ fontSize: 12.5 }}>{previewError}</div>
          ) : (
            <pre
              className="tmpl-preview-body"
              style={{ border: "1px solid var(--border)", borderRadius: 6, padding: 10, fontSize: 12.5, whiteSpace: "pre-wrap", background: "var(--surface-2)", margin: 0 }}
            >
              {preview ?? "Rendering…"}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}
