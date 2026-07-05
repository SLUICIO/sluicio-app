// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Derive the wireframe-style service signature for an integration —
// e.g. `file→queue→http` — from a list of services. The signature
// is intentionally lossy: it shows the *kinds* of services present,
// not their names, so users can scan a dashboard and infer pipeline
// shape at a glance.

import type { FlowEdge, FlowNode, ServiceSummary } from "../api/types";

// Map facet slugs to compact glyphs. Both directions (input/output)
// of the same kind collapse to one glyph since the signature shows
// pipeline *shape*, not direction.
const FACET_GLYPH: Record<string, string> = {
  "file-input": "file",
  "file-output": "file",
  "queue-input": "queue",
  "queue-output": "queue",
  "http-input": "http",
  "http-output": "http",
  "db-output": "db",
  "email-output": "email",
  worker: "worker",
};

// Order of preference when a service carries multiple facets. The
// first one in this list that the service has wins; `core` is
// intentionally not in the map at all so it never represents a
// service in the signature.
const FACET_PRIORITY = [
  "file-input",
  "file-output",
  "queue-input",
  "queue-output",
  "http-input",
  "http-output",
  "db-output",
  "email-output",
  "worker",
];

function glyphFor(s: ServiceSummary): string {
  if (s.service_facets) {
    const present = new Set(s.service_facets.map((f) => f.slug));
    for (const slug of FACET_PRIORITY) {
      if (present.has(slug)) return FACET_GLYPH[slug];
    }
  }
  return s.service_name;
}

export function serviceSignature(services: ServiceSummary[] | undefined): string {
  if (!services || services.length === 0) return "—";
  const seq: string[] = [];
  for (const s of services) {
    const compact = glyphFor(s);
    if (seq[seq.length - 1] !== compact) seq.push(compact);
  }
  return seq.join("→");
}

// When a flow response is available, the topology gives a better
// signature than the unordered service list because it preserves
// order. Sources (no incoming edges) first, then a BFS by edge.
export function signatureFromFlow(flow: { nodes: FlowNode[]; edges: FlowEdge[] }): string {
  if (flow.nodes.length === 0) return "—";

  const incoming = new Map<string, string[]>();
  flow.nodes.forEach((n) => incoming.set(n.service_name, []));
  flow.edges.forEach((e) => {
    incoming.get(e.target)?.push(e.source);
  });
  const adjacency = new Map<string, string[]>();
  flow.nodes.forEach((n) => adjacency.set(n.service_name, []));
  flow.edges.forEach((e) => adjacency.get(e.source)?.push(e.target));

  // Topological-ish BFS from sources.
  const sources = flow.nodes
    .map((n) => n.service_name)
    .filter((n) => (incoming.get(n) ?? []).length === 0);
  const visited = new Set<string>();
  const order: string[] = [];
  const queue = [...sources];
  while (queue.length > 0) {
    const cur = queue.shift()!;
    if (visited.has(cur)) continue;
    visited.add(cur);
    order.push(cur);
    (adjacency.get(cur) ?? []).forEach((t) => queue.push(t));
  }
  // Catch any disconnected nodes (cycles).
  flow.nodes.forEach((n) => {
    if (!visited.has(n.service_name)) order.push(n.service_name);
  });

  // Render compact glyphs. Without service-type info on FlowNodes we
  // fall back to short forms of the service name.
  return order.map(shortName).join("→");
}

function shortName(name: string): string {
  // Drop a common "-svc" suffix and namespace prefix.
  const cleaned = name.replace(/-svc$/, "").split(".").pop() ?? name;
  return cleaned.length > 12 ? cleaned.slice(0, 12) + "…" : cleaned;
}
