// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationServicesGuide — onboarding guidance shown when an
// integration has no matching services yet. Services in Sluicio aren't
// added by hand; one appears automatically the moment telemetry arrives
// whose service.name matches one of the integration's matchers. So the
// two things a user needs are (1) telemetry flowing into the cell and
// (2) matchers that select it. This guide detects which step is
// outstanding — by checking whether the cell has any services at all —
// and points the user straight at it.

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";

interface Props {
  integrationId: string;
  // compact trims the intro for inline use (e.g. an Overview banner)
  // where the full two-step card would be too heavy.
  compact?: boolean;
}

export default function IntegrationServicesGuide({ integrationId, compact = false }: Props) {
  // null = still loading / unknown. Distinguishes "no telemetry yet"
  // (false → set up ingestion) from "telemetry exists but no matcher
  // selects it" (true → fix the matchers).
  const [hasTelemetry, setHasTelemetry] = useState<boolean | null>(null);
  const [ingestBase, setIngestBase] = useState(window.location.origin);

  useEffect(() => {
    let cancelled = false;
    api
      .listServices("24h")
      .then((r) => {
        if (!cancelled) setHasTelemetry((r.services ?? []).length > 0);
      })
      .catch(() => {
        if (!cancelled) setHasTelemetry(null);
      });
    api
      .getSystemSettings()
      .then((s) => {
        if (!cancelled && s.ingest_base_url) setIngestBase(s.ingest_base_url);
      })
      .catch(() => {
        /* keep the browser-origin default */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const settingsTab = `/integrations/${encodeURIComponent(integrationId)}/settings`;

  return (
    <div
      style={{
        padding: compact ? "14px 16px" : "20px",
        textAlign: "left",
        display: "flex",
        flexDirection: "column",
        gap: 14,
      }}
    >
      <div>
        <div style={{ fontWeight: 600, fontSize: compact ? 14 : 15 }}>
          No services in this integration yet
        </div>
        <p className="muted" style={{ fontSize: 13, marginTop: 4, lineHeight: 1.55, maxWidth: 640 }}>
          Services aren't added by hand — Sluicio discovers them automatically. One
          appears the moment telemetry arrives whose <code>service.name</code> matches a{" "}
          <strong>matcher</strong> on this integration. Two steps:
        </p>
      </div>

      <Step
        n={1}
        title="Send telemetry to Sluicio"
        // Only mark "done" when we positively know the cell has data.
        status={hasTelemetry === true ? "done" : hasTelemetry === false ? "todo" : "neutral"}
      >
        {hasTelemetry === true ? (
          <>Telemetry is already arriving at this cell — on to step 2.</>
        ) : (
          <>
            Create an ingest key, then point your OpenTelemetry collector or SDK at{" "}
            <code>{ingestBase}</code> (OTLP/HTTP) with the key as a{" "}
            <code>Authorization: Bearer</code> header.{" "}
            <Link to="/settings?tab=ingestion" className="btn btn--link" style={{ padding: 0 }}>
              Set up ingestion →
            </Link>
          </>
        )}
      </Step>

      <Step
        n={2}
        title="Match it to this integration"
        status={hasTelemetry === true ? "todo" : "neutral"}
      >
        Add a matcher (for example <code>service.name = orders-api</code>) on the Settings
        tab. Any service whose telemetry matches will join this integration automatically.{" "}
        <Link to={settingsTab} className="btn btn--link" style={{ padding: 0 }}>
          Configure matchers →
        </Link>
      </Step>
    </div>
  );
}

function Step({
  n,
  title,
  status,
  children,
}: {
  n: number;
  title: string;
  status: "done" | "todo" | "neutral";
  children: React.ReactNode;
}) {
  const ring =
    status === "done" ? "var(--ok)" : status === "todo" ? "var(--primary)" : "var(--border)";
  const badge = status === "done" ? "var(--ok)" : "var(--surface-3)";
  const badgeInk = status === "done" ? "#fff" : "var(--ink-2)";
  return (
    <div style={{ display: "flex", gap: 12, alignItems: "flex-start" }}>
      <div
        style={{
          flex: "0 0 auto",
          width: 24,
          height: 24,
          borderRadius: "50%",
          border: `2px solid ${ring}`,
          background: badge,
          color: badgeInk,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontSize: 12,
          fontWeight: 700,
        }}
      >
        {status === "done" ? "✓" : n}
      </div>
      <div>
        <div style={{ fontWeight: 600, fontSize: 13.5 }}>{title}</div>
        <div className="muted" style={{ fontSize: 13, marginTop: 2, lineHeight: 1.55, maxWidth: 620 }}>
          {children}
        </div>
      </div>
    </div>
  );
}
