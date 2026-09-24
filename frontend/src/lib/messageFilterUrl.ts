// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// messageFilterUrl — shared serialization between the message
// FilterEditor and the URL query string. Used by the global Messages
// page, the integration Messages tab and the service Messages tab, so a
// link reproduces the filter set the sender was looking at.
//
// The URL is this page's state. It used to carry a third of it: the
// attribute and its value, with the operator thrown away and every
// other field dropped. Refreshing turned "order.id contains ORD" into
// "order.id equals ORD", and a row on service or error type vanished.
// A link that comes back as a DIFFERENT search is worse than one that
// comes back empty, because nothing on screen says it changed.
//
// Grammar, one chunk per row, comma separated in ?q:
//
//	chunk := [@]field[.path] [~op] [*] ":" value
//
//	order.id:ORD-1              a payload row, equals, the short form
//	order.id~contains:ORD       any other operator
//	order.id~exists:            an operator that takes no value
//	order.id*:ORD-1             ... and its child spans
//	@service:order-api          a field that is not an attribute
//
// A bare key is a payload attribute, so every link written before this
// still reads. Values are percent-encoded inside the chunk, because a
// value may contain the comma that separates chunks (an "is one of"
// list always does) or the colon that ends the field.
//
// Time is intentionally NOT encoded here: the page-wide range selector
// owns ?range=. Locked scope rows are implied by the route.

import type { Field, Filter, Operator } from "../components/search/FilterEditor";
import { uid } from "./uid";

const FIELDS: Field[] = [
  "payload",
  "time",
  "integration",
  "status",
  "service",
  "errorType",
  "traceId",
  "spanId",
];

const OPERATORS: Operator[] = [
  "equals",
  "contains",
  "is",
  "in",
  "matches",
  "not_equals",
  "not_contains",
  "exists",
  "not_exists",
];

const VALUELESS: Operator[] = ["exists", "not_exists"];

// A value may hold anything, including the separators. Decoding is
// forgiving: a link written by hand (or by an older build) will not be
// encoded, and "%" on its own is not an error worth dropping a filter
// for.
function decodeValue(raw: string): string {
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

// hydrateFiltersFromUrl reads ?q / ?s into a list of user filters. The
// caller prepends any locked scope filter; this only returns the
// user-set rows.
export function hydrateFiltersFromUrl(search: string): Filter[] {
  const params = new URLSearchParams(search);
  const out: Filter[] = [];

  const s = params.get("s");
  if (s) {
    out.push({ id: uid(), field: "status", op: "is", value: s, removable: true });
  }

  const q = params.get("q");
  if (!q) return out;

  for (const chunk of q.split(",")) {
    const trimmed = chunk.trim();
    if (!trimmed) continue;
    const at = trimmed.indexOf(":");
    if (at < 0) continue;
    let lhs = trimmed.slice(0, at);
    const value = decodeValue(trimmed.slice(at + 1));

    let includeDescendants = false;
    if (lhs.endsWith("*")) {
      includeDescendants = true;
      lhs = lhs.slice(0, -1);
    }

    let op: Operator = "equals";
    const tilde = lhs.indexOf("~");
    if (tilde >= 0) {
      const candidate = lhs.slice(tilde + 1) as Operator;
      if (!OPERATORS.includes(candidate)) continue; // not ours to guess at
      op = candidate;
      lhs = lhs.slice(0, tilde);
    }

    if (lhs.startsWith("@")) {
      const field = lhs.slice(1) as Field;
      if (!FIELDS.includes(field) || field === "payload") continue;
      // time is the header's, never a row.
      if (field === "time") continue;
      out.push({ id: uid(), field, op, value, removable: true });
      continue;
    }

    // Everything else is a payload attribute. "payload." is accepted as
    // a prefix so both spellings resolve to the same row.
    const path = lhs.startsWith("payload.") ? lhs.slice("payload.".length) : lhs;
    if (!path) continue;
    if (!value && !VALUELESS.includes(op)) continue; // half a link is not a filter
    out.push({
      id: uid(),
      field: "payload",
      fieldPath: path,
      op,
      value,
      removable: true,
      ...(includeDescendants ? { includeDescendants: true } : {}),
    });
  }

  return out;
}

// writeFiltersToParams encodes the user-set filters as ?q / ?s on the
// given URLSearchParams (mutating it). Optional and locked rows are
// skipped: locked is implied by the route, and an optional row is one
// the reader muted. It clears any prior q/s/t first, so turning a
// filter off removes it from the URL rather than leaving it behind.
export function writeFiltersToParams(filters: Filter[], params: URLSearchParams): void {
  params.delete("s");
  params.delete("q");
  params.delete("t"); // legacy time param, never re-emitted

  const q: string[] = [];
  for (const f of filters) {
    if (f.optional || f.locked) continue;
    if (f.field === "time") continue; // the header owns the range
    if (f.field === "status") {
      params.set("s", f.value);
      continue;
    }
    const valueless = VALUELESS.includes(f.op);
    if (!f.value && !valueless) continue; // a row still being filled in
    const opPart = f.op === "equals" ? "" : `~${f.op}`;
    const childPart = f.includeDescendants ? "*" : "";
    const value = valueless ? "" : encodeURIComponent(f.value);
    if (f.field === "payload") {
      if (!f.fieldPath) continue;
      q.push(`${f.fieldPath}${opPart}${childPart}:${value}`);
    } else {
      q.push(`@${f.field}${opPart}:${value}`);
    }
  }
  if (q.length > 0) params.set("q", q.join(","));
}
