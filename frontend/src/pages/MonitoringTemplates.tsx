// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Monitoring templates manager. Two lists:
//   - Built-ins (from the system-type catalog): read-only, their checks
//     visible before you fork — so you know what you're getting.
//   - Your templates: expandable check lists with an in-place editor
//     (tune thresholds/severity/names, remove checks). Adding new checks
//     still goes via a service ("Save these checks as a template") where
//     the metric pickers live.
// Applying a template happens per service, from the service editor.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { MonitoringTemplate, MonitoringTemplateCheck, SystemType } from "../api/types";
import { usePageTitle } from "../lib/usePageTitle";
import { checkLine } from "../lib/checkLine";
import { useCurrentUser } from "../lib/useCurrentUser";

// checkLine lives in lib/checkLine — shared with the System types page.

function SeverityPill({ severity }: { severity?: string }) {
  const sev = severity || "warning";
  const color =
    sev === "critical" ? "var(--err, #dc2626)" : sev === "info" ? "var(--muted)" : "var(--warn, #d97706)";
  return (
    <span className="mono" style={{ fontSize: 10.5, fontWeight: 700, color, border: `1px solid ${color}`, borderRadius: 999, padding: "1px 7px" }}>
      {sev.toUpperCase()}
    </span>
  );
}

// CheckList — the read view of a template's checks, shared by built-ins
// and custom templates.
function CheckList({ checks }: { checks: MonitoringTemplateCheck[] }) {
  if (checks.length === 0) return <div className="muted" style={{ fontSize: 12.5 }}>No checks.</div>;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
      {checks.map((c, i) => (
        <div key={i} style={{ display: "flex", alignItems: "baseline", gap: 8, flexWrap: "wrap" }}>
          <span style={{ fontWeight: 600, fontSize: 12.5 }}>{c.name}</span>
          <SeverityPill severity={c.severity} />
          <span className="mono" style={{ color: "var(--muted)", fontSize: 11.5 }}>{checkLine(c)}</span>
          {c.description && <span className="muted" style={{ fontSize: 12, flexBasis: "100%", paddingLeft: 2 }}>{c.description}</span>}
        </div>
      ))}
    </div>
  );
}

export default function MonitoringTemplates() {
  usePageTitle("Monitoring templates");
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  const [templates, setTemplates] = useState<MonitoringTemplate[]>([]);
  const [systemTypes, setSystemTypes] = useState<SystemType[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  // Template id currently in check-edit mode, with its working copy.
  const [editingId, setEditingId] = useState<string | null>(null);
  const [draft, setDraft] = useState<MonitoringTemplateCheck[]>([]);

  const reload = useCallback(() => {
    setLoading(true);
    api
      .listMonitoringTemplates()
      .then((r) => setTemplates(r.templates ?? []))
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
    api
      .listSystemTypes()
      .then((r) => setSystemTypes(r.system_types ?? []))
      .catch(() => setSystemTypes([]));
  }, []);
  useEffect(() => reload(), [reload]);

  const sorted = useMemo(
    () => [...templates].sort((a, b) => a.name.localeCompare(b.name)),
    [templates],
  );
  // Anything in the effective catalog with checks can be forked into an
  // editable copy — built-ins and custom system types alike.
  const forkable = useMemo(
    () => systemTypes.filter((t) => (t.checks?.length ?? 0) > 0).sort((a, b) => a.label.localeCompare(b.label)),
    [systemTypes],
  );

  const fork = async (kind: string, label: string) => {
    const name = window.prompt(`Name the forked ${label} template:`, `${label} (custom)`);
    if (!name || !name.trim()) return;
    setBusy(true);
    setError(null);
    try {
      await api.createMonitoringTemplate({ name: name.trim(), fork_kind: kind });
      reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const rename = async (t: MonitoringTemplate) => {
    const name = window.prompt("Rename template:", t.name);
    if (!name || !name.trim() || name.trim() === t.name) return;
    setBusy(true);
    setError(null);
    try {
      await api.updateMonitoringTemplate(t.id, { name: name.trim(), description: t.description, checks: t.checks });
      reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (t: MonitoringTemplate) => {
    if (!window.confirm(`Delete template "${t.name}"? Health checks already applied to services are not removed.`)) return;
    setBusy(true);
    setError(null);
    try {
      await api.deleteMonitoringTemplate(t.id);
      reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const startEdit = (t: MonitoringTemplate) => {
    setEditingId(t.id);
    setDraft(t.checks.map((c) => ({ ...c })));
    setExpanded((prev) => new Set(prev).add(t.id));
  };

  const saveEdit = async (t: MonitoringTemplate) => {
    if (draft.length === 0 && !window.confirm("Save with no checks? The template will be empty.")) return;
    setBusy(true);
    setError(null);
    try {
      await api.updateMonitoringTemplate(t.id, { name: t.name, description: t.description, checks: draft });
      setEditingId(null);
      reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const patchDraft = (i: number, patch: Partial<MonitoringTemplateCheck>) =>
    setDraft((cur) => cur.map((c, idx) => (idx === i ? { ...c, ...patch } : c)));

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const n = new Set(prev);
      n.has(id) ? n.delete(id) : n.add(id);
      return n;
    });

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Monitoring templates</h1>
          <p className="page__subtitle">
            Reusable bundles of health checks. Create one from a service's checks (on the service page), fork a
            built-in below, then apply it to any service. Built-ins are read-only — fork to customise.
          </p>
        </div>
      </div>

      {error && <div className="alert alert--error">{error}</div>}

      <div className="card" style={{ marginBottom: 16 }}>
        <div className="card__header">Built-in templates · {forkable.length}</div>
        <div style={{ padding: "4px 16px 8px" }}>
          {forkable.map((b, i) => {
            const key = `builtin:${b.key}`;
            return (
              <div key={key} style={{ borderTop: i === 0 ? undefined : "1px solid var(--border)", padding: "10px 0" }}>
                <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                  <button
                    type="button"
                    onClick={() => toggle(key)}
                    style={{ border: 0, background: "transparent", cursor: "pointer", color: "var(--muted)", width: 12 }}
                    aria-label={expanded.has(key) ? "Collapse" : "Expand"}
                  >
                    {expanded.has(key) ? "▾" : "▸"}
                  </button>
                  <button
                    type="button"
                    onClick={() => toggle(key)}
                    style={{ border: 0, background: "transparent", cursor: "pointer", fontWeight: 600, padding: 0, font: "inherit" }}
                  >
                    {b.label}
                  </button>
                  <span className="badge-brand">{b.checks.length} check{b.checks.length === 1 ? "" : "s"}</span>
                  <span className="muted" style={{ fontSize: 12 }}>{b.built_in ? "built-in" : "custom type"}</span>
                  {canWrite && (
                    <button type="button" className="btn btn--sm" disabled={busy} style={{ marginLeft: "auto" }}
                      onClick={() => fork(b.key, b.label)}>
                      Fork
                    </button>
                  )}
                </div>
                {expanded.has(key) && (
                  <div style={{ marginLeft: 22, marginTop: 8 }}>
                    <CheckList checks={b.checks} />
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>

      <div className="card">
        <div className="card__header">Your templates · {templates.length}</div>
        {loading && templates.length === 0 ? (
          <div className="placeholder" style={{ margin: 12 }}>Loading…</div>
        ) : templates.length === 0 ? (
          <div className="placeholder" style={{ margin: 12 }}>
            No custom templates yet. Fork a built-in above, or open a service and use{" "}
            <strong>Save these checks as a template</strong>.
          </div>
        ) : (
          <div style={{ padding: "4px 16px 8px" }}>
            {sorted.map((t, i) => (
              <div key={t.id} style={{ borderTop: i === 0 ? undefined : "1px solid var(--border)", padding: "10px 0" }}>
                <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                  <button
                    type="button"
                    onClick={() => toggle(t.id)}
                    style={{ border: 0, background: "transparent", cursor: "pointer", color: "var(--muted)", width: 12 }}
                    aria-label={expanded.has(t.id) ? "Collapse" : "Expand"}
                  >
                    {expanded.has(t.id) ? "▾" : "▸"}
                  </button>
                  <button
                    type="button"
                    onClick={() => toggle(t.id)}
                    style={{ border: 0, background: "transparent", cursor: "pointer", fontWeight: 600, padding: 0, font: "inherit" }}
                  >
                    {t.name}
                  </button>
                  <span className="badge-brand">{t.checks.length} check{t.checks.length === 1 ? "" : "s"}</span>
                  <span className="muted" style={{ fontSize: 12 }}>{t.source}</span>
                  {canWrite && editingId !== t.id && (
                    <span style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
                      <button type="button" className="btn btn--sm" disabled={busy} onClick={() => startEdit(t)}>Edit checks</button>
                      <button type="button" className="btn btn--sm" disabled={busy} onClick={() => rename(t)}>Rename</button>
                      <button type="button" className="btn btn--sm btn--danger" disabled={busy} onClick={() => remove(t)}>Delete</button>
                    </span>
                  )}
                  {canWrite && editingId === t.id && (
                    <span style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
                      <button type="button" className="btn btn--sm btn--primary" disabled={busy} onClick={() => saveEdit(t)}>
                        {busy ? "Saving…" : "Save checks"}
                      </button>
                      <button type="button" className="btn btn--sm" disabled={busy} onClick={() => setEditingId(null)}>Cancel</button>
                    </span>
                  )}
                </div>
                {t.description && <div className="muted" style={{ fontSize: 13, marginLeft: 22 }}>{t.description}</div>}
                {expanded.has(t.id) && editingId !== t.id && (
                  <div style={{ marginLeft: 22, marginTop: 8 }}>
                    <CheckList checks={t.checks} />
                  </div>
                )}
                {editingId === t.id && (
                  <div style={{ marginLeft: 22, marginTop: 8, display: "flex", flexDirection: "column", gap: 8 }}>
                    {draft.length === 0 && <div className="muted" style={{ fontSize: 12.5 }}>All checks removed.</div>}
                    {draft.map((c, ci) => (
                      <div key={ci} style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                        <input className="search__input" value={c.name} aria-label="Check name"
                          onChange={(e) => patchDraft(ci, { name: e.target.value })}
                          style={{ width: 220, fontSize: 12.5 }} />
                        <select className="search__input" value={c.severity || "warning"} aria-label="Severity"
                          onChange={(e) => patchDraft(ci, { severity: e.target.value })}
                          style={{ width: 100, fontSize: 12.5 }}>
                          <option value="info">info</option>
                          <option value="warning">warning</option>
                          <option value="critical">critical</option>
                        </select>
                        {c.signal?.startsWith("trace") ? (
                          <span className="mono" style={{ fontSize: 11.5, color: "var(--muted)" }}>
                            {checkLine(c)}
                          </span>
                        ) : c.signal === "log" ? (
                          <label className="mono" style={{ fontSize: 11.5, color: "var(--muted)", display: "flex", alignItems: "center", gap: 4 }}>
                            ≥
                            <input type="number" className="search__input" value={c.log_threshold ?? 1} min={1} aria-label="Log threshold"
                              onChange={(e) => patchDraft(ci, { log_threshold: Number(e.target.value) })}
                              style={{ width: 72, fontSize: 12.5 }} />
                            matching logs
                          </label>
                        ) : (
                          <label className="mono" style={{ fontSize: 11.5, color: "var(--muted)", display: "flex", alignItems: "center", gap: 4 }}>
                            {c.agg} {c.metric}
                            <select className="search__input" value={c.op || ">"} aria-label="Operator"
                              onChange={(e) => patchDraft(ci, { op: e.target.value })}
                              style={{ width: 58, fontSize: 12.5 }}>
                              <option value=">">&gt;</option>
                              <option value=">=">&ge;</option>
                              <option value="<">&lt;</option>
                              <option value="<=">&le;</option>
                            </select>
                            <input type="number" className="search__input" value={c.threshold ?? 0} step="any" aria-label="Threshold"
                              onChange={(e) => patchDraft(ci, { threshold: Number(e.target.value) })}
                              style={{ width: 92, fontSize: 12.5 }} />
                            {c.unit || ""}
                          </label>
                        )}
                        <button type="button" className="btn btn--link" style={{ color: "var(--danger, #b91c1c)", fontSize: 12 }}
                          onClick={() => setDraft((cur) => cur.filter((_, idx) => idx !== ci))}>
                          Remove
                        </button>
                      </div>
                    ))}
                    <p className="muted" style={{ fontSize: 12, margin: "2px 0 0" }}>
                      To add new checks, build them on a service and save back as a template —
                      the metric pickers live there. Changes here affect future applies only.
                    </p>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      <p className="muted" style={{ fontSize: 12, marginTop: 12 }}>
        Edit a template's checks in place above, or apply it to a service, adjust there, and{" "}
        <Link to="/services">save the result back as a template</Link>.
      </p>
    </div>
  );
}
