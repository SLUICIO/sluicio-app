// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// System detail (phase 2). Shows a system entity, its member services (with
// health), and lets a writer attach/detach members, rename/retype, or delete
// it. A system's health rolls up from its members' health checks.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api } from "../api/client";
import ServicesTable from "../components/ServicesTable";
import type { AlertInstance, AlertRule, MetadataField, ServiceSummary, System } from "../api/types";
import { formatRelative } from "../lib/format";
import { usePageTitle } from "../lib/usePageTitle";
import { useCurrentUser } from "../lib/useCurrentUser";
import ResourceGroupsCard from "../components/ResourceGroupsCard";
import ResourceSharesCard from "../components/ResourceSharesCard";
import { useTimeWindow } from "../lib/useTimeWindow";
import StatusPip from "../components/primitives/StatusPip";
import { pipForStatus } from "../components/primitives/pipForStatus";
import SystemEditDrawer from "../components/SystemEditDrawer";

export default function SystemDetail() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { can } = useCurrentUser();
  const [serverCanManage, setServerCanManage] = useState<boolean | null>(null);
  // Scoped manage (RBAC v2): server-computed per system.
  const canWrite = serverCanManage ?? can("integration.write");
  const [windowVal] = useTimeWindow();

  const [system, setSystem] = useState<System | null>(null);
  const [members, setMembers] = useState<ServiceSummary[]>([]);
  const [allServices, setAllServices] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(false);
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [instances, setInstances] = useState<AlertInstance[]>([]);
  const [metaFields, setMetaFields] = useState<MetadataField[]>([]);
  const [metaValues, setMetaValues] = useState<Record<string, string>>({});
  const [metaSaving, setMetaSaving] = useState(false);
  const [metaSaved, setMetaSaved] = useState(false);

  usePageTitle(system ? `System · ${system.name}` : "System");

  const reload = useCallback(() => {
    setLoading(true);
    api
      .getSystem(id, windowVal)
      .then((r) => {
        setSystem(r.system);
        setMembers(r.members ?? []);
        setServerCanManage(r.can_manage ?? null);
      })
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  }, [id, windowVal]);
  useEffect(() => {
    reload();
    api.listServices(windowVal).then((r) => setAllServices((r.services ?? []).map((s) => s.service_name))).catch(() => {});
    // Health checks are service-bound alert rules; a failing one has a
    // firing instance. Load both org-wide and filter to this system's
    // members so failing checks show without opening each service.
    // 500 is the server max, and instances come back firing-first, so a
    // firing check can't be pushed out below any realistic concurrent count.
    api.listAlertRules({}).then((r) => setRules(r.rules ?? [])).catch(() => {});
    api.listAlertInstances(500).then((r) => setInstances(r.instances ?? [])).catch(() => {});
  }, [reload, windowVal]);

  const loadMeta = useCallback(() => {
    api
      .getSystemMetadata(id)
      .then((r) => {
        setMetaFields(r.fields ?? []);
        setMetaValues(r.metadata_values ?? {});
      })
      .catch(() => {});
  }, [id]);
  useEffect(() => loadMeta(), [loadMeta]);

  const saveMeta = async () => {
    setMetaSaving(true);
    setMetaSaved(false);
    setError(null);
    try {
      const r = await api.putSystemMetadata(id, metaValues);
      setMetaFields(r.fields ?? []);
      setMetaValues(r.metadata_values ?? {});
      setMetaSaved(true);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setMetaSaving(false);
    }
  };

  const memberNames = useMemo(() => new Set(members.map((m) => m.service_name)), [members]);
  const attachable = useMemo(() => allServices.filter((n) => !memberNames.has(n)), [allServices, memberNames]);

  // Failing health checks across the members — a service-bound rule that
  // currently has a firing instance. Surfaced here so you don't have to
  // open each unhealthy member to see what's wrong. Critical first.
  const failingChecks = useMemo(() => {
    const firing = new Map<string, AlertInstance>();
    for (const i of instances) if (i.state === "firing") firing.set(i.alert_rule_id, i);
    const rank: Record<string, number> = { critical: 3, warning: 2, info: 1 };
    return rules
      .filter((r) => r.service_name && memberNames.has(r.service_name) && firing.has(r.id))
      .map((r) => ({ rule: r, instance: firing.get(r.id) as AlertInstance }))
      .sort((a, b) => (rank[b.rule.severity] ?? 0) - (rank[a.rule.severity] ?? 0));
  }, [rules, instances, memberNames]);

  // Rollup health: worst of the members' statuses.
  const rollup = useMemo(() => {
    if (members.length === 0) return "quiet";
    if (members.some((m) => m.status === "unhealthy")) return "unhealthy";
    if (members.some((m) => m.status === "errors")) return "errors";
    if (members.some((m) => m.status === "ok")) return "ok";
    return "quiet";
  }, [members]);

  const applyChecks = async () => {
    if (!system) return;
    if (!window.confirm(`Apply the ${system.type_key} starter checks to all ${members.length} member service${members.length === 1 ? "" : "s"}?`)) return;
    setBusy(true);
    setError(null);
    try {
      const r = await api.applySystemTemplateAll(id);
      window.alert(r.message ? r.message : `Applied to ${r.members} member(s): ${r.created} created, ${r.skipped} already present.`);
      reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const detach = async (name: string) => {
    if (!window.confirm(`Remove ${name} from this system?`)) return;
    setBusy(true);
    setError(null);
    try {
      await api.detachSystemService(id, name);
      reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!system) return;
    if (!window.confirm(`Delete system "${system.name}"? Its services are detached (their health checks are not removed).`)) return;
    setBusy(true);
    try {
      await api.deleteSystem(id);
      navigate("/systems");
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };

  if (loading && !system) return <div className="placeholder" style={{ margin: 16 }}>Loading…</div>;
  if (error && !system) return <div className="alert alert--error">Failed to load system: {error}</div>;
  if (!system) return <div className="placeholder" style={{ margin: 16 }}>System not found.</div>;

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title" style={{ display: "flex", alignItems: "center", gap: 10 }}>
            {system.name}
            <StatusPip kind={pipForStatus(rollup)} label={rollup} />
          </h1>
          <p className="page__subtitle">
            {system.type_key ? <>Type <span className="badge-brand">{system.type_key}</span> · </> : null}
            {members.length} member service{members.length === 1 ? "" : "s"}
            {system.description ? <> · {system.description}</> : null}
          </p>
        </div>
        {canWrite && (
          <div className="toolbar">
            {system.type_key && members.length > 0 && (
              <button className="btn" onClick={applyChecks} disabled={busy} title="Apply this type's starter health checks to every member service">
                Apply {system.type_key} checks to members
              </button>
            )}
            <button className="btn" onClick={() => setEditing(true)} disabled={busy}>Edit</button>
            <button className="btn btn--danger" onClick={remove} disabled={busy}>Delete</button>
          </div>
        )}
      </div>

      {error && <div className="alert alert--error">{error}</div>}

      {failingChecks.length > 0 && (
        <div className="card" style={{ marginBottom: 16, padding: "12px 16px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
            <StatusPip kind="err" label={`${failingChecks.length} failing health check${failingChecks.length === 1 ? "" : "s"}`} />
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
            {failingChecks.map(({ rule, instance }) => (
              <div key={rule.id} style={{ display: "flex", alignItems: "flex-start", gap: 10, fontSize: 13, flexWrap: "wrap" }}>
                <span
                  style={{
                    fontSize: 11,
                    fontWeight: 600,
                    textTransform: "uppercase",
                    letterSpacing: "0.04em",
                    minWidth: 56,
                    color:
                      rule.severity === "critical" ? "var(--err)" : rule.severity === "warning" ? "var(--warn)" : "var(--muted)",
                  }}
                >
                  {rule.severity}
                </span>
                <div style={{ flex: 1, minWidth: 220 }}>
                  <div>
                    <Link to={`/services/${encodeURIComponent(rule.service_name ?? "")}?tab=health`} style={{ fontWeight: 600 }}>
                      {rule.service_name}
                    </Link>
                    <span className="muted"> · {rule.name}</span>
                  </div>
                  {instance.summary && (
                    <div className="muted" style={{ fontSize: 12, marginTop: 2 }}>{instance.summary}</div>
                  )}
                </div>
                <span className="muted" style={{ fontSize: 12, whiteSpace: "nowrap" }}>
                  since {formatRelative(instance.started_at)}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      <ResourceGroupsCard kind="system" id={id!} />
      <ResourceSharesCard kind="systems" id={id!} canManage={canWrite} />

      {metaFields.length > 0 && (
        <div className="card" style={{ marginBottom: 16, padding: "12px 16px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
            <span style={{ fontSize: 13, fontWeight: 600 }}>Metadata</span>
            {metaSaved && <span className="muted" style={{ fontSize: 12 }}>saved ✓</span>}
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            {metaFields.map((f) => {
              const v = metaValues[f.key] ?? "";
              const set = (val: string) => {
                setMetaValues({ ...metaValues, [f.key]: val });
                setMetaSaved(false);
              };
              return (
                <div key={f.key} style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                  <label style={{ minWidth: 180, fontSize: 13 }}>{f.label}{f.required ? " *" : ""}</label>
                  {f.type === "boolean" ? (
                    <input type="checkbox" disabled={!canWrite} checked={v === "true"} onChange={(e) => set(e.target.checked ? "true" : "false")} />
                  ) : f.type === "select" ? (
                    <select className="svc-input" style={{ minWidth: 220 }} disabled={!canWrite} value={v} onChange={(e) => set(e.target.value)}>
                      <option value="">—</option>
                      {(f.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}
                    </select>
                  ) : (
                    <input className="svc-input" style={{ minWidth: 260 }} type={f.type === "number" ? "number" : "text"} disabled={!canWrite} value={v} onChange={(e) => set(e.target.value)} />
                  )}
                  {f.description && <span className="muted" style={{ fontSize: 12 }}>{f.description}</span>}
                </div>
              );
            })}
          </div>
          {canWrite && <button className="btn primary" style={{ marginTop: 10 }} onClick={saveMeta} disabled={metaSaving}>Save metadata</button>}
        </div>
      )}

      {members.length === 0 ? (
        <div className="card" style={{ padding: "32px 24px", textAlign: "center" }}>
          <div style={{ fontSize: 15, fontWeight: 600, marginBottom: 6 }}>No member services yet</div>
          <p className="muted" style={{ fontSize: 13, margin: "0 auto 16px", maxWidth: 440, lineHeight: 1.5 }}>
            A system groups the services that make it up and rolls up their health.
            Add your first one to get started.
          </p>
          {canWrite ? (
            <>
              <button className="btn primary" onClick={() => setEditing(true)}>Edit &amp; add a service</button>
              <div className="muted" style={{ fontSize: 12, marginTop: 10 }}>
                Or flag a service as this system from the service&rsquo;s own page.
              </div>
            </>
          ) : (
            <span className="muted" style={{ fontSize: 12 }}>Ask an editor to add member services.</span>
          )}
        </div>
      ) : (
        <div className="card">
          <ServicesTable services={members} />
          {canWrite && (
            <div style={{ padding: "8px 16px", display: "flex", flexDirection: "column", gap: 4, borderTop: "1px solid var(--border)" }}>
              <span className="muted" style={{ fontSize: 12, textTransform: "uppercase", letterSpacing: "0.04em" }}>Manage members</span>
              {members.map((m) => (
                <div key={m.service_name} style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 13 }}>
                  <span style={{ flex: 1 }}>{m.service_name}</span>
                  <button className="btn btn--sm" onClick={() => detach(m.service_name)} disabled={busy}>Detach</button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {editing && (
        <SystemEditDrawer
          system={system}
          attachable={attachable}
          canWrite={canWrite}
          onClose={() => setEditing(false)}
          onSaved={reload}
        />
      )}
    </div>
  );
}
