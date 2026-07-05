// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Topology — a multi-perspective relationship explorer:
//   - Services:     flat service dependency graph (caller→callee hops).
//   - Integrations: drill-down tree, integration → member services.
//   - Systems:      drill-down tree, system → member services.
//   - Metadata:     drill-down tree, field → value → integration → services,
//                   filtered by attribute.
// Services uses IntegrationFlow + the time window; the others use ExpandableTree
// (expand/collapse). Backends: GET /api/v1/topology, /integrations, /systems,
// /metadata-graph.

import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import type { FlowResponse, Integration, MetaGraphResponse, ServiceStatus, System } from "../api/types";
import ExpandableTree, { type TreeNode } from "../components/ExpandableTree";
import IntegrationFlow from "../components/IntegrationFlow";
import SearchableSelect from "../components/SearchableSelect";
import TimeWindowPicker from "../components/TimeWindowPicker";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";

type Perspective = "services" | "integrations" | "systems" | "metadata";
const PERSPECTIVES: { key: Perspective; label: string }[] = [
  { key: "services", label: "Services" },
  { key: "integrations", label: "Integrations" },
  { key: "systems", label: "Systems" },
  { key: "metadata", label: "Metadata" },
];

const errText = (e: unknown) => String((e as Error)?.message ?? e);
const svcChild = (prefix: string, name: string): TreeNode => ({
  id: `${prefix}:${name}`,
  label: name,
  kind: "service",
  href: `/services/${encodeURIComponent(name)}`,
});

function buildMetadataTree(meta: MetaGraphResponse | null, integrations: Integration[] | null, field: string): TreeNode[] {
  if (!meta) return [];
  const intById = new Map<string, Integration>();
  (integrations ?? []).forEach((i) => intById.set(i.id, i));
  const intNodeToUUID = new Map<string, string>();
  meta.nodes.forEach((n) => {
    if (n.kind === "integration" && n.integration_id) intNodeToUUID.set(n.id, n.integration_id);
  });
  const intsByAttr = new Map<string, string[]>(); // attr node id → integration node ids
  meta.edges.forEach((e) => {
    const arr = intsByAttr.get(e.target) ?? [];
    arr.push(e.source);
    intsByAttr.set(e.target, arr);
  });
  const labelByNode = new Map(meta.nodes.map((n) => [n.id, n.label] as const));

  const intChild = (intNodeId: string, keyPrefix: string): TreeNode => {
    const uuid = intNodeToUUID.get(intNodeId);
    const integ = uuid ? intById.get(uuid) : undefined;
    return {
      id: `${keyPrefix}:${intNodeId}`,
      label: integ?.name ?? labelByNode.get(intNodeId) ?? "integration",
      kind: "integration",
      href: uuid ? `/integrations/${uuid}` : undefined,
      children: (integ?.services ?? []).map((s) => svcChild(`${keyPrefix}:${intNodeId}`, s)),
    };
  };

  const valuesByField = new Map<string, MetaGraphResponse["nodes"]>();
  meta.nodes.forEach((n) => {
    if (n.kind === "value" && n.field) {
      const a = valuesByField.get(n.field) ?? [];
      a.push(n);
      valuesByField.set(n.field, a);
    }
  });
  const tagNodes = meta.nodes.filter((n) => n.kind === "tag");

  const fieldRoot = (key: string, label: string): TreeNode => ({
    id: `f:${key}`,
    label,
    kind: "field",
    children: (valuesByField.get(key) ?? []).map((v) => ({
      id: `v:${v.id}`,
      label: v.label,
      kind: "value",
      children: (intsByAttr.get(v.id) ?? []).map((n) => intChild(n, `v:${v.id}`)),
    })),
  });
  const tagsRoot = (): TreeNode => ({
    id: "f:__tags__",
    label: "Tags",
    kind: "field",
    children: tagNodes.map((t) => ({
      id: `t:${t.id}`,
      label: t.label,
      kind: "tag",
      children: (intsByAttr.get(t.id) ?? []).map((n) => intChild(n, `t:${t.id}`)),
    })),
  });

  if (field === "__tags__") return [tagsRoot()];
  if (field !== "all") {
    const f = meta.fields.find((x) => x.key === field);
    return f ? [fieldRoot(f.key, f.label)] : [];
  }
  return [...meta.fields.map((f) => fieldRoot(f.key, f.label)), tagsRoot()];
}

export default function Topology() {
  usePageTitle("Topology");
  const navigate = useNavigate();
  const [range] = useTimeWindow();
  const [perspective, setPerspective] = useState<Perspective>("services");

  const [flow, setFlow] = useState<FlowResponse | null>(null);
  const [integrations, setIntegrations] = useState<Integration[] | null>(null);
  const [systems, setSystems] = useState<System[] | null>(null);
  const [meta, setMeta] = useState<MetaGraphResponse | null>(null);
  const [field, setField] = useState("all");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let live = true;
    setLoading(true);
    setError(null);
    const fail = (e: unknown) => live && setError(errText(e));
    const done = () => live && setLoading(false);
    if (perspective === "services") {
      api.getTopology(range, "services").then((d) => live && setFlow(d)).catch(fail).finally(done);
    } else if (perspective === "integrations") {
      api.listIntegrations(range).then((d) => live && setIntegrations(d.integrations)).catch(fail).finally(done);
    } else if (perspective === "systems") {
      api.listSystems().then((d) => live && setSystems(d.systems)).catch(fail).finally(done);
    } else {
      Promise.all([api.getMetadataGraph(), api.listIntegrations(range)])
        .then(([m, i]) => {
          if (!live) return;
          setMeta(m);
          setIntegrations(i.integrations);
        })
        .catch(fail)
        .finally(done);
    }
    return () => {
      live = false;
    };
  }, [perspective, range]);

  const statusByService = useMemo(() => {
    const m: Record<string, ServiceStatus> = {};
    flow?.nodes.forEach((n) => {
      if (n.status) m[n.service_name] = n.status as ServiceStatus;
    });
    return m;
  }, [flow]);

  const integrationRoots = useMemo<TreeNode[]>(
    () =>
      (integrations ?? []).map((i) => ({
        id: `int:${i.id}`,
        label: i.name,
        kind: "integration",
        status: i.status,
        href: `/integrations/${i.id}`,
        children: (i.services ?? []).map((s) => svcChild(`int:${i.id}`, s)),
      })),
    [integrations],
  );
  const systemRoots = useMemo<TreeNode[]>(
    () =>
      (systems ?? []).map((sy) => ({
        id: `sys:${sy.id}`,
        label: sy.name,
        kind: "system",
        status: sy.status as ServiceStatus | undefined,
        href: `/systems/${sy.id}`,
        children: (sy.members ?? []).map((s) => svcChild(`sys:${sy.id}`, s)),
      })),
    [systems],
  );
  const metadataRoots = useMemo(() => buildMetadataTree(meta, integrations, field), [meta, integrations, field]);

  const fieldOptions = useMemo(() => ["all", ...(meta?.fields.map((f) => f.key) ?? []), "__tags__"], [meta]);
  const fieldLabel = (v: string) =>
    v === "all" ? "All attributes" : v === "__tags__" ? "Tags" : meta?.fields.find((f) => f.key === v)?.label ?? v;

  const openNode = (n: TreeNode) => {
    if (n.href) navigate(n.href);
  };

  return (
    <div className="page" style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 12, flexWrap: "wrap" }}>
        <div>
          <h1 className="page__title" style={{ margin: 0 }}>Topology</h1>
          <p className="page__subtitle" style={{ margin: "2px 0 0" }}>
            Relationships across your cell — by service, integration, system, or metadata. Expand a node to drill in.
          </p>
        </div>
        <span style={{ flex: 1 }} />

        <div role="tablist" style={{ display: "inline-flex", border: "1px solid var(--border)", borderRadius: 8, overflow: "hidden" }}>
          {PERSPECTIVES.map((p) => {
            const active = perspective === p.key;
            return (
              <button
                key={p.key}
                role="tab"
                aria-selected={active}
                onClick={() => setPerspective(p.key)}
                style={{
                  border: "none",
                  background: active ? "var(--primary)" : "transparent",
                  color: active ? "#fff" : "var(--ink-2)",
                  padding: "6px 14px",
                  fontSize: 13,
                  cursor: "pointer",
                }}
              >
                {p.label}
              </button>
            );
          })}
        </div>

        {perspective === "metadata" ? (
          <div style={{ minWidth: 220 }}>
            <SearchableSelect value={field} onChange={setField} options={fieldOptions} labelFor={fieldLabel} placeholder="Group by attribute…" align="right" />
          </div>
        ) : (
          <TimeWindowPicker />
        )}
      </div>

      {perspective === "services" && flow?.historical && (
        <div className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
          No hops in the selected window — showing the structural graph from historical data (edge counts shown as zero).
        </div>
      )}
      {error && <div className="alert alert--error" role="alert" style={{ marginBottom: 8 }}>{error}</div>}

      <div style={{ flex: 1, minHeight: 480, border: "1px solid var(--border)", borderRadius: 8, overflow: "hidden" }}>
        {loading && !flow && !integrations && !systems && !meta ? (
          <div className="muted" style={{ padding: 24 }}>Loading…</div>
        ) : perspective === "services" ? (
          flow ? (
            <IntegrationFlow
              nodes={flow.nodes}
              edges={flow.edges}
              statusByService={statusByService}
              onSelect={(name) => name && navigate(`/services/${encodeURIComponent(name)}`)}
            />
          ) : (
            <div className="muted" style={{ padding: 24 }}>Nothing to show yet.</div>
          )
        ) : perspective === "integrations" ? (
          <ExpandableTree roots={integrationRoots} onOpen={openNode} />
        ) : perspective === "systems" ? (
          <ExpandableTree roots={systemRoots} onOpen={openNode} />
        ) : (
          <ExpandableTree roots={metadataRoots} onOpen={openNode} />
        )}
      </div>
    </div>
  );
}
