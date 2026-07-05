// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Monitoring templates manager. Lists user-defined (custom) templates and lets
// you fork a built-in into an editable copy, rename, view its checks, or delete
// it. Built-ins themselves stay read-only (shipped in code) — forking is how
// you customise them. Creating a template from a service's current checks lives
// on the service page ("Save these checks as a template"). Applying a template
// happens per service, from the service editor.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { MonitoringTemplate, MonitoringTemplateCheck } from "../api/types";
import { usePageTitle } from "../lib/usePageTitle";
import { useCurrentUser } from "../lib/useCurrentUser";

const FORKABLE: { kind: string; label: string }[] = [
  { kind: "rabbitmq", label: "RabbitMQ" },
  { kind: "artemis", label: "ActiveMQ Artemis" },
  { kind: "otel-collector", label: "OpenTelemetry Collector" },
  { kind: "dotnet-service", label: ".NET service" },
];

function checkLine(c: MonitoringTemplateCheck): string {
  if (c.signal === "log") {
    const sev = c.min_severity ? `severity≥${c.min_severity}` : "any severity";
    const body = c.body_contains ? ` body~"${c.body_contains}"` : "";
    return `log · ${sev}${body} · ≥${c.log_threshold ?? 1} in window`;
  }
  return `metric · ${c.agg} ${c.metric} ${c.op} ${c.threshold}`;
}

export default function MonitoringTemplates() {
  usePageTitle("Monitoring templates");
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  const [templates, setTemplates] = useState<MonitoringTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const reload = useCallback(() => {
    setLoading(true);
    api
      .listMonitoringTemplates()
      .then((r) => setTemplates(r.templates ?? []))
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  }, []);
  useEffect(() => reload(), [reload]);

  const sorted = useMemo(
    () => [...templates].sort((a, b) => a.name.localeCompare(b.name)),
    [templates],
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

      {canWrite && (
        <div className="card" style={{ marginBottom: 16 }}>
          <div className="card__header">Fork a built-in</div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, padding: "12px 16px" }}>
            {FORKABLE.map((b) => (
              <button key={b.kind} type="button" className="btn" disabled={busy} onClick={() => fork(b.kind, b.label)}>
                Fork {b.label}
              </button>
            ))}
          </div>
        </div>
      )}

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
                  <span style={{ fontWeight: 600 }}>{t.name}</span>
                  <span className="badge-brand">{t.checks.length} check{t.checks.length === 1 ? "" : "s"}</span>
                  <span className="muted" style={{ fontSize: 12 }}>{t.source}</span>
                  {canWrite && (
                    <span style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
                      <button type="button" className="btn btn--sm" disabled={busy} onClick={() => rename(t)}>Rename</button>
                      <button type="button" className="btn btn--sm btn--danger" disabled={busy} onClick={() => remove(t)}>Delete</button>
                    </span>
                  )}
                </div>
                {t.description && <div className="muted" style={{ fontSize: 13, marginLeft: 22 }}>{t.description}</div>}
                {expanded.has(t.id) && (
                  <div style={{ marginLeft: 22, marginTop: 6, display: "flex", flexDirection: "column", gap: 3 }}>
                    {t.checks.map((c, i) => (
                      <div key={i} style={{ fontSize: 12.5 }}>
                        <span style={{ fontWeight: 600 }}>{c.name}</span>
                        <span className="mono" style={{ marginLeft: 8, color: "var(--muted)", fontSize: 11.5 }}>{checkLine(c)}</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      <p className="muted" style={{ fontSize: 12, marginTop: 12 }}>
        To change a template's checks: apply it to a service, adjust the checks there, then{" "}
        <Link to="/services">save them back as a template</Link>. (An in-place check editor is a later add.)
      </p>
    </div>
  );
}
