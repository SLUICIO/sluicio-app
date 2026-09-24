// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which columns the integrations table shows, and in what order.
//
// Four sources can answer, and the order they are asked in is the whole
// of it:
//
//   1. what the reader has just done, this visit
//   2. ?cols= in the link they opened
//   3. the layout saved for this user on the server
//   4. the legacy localStorage value from before that existed
//
// The first source is new. The state of the picker used to be read back
// out of the URL, which was fine while a navigation applied
// synchronously and stopped being fine when it became a transition: a
// control whose state waits on a round trip flickers back under the
// hand that moved it, and a test that clicks and looks immediately sees
// the old value. State belongs where it is decided; the URL, the
// preference and localStorage are copies for the next visit.

export interface ColumnLayout {
  order: string[];
  hidden: string[];
}

export interface LayoutSources {
  /** What the reader did this visit. Null until they touch the picker. */
  session: ColumnLayout | null;
  /** ?cols=, an ORDERED VISIBLE list. Null when the link carries none. */
  colsParam: string | null;
  /** The per-user saved layout, which stores the hidden set directly. */
  saved: ColumnLayout | null;
  /** The legacy localStorage value: an ordered visible list. */
  legacy: string | null;
  /** Every column that exists, in default order. */
  defaultOrder: string[];
}

/**
 * The effective order: the chosen source's ids first, then everything it
 * does not mention, appended in default order.
 *
 * Unknown ids are dropped (a metadata field somebody deleted) and new
 * ones appear rather than vanishing for a reader with a saved layout.
 */
export function resolveColumnOrder(s: LayoutSources): string[] {
  const known = new Set(s.defaultOrder);
  const base = (
    s.session
      ? s.session.order
      : s.colsParam != null
        ? s.colsParam.split(",").filter(Boolean)
        : (s.saved?.order ?? [])
  ).filter((id) => known.has(id));
  const seen = new Set(base);
  return [...base, ...s.defaultOrder.filter((id) => !seen.has(id))];
}

/**
 * The hidden set.
 *
 * ?cols= and the legacy value are VISIBLE lists, so a column added to
 * the product after that link was written reads as hidden unless it is
 * converted here; the saved layout stores the hidden set directly, so a
 * new column defaults to visible for anyone who has one.
 */
export function resolveHiddenColumns(s: LayoutSources): Set<string> {
  const fromVisibleList = (raw: string) => {
    const visible = new Set(raw.split(",").filter(Boolean));
    return new Set(s.defaultOrder.filter((id) => !visible.has(id)));
  };
  if (s.session) return new Set(s.session.hidden.filter((id) => s.defaultOrder.includes(id)));
  if (s.colsParam != null) return fromVisibleList(s.colsParam);
  if (s.saved) return new Set(s.saved.hidden.filter((id) => s.defaultOrder.includes(id)));
  if (s.legacy != null) return fromVisibleList(s.legacy);
  return new Set();
}
