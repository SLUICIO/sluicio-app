// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationLogs — the Logs tab inside an integration. Renders the
// same filter bar / volume histogram / virtualized table the global
// Logs page does, but scoped to one integration. The scope is locked:
// every query carries `integration=<name>` and the cell-api intersects
// it with the caller's policy allowlist (G5), so a user only sees logs
// from services they have access to within that integration.
//
// Path: /integrations/:id/logs

import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { api } from "../api/client";
import IntegrationPageHeader from "../components/IntegrationPageHeader";
import IntegrationTabs from "../components/IntegrationTabs";
import { integrationProblemCount } from "../lib/integrationHealth";
import LogsView from "../components/logs/LogsView";
import type { IntegrationDetail } from "../api/types";
import { usePageTitle } from "../lib/usePageTitle";

export default function IntegrationLogs() {
  const { id = "" } = useParams();
  const [data, setData] = useState<IntegrationDetail | null>(null);
  const [error, setError] = useState<string | null>(null);

  usePageTitle(data ? `${data.integration.name} · Logs` : "Integration logs");

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
            <h1 className="page__title">Integration logs</h1>
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
            <h1 className="page__title">Integration logs</h1>
          </div>
        </div>
        <div className="placeholder">Loading…</div>
      </div>
    );
  }

  const integrationName = data.integration.name;

  return (
    <div className="flex flex-col gap-6">
      <IntegrationPageHeader detail={data} />
      <IntegrationTabs integrationId={id} errorsCount={integrationProblemCount(data)} />
      <LogsView forcedIntegration={integrationName} forcedIntegrationId={id} />
    </div>
  );
}
