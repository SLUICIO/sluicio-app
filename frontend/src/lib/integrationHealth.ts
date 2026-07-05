// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import type { IntegrationDetail } from "../api/types";

// integrationProblemCount totals every failure mode the Errors tab surfaces —
// failed traces, delayed traces, failing health checks, and unacknowledged
// errors. The Errors-tab badge uses it so an unhealthy integration reads as
// such even with no failed traces (e.g. only a failing "Failed to scrape"
// health check). Mirrors the four "bad" tiles on the Overview.
export function integrationProblemCount(
  d: IntegrationDetail | null | undefined,
): number {
  if (!d) return 0;
  return (
    (d.error_message_count ?? 0) +
    (d.delayed_message_count ?? 0) +
    (d.failing_check_count ?? 0) +
    (d.open_error_count ?? 0)
  );
}
