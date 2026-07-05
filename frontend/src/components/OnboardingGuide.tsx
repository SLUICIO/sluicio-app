// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// OnboardingGuide — adaptive empty-state shown on the dashboard so a new
// org isn't staring at zeros. It self-detects where the org is and shows
// the next step:
//
//   • No ingest key yet            → guide to Settings → Ingestion to
//                                      create the org's first key. The
//                                      actual SDK/Collector config lives
//                                      on that page (shown at key creation),
//                                      NOT on the dashboard.
//   • Telemetry but no integration → group services into an integration.
//   • Configured / waiting on data → render nothing (normal dashboard).
//
// The "get your first data" nag only fires when the org has no ingest key
// at all — once a key exists the user has already been pointed at the
// ingestion page, so the dashboard stays quiet while we wait for data.
//
// Self-contained: it fetches its own signals (services, integrations,
// ingest keys) and renders null on error/loading so it can never break
// the dashboard.

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { IngestKey } from "../api/types";
import { useTimeWindow } from "../lib/useTimeWindow";

type Stage = "loading" | "hidden" | "no-key" | "no-integration";

export default function OnboardingGuide() {
  const [windowVal] = useTimeWindow();
  const [stage, setStage] = useState<Stage>("loading");
  const [serviceCount, setServiceCount] = useState(0);

  useEffect(() => {
    let cancelled = false;
    Promise.all([
      api.listServices(windowVal).then((r) => r.services ?? []).catch(() => []),
      api.listIntegrations(windowVal).then((r) => r.integrations ?? []).catch(() => []),
      api.listIngestKeys().then((r) => r.keys ?? []).catch(() => [] as IngestKey[]),
    ]).then(([services, integrations, ingestKeys]) => {
      if (cancelled) return;
      setServiceCount(services.length);
      // Having an integration is the "configured" signal — established
      // orgs always have one, so a quiet window never falsely triggers.
      if (integrations.length > 0) setStage("hidden");
      else if (services.length > 0) setStage("no-integration");
      // No services AND no key → the org hasn't started. Guide to ingestion.
      else if (ingestKeys.length === 0) setStage("no-key");
      // Key exists but no data yet: already guided — don't nag on the
      // dashboard while the first batch is in flight.
      else setStage("hidden");
    });
    return () => {
      cancelled = true;
    };
  }, [windowVal]);

  if (stage === "loading" || stage === "hidden") return null;

  return (
    <section
      className="mb-6 rounded-xl border p-5"
      style={{
        borderColor: "color-mix(in oklab, var(--primary) 30%, transparent)",
        background: "var(--primary-soft)",
      }}
    >
      {stage === "no-key" ? (
        <>
          <h2 className="text-lg font-semibold" style={{ color: "var(--primary-ink)" }}>
            👋 Let's get your first data into Sluicio
          </h2>
          <p className="mt-1 text-sm" style={{ color: "var(--ink-2)" }}>
            No telemetry has arrived yet, and your organization doesn't have an ingest key.
            Telemetry is authenticated per-org with a key — create one to get started.
          </p>
          <div className="mt-4">
            <Link to="/settings?tab=ingestion" className="btn btn--primary">
              Set up ingestion →
            </Link>
          </div>
          <p className="mt-3 text-xs text-muted">
            On that page you'll create a key and get a ready-to-paste OpenTelemetry SDK and
            Collector config. Once data starts flowing, your services appear here automatically.
          </p>
        </>
      ) : (
        <>
          <h2 className="text-lg font-semibold" style={{ color: "var(--primary-ink)" }}>
            🎉 You're receiving telemetry
          </h2>
          <p className="mt-1 text-sm" style={{ color: "var(--ink-2)" }}>
            {serviceCount} service{serviceCount === 1 ? "" : "s"} seen, but no integrations yet. Group
            related services into an <b>integration</b> to monitor them as one pipeline — health,
            completion SLAs, and scoped message search.
          </p>
          <div className="mt-4">
            <Link to="/integrations/new" className="btn btn--primary">
              Create your first integration →
            </Link>
          </div>
        </>
      )}
    </section>
  );
}
