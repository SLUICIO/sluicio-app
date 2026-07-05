// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// "Trim ingestion" for logs — the log-side mirror of the metrics
// TrimIngestionPanel, but as a smart rule builder seeded from the log you're
// viewing. Toggle which dimensions to match (service, severity ≤, body
// pattern, specific attributes); Sluicio AND-combines them into one OTel
// Collector filter processor condition that drops matching logs upstream,
// before they reach Sluicio. Sluicio enforces nothing — you paste the config
// into your own collector.

import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import type { LogEntry } from "../../api/types";

const CodeEditor = lazy(() => import("../CodeEditor"));

function escapeStr(s: string): string {
  return s.replace(/"/g, '\\"');
}
// Escape regex metacharacters so a literal body snippet is a safe IsMatch arg.
function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/"/g, '\\"');
}

function buildLogsConfig(conditions: string[]): string {
  if (conditions.length === 0) {
    return "# Toggle conditions on the left to generate a collector config…";
  }
  // One AND-combined log_record condition: drop logs matching ALL of them.
  const cond = conditions.join(" and ");
  return `# Drop logs matching this rule at your OpenTelemetry Collector,
# before they reach Sluicio. Records matching the condition are dropped.
processors:
  filter/sluicio-exclude:
    error_mode: ignore
    logs:
      log_record:
        - '${cond}'

service:
  pipelines:
    logs:
      # add the processor alongside your existing ones, before the
      # exporter that sends to Sluicio
      processors: [filter/sluicio-exclude]`;
}

interface AttrChoice {
  scope: "log" | "resource";
  key: string;
  value: string;
}

export default function LogTrimPanel({ log, onClose }: { log: LogEntry; onClose: () => void }) {
  const [useService, setUseService] = useState(true);
  const [useSeverity, setUseSeverity] = useState(false);
  const [useBody, setUseBody] = useState(false);
  const [bodyText, setBodyText] = useState(log.body ?? "");
  const [attrSel, setAttrSel] = useState<Set<string>>(new Set());
  const [copied, setCopied] = useState(false);

  const attrChoices = useMemo<AttrChoice[]>(() => {
    const out: AttrChoice[] = [];
    for (const [key, value] of Object.entries(log.log_attributes ?? {})) out.push({ scope: "log", key, value });
    for (const [key, value] of Object.entries(log.resource_attributes ?? {})) {
      if (key === "service.name") continue; // covered by the service toggle
      out.push({ scope: "resource", key, value });
    }
    return out;
  }, [log]);

  const conditions = useMemo(() => {
    const c: string[] = [];
    if (useService && log.service_name) {
      c.push(`resource.attributes["service.name"] == "${escapeStr(log.service_name)}"`);
    }
    if (useSeverity) {
      c.push(`severity_number <= ${log.severity_number}`);
    }
    if (useBody && bodyText.trim()) {
      c.push(`IsMatch(body, "${escapeRe(bodyText.trim())}")`);
    }
    for (const a of attrChoices) {
      if (!attrSel.has(`${a.scope}:${a.key}`)) continue;
      const lhs = a.scope === "log" ? `attributes["${escapeStr(a.key)}"]` : `resource.attributes["${escapeStr(a.key)}"]`;
      c.push(`${lhs} == "${escapeStr(a.value)}"`);
    }
    return c;
  }, [useService, useSeverity, useBody, bodyText, attrChoices, attrSel, log]);

  const yaml = useMemo(() => buildLogsConfig(conditions), [conditions]);

  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", h);
    return () => document.removeEventListener("keydown", h);
  }, [onClose]);

  const toggleAttr = (id: string) =>
    setAttrSel((prev) => {
      const n = new Set(prev);
      n.has(id) ? n.delete(id) : n.add(id);
      return n;
    });

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(yaml);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard blocked — the config is visible to copy manually */
    }
  };

  const rowStyle = { display: "flex", gap: 8, alignItems: "baseline", padding: "6px 0", fontSize: 13 } as const;

  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(15,23,42,0.45)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 300,
        padding: 24,
      }}
    >
      <div
        className="card"
        onClick={(e) => e.stopPropagation()}
        style={{ width: "min(880px, 94vw)", maxHeight: "88vh", display: "flex", flexDirection: "column" }}
      >
        <div className="card__header" style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <span>Trim ingestion · drop logs like this one</span>
          <button className="drawer__close" type="button" onClick={onClose} aria-label="Close">✕</button>
        </div>

        <div
          style={{
            padding: "12px 16px",
            display: "grid",
            gridTemplateColumns: "minmax(0, 1fr) minmax(0, 1fr)",
            gap: 12,
            minHeight: 0,
            flex: 1,
            overflow: "hidden",
          }}
        >
          {/* rule builder */}
          <div style={{ display: "flex", flexDirection: "column", minHeight: 0, overflow: "auto" }}>
            <div className="muted" style={{ fontSize: 12, marginBottom: 6 }}>
              Drop logs matching <strong>all</strong> checked conditions:
            </div>

            <label style={rowStyle}>
              <input type="checkbox" checked={useService} onChange={(e) => setUseService(e.target.checked)} />
              <span>
                service is <span className="mono">{log.service_name || "—"}</span>
              </span>
            </label>

            <label style={rowStyle}>
              <input type="checkbox" checked={useSeverity} onChange={(e) => setUseSeverity(e.target.checked)} />
              <span>
                severity at or below <span className="mono">{log.severity_text || log.severity_number}</span>
              </span>
            </label>

            <label style={{ ...rowStyle, flexDirection: "column", alignItems: "stretch" }}>
              <span style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
                <input type="checkbox" checked={useBody} onChange={(e) => setUseBody(e.target.checked)} />
                <span>body matches (regex):</span>
              </span>
              <input
                className="search__input mono"
                style={{ flex: "none", marginTop: 4, fontSize: 12 }}
                value={bodyText}
                onChange={(e) => setBodyText(e.target.value)}
                onFocus={() => setUseBody(true)}
                placeholder="a stable substring of the log body…"
              />
            </label>

            {attrChoices.length > 0 && (
              <>
                <div className="muted" style={{ fontSize: 12, margin: "10px 0 4px" }}>Attributes</div>
                {attrChoices.map((a) => {
                  const id = `${a.scope}:${a.key}`;
                  return (
                    <label key={id} style={rowStyle}>
                      <input type="checkbox" checked={attrSel.has(id)} onChange={() => toggleAttr(id)} />
                      <span className="mono" style={{ fontSize: 12, overflow: "hidden", textOverflow: "ellipsis" }}>
                        {a.key} = {a.value}
                        {a.scope === "resource" && <span className="muted"> (resource)</span>}
                      </span>
                    </label>
                  );
                })}
              </>
            )}
          </div>

          {/* generated config */}
          <div style={{ display: "flex", flexDirection: "column", minHeight: 0 }}>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 8 }}>
              <span className="m-field-label">OTel Collector config</span>
              <button className="btn btn--sm btn--primary" type="button" onClick={copy} disabled={conditions.length === 0}>
                {copied ? "Copied ✓" : "Copy"}
              </button>
            </div>
            <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
              <Suspense fallback={<pre className="drawer__raw" style={{ flex: 1, margin: 0 }}>{yaml}</pre>}>
                <CodeEditor value={yaml} onChange={() => {}} format="yaml" readOnly height="100%" />
              </Suspense>
            </div>
          </div>
        </div>

        <div style={{ padding: "10px 16px", borderTop: "1px solid var(--border)", fontSize: 12, color: "var(--muted)" }}>
          Sluicio doesn't enforce this — paste the processor into your OpenTelemetry Collector's logs pipeline. Narrow the
          rule (keep body/attributes specific) so you don't drop more than the noise you're targeting.
        </div>
      </div>
    </div>
  );
}
