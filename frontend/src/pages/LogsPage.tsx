// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// LogsPage — the global Logs surface (route /logs). All the rendering
// (filter bar, volume histogram, virtualized table, group rollup) lives
// in <LogsView> so the integration-scoped Logs tab can reuse it without
// duplicating any of the keyset-paging or attribute-filter wiring.

import LogsView from "../components/logs/LogsView";
import { usePageTitle } from "../lib/usePageTitle";

export default function LogsPage() {
  usePageTitle("Logs");
  // Header (title + Refresh) is rendered by <LogsView> in standalone mode so
  // the Refresh button can sit top-right next to the title, matching Metrics.
  return <LogsView />;
}
