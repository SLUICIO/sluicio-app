// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which dashboard you land on, and what order the tabs come in.
//
// Marking a dashboard "my default" used to be very close to decorative.
// The tab strip came back from the server ordered by position, so the
// default sat wherever it happened to sit; and the landing pick put the
// last-used board AHEAD of the default, so the setting only ever applied
// to someone who had never clicked a tab on that browser. One click on
// any other dashboard buried it permanently.
//
// So the default now leads the strip and wins the landing pick. Last-used
// is kept as the fallback for the common case of having no default at
// all, where returning to the board you were on is the friendliest thing
// to do.
//
// The trade-off is deliberate: with a default set, arriving at the page
// always shows the default, even if you were looking at another board a
// minute ago. That is what "default" has to mean for the setting to be
// worth having — and switching back is one click.

import type { Dashboard } from "../api/types";

/**
 * Tab order: the default first, then the server's own order.
 *
 * Sorts a copy — the caller's array is state, and mutating it in place
 * would skip re-renders that depend on identity.
 */
export function orderDashboards(dashboards: Dashboard[]): Dashboard[] {
  return dashboards
    .slice()
    .sort(
      (a, b) =>
        Number(b.isDefault) - Number(a.isDefault) ||
        a.position - b.position ||
        a.createdAt.localeCompare(b.createdAt),
    );
}

/**
 * The dashboard to show when arriving at the page.
 *
 * Order: the default, then the remembered one, then the first tab.
 * Returns null only when there are no dashboards at all.
 */
export function pickActiveDashboard(
  dashboards: Dashboard[],
  rememberedId: string | null,
): Dashboard | null {
  if (dashboards.length === 0) return null;
  const remembered = rememberedId
    ? dashboards.find((d) => d.id === rememberedId)
    : undefined;
  // Ordered first so "the first tab" is the same board the user sees at
  // the left of the strip, rather than whatever the server listed first.
  const ordered = orderDashboards(dashboards);
  return ordered.find((d) => d.isDefault) ?? remembered ?? ordered[0];
}
