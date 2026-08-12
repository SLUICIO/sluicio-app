// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The whole hand-off chain around one message — "Include all" (#24).
//
// A chain of MESSAGES, not a merged trace. Each hop keeps its own row,
// its own summary and its own link, because one message is one trace and
// the counting, SLAs and health all follow from that. Drawing the hops
// as one continuous picture would make a chain read as a single long
// message, which is the reading the model forbids.
//
// Laid out as a vertical sequence rather than a graph. A chain is
// almost always linear — queue, retry, delayed delivery — and a list
// reads better than a diagram for a line. The fan-out case (one message
// into several) still renders correctly: hops share a depth and sit at
// the same indent.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { TraceChainResponse } from "../api/types";
import { formatDateTime } from "../lib/format";
import { StatusPip } from "./primitives";

interface Props {
  traceId: string;
  /** Opens one message in the chain. */
  onOpenTrace?: (traceId: string) => void;
}

export default function MessageChain({ traceId, onOpenTrace }: Props) {
  const [data, setData] = useState<TraceChainResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setErr(null);
    setData(null);
    api
      .traceChain(traceId)
      .then((d) => !cancelled && setData(d))
      .catch((e) => !cancelled && setErr(String((e as Error).message ?? e)))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [traceId]);

  if (loading) return <div className="placeholder" style={{ padding: 12 }}>Following the chain…</div>;
  if (err) return <div className="alert alert--error" style={{ margin: 12 }}>{err}</div>;
  if (!data) return null;

  // Order by depth, then by start. Depth is hops from the message you
  // opened, in either direction — so the origin sits first and its
  // neighbours follow, which is the order someone reasons in.
  const nodes = [...data.nodes].sort(
    (a, b) => a.depth - b.depth || a.started_at.localeCompare(b.started_at),
  );

  if (nodes.length <= 1) {
    return (
      <div className="placeholder" style={{ padding: 12, fontSize: 13 }}>
        This message is not part of a chain — nothing links to it, and it links to nothing.
      </div>
    );
  }

  return (
    <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 6 }}>
      <div className="muted" style={{ fontSize: 12, marginBottom: 2 }}>
        {nodes.length} messages in this chain. Each is a separate message with its own trace —
        shown together, not merged.
      </div>

      {nodes.map((n) => {
        const isOrigin = n.trace_id === data.origin;
        return (
          <div
            key={n.trace_id}
            onClick={() => !isOrigin && onOpenTrace?.(n.trace_id)}
            style={{
              display: "grid",
              gridTemplateColumns: "16px 1fr auto",
              alignItems: "center",
              gap: 8,
              padding: "7px 10px",
              marginLeft: n.depth * 14,
              borderRadius: 8,
              border: `1px ${isOrigin ? "solid" : "dashed"} ${isOrigin ? "var(--primary)" : "var(--border-strong)"}`,
              background: isOrigin ? "var(--surface-2)" : "var(--surface)",
              cursor: isOrigin || !onOpenTrace ? "default" : "pointer",
            }}
          >
            <StatusPip kind={n.has_error ? "err" : "ok"} />
            <div style={{ minWidth: 0 }}>
              <div className="truncate" style={{ fontSize: 13 }}>
                <span style={{ fontWeight: 600 }}>{n.service_name}</span>
                <span className="text-muted"> · {n.span_name}</span>
                {isOrigin && (
                  <span className="badge" style={{ marginLeft: 6, fontSize: 10 }}>
                    you are here
                  </span>
                )}
              </div>
              <div className="muted mono" style={{ fontSize: 11 }}>
                {formatDateTime(n.started_at)} · {n.span_count} span{n.span_count === 1 ? "" : "s"}
              </div>
            </div>
            {!isOrigin && (
              <span style={{ fontSize: 12, color: "var(--primary)" }}>open ›</span>
            )}
          </div>
        );
      })}

      {/* Every way the chain can be incomplete says so. A chain that is
          quietly short looks whole, and the three reasons are not
          interchangeable: one is a permission boundary, one says the
          chain is longer, one says it is wider. */}
      {(data.hidden ?? 0) > 0 && (
        <div className="muted" style={{ fontSize: 12, marginTop: 4 }}>
          {data.hidden} more message{data.hidden === 1 ? "" : "s"} in this chain
          {data.hidden === 1 ? " is" : " are"} not visible to you.
        </div>
      )}
      {data.truncated_depth && (
        <div className="muted" style={{ fontSize: 12 }}>
          The chain continues beyond this — open the message at either end to keep following it.
        </div>
      )}
      {data.truncated_nodes && (
        <div className="muted" style={{ fontSize: 12 }}>
          This chain is wider than can be shown at once; some branches are not listed.
        </div>
      )}
    </div>
  );
}
