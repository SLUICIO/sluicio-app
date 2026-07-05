// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationFlow — node graph for an integration's service pipeline.
// Built on React Flow per the handoff direction (variant B: Graph +
// inspector). Selecting a node fires `onSelect(serviceName)`; the
// parent renders the right-rail inspector.
//
// Layout: longest-path-from-root depth assignment, same as the old
// ServiceFlowGraph, but rendered through React Flow so we get pan,
// zoom, fitView, and accessible node interactions out of the box.
// The graph shows service HEALTH: a node reads red iff one of its configured
// health checks is firing, with an "unhealthy" badge. Raw trace errors don't
// colour nodes or edges — they're visible on the Errors page, not here.

import { useEffect, useMemo } from "react";
import ReactFlow, {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  NodeProps,
  Position,
  ReactFlowProvider,
  useReactFlow,
  type Edge,
  type Node,
} from "reactflow";
import "reactflow/dist/style.css";
import type { FlowEdge, FlowMap, FlowNode, FlowSchemaRef, ServiceStatus } from "../api/types";
import { formatNumber } from "../lib/format";

interface Props {
  nodes: FlowNode[];
  edges: FlowEdge[];
  selected?: string | null;
  onSelect?: (serviceName: string) => void;
  highlightPath?: Set<string>; // node ids along a single message path
  highlightEdges?: Set<string>; // "source|target" edge keys
  // Data-shape overlay: schemas pinned per service (in/out) and the
  // maps transforming between them. When present, nodes show schema
  // chips and a hop is labeled with the map that bridges it.
  serviceSchemas?: Record<string, FlowSchemaRef[]>;
  maps?: FlowMap[];
  // Real health per service (firing checks + open errors), so a node reads
  // unhealthy even with no error traces in the window. Keyed by service name.
  statusByService?: Record<string, ServiceStatus>;
}

const NODE_W = 180;
const NODE_H = 76; // base height (no schema chips)
const NODE_H_SCHEMA = 122; // taller so a row of in/out chips fits
const COL_GAP = 80;
const ROW_GAP = 28;

// ── Custom node ─────────────────────────────────────────────────────
interface ServiceNodeData {
  label: string;
  errors: number;
  traces: number;
  status?: ServiceStatus;
  selected: boolean;
  highlighted: boolean;
  schemas?: FlowSchemaRef[];
  heightPx: number;
}

// SchemaChip — one in/out schema pill on a service node. "in" is a
// downstream arrow (data arriving), "out" an upstream arrow (data
// leaving), color-coded so the direction reads at a glance.
function SchemaChip({ s }: { s: FlowSchemaRef }) {
  const isIn = s.direction === "in";
  return (
    <span
      title={`${isIn ? "incoming" : "outgoing"} schema: ${s.name}`}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 3,
        maxWidth: "100%",
        padding: "1px 5px",
        borderRadius: 4,
        fontSize: 10,
        lineHeight: 1.5,
        border: "1px solid var(--border)",
        background: "var(--surface-3)",
        color: "var(--ink-2)",
      }}
    >
      <span aria-hidden style={{ color: isIn ? "var(--primary)" : "var(--ok)" }}>
        {isIn ? "↓" : "↑"}
      </span>
      <span
        style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
      >
        {s.name}
      </span>
    </span>
  );
}

function ServiceNode({ data, selected }: NodeProps<ServiceNodeData>) {
  // Health reflects the service's configured health checks only: a node is
  // red iff a check is firing. Raw error traces no longer redden it — the
  // graph shows health, not error counts.
  const unhealthy = data.status === "unhealthy";
  const hasErrors = unhealthy;
  const isActive = selected || data.selected;
  const schemas = data.schemas ?? [];
  return (
    <div
      style={{
        width: NODE_W,
        height: data.heightPx,
        borderRadius: 8,
        background: "var(--surface-2)",
        border: `${isActive ? 2 : 1}px solid ${
          isActive
            ? "var(--primary)"
            : hasErrors
              ? "var(--err)"
              : "var(--border)"
        }`,
        padding: "10px 12px",
        boxShadow: isActive
          ? "0 0 0 3px color-mix(in srgb, var(--primary) 22%, transparent)"
          : data.highlighted
            ? "0 0 0 3px color-mix(in srgb, var(--ok) 22%, transparent)"
            : undefined,
        position: "relative",
        cursor: "pointer",
        fontFamily: "inherit",
        color: "var(--ink-2)",
      }}
    >
      <div
        style={{
          fontSize: 10,
          textTransform: "uppercase",
          letterSpacing: "0.04em",
          color: "var(--muted)",
        }}
      >
        service
      </div>
      <div
        style={{
          fontSize: 14,
          fontWeight: 600,
          marginTop: 2,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      >
        {data.label}
      </div>
      <div style={{ fontSize: 11, color: "var(--muted)", marginTop: 4 }}>
        {formatNumber(data.traces)} traces
      </div>
      {schemas.length > 0 && (
        <div
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: 3,
            marginTop: 6,
            maxHeight: 38,
            overflow: "hidden",
          }}
        >
          {schemas.slice(0, 4).map((s) => (
            <SchemaChip key={`${s.schema_id}-${s.direction}`} s={s} />
          ))}
          {schemas.length > 4 && (
            <span style={{ fontSize: 10, color: "var(--muted)" }}>
              +{schemas.length - 4}
            </span>
          )}
        </div>
      )}
      {hasErrors && (
        <span
          style={{
            position: "absolute",
            top: -10,
            right: -10,
            background: "var(--err)",
            color: "white",
            borderRadius: 12,
            padding: "2px 8px",
            fontSize: 11,
            fontWeight: 600,
            border: "2px solid var(--surface)",
            boxShadow: "0 1px 3px rgba(0,0,0,0.15)",
          }}
        >
          unhealthy
        </span>
      )}
      <Handle
        type="target"
        position={Position.Left}
        style={{ background: "var(--border)", border: "none", width: 6, height: 6 }}
      />
      <Handle
        type="source"
        position={Position.Right}
        style={{ background: "var(--border)", border: "none", width: 6, height: 6 }}
      />
    </div>
  );
}

const nodeTypes = { service: ServiceNode };

// ── Layout ──────────────────────────────────────────────────────────
function layout(nodes: FlowNode[], edges: FlowEdge[], nodeH: number) {
  if (nodes.length === 0) return new Map<string, { x: number; y: number }>();

  const incoming = new Map<string, string[]>();
  nodes.forEach((n) => incoming.set(n.service_name, []));
  edges.forEach((e) => {
    if (!incoming.has(e.target)) incoming.set(e.target, []);
    incoming.get(e.target)!.push(e.source);
  });

  const depths = new Map<string, number>();
  const visiting = new Set<string>();
  const depth = (name: string): number => {
    if (depths.has(name)) return depths.get(name)!;
    if (visiting.has(name)) return 0;
    visiting.add(name);
    const ins = incoming.get(name) ?? [];
    const d = ins.length === 0 ? 0 : Math.max(0, ...ins.map(depth)) + 1;
    visiting.delete(name);
    depths.set(name, d);
    return d;
  };
  nodes.forEach((n) => depth(n.service_name));

  const columns = new Map<number, string[]>();
  let maxDepth = 0;
  nodes.forEach((n) => {
    const d = depths.get(n.service_name) ?? 0;
    if (d > maxDepth) maxDepth = d;
    if (!columns.has(d)) columns.set(d, []);
    columns.get(d)!.push(n.service_name);
  });

  let maxCol = 0;
  columns.forEach((c) => {
    if (c.length > maxCol) maxCol = c.length;
  });
  const innerHeight = Math.max(nodeH, maxCol * nodeH + (maxCol - 1) * ROW_GAP);

  const pos = new Map<string, { x: number; y: number }>();
  columns.forEach((services, d) => {
    const colH = services.length * nodeH + (services.length - 1) * ROW_GAP;
    const startY = (innerHeight - colH) / 2;
    services.forEach((s, i) => {
      pos.set(s, {
        x: d * (NODE_W + COL_GAP),
        y: startY + i * (nodeH + ROW_GAP),
      });
    });
  });
  return pos;
}

// ── Inner component (needs Provider for useReactFlow) ───────────────
function Inner({
  nodes: rawNodes,
  edges: rawEdges,
  selected,
  onSelect,
  highlightPath,
  highlightEdges,
  serviceSchemas,
  maps,
  statusByService,
}: Props) {
  const rf = useReactFlow();

  // Taller nodes only when at least one member service has schemas, so
  // the graph stays compact when there's no data-shape overlay.
  const hasSchemas = useMemo(
    () => Object.values(serviceSchemas ?? {}).some((arr) => arr.length > 0),
    [serviceSchemas],
  );
  const nodeH = hasSchemas ? NODE_H_SCHEMA : NODE_H;

  const positions = useMemo(
    () => layout(rawNodes, rawEdges, nodeH),
    [rawNodes, rawEdges, nodeH],
  );

  // Per-service produced ("out") / consumed ("in") schema name sets, so
  // a hop A→B can be labeled with a map whose from-schema A produces and
  // to-schema B consumes — "schema 1 → map x → schema 2" on the wire.
  const { outBySvc, inBySvc } = useMemo(() => {
    const out = new Map<string, Set<string>>();
    const inn = new Map<string, Set<string>>();
    for (const [svc, list] of Object.entries(serviceSchemas ?? {})) {
      for (const s of list) {
        const m = s.direction === "out" ? out : inn;
        if (!m.has(svc)) m.set(svc, new Set());
        m.get(svc)!.add(s.name);
      }
    }
    return { outBySvc: out, inBySvc: inn };
  }, [serviceSchemas]);

  const mapForEdge = useMemo(() => {
    const byEdge = new Map<string, string>();
    if (!maps?.length) return byEdge;
    for (const e of rawEdges) {
      const srcOut = outBySvc.get(e.source);
      const tgtIn = inBySvc.get(e.target);
      if (!srcOut || !tgtIn) continue;
      const m = maps.find(
        (mp) =>
          mp.from_schema &&
          mp.to_schema &&
          srcOut.has(mp.from_schema) &&
          tgtIn.has(mp.to_schema),
      );
      if (m) byEdge.set(`${e.source}|${e.target}`, m.name);
    }
    return byEdge;
  }, [maps, rawEdges, outBySvc, inBySvc]);

  const nodes: Node<ServiceNodeData>[] = useMemo(
    () =>
      rawNodes.map((n) => {
        const p = positions.get(n.service_name) ?? { x: 0, y: 0 };
        return {
          id: n.service_name,
          type: "service",
          position: p,
          data: {
            label: n.service_name,
            errors: n.error_trace_count,
            traces: n.trace_count,
            status: statusByService?.[n.service_name],
            selected: n.service_name === selected,
            highlighted: highlightPath?.has(n.service_name) ?? false,
            schemas: serviceSchemas?.[n.service_name],
            heightPx: nodeH,
          },
          draggable: false,
          selectable: true,
        };
      }),
    [rawNodes, positions, selected, highlightPath, serviceSchemas, nodeH, statusByService],
  );

  const edges: Edge[] = useMemo(
    () =>
      rawEdges.map((e) => {
        // The integration graph shows health, not error traces — hops aren't
        // reddened by errored calls any more (only highlighted-path styling).
        const isError = false;
        const key = `${e.source}|${e.target}`;
        const isPath = highlightEdges?.has(key) ?? false;
        const mapName = mapForEdge.get(key);
        return {
          id: key,
          source: e.source,
          target: e.target,
          type: "smoothstep",
          animated: isPath,
          // Prefer the bridging map name (the data transformation on
          // this hop); fall back to the call count.
          label: mapName
            ? `⇄ ${mapName}`
            : e.call_count > 1
              ? formatNumber(e.call_count)
              : undefined,
          labelStyle: {
            fill: mapName ? "var(--primary-ink)" : "var(--muted)",
            fontSize: 11,
            fontWeight: mapName ? 600 : 400,
          },
          labelBgStyle: { fill: mapName ? "var(--primary-soft)" : "var(--surface)" },
          labelBgPadding: [4, 2],
          style: {
            stroke: isError
              ? "var(--err)"
              : isPath
                ? "var(--ok)"
                : "var(--ink-2)",
            strokeWidth: isPath ? 2 : 1.4,
            opacity: isError || isPath ? 1 : 0.55,
          },
          markerEnd: {
            type: MarkerType.ArrowClosed,
            color: isError
              ? "var(--err)"
              : isPath
                ? "var(--ok)"
                : "var(--ink-2)",
            width: 14,
            height: 14,
          },
        };
      }),
    [rawEdges, highlightEdges, mapForEdge],
  );

  // Auto-fit whenever the data shape changes.
  useEffect(() => {
    const t = window.setTimeout(() => rf.fitView({ padding: 0.15, maxZoom: 1.05 }), 0);
    return () => window.clearTimeout(t);
  }, [rf, rawNodes.length, rawEdges.length]);

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      onNodeClick={(_, n) => onSelect?.(n.id)}
      onPaneClick={() => onSelect?.("")}
      proOptions={{ hideAttribution: true }}
      nodesDraggable={false}
      nodesConnectable={false}
      panOnScroll
      fitView
    >
      <Background variant={BackgroundVariant.Dots} gap={20} size={1} color="var(--border)" />
      <Controls
        showInteractive={false}
        style={{ background: "var(--surface-2)", border: "1px solid var(--border)" }}
      />
    </ReactFlow>
  );
}

export default function IntegrationFlow(props: Props) {
  if (props.nodes.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border p-6 text-sm text-muted">
        No service flow to draw — this integration has no spans in the window.
      </div>
    );
  }
  // Fill the caller's box exactly — React Flow positions its zoom/fit
  // controls absolutely against this element, so it must not exceed the
  // parent's fixed height or the controls spill out of the card. (A
  // previous minHeight:320 overflowed the trace page's 280px box.)
  return (
    <div style={{ width: "100%", height: "100%" }}>
      <ReactFlowProvider>
        <Inner {...props} />
      </ReactFlowProvider>
    </div>
  );
}
