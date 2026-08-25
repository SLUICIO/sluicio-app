// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// TabStrip — the one tab strip. Active tab gets a --primary-soft pill
// background, a 3px --primary bottom border and bold weight; inactive
// tabs stay plain --ink-2, per the Sluicio handoff.
//
// Extracted because a second surface needed the same strip and the
// styling lived inline in IntegrationTabs. Copying it would have made two
// descriptions of one visual rule, and the copy that nobody edits is the
// one that drifts - a settings page whose tabs are subtly not the tabs
// one click away reads as a different product, not a different page.
//
// Items navigate (`to`) or switch state (`onClick`). Both exist because
// integration sections are routes and settings sections are not, and
// forcing either into the other's shape would be worse than supporting
// two: a route per settings group would multiply the page, and a state
// tab for a route would break the back button.

import { ReactNode } from "react";
import { Link } from "react-router-dom";

export interface TabItem {
  key: string;
  label: string;
  /** Navigates when set; otherwise the item is a button using onClick. */
  to?: string;
  onClick?: () => void;
  active: boolean;
  /** Renders muted and unclickable, for a section not built yet. */
  disabled?: boolean;
  /** A count beside the label. "err" renders the pill; otherwise "· N". */
  count?: number;
  tone?: "err";
  countTitle?: string;
}

export function formatTabCount(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

export default function TabStrip({
  items,
  ariaLabel,
}: {
  items: TabItem[];
  ariaLabel: string;
}) {
  return (
    <nav
      aria-label={ariaLabel}
      className="flex items-end gap-1 border-b"
      style={{ borderColor: "var(--border)" }}
    >
      {items.map((t) => {
        const errBadge = t.tone === "err" && (t.count ?? 0) > 0;
        const content: ReactNode = (
          <span className="inline-flex items-baseline gap-1.5">
            <span>{t.label}</span>
            {errBadge ? (
              // Count pill matching the service-detail tab counts
              // (.svc-tab .count): a monospace number in a rounded chip,
              // surface-3 by default, primary-soft when the tab is active.
              <span
                style={{
                  font: "500 11px 'JetBrains Mono', monospace",
                  padding: "1px 6px",
                  borderRadius: 999,
                  background: t.active ? "var(--primary-soft)" : "var(--surface-3)",
                  color: t.active ? "var(--primary-ink)" : "var(--ink-2)",
                  border: t.active
                    ? "1px solid color-mix(in oklab, var(--primary) 25%, transparent)"
                    : "1px solid var(--border)",
                }}
                title={t.countTitle}
              >
                {formatTabCount(t.count!)}
              </span>
            ) : t.count !== undefined && t.tone !== "err" ? (
              <span className="text-xs" style={{ color: "var(--muted)", fontWeight: 400 }}>
                · {formatTabCount(t.count)}
              </span>
            ) : null}
          </span>
        );

        const baseStyle = {
          padding: "8px 14px",
          marginBottom: -1,
          fontSize: 14,
          fontWeight: t.active ? 700 : 400,
          borderBottom: t.active ? "3px solid var(--primary)" : "3px solid transparent",
          background: t.active ? "var(--primary-soft)" : "transparent",
          color: t.active ? "var(--primary-ink)" : t.disabled ? "var(--muted)" : "var(--ink-2)",
          borderTopLeftRadius: 6,
          borderTopRightRadius: 6,
          cursor: t.disabled ? "not-allowed" : "pointer",
        } as const;

        if (t.disabled) {
          return (
            <span key={t.key} style={baseStyle} title="Coming soon" aria-disabled="true">
              {content}
            </span>
          );
        }
        if (t.to) {
          return (
            <Link key={t.key} to={t.to} style={baseStyle}>
              {content}
            </Link>
          );
        }
        return (
          <button
            key={t.key}
            type="button"
            // The strip is a row of peers, so the button must not carry
            // the default button chrome that would make one look raised
            // beside a Link that never could.
            //
            // Longhands only, deliberately. The `border` and `font`
            // shorthands reset every longhand they cover whatever order
            // they are written in, so a shorthand here silently ate the
            // active tab's underline and its 14px/700 text and left the
            // button at the browser's 16px/400 default. Killing three
            // edges by name leaves borderBottom alone.
            style={{
              ...baseStyle,
              borderTop: "none",
              borderLeft: "none",
              borderRight: "none",
              fontFamily: "inherit",
            }}
            aria-current={t.active ? "page" : undefined}
            onClick={t.onClick}
          >
            {content}
          </button>
        );
      })}
    </nav>
  );
}
