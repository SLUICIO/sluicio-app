// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The global Metrics page — the unscoped MetricsExplorer (every service's
// metrics). The same explorer, scoped to one service, backs the service
// Metrics tab.

import MetricsExplorer from "../components/metrics/MetricsExplorer";
import { usePageTitle } from "../lib/usePageTitle";

export default function MetricsPage() {
  usePageTitle("Metrics");
  return <MetricsExplorer />;
}
