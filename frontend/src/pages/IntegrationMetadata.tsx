// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationMetadata — the Metadata tab on an integration detail page,
// mounted at /integrations/:id/metadata. Shows the integration's
// user-defined metadata (Contact Person, Handles GDPR data?, …) and lets a
// contributor edit it. The schema (which fields apply) lives at
// /metadata-fields. Moved off the Overview tab, which stays operational.

import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { api } from "../api/client";
import IntegrationPageHeader from "../components/IntegrationPageHeader";
import IntegrationTabs from "../components/IntegrationTabs";
import MetadataPanel from "../components/MetadataPanel";
import { integrationProblemCount } from "../lib/integrationHealth";
import type { IntegrationDetail } from "../api/types";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";

export default function IntegrationMetadata() {
  const { id = "" } = useParams();
  const [windowVal] = useTimeWindow();

  const [data, setData] = useState<IntegrationDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  usePageTitle(
    data ? `${data.integration.name} · Metadata` : "Integration metadata",
  );

  const refresh = () => {
    if (!id) return;
    api
      .getIntegration(id, windowVal)
      .then(setData)
      .catch((e) => setError(String((e as Error).message ?? e)));
  };

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
        <MetadataPanel
          fields={data.metadata_fields ?? []}
          values={data.metadata_values ?? {}}
          onSave={async (next) => {
            await api.setIntegrationMetadata(id, next);
            refresh();
          }}
        />
      )}
    </div>
  );
}
