// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// useTraceHref — builds links to the full trace view (/traces/:id) that
// carry where the user came from: the current path + query as ?from=,
// and optionally the integration context as ?integration=. TraceDetail
// turns that into a breadcrumb linking back to the exact filtered list
// the user left. Use this instead of hand-writing `/traces/${id}`.

import { useLocation } from "react-router-dom";

export function useTraceHref(): (traceId: string, integrationId?: string) => string {
  const location = useLocation();
  return (traceId, integrationId) => {
    const params = new URLSearchParams();
    if (integrationId) params.set("integration", integrationId);
    params.set("from", `${location.pathname}${location.search}`);
    return `/traces/${encodeURIComponent(traceId)}?${params.toString()}`;
  };
}
