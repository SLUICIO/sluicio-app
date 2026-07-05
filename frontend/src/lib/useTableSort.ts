// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// useTableSort — small three-state sort hook shared by every
// `.table` in Sluicio. The third state ("no sort") restores the
// caller's natural row order, which matters for the lists that come
// out of the API already sorted in a meaningful way (e.g. services
// by last_seen desc, integrations by updated_at desc).
//
// Sort key extraction is supplied per-column as a map so each table
// can decide what "sort by Status" actually means without leaking
// presentation concerns into this file. Returning `null` /
// `undefined` / `""` from a key function pushes that row to the
// bottom regardless of direction — empty cells shouldn't dominate
// the top of an ascending sort.
import { useCallback, useMemo, useRef, useState } from "react";

export type SortDir = "asc" | "desc";

export interface SortState<K extends string> {
  key: K;
  dir: SortDir;
}

export type SortValue = string | number | boolean | null | undefined;
export type SortKeyFn<T> = (row: T) => SortValue;

export interface UseTableSortResult<T, K extends string> {
  sortedRows: T[];
  sort: SortState<K> | null;
  toggleSort: (key: K) => void;
}

/**
 * Returns the row list in the current sort order plus the controls
 * the table header needs to drive sorting. Three-state: click a
 * header to sort ascending, click again to descend, click a third
 * time to clear the sort (rows return to the caller's order).
 */
export function useTableSort<T, K extends string>(
  rows: readonly T[],
  keyFns: Record<K, SortKeyFn<T>>,
  defaultSort: SortState<K> | null = null,
): UseTableSortResult<T, K> {
  const [sort, setSort] = useState<SortState<K> | null>(defaultSort);

  const toggleSort = useCallback((key: K) => {
    setSort((curr) => {
      if (!curr || curr.key !== key) return { key, dir: "asc" };
      if (curr.dir === "asc") return { key, dir: "desc" };
      return null;
    });
  }, []);

  // The key-function map is typically recreated on every render
  // (inline arrow functions, captured props). Stash it in a ref so
  // the memo can depend on the stable trigger pair (rows, sort)
  // without re-running on every parent render.
  const keyFnsRef = useRef(keyFns);
  keyFnsRef.current = keyFns;

  const sortedRows = useMemo(() => {
    const base = rows.slice();
    if (!sort) return base;
    const fn = keyFnsRef.current[sort.key];
    if (!fn) return base;
    base.sort((a, b) => compareSortValues(fn(a), fn(b)));
    if (sort.dir === "desc") base.reverse();
    return base;
  }, [rows, sort]);

  return { sortedRows, sort, toggleSort };
}

// Comparison rules:
//   1. Empty values (null / undefined / "") always sort to the
//      bottom, regardless of direction. This is what most apps do
//      because the alternative — a wall of "—" at the top of the
//      asc sort — is rarely what the user wanted.
//   2. Numbers compare numerically.
//   3. Booleans compare with false < true (asc puts unchecked first).
//   4. Strings use localeCompare with `numeric` so "item 2" sorts
//      before "item 10".
function compareSortValues(a: SortValue, b: SortValue): number {
  const aEmpty = a === null || a === undefined || a === "";
  const bEmpty = b === null || b === undefined || b === "";
  if (aEmpty && bEmpty) return 0;
  if (aEmpty) return 1;
  if (bEmpty) return -1;
  if (typeof a === "number" && typeof b === "number") return a - b;
  if (typeof a === "boolean" && typeof b === "boolean") {
    return a === b ? 0 : a ? 1 : -1;
  }
  return String(a).localeCompare(String(b), undefined, {
    numeric: true,
    sensitivity: "base",
  });
}
