// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationPageHeader — the shared header used across every
// integration page (Overview, Messages, Logs, Settings) so the
// identity of the integration is consistent regardless of which tab
// you're on.
//
// Layout (left-aligned column):
//   - breadcrumb     "integrations / orders-team"
//   - title row      "Orders Team [status-pip]"
//   - stats line     "3 services · description · last updated 5m ago"
//   - belowStats     optional slot (Overview puts tag editing here)
//
// Right side (header__actions):
//   - actions        optional slot (Overview puts Delete here)
//
// The header deliberately mirrors what the Overview page used to
// render inline — Overview just passes its tag picker as `belowStats`
// and its Delete button as `actions`. Logs / Settings render the
// same identity bar with no extras.

import { Link } from "react-router-dom";
import type { ReactNode } from "react";
import { StatusPip, pipForStatus } from "./primitives";
import type { IntegrationDetail, ServiceStatus } from "../api/types";
import { formatRelative, statusLabel } from "../lib/format";
import { useBreadcrumbLeaf } from "../lib/breadcrumb";

interface Props {
  // Source of the displayed identity. null while loading.
  detail: IntegrationDetail | null;
  // Optional right-side header slot — Overview uses it for Delete.
  actions?: ReactNode;
  // Optional slot rendered under the stats line — Overview uses it
  // for the tag editor. Logs/Settings leave it empty.
  belowStats?: ReactNode;
}

export default function IntegrationPageHeader({ detail, actions, belowStats }: Props) {
  // Feed the integration's name to the top-bar breadcrumb (its route
  // carries the id, not the name). Covers Overview/Services/Settings/Logs.
  useBreadcrumbLeaf(detail?.integration.name);
  return (
    <header className="flex items-start justify-between gap-4">
      <div className="min-w-0">
        <p className="text-xs uppercase tracking-wide text-muted">
          <Link to="/integrations" className="hover:underline">integrations</Link>
          {" / "}
          {detail?.integration.slug ?? "—"}
        </p>
        <h1 className="mt-1 flex items-center gap-3 text-2xl font-semibold">
          <span>{detail?.integration.name ?? "Loading…"}</span>
          {detail?.status && (
            <StatusPip
              kind={pipForStatus(detail.status)}
              label={statusLabel(detail.status as ServiceStatus)}
            />
          )}
        </h1>
        {detail && (
          <p className="mt-1 text-sm text-muted">
            {detail.services?.length ?? 0} service{(detail.services?.length ?? 0) === 1 ? "" : "s"}
            {detail.integration.description && <> · {detail.integration.description}</>}
            <> · last updated {formatRelative(detail.integration.updated_at)}</>
          </p>
        )}
        {belowStats}
      </div>
      {actions && <div className="flex items-center gap-2 flex-shrink-0">{actions}</div>}
    </header>
  );
}
