// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A trace drawn as a graph rather than a waterfall (issue #19).
//
// The waterfall answers "where did the time go". This answers "what
// called what, what ran together, and where did it hand off". Both
// questions get asked; this does not replace it.
//
// Laid out in depth columns with services as labelled bands, so which
// spans belong to which service is a position rather than a repeated
// label down a column. Deliberately hand-rolled SVG rather than a graph
// library: the shape here is a tree with a few extra edges, the node
// count is capped before we draw, and a layout engine would be a large
// dependency for a small problem.

import { useMemo } from "react";
import type { LinkedTrace, SpanSummary } from "../api/types";
import { buildSpanGraph, graphRefusal, type SpanNode } from "../lib/spanGraph";

const NODE_W = 168;
const NODE_H = 46;
const COL_GAP = 56;
const ROW_GAP = 14;
const PAD = 16;

interface Props {
  spans: SpanSummary[];
  /** When this cell began storing span links. See buildSpanGraph. */
  linksRecordedSince?: string;
  selectedSpanId?: string | null;
  onSelect?: (spanId: string) => void;
  /**
   * Summaries of the traces this one hands off to, so the far side can
   * be named. A hand-off with no entry here is still drawn — it just
   * says less: the trace is either outside the caller's policy or aged
   * out of retention, and both are honest answers.
   */
  linkedTraces?: LinkedTrace[];
  /** Opens a linked trace. Omit to render the far side unclickable. */
  onOpenTrace?: (traceId: string) => void;
}

/** Depth of each node from its root, for column placement. */
function depths(nodes: SpanNode[]): Map<string, number> {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const out = new Map<string, number>();
  const walk = (n: SpanNode, guard: Set<string>): number => {
    const known = out.get(n.id);
    if (known !== undefined) return known;
    // A malformed parent chain must not hang the page; treat a cycle as
    // a root rather than recursing for ever.
    if (!n.parentId || guard.has(n.id)) {
      out.set(n.id, 0);
      return 0;
    }
    const parent = byId.get(n.parentId);
    if (!parent) {
      out.set(n.id, 0);
      return 0;
    }
    guard.add(n.id);
    const d = walk(parent, guard) + 1;
    guard.delete(n.id);
    out.set(n.id, d);
    return d;
  };
  nodes.forEach((n) => walk(n, new Set()));
  return out;
}

export default function SpanGraph({
  spans,
  linksRecordedSince,
  selectedSpanId,
  onSelect,
  linkedTraces,
  onOpenTrace,
}: Props) {
  const graph = useMemo(
    () => buildSpanGraph(spans, linksRecordedSince),
    [spans, linksRecordedSince],
  );
  const linkedByTrace = useMemo(
    () => new Map((linkedTraces ?? []).map((t) => [t.trace_id, t])),
    [linkedTraces],
  );
  const refusal = graphRefusal(graph);

  const layout = useMemo(() => {
    if (refusal) return null;
    const depth = depths(graph.nodes);
    // Ordered by service so nodes of one service sit together, then by
    // start time so the picture still reads left-to-right in a column.
    const sorted = [...graph.nodes].sort(
      (a, b) => a.service.localeCompare(b.service) || a.startMs - b.startMs,
    );
    const rowOf = new Map<string, number>();
    const perCol = new Map<number, number>();
    for (const n of sorted) {
      const d = depth.get(n.id) ?? 0;
      const row = perCol.get(d) ?? 0;
      rowOf.set(n.id, row);
      perCol.set(d, row + 1);
    }
    const pos = new Map<string, { x: number; y: number }>();
    for (const n of graph.nodes) {
      pos.set(n.id, {
        x: PAD + (depth.get(n.id) ?? 0) * (NODE_W + COL_GAP),
        y: PAD + (rowOf.get(n.id) ?? 0) * (NODE_H + ROW_GAP),
      });
    }
    const cols = Math.max(...[...perCol.keys()], 0) + 1;
    const rows = Math.max(...[...perCol.values()], 1);
    // A hand-off's node sits one column to the RIGHT of the node it
    // leaves, which can be the rightmost node there is. Without this the
    // viewBox ends at the last real column and the far side of every
    // hand-off is clipped away — the exact thing this is meant to show.
    const handoffOverhang = graph.handoffs.length > 0 ? COL_GAP + NODE_W : 0;
    return {
      pos,
      width: PAD * 2 + cols * NODE_W + (cols - 1) * COL_GAP + handoffOverhang,
      height: PAD * 2 + rows * NODE_H + (rows - 1) * ROW_GAP,
      sorted,
    };
  }, [graph, refusal]);

  if (graph.nodes.length === 0) {
    return <div className="placeholder">No spans to draw.</div>;
  }

  if (refusal) {
    return (
      <div className="alert" role="status" style={{ margin: 12 }}>
        <div style={{ fontWeight: 600, marginBottom: 4 }}>Too many steps to draw</div>
        <div>{refusal.reason}</div>
        <div className="muted" style={{ marginTop: 6, fontSize: 12.5 }}>
          {refusal.fallback}
        </div>
      </div>
    );
  }

  const l = layout!;
  const selectedNode = graph.nodes.find((n) => n.spanIds.includes(selectedSpanId ?? ""));

  return (
    <div style={{ overflowX: "auto" }}>
      <svg
        width={l.width}
        height={l.height}
        viewBox={`0 0 ${l.width} ${l.height}`}
        role="img"
        aria-label={`Span graph: ${graph.nodes.length} steps across ${new Set(graph.nodes.map((n) => n.service)).size} services`}
      >
        <defs>
          <marker id="sg-arrow" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto">
            <path d="M0,0 L0,6 L7,3 z" fill="var(--border)" />
          </marker>
        </defs>

        {l.sorted.map((n) => {
          const from = l.pos.get(n.id)!;
          return graph.edges
            .filter((e) => e.to === n.id)
            .map((e) => {
              const p = l.pos.get(e.from)!;
              const x1 = p.x + NODE_W;
              const y1 = p.y + NODE_H / 2;
              const x2 = from.x;
              const y2 = from.y + NODE_H / 2;
              const mid = (x1 + x2) / 2;
              return (
                <path
                  key={`${e.from}-${e.to}`}
                  d={`M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`}
                  fill="none"
                  stroke="var(--border)"
                  strokeWidth={1.5}
                  markerEnd="url(#sg-arrow)"
                />
              );
            });
        })}

        {/* Hand-offs leave this trace, because their target is another
            message. The first version drew a dashed stub into empty
            space, on the reasoning that an arrow pointing at nothing
            reads as a rendering bug. True, and not enough: a stub says
            something continued without saying what, which leaves the
            reader copying a trace id out of a tooltip.

            So the far side gets a NODE — named, dated, clickable. Drawn
            dashed and set apart, because it is a doorway into a
            different message and not part of this picture. Inlining its
            spans would erase the distinction the whole model rests on.

            Neutral, not warn-coloured: a hand-off is normal. Warning
            and error colours stay reserved for actual failure, or
            people learn to ignore them. */}
        {graph.handoffs.map((h, i) => {
          const p = l.pos.get(h.from);
          if (!p) return null;
          const head = linkedByTrace?.get(h.traceId);
          const x = p.x + NODE_W + COL_GAP;
          const y = p.y;
          const mid = p.x + NODE_W + COL_GAP / 2;
          return (
            <g key={`${h.from}-${h.traceId}-${i}`}>
              <path
                d={`M${p.x + NODE_W},${p.y + NODE_H / 2} C${mid},${p.y + NODE_H / 2} ${mid},${y + NODE_H / 2} ${x},${y + NODE_H / 2}`}
                stroke="var(--border-strong)"
                strokeWidth={1.5}
                strokeDasharray="4 3"
                fill="none"
                markerEnd="url(#sg-arrow)"
              />
              <g
                transform={`translate(${x},${y})`}
                style={{ cursor: onOpenTrace ? "pointer" : "default" }}
                onClick={() => onOpenTrace?.(h.traceId)}
              >
                <rect
                  width={NODE_W}
                  height={NODE_H}
                  rx={6}
                  fill="var(--surface)"
                  stroke={head?.has_error ? "var(--err)" : "var(--border-strong)"}
                  strokeWidth={head?.has_error ? 2 : 1}
                  strokeDasharray="5 3"
                />
                <text x={10} y={17} fontSize={10} fill="var(--muted)">
                  {head ? head.service_name : "another message"}
                </text>
                <text x={10} y={32} fontSize={12} fontWeight={600} fill="var(--ink)">
                  {head
                    ? head.span_name.length > 20
                      ? head.span_name.slice(0, 19) + "…"
                      : head.span_name
                    : `${h.traceId.slice(0, 12)}…`}
                </text>
                <title>
                  {head
                    ? `Handed off to another message: ${head.service_name} · ${head.span_name} (${head.span_count} spans). Click to open.`
                    : `Handed off to trace ${h.traceId} — not visible to you, or aged out of retention.`}
                </title>
              </g>
            </g>
          );
        })}

        {l.sorted.map((n) => {
          const p = l.pos.get(n.id)!;
          const active = selectedNode?.id === n.id;
          return (
            <g
              key={n.id}
              transform={`translate(${p.x},${p.y})`}
              onClick={() => onSelect?.(n.id)}
              style={{ cursor: onSelect ? "pointer" : "default" }}
            >
              <rect
                width={NODE_W}
                height={NODE_H}
                rx={6}
                fill="var(--surface-2)"
                stroke={active ? "var(--primary)" : n.failed ? "var(--err)" : "var(--border)"}
                strokeWidth={active || n.failed ? 2 : 1}
              />
              <text x={10} y={17} fontSize={10} fill="var(--muted)">
                {n.service}
              </text>
              <text x={10} y={32} fontSize={12} fontWeight={600} fill="var(--ink)">
                {n.name.length > 22 ? n.name.slice(0, 21) + "…" : n.name}
              </text>
              {n.count > 1 && (
                <>
                  <text x={NODE_W - 10} y={17} fontSize={11} fill="var(--muted)" textAnchor="end">
                    ×{n.count}
                  </text>
                  {/* "overlapping" rather than "parallel": overlapping
                      time ranges are measured, parallelism is an
                      interpretation, and a retry looks the same from
                      out here. */}
                  <text x={NODE_W - 10} y={32} fontSize={10} fill="var(--muted)" textAnchor="end">
                    {n.overlapping ? "overlapping" : "in sequence"}
                  </text>
                </>
              )}
              <title>
                {n.service} · {n.name}
                {n.count > 1
                  ? ` · ${n.count} runs, ${n.overlapping ? "overlapping in time" : "one after another"}`
                  : ""}
              </title>
            </g>
          );
        })}
      </svg>

      <div className="muted" style={{ fontSize: 12, padding: "6px 12px 0" }}>
        {graph.rawSpans !== graph.nodes.length && (
          <>
            {graph.rawSpans} spans folded into {graph.nodes.length} steps.{" "}
          </>
        )}
        {graph.handoffsUnknown
          ? "Hand-offs to other traces are not known for this message: it was recorded before Sluicio stored them, and there is no way to fill that in afterwards."
          : graph.handoffs.length > 0 && `${graph.handoffs.length} hand-off to another trace.`}
      </div>
    </div>
  );
}
