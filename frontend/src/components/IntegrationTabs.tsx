// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationTabs — the sub-tab strip shown at the top of every
// integration detail surface (Overview, Messages, Services, Errors,
// Settings). The active tab gets a --primary-soft pill background +
// 3px --primary bottom border + bold weight, per the Sluicio handoff.
//
// Inactive tabs use --ink-2 and stay plain. The Messages tab takes an
// optional count suffix shown in muted weight ("Messages · 12.4k"); the
// Errors tab takes an optional count pill styled like the service-detail
// tab counts (.svc-tab .count).

import { useLocation } from "react-router-dom";
import { TabStrip } from "./primitives";
import { useNavigationReach } from "../lib/useNavigationReach";

interface Props {
  integrationId: string;
  messagesCount?: number;
  // Total open-issue count for the Errors tab (failed traces + delayed traces
  // + failing health checks + unacknowledged errors — see integrationProblemCount).
  // >0 renders a count pill (matching the service-detail tab counts);
  // 0/undefined shows no badge.
  errorsCount?: number;
}

interface Tab {
  label: string;
  path: string;
  // exact: true means location.pathname must equal the path; otherwise
  // it's a prefix match. Overview is the exact "/integrations/:id" so
  // we want exact match to avoid stealing the active state from the
  // Messages tab.
  exact?: boolean;
  count?: number;
  // "err" renders the count as a pill matching the service-detail tab
  // counts (Errors tab); otherwise a plain muted "· N" suffix.
  tone?: "err";
  // disabled tabs render as muted, non-link spans — useful for tabs
  // we haven't built yet but want to advertise in the UI.
  disabled?: boolean;
}

export default function IntegrationTabs({ integrationId, messagesCount, errorsCount }: Props) {
  // What this reader can actually reach; null while unknown, which
  // reads as "offer everything".
  const reach = useNavigationReach();
  const loc = useLocation();
  const base = `/integrations/${encodeURIComponent(integrationId)}`;
  const tabs: Tab[] = [
    { label: "Overview", path: base, exact: true },
    { label: "Messages", path: `${base}/messages`, count: messagesCount },
    // Metrics, Logs and Services are dropped for a reader who cannot
    // reach them (issue #32). A grant of an INTEGRATION carries its
    // messages but not its services, and neither logs nor metrics —
    // those are emitted by the service and carry nothing attributing
    // them to one flow. Offering the tabs anyway means three of seven
    // lead to an empty page or "not found".
    ...(reach?.metrics === false ? [] : [{ label: "Metrics", path: `${base}/metrics` }]),
    ...(reach?.logs === false ? [] : [{ label: "Logs", path: `${base}/logs` }]),
    ...(reach?.services === false ? [] : [{ label: "Services", path: `${base}/services` }]),
    { label: "Errors", path: `${base}/errors`, count: errorsCount, tone: "err" },
    { label: "Metadata", path: `${base}/metadata` },
    // No Settings tab — editing the integration is the "✎ Edit integration"
    // button on the Overview header (which opens the tab-less settings view).
  ];

  const isActive = (t: Tab) => {
    if (t.exact) return loc.pathname === t.path;
    return loc.pathname === t.path || loc.pathname.startsWith(`${t.path}/`);
  };

  return (
    <TabStrip
      ariaLabel="Integration sections"
      items={tabs.map((t) => ({
        key: t.label,
        label: t.label,
        to: t.path,
        active: isActive(t),
        disabled: t.disabled,
        count: t.count,
        tone: t.tone,
        countTitle:
          t.tone === "err" && t.count !== undefined
            ? `${t.count} open issue${t.count === 1 ? "" : "s"} (failed traces, delayed traces, failing health checks, unacknowledged errors)`
            : undefined,
      }))}
    />
  );
}

