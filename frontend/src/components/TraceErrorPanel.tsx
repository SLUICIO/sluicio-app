// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// TraceErrorPanel — variant C from the message-trace handoff. Shown
// in the right column when the trace has at least one error span:
// an accent error callout, a "where" card with file:line and
// dead-letter info, the offending payload (when discoverable from
// attributes), and a "similar errors" link.
//
// Runtime actions (retry / edit+replay / drop) are intentionally omitted
// — Sluicio is an observability tool, not a control plane.

import type { SpanSummary } from "../api/types";

interface Props {
  errorSpan: SpanSummary;
  integrationName?: string;
  onSimilarErrors?: () => void;
}

export default function TraceErrorPanel({ errorSpan, onSimilarErrors }: Props) {
  const attrs = errorSpan.attributes ?? {};
  const errClass =
    attrs["error.type"] ||
    attrs["exception.type"] ||
    errorSpan.status_message?.split(":")[0] ||
    "Error";
  const errMessage =
    attrs["error.message"] ||
    attrs["exception.message"] ||
    errorSpan.status_message ||
    "Span ended with an error.";
  const file =
    attrs["code.filepath"] ||
    attrs["code.function"] ||
    attrs["exception.stacktrace"]?.split("\n")[1]?.trim();
  const dlq = attrs["dead_letter.destination"] || attrs["messaging.destination.name"];
  const attempt = attrs["retry.attempt"] || attrs["messaging.attempt"];
  const offending = attrs["http.request.body"] || attrs["messaging.payload"] || attrs["payload"];

  return (
    <div className="flex flex-col gap-3">
      {/* Hero error callout */}
      <div
        className="rounded-xl p-4"
        style={{
          borderLeft: "4px solid var(--err)",
          background: "var(--err-soft)",
          color: "var(--err-ink)",
        }}
      >
        <div className="text-[11px] uppercase tracking-wide" style={{ color: "var(--err-ink)" }}>
          error · permanent
        </div>
        <div className="mt-1 text-xl font-semibold">{errClass}</div>
        <div className="mt-1 text-sm">{errMessage}</div>
      </div>

      {/* Where card */}
      <div className="rounded-xl border p-3 text-sm shadow-sm" style={{ borderColor: "var(--border)", background: "var(--surface-2)" }}>
        <div className="mb-1 text-xs text-muted">where</div>
        <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1">
          <dt className="text-muted">service</dt>
          <dd className="font-medium">{errorSpan.service_name}</dd>
          {file && (
            <>
              <dt className="text-muted">code</dt>
              <dd className="font-mono text-xs">{file}</dd>
            </>
          )}
          {dlq && (
            <>
              <dt className="text-muted">dead-letter</dt>
              <dd className="font-mono text-xs">{dlq}</dd>
            </>
          )}
          {attempt && (
            <>
              <dt className="text-muted">attempt</dt>
              <dd className="font-mono text-xs">{attempt}</dd>
            </>
          )}
        </dl>
      </div>

      {/* Offending payload */}
      {offending && (
        <div
          className="overflow-hidden rounded-xl border shadow-sm"
          style={{ borderColor: "var(--border)", background: "var(--surface-2)" }}
        >
          <div className="border-b px-3 py-2 text-sm font-semibold" style={{ borderColor: "var(--border)" }}>
            offending payload
          </div>
          <pre
            className="m-0 max-h-64 overflow-auto p-3 font-mono text-xs leading-relaxed"
            style={{ background: "var(--surface-3)" }}
          >
            {tryFormat(String(offending))}
          </pre>
        </div>
      )}

      {/* Similar errors */}
      <button
        type="button"
        onClick={onSimilarErrors}
        className="rounded-xl border p-3 text-left text-sm shadow-sm transition-colors hover:bg-surface-elevated"
        style={{ borderColor: "var(--border)", background: "var(--surface-2)" }}
      >
        <div className="text-xs" style={{ color: "var(--muted)" }}>similar errors · last 24h</div>
        <div className="mt-1 text-lg font-semibold">Open grouped search →</div>
      </button>
    </div>
  );
}

function tryFormat(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}
