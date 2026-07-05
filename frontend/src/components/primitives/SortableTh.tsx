// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// SortableTh — a `<th>` that doubles as a sort toggle. Pair it with
// the `useTableSort` hook: feed in the column's sort key plus the
// current sort state, and the header renders an indicator (▲/▼) +
// emits `aria-sort` so assistive tech announces direction changes.
//
// Non-data columns (action menus, drag handles) should still use a
// plain `<th>` so users don't see a clickable header that does
// nothing.
import { ReactNode } from "react";
import type { SortState } from "../../lib/useTableSort";

interface Props<K extends string> {
  sortKey: K;
  state: SortState<K> | null;
  onSort: (key: K) => void;
  /** Extra className forwarded to the `<th>` (e.g. "num"). */
  className?: string;
  /** Forwarded to the `<th>` so callers can pin a column width. */
  style?: React.CSSProperties;
  /** Optional explicit title; defaults to "Sort by <children>". */
  title?: string;
  children: ReactNode;
}

export default function SortableTh<K extends string>({
  sortKey,
  state,
  onSort,
  className,
  style,
  title,
  children,
}: Props<K>) {
  const active = state?.key === sortKey;
  const dir = active ? state!.dir : null;
  const ariaSort: "ascending" | "descending" | "none" = active
    ? dir === "asc"
      ? "ascending"
      : "descending"
    : "none";

  return (
    <th
      className={[
        "th-sortable",
        active ? "th-sortable--active" : "",
        className ?? "",
      ]
        .filter(Boolean)
        .join(" ")}
      style={style}
      aria-sort={ariaSort}
    >
      <button
        type="button"
        className="th-sortable__btn"
        onClick={() => onSort(sortKey)}
        title={title ?? (typeof children === "string" ? `Sort by ${children}` : "Sort")}
      >
        <span className="th-sortable__label">{children}</span>
        <span aria-hidden="true" className="th-sortable__indicator">
          {dir === "asc" ? "▲" : dir === "desc" ? "▼" : "↕"}
        </span>
      </button>
    </th>
  );
}
