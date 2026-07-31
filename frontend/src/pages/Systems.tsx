// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Systems — first-class entities (phase 2). A system is an instance of a system
// type that spans member services. This lists the systems; open one to see and
// manage its members. Flagging a service as a system (on its detail page) also
// creates/attaches a system here.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { System, SystemType } from "../api/types";
import { formatNumber } from "../lib/format";
import { usePageTitle } from "../lib/usePageTitle";
import { useCurrentUser } from "../lib/useCurrentUser";
import StatusPip from "../components/primitives/StatusPip";
import { EditDrawer } from "../components/primitives";
import SystemTypePicker from "../components/SystemTypePicker";
import { pipForStatus } from "../components/primitives/pipForStatus";

export default function Systems() {
  usePageTitle("Systems");
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  const [systems, setSystems] = useState<System[]>([]);
  const [types, setTypes] = useState<SystemType[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [newTypeKey, setNewTypeKey] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [searchParams, setSearchParams] = useSearchParams();
  const statusFilter = searchParams.get("status") ?? "";
  const clearStatus = () => {
    const p = new URLSearchParams(searchParams);
    p.delete("status");
    setSearchParams(p, { replace: true });
  };

  const refresh = useCallback(() => {
    setLoading(true);
    setError(null);
    api
      .listSystems()
      .then((r) => setSystems(r.systems ?? []))
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  }, []);
  useEffect(() => {
    refresh();
    api.listSystemTypes().then((r) => setTypes(r.system_types ?? [])).catch(() => {});
  }, [refresh]);

  const typeLabel = useMemo(() => {
    const m = new Map(types.map((t) => [t.key, t.label]));
    return (key: string) => m.get(key) || key || "—";
  }, [types]);

  const totalMembers = systems.reduce((n, s) => n + s.member_count, 0);

  // Health-status filter from the URL (?status=unhealthy), set by the
  // dashboard KPI drill-in. "unhealthy" spans errors + unhealthy to match
  // the dashboard's count.
  const visibleSystems = useMemo(() => {
    if (!statusFilter) return systems;
    return systems.filter((s) => {
      const st = s.status ?? "";
      return statusFilter === "unhealthy" ? st === "unhealthy" || st === "errors" : st === statusFilter;
    });
  }, [systems, statusFilter]);

  // Creating a system used to be two window.prompt() calls, the second
  // asking for the type KEY from memory ("e.g. rabbitmq, kafka,
  // postgresql"). type_key is stored verbatim and never checked against
  // the catalog, so "RabbitMQ" or "rabbit" saved cleanly and then matched
  // no type — the system got no starter checks, no monitoring template
  // and no docs link, with nothing to say why. A picker over the catalog
  // makes the valid answers visible and the invalid ones unreachable.
  const create = async () => {
    setNewName("");
    setNewTypeKey("");
    setCreateError(null);
    setCreating(true);
  };

  const submitCreate = async () => {
    const name = newName.trim();
    if (!name) {
      setCreateError("A name is required.");
      return;
    }
    setBusy(true);
    setCreateError(null);
    try {
      await api.createSystem({ name, type_key: newTypeKey });
      setCreating(false);
      refresh();
    } catch (e) {
      setCreateError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Systems</h1>
          <p className="page__subtitle">
            Infrastructure you monitor through Sluicio — a system (RabbitMQ, SQL Server, a Kafka estate, …) spans
            the services that make it up. Open one to manage its members; its health rolls up from theirs.
          </p>
        </div>
        <div className="toolbar">
          {canWrite && (
            <button className="btn primary" onClick={create} disabled={busy}>New system</button>
          )}
          <button className="btn" onClick={refresh} disabled={loading}>
            {loading ? "Loading…" : "Refresh"}
          </button>
        </div>
      </div>

      <div className="tiles">
        <Tile label="Systems" value={formatNumber(systems.length)} tone="neutral" />
        <Tile label="Member services" value={formatNumber(totalMembers)} tone="neutral" />
      </div>

      {statusFilter && (
        <div style={{ display: "flex", alignItems: "center", gap: 8, margin: "0 0 12px" }}>
          <span className="chip">Showing {statusFilter === "unhealthy" ? "unhealthy" : statusFilter} systems</span>
          <button type="button" className="btn btn--link" onClick={clearStatus}>Clear filter</button>
        </div>
      )}

      {error && <div className="alert alert--error">Failed to load systems: {error}</div>}

      {!error && systems.length === 0 && !loading && (
        <div className="placeholder">
          No systems yet. Create one above, or open a service (e.g. your RabbitMQ or SQL Server exporter) and use{" "}
          <strong>Mark as system</strong> on its page.
        </div>
      )}

      {systems.length > 0 && visibleSystems.length === 0 && statusFilter && (
        <div className="placeholder">
          No systems are currently {statusFilter === "unhealthy" ? "unhealthy" : statusFilter}.{" "}
          <button type="button" className="btn btn--link" onClick={clearStatus}>Show all</button>
        </div>
      )}

      {visibleSystems.length > 0 && (
        <div className="card">
          <div className="mtbl">
            <div className="mtbl-head" style={{ gridTemplateColumns: "2fr 1fr 140px 100px" }}>
              <div>Name</div>
              <div>Type</div>
              <div>Health</div>
              <div className="th-right">Members</div>
            </div>
            <div className="mtbl-body">
              {visibleSystems.map((s) => (
                <Link
                  key={s.id}
                  to={`/systems/${s.id}`}
                  className="mtbl-row"
                  style={{ display: "grid", gridTemplateColumns: "2fr 1fr 140px 100px", alignItems: "center", textDecoration: "none", color: "inherit" }}
                >
                  <div style={{ fontWeight: 600 }}>{s.name}</div>
                  <div><span className="badge-brand">{typeLabel(s.type_key)}</span></div>
                  <div><StatusPip kind={pipForStatus(s.status)} label={s.status || "—"} /></div>
                  <div className="th-right">{formatNumber(s.member_count)}</div>
                </Link>
              ))}
            </div>
          </div>
        </div>
      )}

      {creating && (
        <EditDrawer title="New system" onClose={() => setCreating(false)}>
          <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 13 }}>
              Name
              <input
                className="svc-input"
                autoFocus
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="e.g. Order Bus, Billing DB"
                onKeyDown={(e) => {
                  if (e.key === "Enter") submitCreate();
                }}
              />
            </label>
            <SystemTypePicker label="Type" value={newTypeKey} onChange={setNewTypeKey} />
            <span className="muted" style={{ fontSize: 11.5 }}>
              The type decides which starter checks and monitoring template this system can apply. It can be
              changed later, and left unset if you are not sure yet.
            </span>
            {createError && <div className="alert alert--error" style={{ margin: 0 }}>{createError}</div>}
            <div style={{ display: "flex", gap: 8 }}>
              <button className="btn primary" type="button" onClick={submitCreate} disabled={busy}>
                {busy ? "Creating…" : "Create system"}
              </button>
              <button className="btn" type="button" onClick={() => setCreating(false)} disabled={busy}>
                Cancel
              </button>
            </div>
          </div>
        </EditDrawer>
      )}
    </div>
  );
}

function Tile({ label, value, tone }: { label: string; value: string; tone: "ok" | "errors" | "neutral" }) {
  return (
    <div className={`tile tile--${tone}`}>
      <div className="tile__value">{value}</div>
      <div className="tile__label">{label}</div>
    </div>
  );
}
