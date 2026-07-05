// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// System-types catalog manager. The managed list of system kinds (RabbitMQ,
// Kafka, OTel Collector, …) and what each owns: detection prefixes (auto-
// identify the type from a service's emitted metrics) and starter health
// checks. Built-ins are read-only (shipped in code); orgs add custom types and
// can override a built-in by reusing its key. A new type can copy its starter
// checks from a built-in (per-check editing happens on a service, as elsewhere).

import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import type { MonitoringTemplateCheck, SystemType } from "../api/types";
import { usePageTitle } from "../lib/usePageTitle";
import { useCurrentUser } from "../lib/useCurrentUser";
import { EditDrawer } from "../components/primitives";

function checkLine(c: MonitoringTemplateCheck): string {
  if (c.signal === "log") {
    const sev = c.min_severity ? `severity≥${c.min_severity}` : "any severity";
    const body = c.body_contains ? ` body~"${c.body_contains}"` : "";
    return `log · ${sev}${body} · ≥${c.log_threshold ?? 1} in window`;
  }
  return `metric · ${c.agg} ${c.metric} ${c.op} ${c.threshold}`;
}

const blankDraft = { key: "", label: "", is_system: true, prefixes: "", copyFrom: "" };

export default function SystemTypesPage() {
  usePageTitle("System types");
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");

  const [types, setTypes] = useState<SystemType[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [creating, setCreating] = useState(false);
  const [draft, setDraft] = useState(blankDraft);

  const reload = useCallback(() => {
    setLoading(true);
    api
      .listSystemTypes()
      .then((r) => setTypes(r.system_types ?? []))
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  }, []);
  useEffect(() => reload(), [reload]);

  const builtIns = useMemo(() => types.filter((t) => t.built_in), [types]);
  const rowKey = (t: SystemType) => t.id || `builtin:${t.key}`;

  const create = async () => {
    const key = draft.key.trim().toLowerCase();
    const label = draft.label.trim();
    if (!key || !label) {
      setError("Key and label are required.");
      return;
    }
    const prefixes = draft.prefixes.split(",").map((p) => p.trim()).filter(Boolean);
    const checks = draft.copyFrom ? builtIns.find((b) => b.key === draft.copyFrom)?.checks ?? [] : [];
    setBusy(true);
    setError(null);
    try {
      await api.createSystemType({ key, label, is_system: draft.is_system, detect_prefixes: prefixes, checks });
      setDraft(blankDraft);
      setCreating(false);
      reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const editIdentity = async (t: SystemType) => {
    const label = window.prompt(`Label for "${t.key}":`, t.label);
    if (label === null) return;
    const prefixes = window.prompt(
      "Detection prefixes (comma-separated metric-name prefixes):",
      t.detect_prefixes.join(", "),
    );
    if (prefixes === null) return;
    setBusy(true);
    setError(null);
    try {
      await api.updateSystemType(t.id, {
        label: label.trim() || t.label,
        is_system: t.is_system,
        detect_prefixes: prefixes.split(",").map((p) => p.trim()).filter(Boolean),
        checks: t.checks,
      });
      reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (t: SystemType) => {
    if (!window.confirm(`Delete custom system type "${t.label}"? Checks already applied to services are not removed.`)) return;
    setBusy(true);
    setError(null);
    try {
      await api.deleteSystemType(t.id);
      reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const toggle = (k: string) =>
    setExpanded((prev) => {
      const n = new Set(prev);
      n.has(k) ? n.delete(k) : n.add(k);
      return n;
    });

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">System types</h1>
          <p className="page__subtitle">
            The catalog of system kinds. Each type owns the metric-name prefixes that auto-identify it and the
            starter health checks it applies. Built-ins are read-only — add your own, or override a built-in by
            reusing its key.
          </p>
        </div>
        {canWrite && (
          <button type="button" className="btn primary" disabled={busy} onClick={() => setCreating((c) => !c)}>
            {creating ? "Cancel" : "New system type"}
          </button>
        )}
      </div>

      {error && <div className="alert alert--error">{error}</div>}

      {creating && canWrite && (
        <EditDrawer title="New system type" width="narrow" onClose={() => setCreating(false)}>
          <form className="form" onSubmit={(e) => { e.preventDefault(); create(); }}>
            <div className="form__row">
              <label className="form__label">
                Key
                <input className="search__input" placeholder="e.g. nats" value={draft.key} onChange={(e) => setDraft({ ...draft, key: e.target.value })} autoFocus required />
                <span className="form__hint">Machine identifier; reuse a built-in's key to override it.</span>
              </label>
              <label className="form__label">
                Label
                <input className="search__input" placeholder="e.g. NATS" value={draft.label} onChange={(e) => setDraft({ ...draft, label: e.target.value })} required />
              </label>
            </div>
            <label className="form__label">
              Copy starter checks from
              <select className="search__input" value={draft.copyFrom} onChange={(e) => setDraft({ ...draft, copyFrom: e.target.value })}>
                <option value="">— none (empty) —</option>
                {builtIns.map((b) => (
                  <option key={b.key} value={b.key}>{b.label} ({b.checks.length})</option>
                ))}
              </select>
            </label>
            <label className="form__label">
              Detection prefixes
              <input className="search__input" placeholder="e.g. nats_, nats.server" value={draft.prefixes} onChange={(e) => setDraft({ ...draft, prefixes: e.target.value })} />
              <span className="form__hint">Comma-separated metric-name prefixes that auto-identify this type.</span>
            </label>
            <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13 }}>
              <input type="checkbox" checked={draft.is_system} onChange={(e) => setDraft({ ...draft, is_system: e.target.checked })} />
              This is a system (broker / infrastructure) — appears in the Systems view
            </label>
            <div className="form__actions">
              <button type="button" className="btn" onClick={() => setCreating(false)} disabled={busy}>Cancel</button>
              <button type="submit" className="btn btn--primary" disabled={busy}>Create system type</button>
            </div>
          </form>
        </EditDrawer>
      )}

      <div className="card">
        <div className="card__header">Catalog · {types.length}</div>
        {loading && types.length === 0 ? (
          <div className="placeholder" style={{ margin: 12 }}>Loading…</div>
        ) : types.length === 0 ? (
          <div className="placeholder" style={{ margin: 12 }}>No system types.</div>
        ) : (
          <div style={{ padding: "4px 16px 8px" }}>
            {types.map((t, i) => {
              const k = rowKey(t);
              return (
                <div key={k} style={{ borderTop: i === 0 ? undefined : "1px solid var(--border)", padding: "10px 0" }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                    <button
                      type="button"
                      onClick={() => toggle(k)}
                      style={{ border: 0, background: "transparent", cursor: "pointer", color: "var(--muted)", width: 12 }}
                      aria-label={expanded.has(k) ? "Collapse" : "Expand"}
                    >
                      {expanded.has(k) ? "▾" : "▸"}
                    </button>
                    <span style={{ fontWeight: 600 }}>{t.label}</span>
                    <span className="mono" style={{ fontSize: 11.5, color: "var(--muted)" }}>{t.key}</span>
                    {t.built_in ? (
                      <span className="muted" style={{ fontSize: 11 }}>built-in</span>
                    ) : (
                      <span className="badge-brand">custom</span>
                    )}
                    {t.is_system && <span className="muted" style={{ fontSize: 11 }}>system</span>}
                    <span className="badge-brand">{t.checks.length} check{t.checks.length === 1 ? "" : "s"}</span>
                    {canWrite && !t.built_in && (
                      <span style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
                        <button type="button" className="btn btn--sm" disabled={busy} onClick={() => editIdentity(t)}>Edit</button>
                        <button type="button" className="btn btn--sm btn--danger" disabled={busy} onClick={() => remove(t)}>Delete</button>
                      </span>
                    )}
                  </div>
                  {expanded.has(k) && (
                    <div style={{ marginLeft: 22, marginTop: 6, display: "flex", flexDirection: "column", gap: 4 }}>
                      <div style={{ fontSize: 12 }}>
                        <span className="muted">Detection prefixes: </span>
                        {t.detect_prefixes.length ? (
                          <span className="mono" style={{ fontSize: 11.5 }}>{t.detect_prefixes.join(", ")}</span>
                        ) : (
                          <span className="muted">none — won't auto-detect</span>
                        )}
                      </div>
                      {t.checks.length === 0 ? (
                        <div className="muted" style={{ fontSize: 12.5 }}>No starter checks.</div>
                      ) : (
                        t.checks.map((c, j) => (
                          <div key={j} style={{ fontSize: 12.5 }}>
                            <span style={{ fontWeight: 600 }}>{c.name}</span>
                            <span className="mono" style={{ marginLeft: 8, color: "var(--muted)", fontSize: 11.5 }}>{checkLine(c)}</span>
                          </div>
                        ))
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>

      <p className="muted" style={{ fontSize: 12, marginTop: 12 }}>
        To change a type's checks: create or fork it with starter checks, apply it to a service, adjust the checks
        there, then save them back. (An in-place check editor is a later add.)
      </p>
    </div>
  );
}
