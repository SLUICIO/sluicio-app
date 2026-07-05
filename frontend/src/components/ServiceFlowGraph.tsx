// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ServiceFlowGraph renders a left-to-right service map: nodes are
// services, edges are parent→child span hops aggregated to the
// service level. Layout is a simple longest-path-from-root depth
// assignment; nodes at the same depth are stacked vertically and the
// whole graph is centered.
//
// Used by:
//   - IntegrationDetail (aggregated across all traces in the window)
//   - TraceDetail (one trace's flow, computed client-side)
//
// The shape rendered is the same; only the data source differs.

import { useMemo } from "react";
import { Link } from "react-router-dom";
import type { FlowEdge, FlowNode } from "../api/types";
import { formatNumber } from "../lib/format";

interface Props {
  nodes: FlowNode[];
  edges: FlowEdge[];
  // When true, edge / node labels use "Calls" / "calls" rather than
  // "Traces" wording — appropriate for the single-trace view.
  singleTrace?: boolean;
  // Service name to mark as "you are here" — its node gets a Sluicio-
  // primary border + glow. Used by the ServiceDetail dependency graph.
  highlight?: string;
}

const NODE_W = 168;
const NODE_H = 56;
const COL_GAP = 80;
const ROW_GAP = 28;
const PADDING = 24;

export default function ServiceFlowGraph({ nodes, edges, singleTrace = false, highlight }: Props) {
  const layout = useMemo(() => computeLayout(nodes, edges), [nodes, edges]);
  // Integration (aggregated) view shows service HEALTH, not raw trace errors:
  // nodes colour by their configured health checks and carry no error count,
  // and edges aren't reddened by error traces. The single-trace view keeps
  // its span-error colouring — there "error" means a span in *this* trace.
  const healthMode = !singleTrace;

  if (nodes.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border p-6 text-sm text-muted">
        No service flow to draw — this scope has no spans in the window.
      </div>
    );
  }
  if (nodes.length === 1) {
    const n = nodes[0];
    return (
      <div className="rounded-md border border-border bg-surface-2 p-4 text-sm">
        <div className="text-muted">Only one service participates in this scope:</div>
        <Link
          to={`/services/${encodeURIComponent(n.service_name)}`}
          className="mt-1 inline-block font-medium text-foreground"
        >
          {n.service_name}
        </Link>
        <div className="text-xs text-muted">
          {formatNumber(n.trace_count)} {singleTrace ? "span" : "trace"}
          {n.trace_count === 1 ? "" : "s"}
        </div>
      </div>
    );
  }

  return (
    <div className="overflow-x-auto">
      <svg
        width={layout.width}
        height={layout.height}
        viewBox={`0 0 ${layout.width} ${layout.height}`}
        role="img"
        aria-label="Service flow graph"
        style={{ minWidth: "100%" }}
      >
        <defs>
          {/* Two markers because SVG 2's `context-stroke` keyword
              only picks up stroke set via the SVG attribute, not via
              CSS / inline style — and we have to use inline style to
              get CSS vars to resolve. So we keep one marker per
              colour, fills set via inline style so they theme-switch
              with the rest of the app. */}
          <marker
            id="flow-arrow"
            viewBox="0 0 10 10"
            refX="9"
            refY="5"
            markerWidth="7"
            markerHeight="7"
            orient="auto-start-reverse"
          >
            <path d="M0,0 L10,5 L0,10 z" style={{ fill: "var(--text)" }} />
          </marker>
          <marker
            id="flow-arrow-error"
            viewBox="0 0 10 10"
            refX="9"
            refY="5"
            markerWidth="7"
            markerHeight="7"
            orient="auto-start-reverse"
          >
            <path d="M0,0 L10,5 L0,10 z" style={{ fill: "var(--critical)" }} />
          </marker>
        </defs>

        {edges.map((e) => {
          const from = layout.nodes.get(e.source);
          const to = layout.nodes.get(e.target);
          if (!from || !to) return null;
          const x1 = from.x + NODE_W;
          const y1 = from.y + NODE_H / 2;
          const x2 = to.x;
          const y2 = to.y + NODE_H / 2;
          const dx = Math.max(40, (x2 - x1) / 2);
          // In health mode the graph doesn't redden hops by error traces.
          const isError = !healthMode && e.error_count > 0;
          // CSS variables don't reliably resolve when used inside SVG
          // presentation attributes (stroke="var(--muted)"); they DO
          // work via inline style, so route everything through there.
          //
          // We use var(--text) — near-black in light, near-white
          // in dark — for the default edge so the lines stand out
          // properly in both themes. var(--muted) was too faint to
          // read against the dark surface.
          const strokeVar = isError ? "var(--critical)" : "var(--text)";
          return (
            <g key={`${e.source}|${e.target}`}>
              <path
                d={`M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`}
                fill="none"
                strokeWidth={1.5}
                markerEnd={`url(#${isError ? "flow-arrow-error" : "flow-arrow"})`}
                style={{ stroke: strokeVar }}
              />
              {e.call_count > 1 && (
                <text
                  x={(x1 + x2) / 2}
                  y={(y1 + y2) / 2 - 6}
                  fontSize="11"
                  textAnchor="middle"
                  style={{ fill: "var(--text-muted)" }}
                >
                  {formatNumber(e.call_count)}
                </text>
              )}
            </g>
          );
        })}

        {nodes.map((n) => {
          const pos = layout.nodes.get(n.service_name);
          if (!pos) return null;
          // Health mode → red iff the service's checks say unhealthy; single
          // trace → red iff this trace errored on the service.
          const isError = healthMode ? n.status === "unhealthy" : n.error_trace_count > 0;
          // "You are here": Sluicio-primary border + soft glow. Health
          // still wins the stroke — an unhealthy focal service stays red,
          // but keeps the glow so it's still findable at a glance.
          const isMe = highlight != null && n.service_name === highlight;
          return (
            <g
              key={n.service_name}
              transform={`translate(${pos.x}, ${pos.y})`}
              style={{ cursor: "pointer" }}
              onClick={() => {
                window.location.assign(`/services/${encodeURIComponent(n.service_name)}`);
              }}
            >
              <rect
                width={NODE_W}
                height={NODE_H}
                rx={8}
                strokeWidth={isError || isMe ? 2 : 1}
                style={{
                  fill: isMe
                    ? "color-mix(in oklab, var(--primary) 7%, var(--surface-2))"
                    : "var(--surface-2)",
                  stroke: isError ? "var(--critical)" : isMe ? "var(--primary)" : "var(--border)",
                  filter: isMe
                    ? "drop-shadow(0 0 6px color-mix(in oklab, var(--primary) 70%, transparent))"
                    : undefined,
                }}
              />
              <text
                x={NODE_W / 2}
                y={22}
                textAnchor="middle"
                fontSize="13"
                fontWeight={500}
                style={{ fill: "var(--text)" }}
              >
                {n.service_name}
              </text>
              <text
                x={NODE_W / 2}
                y={42}
                textAnchor="middle"
                fontSize="11"
                style={{ fill: "var(--text-muted)" }}
              >
                {formatNumber(n.trace_count)} {singleTrace ? "span" : "trace"}
                {n.trace_count === 1 ? "" : "s"}
                {!healthMode && n.error_trace_count > 0
                  ? ` · ${formatNumber(n.error_trace_count)} error trace${n.error_trace_count === 1 ? "" : "s"}`
                  : ""}
              </text>
            </g>
          );
        })}
      </svg>
    </div>
  );
}

// ---- layout ---------------------------------------------------------

interface NodePos {
  x: number;
  y: number;
}

interface Layout {
  width: number;
  height: number;
  nodes: Map<string, NodePos>;
}

function computeLayout(nodes: FlowNode[], edges: FlowEdge[]): Layout {
  // Build incoming-edges map for depth assignment.
  const incoming = new Map<string, string[]>();
  for (const n of nodes) incoming.set(n.service_name, []);
  for (const e of edges) {
    if (!incoming.has(e.target)) incoming.set(e.target, []);
    incoming.get(e.target)!.push(e.source);
  }

  const depths = new Map<string, number>();
  const visiting = new Set<string>();
  const depth = (name: string): number => {
    if (depths.has(name)) return depths.get(name)!;
    if (visiting.has(name)) return 0; // break cycles
    visiting.add(name);
    const ins = incoming.get(name) ?? [];
    const d = ins.length === 0 ? 0 : Math.max(0, ...ins.map(depth)) + 1;
    visiting.delete(name);
    depths.set(name, d);
    return d;
  };
  for (const n of nodes) depth(n.service_name);

  // Group by depth column.
  const columns = new Map<number, string[]>();
  let maxDepth = 0;
  for (const n of nodes) {
    const d = depths.get(n.service_name) ?? 0;
    if (d > maxDepth) maxDepth = d;
    if (!columns.has(d)) columns.set(d, []);
    columns.get(d)!.push(n.service_name);
  }

  let maxColLen = 0;
  columns.forEach((c) => {
    if (c.length > maxColLen) maxColLen = c.length;
  });

  const innerHeight = Math.max(NODE_H, maxColLen * NODE_H + (maxColLen - 1) * ROW_GAP);
  const innerWidth =
    (maxDepth + 1) * NODE_W + maxDepth * COL_GAP;

  const positions = new Map<string, NodePos>();
  for (const [d, services] of columns) {
    const colHeight = services.length * NODE_H + (services.length - 1) * ROW_GAP;
    const startY = PADDING + (innerHeight - colHeight) / 2;
    services.forEach((s, i) => {
      positions.set(s, {
        x: PADDING + d * (NODE_W + COL_GAP),
        y: startY + i * (NODE_H + ROW_GAP),
      });
    });
  }

  return {
    width: innerWidth + PADDING * 2,
    height: innerHeight + PADDING * 2,
    nodes: positions,
  };
}
