// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationMetrics — the Metrics tab inside an integration. Renders the
// same explorer the global Metrics page and the service Metrics tab do,
// scoped to one integration.
//
// Why it exists: an integration's health pill is driven by its metric
// health checks, but nothing on the integration could show the metric
// those checks judge. The Errors tab named the failing check and its
// current reading; "what did the number do all week" meant leaving for a
// member service's Metrics tab — which means already knowing which
// service belongs to the integration, the very thing the integration is
// there to encapsulate.
//
// The scope is locked: the catalogue query carries `integration=<name>`
// and cell-api resolves it to the member services and intersects with the
// caller's metrics-signal allowlist (G5), so a user only sees metrics
// from services they have access to within that integration.
//
// Path: /integrations/:id/metrics

import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { api } from "../api/client";
import IntegrationPageHeader from "../components/IntegrationPageHeader";
import IntegrationTabs from "../components/IntegrationTabs";
import { integrationProblemCount } from "../lib/integrationHealth";
import MetricsExplorer from "../components/metrics/MetricsExplorer";
import type { IntegrationDetail } from "../api/types";
import { usePageTitle } from "../lib/usePageTitle";

export default function IntegrationMetrics() {
  const { id = "" } = useParams();
  const [data, setData] = useState<IntegrationDetail | null>(null);
  const [error, setError] = useState<string | null>(null);

  usePageTitle(data ? `${data.integration.name} · Metrics` : "Integration metrics");

  useEffect(() => {
    if (!id) return;
    api
      .getIntegration(id)
      .then(setData)
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, [id]);

  if (error) {
    return (
      <div>
        <div className="page__header">
          <div>
            <h1 className="page__title">Integration metrics</h1>
          </div>
        </div>
        <div className="alert alert--error">Failed to load integration: {error}</div>
      </div>
    );
  }

  if (!data) {
    return (
      <div>
        <div className="page__header">
          <div>
            <h1 className="page__title">Integration metrics</h1>
          </div>
        </div>
        <div className="placeholder">Loading…</div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <IntegrationPageHeader detail={data} />
      <IntegrationTabs integrationId={id} errorsCount={integrationProblemCount(data)} />
      {/* The catalogue resolves an integration by display name, the same
          form the Logs tab passes. `embedded` drops the page chrome and
          hides group-by + Trim ingestion, both org-wide concerns. */}
      <MetricsExplorer integration={data.integration.name} embedded />
    </div>
  );
}
