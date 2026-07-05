// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Generic summary-rollup list: one row per group (label + stats), each
// expandable to lazily load and render its items. Used by the Metrics
// and Logs pages for group-by.
//
// `cacheKey` is the parent's signal for "the active filter set changed
// — invalidate cached per-group items". When it changes, we drop the
// memoised items map and, if a group is currently expanded, refetch
// its items immediately so the open list reflects the new filters
// (fix for issue #7: Logs grouped view's row counts updated when the
// severity filter changed but the expanded entries stayed stale).

import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";

export default function GroupRollup<G, I>({
  groups,
  groupKey,
  renderLabel,
  renderStats,
  loadItems,
  renderItems,
  loading,
  emptyLabel,
  cacheKey,
}: {
  groups: G[];
  groupKey: (g: G) => string;
  renderLabel: (g: G) => ReactNode;
  renderStats: (g: G) => ReactNode;
  loadItems: (g: G) => Promise<I[]>;
  renderItems: (items: I[]) => ReactNode;
  loading?: boolean;
  emptyLabel: string;
  // Stable string the parent computes from its filter state. When this
  // changes, the items cache is dropped and the open group is
  // refetched. Required, not optional — silently passing undefined
  // would re-introduce the staleness bug for future callers.
  cacheKey: string;
}) {
  const [openKey, setOpenKey] = useState<string | null>(null);
  const [items, setItems] = useState<Record<string, I[]>>({});
  const [itemsLoading, setItemsLoading] = useState<string | null>(null);

  // groupsRef keeps the latest groups array reachable from the
  // cacheKey effect without making `groups` a dependency (we only
  // want refetch-on-filter-change, not refetch-on-every-render).
  const groupsRef = useRef(groups);
  groupsRef.current = groups;
  const loadItemsRef = useRef(loadItems);
  loadItemsRef.current = loadItems;
  const groupKeyRef = useRef(groupKey);
  groupKeyRef.current = groupKey;

  // Drop the cache and refetch the open group whenever filters change.
  useEffect(() => {
    setItems({});
    setItemsLoading(null);
    if (openKey === null) return;
    const g = groupsRef.current.find((x) => groupKeyRef.current(x) === openKey);
    if (!g) {
      // The expanded group disappeared from the new result set (e.g.
      // filtering it out of existence). Collapse to a clean state.
      setOpenKey(null);
      return;
    }
    setItemsLoading(openKey);
    const target = openKey;
    loadItemsRef.current(g)
      .then((r) => setItems({ [target]: r }))
      .catch(() => setItems({ [target]: [] }))
      .finally(() => setItemsLoading((curr) => (curr === target ? null : curr)));
    // openKey is intentionally NOT in the dep array — we only refetch
    // when the parent's filter set changes, not when the user toggles.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cacheKey]);

  const toggle = (g: G) => {
    const k = groupKey(g);
    if (openKey === k) {
      setOpenKey(null);
      return;
    }
    setOpenKey(k);
    if (!items[k]) {
      setItemsLoading(k);
      loadItems(g)
        .then((r) => setItems((s) => ({ ...s, [k]: r })))
        .catch(() => setItems((s) => ({ ...s, [k]: [] })))
        .finally(() => setItemsLoading(null));
    }
  };

  if (loading && groups.length === 0) {
    return <div className="placeholder" style={{ margin: 12 }}>Loading groups…</div>;
  }
  if (groups.length === 0) {
    return <div className="placeholder" style={{ margin: 12 }}>{emptyLabel}</div>;
  }

  return (
    <div className="grp">
      {groups.map((g) => {
        const k = groupKey(g);
        const open = openKey === k;
        return (
          <div key={k} className="grp-group">
            <button type="button" className="grp-head" onClick={() => toggle(g)}>
              <span className="grp-chev">{open ? "▾" : "▸"}</span>
              <span className="grp-label">{renderLabel(g)}</span>
              <span className="grp-stats">{renderStats(g)}</span>
            </button>
            {open && (
              <div className="grp-body">
                {itemsLoading === k ? (
                  <div className="placeholder" style={{ margin: 10 }}>Loading…</div>
                ) : (
                  renderItems(items[k] ?? [])
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
