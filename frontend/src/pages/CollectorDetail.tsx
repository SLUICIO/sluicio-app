// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// One collector (Beta): what it reports about itself, its health, and the
// configuration it runs with its history. Configurations are always
// masked by the server; this page never sees a secret.

import { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type {
  CollectorConfigResponse,
  CollectorDetailResponse,
  CollectorHealth,
} from "../api/types";
import { formatDateTime, formatRelative } from "../lib/format";
import { usePageTitle } from "../lib/usePageTitle";
import { useBreadcrumbLeaf } from "../lib/breadcrumb";
import KVTable, { attributeRows } from "../components/primitives/KVTable";
import StatusPip from "../components/primitives/StatusPip";
import SyntaxView from "../components/SyntaxView";
import BetaBadge from "../components/BetaBadge";
import TabStrip from "../components/primitives/TabStrip";
import { useCurrentUser } from "../lib/useCurrentUser";
import { otelflowEmbedUrl, otelflowPayload } from "../lib/otelflowEmbed";
import { KindBadge, collectorName, collectorStatus } from "./Collectors";

function Section({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border bg-surface-2">
      <div className="border-b border-border px-4 py-3">
        <h2 style={{ margin: 0, fontSize: 15, fontWeight: 600 }}>{title}</h2>
        {subtitle && (
          <div className="muted" style={{ fontSize: 12.5 }}>
            {subtitle}
          </div>
        )}
      </div>
      <div style={{ padding: "12px 16px" }}>{children}</div>
    </section>
  );
}

function healthRows(
  h: CollectorHealth,
  prefix = "",
): { k: string; v: React.ReactNode }[] {
  const rows: { k: string; v: React.ReactNode }[] = [
    {
      k: prefix || "collector",
      v: (
        <span>
          <StatusPip
            kind={h.healthy ? "ok" : "err"}
            label={h.status || (h.healthy ? "healthy" : "unhealthy")}
          />
          {h.last_error && (
            <span
              className="mono"
              style={{ marginLeft: 8, color: "var(--err-ink)" }}
            >
              {h.last_error}
            </span>
          )}
        </span>
      ),
    },
  ];
  for (const [name, c] of Object.entries(h.components ?? {}).sort(([a], [b]) =>
    a.localeCompare(b),
  )) {
    rows.push(...healthRows(c, prefix ? `${prefix} / ${name}` : name));
  }
  return rows;
}

// PipelineEmbed draws the configuration with the cell's own OTelFlow, read
// only. The configuration travels in the URL fragment of the iframe, so it
// reaches neither OTelFlow's server nor anything outside the cell.
function PipelineEmbed({
  base,
  yaml,
  version,
  supervisor,
}: {
  base: string;
  yaml: string;
  version: string;
  supervisor: boolean;
}) {
  const [payload, setPayload] = useState<string | null>(null);
  useEffect(() => {
    let cancelled = false;
    otelflowPayload(yaml, version).then((p) => !cancelled && setPayload(p));
    return () => {
      cancelled = true;
    };
  }, [yaml, version]);
  if (!payload) return null;
  const root = base.endsWith("/") ? base : base + "/";
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <div
        className="muted"
        style={{
          fontSize: 12.5,
          display: "flex",
          gap: 12,
          alignItems: "baseline",
        }}
      >
        <span>
          Pipeline drawn by OTelFlow from the configuration the collector
          reports. The collector leaves some settings out of that report (a
          receiver's scrapers, for example), so OTelFlow can flag them as
          missing.
          {supervisor && " Editing and sending a configuration comes later."}
        </span>
        <a
          href={`${root}#share=${payload}`}
          target="_blank"
          rel="noopener"
          style={{ marginLeft: "auto", whiteSpace: "nowrap" }}
        >
          Open in OTelFlow
        </a>
      </div>
      <iframe
        title="Collector pipeline (OTelFlow)"
        src={otelflowEmbedUrl(base, payload, "canvas")}
        style={{
          width: "100%",
          height: 480,
          border: "1px solid var(--border)",
          borderRadius: 8,
          background: "var(--surface)",
        }}
        loading="lazy"
      />
    </div>
  );
}

// The tabs, in order. ?tab= keeps the choice in the URL, so a link or a
// reload lands on the same tab.
const TABS = [
  { key: "agent", label: "Agent" },
  { key: "health", label: "Health" },
  { key: "configuration", label: "Configuration" },
] as const;
type TabKey = (typeof TABS)[number]["key"];

export default function CollectorDetail() {
  const { id = "" } = useParams();
  const { otelflowUrl } = useCurrentUser();
  const [searchParams, setSearchParams] = useSearchParams();
  const tab: TabKey =
    TABS.find((t) => t.key === searchParams.get("tab"))?.key ?? "agent";
  const selectTab = (key: TabKey) =>
    setSearchParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        if (key === "agent") p.delete("tab");
        else p.set("tab", key);
        return p;
      },
      { replace: true },
    );
  const [detail, setDetail] = useState<CollectorDetailResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [version, setVersion] = useState<string>("latest");
  const [config, setConfig] = useState<CollectorConfigResponse | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);

  const name = detail ? collectorName(detail.collector) : null;
  usePageTitle(name ?? "Collector");
  useBreadcrumbLeaf(name);

  useEffect(() => {
    api
      .getCollector(id)
      .then(setDetail)
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, [id]);

  const hasConfig =
    !!detail && detail.config_storage && detail.history.length > 0;
  useEffect(() => {
    if (!hasConfig) return;
    setConfigError(null);
    api
      .getCollectorConfig(id, version)
      .then(setConfig)
      .catch((e) => setConfigError(String((e as Error).message ?? e)));
  }, [id, version, hasConfig]);

  if (error)
    return (
      <div className="alert alert--error">
        Failed to load collector: {error}
      </div>
    );
  if (!detail) return <div className="placeholder">Loading…</div>;
  const c = detail.collector;
  const st = collectorStatus(c);
  // The collector reports one unnamed file; the flow view draws that one.
  const mainFile = config?.files.find((f) => f.name === "") ?? config?.files[0];
  const canFlow = !!otelflowUrl && !!mainFile && !mainFile.hidden;
  // Flow by default where it can be drawn; ?view=yaml remembers the code
  // view in the URL like the tab.
  const configView: "flow" | "yaml" =
    canFlow && searchParams.get("view") !== "yaml" ? "flow" : "yaml";
  const selectConfigView = (v: "flow" | "yaml") =>
    setSearchParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        if (v === "flow") p.delete("view");
        else p.set("view", v);
        return p;
      },
      { replace: true },
    );

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">
            {collectorName(c)}
            <BetaBadge />
          </h1>
          <p className="page__subtitle">
            <KindBadge kind={c.kind} />{" "}
            <StatusPip kind={st.kind} label={st.label} /> in team{" "}
            <Link to="/collectors">{c.group_name}</Link>, last seen{" "}
            {formatRelative(c.last_seen_at)}.
          </p>
        </div>
      </div>

      <TabStrip
        ariaLabel="Collector sections"
        items={TABS.map((t) => ({
          key: t.key,
          label: t.label,
          active: tab === t.key,
          onClick: () => selectTab(t.key),
          count: t.key === "configuration" ? detail.history.length : undefined,
          countTitle: t.key === "configuration" ? "Stored versions" : undefined,
        }))}
      />

      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 16,
          marginTop: 16,
        }}
      >
        {tab === "agent" && (
          <Section
            title="Agent"
            subtitle="What the collector reported when it connected"
          >
            <KVTable
              variant="mono"
              rows={[
                {
                  k: "instance_uid",
                  v: c.instance_uid,
                  copyValue: c.instance_uid,
                },
                { k: "team", v: c.group_name, copyValue: null },
                {
                  k: "type",
                  v:
                    c.kind === "supervisor"
                      ? "supervisor (accepts remote configuration)"
                      : "extension (read only)",
                  copyValue: null,
                },
                { k: "transport", v: c.transport || "-", copyValue: null },
                {
                  k: "remote address",
                  v: c.remote_addr || "-",
                  copyValue: c.remote_addr || null,
                },
                {
                  k: "first seen",
                  v: formatDateTime(c.first_seen_at),
                  copyValue: null,
                },
                {
                  k: "last seen",
                  v: formatDateTime(c.last_seen_at),
                  copyValue: null,
                },
                {
                  k: "capabilities",
                  v: c.capabilities.join(", ") || "-",
                  copyValue: null,
                },
                ...attributeRows(c.description),
              ]}
            />
          </Section>
        )}

        {tab === "health" && (
          <Section
            title="Health"
            subtitle={
              c.online
                ? "As the collector reports it"
                : "Last report before the collector went offline"
            }
          >
            {c.health ? (
              <KVTable
                variant="mono"
                rows={healthRows(c.health).map((r) => ({
                  ...r,
                  copyValue: null,
                }))}
              />
            ) : (
              <div className="muted" style={{ fontSize: 13 }}>
                The collector has not reported its health.
              </div>
            )}
          </Section>
        )}

        {tab === "configuration" && (
          <Section
            title="Configuration"
            subtitle="The effective configuration the collector reports, secrets masked. A new version is kept whenever it changes."
          >
            {!detail.config_storage ? (
              <div className="alert alert--warn" style={{ margin: 0 }}>
                Configurations are not stored on this cell:{" "}
                <code>SLUICIO_COLLECTOR_CONFIG_KEY</code> is not set.
              </div>
            ) : detail.history.length === 0 ? (
              <div className="muted" style={{ fontSize: 13 }}>
                The collector has not reported a configuration. The OpAMP
                extension needs <code>reports_effective_config: true</code>.
              </div>
            ) : (
              <div
                style={{ display: "flex", flexDirection: "column", gap: 10 }}
              >
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 12,
                    flexWrap: "wrap",
                  }}
                >
                  <label
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 8,
                      fontSize: 13,
                    }}
                  >
                    Version
                    <select
                      className="search__input"
                      value={version}
                      onChange={(e) => setVersion(e.target.value)}
                      style={{ maxWidth: 360 }}
                    >
                      {detail.history.map((v, i) => (
                        <option key={v.id} value={i === 0 ? "latest" : v.id}>
                          {formatDateTime(v.received_at)}
                          {i === 0 ? " (current)" : ""} · {v.hash.slice(0, 8)}
                        </option>
                      ))}
                    </select>
                    <span className="muted">
                      {detail.history.length} version
                      {detail.history.length === 1 ? "" : "s"}
                    </span>
                  </label>
                  {canFlow && (
                    <div
                      className="m-seg"
                      role="radiogroup"
                      aria-label="Configuration view"
                      style={{ marginLeft: "auto" }}
                    >
                      {(
                        [
                          ["flow", "Flow"],
                          ["yaml", "YAML"],
                        ] as const
                      ).map(([v, label]) => (
                        <button
                          key={v}
                          type="button"
                          role="radio"
                          aria-checked={configView === v}
                          className={`m-seg-b ${configView === v ? "on" : ""}`}
                          onClick={() => selectConfigView(v)}
                        >
                          {label}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
                {configError && (
                  <div className="alert alert--error">{configError}</div>
                )}
                {canFlow && configView === "flow" && mainFile && (
                  <PipelineEmbed
                    base={otelflowUrl}
                    yaml={mainFile.body}
                    version={c.description["service.version"] ?? ""}
                    supervisor={c.kind === "supervisor"}
                  />
                )}
                {!(canFlow && configView === "flow") &&
                  config?.files.map((f) => (
                    <div key={f.name}>
                      {config.files.length > 1 && (
                        <div
                          className="mono muted"
                          style={{ fontSize: 12, marginBottom: 4 }}
                        >
                          {f.name || "(main)"}
                        </div>
                      )}
                      {f.hidden ? (
                        <div
                          className="alert alert--warn"
                          style={{ margin: 0 }}
                        >
                          This file could not be read to mask its secrets, so it
                          is not shown.
                        </div>
                      ) : (
                        <SyntaxView
                          content={f.body}
                          format="yaml"
                          maxHeight={600}
                        />
                      )}
                    </div>
                  ))}
              </div>
            )}
          </Section>
        )}
      </div>
    </div>
  );
}
