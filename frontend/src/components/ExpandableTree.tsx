// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ExpandableTree — a drill-down graph for the Topology page's hierarchical
// perspectives (Integrations → services, Systems → services, Metadata field →
// value → integration → service). Roots render collapsed; a node with children
// shows a ▸/▾ toggle that reveals/hides its subtree. Clicking a node's body
// fires onOpen (navigation). Rendered with React Flow for pan/zoom/fit. The
// full tree is passed in precomputed; this component only manages expansion.

import { useEffect, useMemo, useState } from "react";
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
import type { ServiceStatus } from "../api/types";

export interface TreeNode {
  id: string;
  label: string;
  kind: string; // integration | system | service | field | value | tag
  status?: ServiceStatus;
  href?: string;
  children?: TreeNode[];
}

const NODE_W = 210;
const NODE_H = 46;
const ROW_GAP = 14;
const COL_GAP = 56;

interface NData {
  label: string;
  kind: string;
  status?: ServiceStatus;
  expandable: boolean;
  expanded: boolean;
  onToggle?: () => void;
  onOpen?: () => void;
}

function accentFor(kind: string, status?: ServiceStatus) {
  if (status === "unhealthy") return "var(--err)";
  switch (kind) {
    case "integration":
      return "var(--primary)";
    case "system":
      return "#c08a2d";
    case "tag":
      return "var(--ok)";
    case "field":
    case "value":
      return "var(--ink-2)";
    default:
      return "var(--border)"; // service
  }
}

function TreeNodeView({ data }: NodeProps<NData>) {
  return (
    <div
      style={{
        width: NODE_W,
        minHeight: NODE_H,
        display: "flex",
        alignItems: "center",
        gap: 8,
        borderRadius: 8,
        background: "var(--surface-2)",
        border: `1px solid ${accentFor(data.kind, data.status)}`,
        padding: "6px 10px",
        color: "var(--ink-2)",
        fontFamily: "inherit",
      }}
    >
      {data.expandable ? (
        <button
          onClick={(e) => {
            e.stopPropagation();
            data.onToggle?.();
          }}
          aria-label={data.expanded ? "Collapse" : "Expand"}
          style={{ border: "none", background: "transparent", cursor: "pointer", color: "var(--muted)", fontSize: 12, width: 14, flexShrink: 0 }}
        >
          {data.expanded ? "▾" : "▸"}
        </button>
      ) : (
        <span style={{ width: 14, flexShrink: 0 }} />
      )}
      <div onClick={data.onOpen} style={{ cursor: data.onOpen ? "pointer" : "default", overflow: "hidden" }}>
        <div style={{ fontSize: 9, textTransform: "uppercase", letterSpacing: "0.04em", color: "var(--muted)" }}>{data.kind}</div>
        <div style={{ fontSize: 13, fontWeight: 600, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{data.label}</div>
      </div>
      <Handle type="target" position={Position.Left} style={{ background: "var(--border)", border: "none", width: 6, height: 6 }} />
      <Handle type="source" position={Position.Right} style={{ background: "var(--border)", border: "none", width: 6, height: 6 }} />
    </div>
  );
}

const nodeTypes = { tree: TreeNodeView };

function Inner({ roots, onOpen }: { roots: TreeNode[]; onOpen?: (n: TreeNode) => void }) {
  const rf = useReactFlow();
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const { nodes, edges } = useMemo(() => {
    const rfNodes: Node<NData>[] = [];
    const rfEdges: Edge[] = [];
    let order = 0;
    const walk = (node: TreeNode, depth: number, parentId?: string) => {
      const y = order * (NODE_H + ROW_GAP);
      order += 1;
      const hasChildren = !!node.children && node.children.length > 0;
      const isExp = expanded.has(node.id);
      rfNodes.push({
        id: node.id,
        type: "tree",
        draggable: false,
        position: { x: depth * (NODE_W + COL_GAP), y },
        data: {
          label: node.label,
          kind: node.kind,
          status: node.status,
          expandable: hasChildren,
          expanded: isExp,
          onToggle: hasChildren ? () => toggle(node.id) : undefined,
          onOpen: onOpen ? () => onOpen(node) : undefined,
        },
      });
      if (parentId) {
        rfEdges.push({
          id: `${parentId}->${node.id}`,
          source: parentId,
          target: node.id,
          type: "smoothstep",
          style: { stroke: "var(--ink-2)", strokeWidth: 1.1, opacity: 0.5 },
          markerEnd: { type: MarkerType.ArrowClosed, color: "var(--ink-2)", width: 10, height: 10 },
        });
      }
      if (hasChildren && isExp) node.children!.forEach((c) => walk(c, depth + 1, node.id));
    };
    roots.forEach((r) => walk(r, 0));
    return { nodes: rfNodes, edges: rfEdges };
  }, [roots, expanded, onOpen]);

  useEffect(() => {
    const t = window.setTimeout(() => rf.fitView({ padding: 0.15, maxZoom: 1.1 }), 0);
    return () => window.clearTimeout(t);
  }, [rf, nodes.length]);

  if (roots.length === 0) {
    return <div className="muted" style={{ padding: 24 }}>Nothing to show for this perspective yet.</div>;
  }

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      proOptions={{ hideAttribution: true }}
      nodesDraggable={false}
      nodesConnectable={false}
      panOnScroll
      fitView
    >
      <Background variant={BackgroundVariant.Dots} gap={20} size={1} color="var(--border)" />
      <Controls showInteractive={false} style={{ background: "var(--surface-2)", border: "1px solid var(--border)" }} />
    </ReactFlow>
  );
}

export default function ExpandableTree({ roots, onOpen }: { roots: TreeNode[]; onOpen?: (n: TreeNode) => void }) {
  return (
    <ReactFlowProvider>
      <Inner roots={roots} onOpen={onOpen} />
    </ReactFlowProvider>
  );
}
