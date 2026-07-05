// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Top-of-app banner shown to admins when the org has reached its licensed
// cap on monitored entities (integrations + systems). Advisory only —
// monitoring never stops over a count; this just makes the limit visible and
// nudges an upgrade. Reads the live usage from /api/v1/license (cached via
// useLicense). Hidden for non-admins and on the Usage page itself (the KPI is
// already there).

import { Link, useLocation } from "react-router-dom";
import { useLicense } from "../lib/useLicense";
import { useCurrentUser } from "../lib/useCurrentUser";

export function IntegrationLimitBanner() {
  const { status } = useLicense();
  const { can } = useCurrentUser();
  const loc = useLocation();

  const usage = status?.integration_usage;
  if (!can("org.manage")) return null;
  if (!usage || usage.unlimited || !usage.over_limit) return null;
  if (loc.pathname.startsWith("/usage")) return null;

  return (
    <div
      className="alert alert--error"
      style={{ margin: "0 0 16px", display: "flex", alignItems: "center", gap: 12 }}
    >
      <span style={{ fontSize: 13.5 }}>
        <strong>Plan limit reached</strong> — your org is using {usage.used} of {usage.limit}{" "}
        integrations &amp; systems on its plan. Monitoring keeps running; upgrade the plan to add more.
      </span>
      <Link to="/usage" className="btn btn--sm" style={{ marginLeft: "auto", whiteSpace: "nowrap" }}>
        View usage
      </Link>
    </div>
  );
}
