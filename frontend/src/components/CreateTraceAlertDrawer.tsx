// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// CreateTraceAlertDrawer — a compact builder for a failed-trace alert
// rule ("alert when ≥ N failed traces in the last W"). It can be scoped
// to EITHER an integration (all its services) or a single service; the
// caller passes exactly one. Used from the integration Errors breakdown
// and from a service's Traces tab. Posts the standard alert-rule create
// endpoint (signal=trace) and reuses the shared notification channels.

import { FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { AlertSeverity, NotificationChannel } from "../api/types";
import { EditDrawer } from "./primitives";

const WINDOW_OPTS: { label: string; seconds: number }[] = [
  { label: "5 minutes", seconds: 300 },
  { label: "15 minutes", seconds: 900 },
  { label: "1 hour", seconds: 3600 },
];

export default function CreateTraceAlertDrawer({
  integrationId,
  serviceName,
  onClose,
}: {
  integrationId?: string;
  serviceName?: string;
  onClose: () => void;
}) {
  const scopeNoun = serviceName ? "this service" : "this integration's services";
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [name, setName] = useState("Failed traces");
  const [threshold, setThreshold] = useState(1);
  const [windowSeconds, setWindowSeconds] = useState(300);
  const [severity, setSeverity] = useState<AlertSeverity>("warning");
  const [selectedChannels, setSelectedChannels] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  useEffect(() => {
    api
      .listChannels()
      .then((r) => setChannels(r.channels ?? []))
      .catch(() => setChannels([]));
  }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (threshold < 1) {
      setError("Threshold must be at least 1.");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await api.createAlertRule({
        name: name.trim() || "Failed traces",
        severity,
        enabled: true,
        signal: "trace",
        // Exactly one scope is set; the backend requires an integration
        // or a service for a trace rule.
        integration_id: integrationId || undefined,
        service_name: serviceName || undefined,
        trace_error_spec: { threshold, window_seconds: windowSeconds },
        channel_ids: selectedChannels,
      });
      setDone(true);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <EditDrawer title="Alert on failed traces" width="narrow" onClose={onClose}>
      {done ? (
        <div className="p-1">
          <div className="text-sm" style={{ color: "var(--ok)" }}>
            Alert rule created.
          </div>
          <p className="muted mt-1" style={{ fontSize: 13 }}>
            It fires when {scopeNoun} {serviceName ? "has" : "have"} ≥ {threshold} failed trace
            {threshold === 1 ? "" : "s"} in the selected window. Manage it on the{" "}
            <Link to="/alerts" style={{ color: "var(--primary)" }} className="hover:underline">
              Alerts page
            </Link>
            .
          </p>
          <div className="form__actions">
            <button type="button" className="btn btn--primary" onClick={onClose}>
              Done
            </button>
          </div>
        </div>
      ) : (
        <form onSubmit={submit} className="form" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          {error && <div className="alert alert--error">{error}</div>}
          <p className="muted" style={{ fontSize: 13, lineHeight: 1.5 }}>
            Notifies you when {scopeNoun} accumulate failed traces (a trace with
            an error span) above a threshold.
          </p>
          <label className="form__label">
            Rule name
            <input className="search__input" value={name} maxLength={120} onChange={(e) => setName(e.target.value)} />
          </label>
          <div className="form__row" style={{ display: "flex", gap: 10 }}>
            <label className="form__label" style={{ flex: 1 }}>
              Fire when ≥
              <input
                className="search__input"
                type="number"
                min={1}
                value={threshold}
                onChange={(e) => setThreshold(Math.max(1, parseInt(e.target.value || "1", 10)))}
              />
              <span className="form__hint">failed traces</span>
            </label>
            <label className="form__label" style={{ flex: 1 }}>
              Within
              <select
                className="toolbar__select"
                value={windowSeconds}
                onChange={(e) => setWindowSeconds(parseInt(e.target.value, 10))}
              >
                {WINDOW_OPTS.map((o) => (
                  <option key={o.seconds} value={o.seconds}>
                    {o.label}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <label className="form__label">
            Severity
            <select
              className="toolbar__select"
              value={severity}
              onChange={(e) => setSeverity(e.target.value as AlertSeverity)}
            >
              <option value="info">Info</option>
              <option value="warning">Warning</option>
              <option value="critical">Critical</option>
            </select>
          </label>
          <div className="form__label">
            Notify
            {channels.length === 0 ? (
              <span className="form__hint">
                No notification channels yet — the alert will still fire and show on the
                Alerts page. Add channels on the Alerts page to get notified.
              </span>
            ) : (
              <div style={{ display: "flex", flexDirection: "column", gap: 4, paddingTop: 4 }}>
                {channels.map((c) => (
                  <label key={c.id} className="inline-flex items-center gap-2" style={{ fontSize: 13 }}>
                    <input
                      type="checkbox"
                      checked={selectedChannels.includes(c.id)}
                      onChange={(e) =>
                        setSelectedChannels((cur) =>
                          e.target.checked ? [...cur, c.id] : cur.filter((x) => x !== c.id),
                        )
                      }
                    />
                    {c.name} <span className="muted">· {c.kind}</span>
                  </label>
                ))}
              </div>
            )}
          </div>
          <div className="form__actions">
            <button type="button" className="btn" onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className="btn btn--primary" disabled={saving}>
              {saving ? "Creating…" : "Create alert rule"}
            </button>
          </div>
        </form>
      )}
    </EditDrawer>
  );
}
