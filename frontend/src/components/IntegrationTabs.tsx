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

import { Link, useLocation } from "react-router-dom";

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
  const loc = useLocation();
  const base = `/integrations/${encodeURIComponent(integrationId)}`;
  const tabs: Tab[] = [
    { label: "Overview", path: base, exact: true },
    { label: "Messages", path: `${base}/messages`, count: messagesCount },
    { label: "Logs", path: `${base}/logs` },
    { label: "Services", path: `${base}/services` },
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
    <nav
      aria-label="Integration sections"
      className="flex items-end gap-1 border-b"
      style={{ borderColor: "var(--border)" }}
    >
      {tabs.map((t) => {
        const active = isActive(t);
        const errBadge = t.tone === "err" && (t.count ?? 0) > 0;
        const content = (
          <span className="inline-flex items-baseline gap-1.5">
            <span>{t.label}</span>
            {errBadge ? (
              // Count pill matching the service-detail tab counts
              // (.svc-tab .count): a monospace number in a rounded chip,
              // surface-3 by default and primary-soft when the tab is active.
              <span
                style={{
                  font: "500 11px 'JetBrains Mono', monospace",
                  padding: "1px 6px",
                  borderRadius: 999,
                  background: active ? "var(--primary-soft)" : "var(--surface-3)",
                  color: active ? "var(--primary-ink)" : "var(--ink-2)",
                  border: active
                    ? "1px solid color-mix(in oklab, var(--primary) 25%, transparent)"
                    : "1px solid var(--border)",
                }}
                title={`${t.count} open issue${t.count === 1 ? "" : "s"} (failed traces, delayed traces, failing health checks, unacknowledged errors)`}
              >
                {formatCount(t.count!)}
              </span>
            ) : t.count !== undefined && t.tone !== "err" ? (
              <span
                className="text-xs"
                style={{ color: "var(--muted)", fontWeight: 400 }}
              >
                · {formatCount(t.count)}
              </span>
            ) : null}
          </span>
        );
        const baseStyle = {
          padding: "8px 14px",
          marginBottom: -1,
          fontSize: 14,
          fontWeight: active ? 700 : 400,
          borderBottom: active
            ? "3px solid var(--primary)"
            : "3px solid transparent",
          background: active ? "var(--primary-soft)" : "transparent",
          color: active
            ? "var(--primary-ink)"
            : t.disabled
              ? "var(--muted)"
              : "var(--ink-2)",
          borderTopLeftRadius: 6,
          borderTopRightRadius: 6,
          cursor: t.disabled ? "not-allowed" : "pointer",
        } as const;

        if (t.disabled) {
          return (
            <span
              key={t.label}
              style={baseStyle}
              title="Coming soon"
              aria-disabled="true"
            >
              {content}
            </span>
          );
        }
        return (
          <Link key={t.label} to={t.path} style={baseStyle}>
            {content}
          </Link>
        );
      })}
    </nav>
  );
}

function formatCount(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}
