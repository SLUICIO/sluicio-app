// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationServices — the Services tab on an integration detail page,
// mounted at /integrations/:id/services. Lists every service that
// currently matches this integration's matchers (the "Services in this
// integration" table that used to live on the Overview tab). Click a row
// to drill into the service. Matcher configuration lives on the Settings
// tab.

import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { api } from "../api/client";
import IntegrationPageHeader from "../components/IntegrationPageHeader";
import IntegrationTabs from "../components/IntegrationTabs";
import { integrationProblemCount } from "../lib/integrationHealth";
import IntegrationServicesGuide from "../components/IntegrationServicesGuide";
import ServicesTable from "../components/ServicesTable";
import type { IntegrationDetail } from "../api/types";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";

export default function IntegrationServices() {
  const { id = "" } = useParams();
  const [windowVal] = useTimeWindow();

  const [data, setData] = useState<IntegrationDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  usePageTitle(
    data ? `${data.integration.name} · Services` : "Integration services",
  );

  useEffect(() => {
    if (!id) return;
    setLoading(true);
    setError(null);
    api
      .getIntegration(id, windowVal)
      .then(setData)
      .catch((e) => setError(String((e as Error).message ?? e)))
      .finally(() => setLoading(false));
  }, [id, windowVal]);

  return (
    <div className="flex flex-col gap-6">
      <IntegrationPageHeader detail={data} />
      <IntegrationTabs integrationId={id} errorsCount={integrationProblemCount(data)} />

      {error && <div className="alert alert--error">{error}</div>}
      {loading && !data && <div className="placeholder">Loading…</div>}

      {data && (
        <section
          className="overflow-hidden rounded-lg border bg-surface-2"
          style={{ borderColor: "var(--border)" }}
        >
          <div className="flex items-baseline justify-between border-b border-border px-4 py-3">
            <h2 className="text-base font-semibold">
              Services in this integration ({data.services?.length ?? 0})
            </h2>
            <span className="text-xs text-muted">Click a row to drill into a service</span>
          </div>
          <ServicesTable
            services={data.services ?? []}
            showIntegrations={false}
            showFirstSeen={false}
            showTags={false}
            emptyState={<IntegrationServicesGuide integrationId={id} />}
          />
        </section>
      )}
    </div>
  );
}
