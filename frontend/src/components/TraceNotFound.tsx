// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What the trace views show for a trace id with no spans here, in place
// of a bare "not found". A log names the trace it was written in whether
// or not that trace was ever sent to this cell, so "not found" read like
// a broken link. This says what usually causes it, and when the log is
// older than traces are kept, that the trace has most likely expired.
// The access case is listed but never singled out: a trace hidden from
// the reader must read the same as one that does not exist.

import { formatRelative } from "../lib/format";
import { missingTraceReason } from "../lib/missingTrace";

export default function TraceNotFound({
  traceId,
  retentionDays,
  seenAt,
}: {
  traceId: string;
  retentionDays?: number;
  // When the log (or whatever named the trace) was written, if known.
  seenAt?: string;
}) {
  const expired = missingTraceReason(seenAt, retentionDays) === "expired";
  const kept = retentionDays ? `${retentionDays} day${retentionDays === 1 ? "" : "s"}` : undefined;
  return (
    <div className="placeholder" style={{ margin: 0, fontSize: 13, lineHeight: 1.5 }} data-testid="trace-not-found">
      <div style={{ fontWeight: 600, color: "var(--ink)", marginBottom: 6 }}>This trace has no spans here</div>
      <p style={{ margin: "0 0 8px" }}>
        Something referred to trace <span className="mono">{traceId}</span>, but no spans of it are stored that you can see.
      </p>
      {expired ? (
        <p style={{ margin: 0 }}>
          The log is from {formatRelative(seenAt!)}, and traces are kept {kept}, so this one has most likely been deleted.
        </p>
      ) : (
        <ul style={{ margin: 0, paddingLeft: 18, listStyle: "disc" }}>
          <li>
            <b>It was never sent here.</b> A log carries its trace id even when the trace is not exported: it was sampled
            out, the service sends logs but not traces, or the call arrived with trace context from a caller that does not
            report to this cell.
          </li>
          <li>
            <b>It has expired.</b> {kept ? `Traces are kept ${kept}.` : "Traces are deleted after the retention period."}
          </li>
          <li>
            <b>Its services are outside what you may see.</b>
          </li>
        </ul>
      )}
    </div>
  );
}
