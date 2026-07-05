// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// messageFilterUrl — shared serialization between the message FilterEditor
// and the URL query string. Used by the global Messages page, the
// integration Messages tab, and the service Messages tab so a link
// reproduces the exact filter set (the canonical "shareable URL state").
//
// Shorthand params:
//   ?q=field:value[,field:value…]   one or more payload filters
//   ?s=<status>                     a single status filter (e.g. "err only")
//
// Time is intentionally NOT encoded here — the page-wide range selector
// owns ?range= (via useTimeWindow). Locked/scope filters are implied by
// the route, so only the user-set rows round-trip through the URL.

import type { Filter } from "../components/search/FilterEditor";

// hydrateFiltersFromUrl reads ?q / ?s into a list of user filters. The
// caller prepends any locked scope filter; this only returns the
// user-set rows.
export function hydrateFiltersFromUrl(search: string): Filter[] {
  const params = new URLSearchParams(search);
  const out: Filter[] = [];

  const s = params.get("s");
  if (s) {
    out.push({
      id: crypto.randomUUID(),
      field: "status",
      op: "is",
      value: s,
      removable: true,
    });
  }

  const q = params.get("q");
  if (q) {
    for (const chunk of q.split(",")) {
      const m = chunk.trim().match(/^([^:]+):(.+)$/);
      if (!m) continue;
      const [, lhs, rhs] = m;
      // Strip a leading "payload." so both "payload.orderId:1" and the
      // bare "orderId:1" shorthand resolve to the same payload row.
      const path = lhs.startsWith("payload.") ? lhs.slice("payload.".length) : lhs;
      out.push({
        id: crypto.randomUUID(),
        field: "payload",
        fieldPath: path,
        op: "equals",
        value: rhs,
        removable: true,
      });
    }
  }

  return out;
}

// writeFiltersToParams encodes the user-set filters as ?q / ?s on the
// given URLSearchParams (mutating it). Only payload + status rows are
// serialized; optional/locked/time rows are skipped (locked is implied
// by the route, time is the header's job). It clears any prior q/s/t
// first so toggling a filter off removes it from the URL.
export function writeFiltersToParams(
  filters: Filter[],
  params: URLSearchParams,
): void {
  params.delete("s");
  params.delete("q");
  params.delete("t"); // legacy time param, never re-emitted
  const q: string[] = [];
  for (const f of filters) {
    if (f.optional || f.locked) continue;
    if (f.field === "status") {
      params.set("s", f.value);
    } else if (f.field === "payload" && f.fieldPath) {
      q.push(`${f.fieldPath}:${f.value}`);
    }
  }
  if (q.length > 0) params.set("q", q.join(","));
}
