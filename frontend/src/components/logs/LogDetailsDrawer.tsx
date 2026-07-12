// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The right-side details drawer. Reveals the full LogRecord for the
// selected row: header (level · time · service · close) + message +
// actions, then Trace context / Attributes / Resource / Raw-JSON
// sections. Rendered from the row data the list already carries.

import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useTraceHref } from "../../lib/traceHref";
import type { LogEntry } from "../../api/types";
import LevelBadge from "./LevelBadge";
import LogTrimPanel from "./LogTrimPanel";
import { KVTable, attributeRows } from "../primitives";

function fmtTime(iso: string): string {
  const d = new Date(iso);
  const p = (n: number, w = 2) => String(n).padStart(w, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`;
}

// CopyLinkButton copies a deep link to this exact log: the current page
// path with a `?log=<LogId>` param layered on top of the existing query
// (so the time window / filters in the URL are preserved). Opening that
// link makes LogsView fetch the log by id and re-open this drawer, even
// if the log falls outside the current window or page. Falls back to the
// plain page URL when the log has no id (older API without log_id).
function CopyLinkButton({ logId }: { logId?: string }) {
  const [copied, setCopied] = useState(false);

  const buildLink = (): string => {
    if (!logId) return window.location.href;
    const url = new URL(window.location.href);
    url.searchParams.set("log", logId);
    return url.toString();
  };

  const onCopy = async () => {
    try {
      await navigator.clipboard?.writeText(buildLink());
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable — no-op */
    }
  };

  return (
    <button
      className="btn"
      type="button"
      onClick={onCopy}
      disabled={!logId}
      title={logId ? "Copy a deep link to this log" : "No id for this log"}
    >
      {copied ? "Copied ✓" : "Copy link"}
    </button>
  );
}

export default function LogDetailsDrawer({
  log,
  integrations = [],
  onClose,
  onOpenTrace,
}: {
  log: LogEntry;
  // Integration(s) the log's service belongs to (persisted membership).
  integrations?: { id: string; name: string }[];
  onClose: () => void;
  // When provided, "View trace" opens the trace as a slide-over blade
  // in place (the TraceDrawer) instead of navigating to /traces/:id.
  onOpenTrace?: (traceId: string) => void;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [trimOpen, setTrimOpen] = useState(false);
  const traceHref = useTraceHref();

  // Escape closes when focus is inside the drawer.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const logAttrs = attributeRows(log.log_attributes);
  const resourceAttrs = attributeRows(log.resource_attributes);

  const raw = JSON.stringify(
    {
      timestamp: log.timestamp,
      severity_text: log.severity_text,
      severity_number: log.severity_number,
      body: log.body,
      attributes: log.log_attributes ?? {},
      resource: log.resource_attributes ?? {},
      trace_id: log.trace_id || null,
      span_id: log.span_id || null,
    },
    null,
    2,
  );

  return (
    <aside className="drawer" ref={ref} aria-label="Log details">
      <div className="drawer__head">
        <div className="drawer__top">
          <LevelBadge num={log.severity_number} />
          <span className="mono" style={{ fontSize: 12, color: "var(--muted)" }} title={log.timestamp}>
            {fmtTime(log.timestamp)}
          </span>
          <span style={{ color: "var(--muted)" }}>·</span>
          {log.service_name ? (
            <Link className="svc-chip" to={`/services/${encodeURIComponent(log.service_name)}`} title={`Open ${log.service_name}`}>
              {log.service_name}
            </Link>
          ) : (
            <span className="svc-chip">—</span>
          )}
          {integrations.length > 0 && (
            <>
              <span style={{ color: "var(--muted)", fontSize: 11 }}>in</span>
              {integrations.map((intg) => (
                <Link key={intg.id} className="svc-chip" to={`/integrations/${intg.id}`} title={`Open ${intg.name}`}>
                  {intg.name}
                </Link>
              ))}
            </>
          )}
          <span style={{ flex: 1 }} />
          <button className="drawer__close" type="button" onClick={onClose} aria-label="Close details">✕</button>
        </div>
        <p className="drawer__msg">{log.body || "—"}</p>
        <div className="drawer__actions">
          {log.trace_id ? (
            onOpenTrace ? (
              <button
                className="btn btn--primary"
                type="button"
                onClick={() => onOpenTrace(log.trace_id!)}
              >
                View trace →
              </button>
            ) : (
              <Link className="btn btn--primary" to={traceHref(log.trace_id)}>
                View full trace →
              </Link>
            )
          ) : (
            <button className="btn" type="button" disabled title="No trace context on this log">
              View trace →
            </button>
          )}
          <CopyLinkButton logId={log.log_id} />
          <button
            className="btn"
            type="button"
            onClick={() => setTrimOpen(true)}
            title="Generate a collector rule to drop logs like this"
          >
            ⚙ Trim ingestion
          </button>
          {/* (View full trace + Copy link share the actions row) */}
        </div>
      </div>

      {trimOpen && <LogTrimPanel log={log} onClose={() => setTrimOpen(false)} />}

      <div className="drawer__body">
        {log.trace_id && (
          <section>
            <div className="drawer__section-title">Trace context</div>
            <KVTable
              bordered
              rows={[
                { k: "trace_id", v: log.trace_id, copyValue: log.trace_id },
                ...(log.span_id
                  ? [{ k: "span_id", v: log.span_id, copyValue: log.span_id }]
                  : []),
              ]}
            />
          </section>
        )}

        <section>
          <div className="drawer__section-title">
            Attributes<span className="count">{logAttrs.length}</span>
          </div>
          <KVTable bordered rows={logAttrs} />
        </section>

        <section>
          <div className="drawer__section-title">Resource</div>
          <KVTable bordered rows={resourceAttrs} />
        </section>

        <section>
          <div className="drawer__section-title">Raw</div>
          <pre className="drawer__raw">{raw}</pre>
        </section>
      </div>
    </aside>
  );
}
