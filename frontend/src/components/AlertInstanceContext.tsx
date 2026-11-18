// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which environment a firing alert is about, and the trace a
// trace-completion alert waits on. One rule can fire for several
// environments and many traces at once, and its name is the same on
// every one of those rows, so without these the rows on the Alerts and
// Errors pages could not be told apart or followed. The trace opens in
// the alert's integration; the trace page applies the reader's access.

import { Link } from "react-router-dom";
import { environmentLabel } from "../lib/activeEnvironment";
import { useTraceHref } from "../lib/traceHref";

export default function AlertInstanceContext({
  environment,
  traceId,
  integrationId,
}: {
  environment?: string | null;
  traceId?: string;
  integrationId?: string | null;
}) {
  const traceHref = useTraceHref();
  if (environment == null && !traceId) return null;
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 6, flexShrink: 0 }}>
      {environment != null && (
        <span className="badge" style={{ fontSize: 10.5 }} title="The environment this alert is about" data-testid="alert-environment">
          {environmentLabel(environment)}
        </span>
      )}
      {traceId && (
        <Link
          to={traceHref(traceId, integrationId ?? undefined)}
          className="mono"
          style={{ fontSize: 11.5, color: "var(--primary)", whiteSpace: "nowrap" }}
          title="Open the trace this alert is about"
          data-testid="alert-trace-link"
          onClick={(e) => e.stopPropagation()}
        >
          trace {traceId.slice(0, 12)}… →
        </Link>
      )}
    </span>
  );
}
