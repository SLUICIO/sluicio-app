// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The deferred half of an integration list: the names come from a
// Postgres read (`listIntegrations(…, { stats: "defer" })`) and the
// numbers are filled in afterwards, a batch at a time, from
// `integrationStats`. The Integrations page and the dashboard both read
// this way, so the batching, the merge and the stale-pass rule live here
// once rather than drifting apart in two pages.

import type { Integration, IntegrationStats } from "../api/types";

/** Rows per stats request. The server caps a request at 25. */
export const STATS_BATCH = 10;

/** Fold a batch of stats into the rows they belong to, by id. */
export function mergeStats(rows: Integration[], stats: IntegrationStats[]): Integration[] {
  if (stats.length === 0) return rows;
  const byID = new Map(stats.map((s) => [s.id, s] as const));
  return rows.map((row) => {
    const s = byID.get(row.id);
    return s ? ({ ...row, ...s } as Integration) : row;
  });
}

/**
 * Keep the numbers already on screen while a refresh re-reads the names.
 *
 * A deferred row carries no stats fields at all (the server leaves them
 * out rather than sending zeroes), so spreading it over the previous row
 * replaces what Postgres knows and keeps the last counts until the new
 * ones arrive. Without this every auto-refresh would blank the cards
 * back to "···" for the length of a pass.
 */
export function carryStats(prev: Integration[] | null, rows: Integration[]): Integration[] {
  if (!prev || prev.length === 0) return rows;
  const byID = new Map(prev.map((p) => [p.id, p] as const));
  return rows.map((row) => {
    const old = byID.get(row.id);
    return old ? ({ ...old, ...row } as Integration) : row;
  });
}

/** `ids` with `first` moved to the front, in `first`'s order. */
export function prioritise(ids: string[], first: string[]): string[] {
  const all = new Set(ids);
  const head = first.filter((id) => all.has(id));
  const seen = new Set(head);
  return [...new Set(head), ...ids.filter((id) => !seen.has(id))];
}

/**
 * Ask for the stats of `ids` one batch after another.
 *
 * One batch at a time rather than all at once: a burst of parallel
 * requests would put the whole slow read back on the cell at the same
 * moment and undo the point of splitting it. `isCurrent` is checked
 * after every answer, so a pass that a newer one has replaced (a window
 * change, an environment switch) stops writing numbers nobody is
 * looking at. A failed batch is reported done without stats: those rows
 * show a dash rather than taking the page down.
 */
export async function fillStatsInBatches(
  ids: string[],
  fetchBatch: (batch: string[]) => Promise<IntegrationStats[]>,
  on: {
    isCurrent: () => boolean;
    stats: (stats: IntegrationStats[]) => void;
    done: (batch: string[]) => void;
  },
  batchSize: number = STATS_BATCH,
): Promise<void> {
  for (let i = 0; i < ids.length; i += batchSize) {
    const batch = ids.slice(i, i + batchSize);
    try {
      const stats = await fetchBatch(batch);
      if (!on.isCurrent()) return;
      on.stats(stats);
    } catch {
      // Left without numbers; see above.
    }
    if (!on.isCurrent()) return;
    on.done(batch);
  }
}
