// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// CSV export for the message (trace) search result set. Shared by the
// integration Messages page and the global Search page so both export
// identical columns. The export paginates the same /messages/search
// endpoint the table uses (keyset cursor) so it covers the WHOLE filtered
// result set — not just the rows currently loaded into the table — up to
// a safety cap.

import { api } from "../api/client";
import type {
  MessageCursor,
  MessageFilter,
  TraceSearchResult,
} from "../api/types";

// Hard cap so an unbounded filter can't try to stream millions of rows
// into the browser. If we hit it, the caller is told the export was
// truncated so it can warn the user.
export const CSV_EXPORT_CAP = 50_000;

// Page size used while paginating for export. Larger than the table's
// page size — fewer round-trips for a big export.
const EXPORT_PAGE = 1_000;

// Fixed columns, in order. attributes is a variable bag, so it's emitted
// last as a single JSON column to keep the row shape rectangular.
const COLUMNS: Array<{ header: string; get: (r: TraceSearchResult) => unknown }> = [
  { header: "trace_id", get: (r) => r.trace_id },
  { header: "trace_start", get: (r) => r.trace_start },
  { header: "duration_ms", get: (r) => r.duration_ms },
  { header: "has_error", get: (r) => r.has_error },
  { header: "total_spans", get: (r) => r.total_spans },
  { header: "service_count", get: (r) => r.service_count },
  { header: "matched_service", get: (r) => r.matched_service },
  { header: "matched_span_name", get: (r) => r.matched_span_name },
  {
    header: "attributes",
    get: (r) =>
      r.attributes && Object.keys(r.attributes).length
        ? JSON.stringify(r.attributes)
        : "",
  },
];

// escapeCell quotes a value per RFC 4180 when it contains a comma, quote,
// or newline; internal quotes are doubled. null/undefined become "".
function escapeCell(value: unknown): string {
  if (value === null || value === undefined) return "";
  const s = String(value);
  if (/[",\r\n]/.test(s)) {
    return `"${s.replace(/"/g, '""')}"`;
  }
  return s;
}

export function messageRowsToCsv(rows: TraceSearchResult[]): string {
  const header = COLUMNS.map((c) => c.header).join(",");
  const lines = rows.map((r) =>
    COLUMNS.map((c) => escapeCell(c.get(r))).join(","),
  );
  return [header, ...lines].join("\r\n");
}

// downloadCsv triggers a browser download of `csv` as `filename`. A UTF-8
// BOM is prepended so Excel opens non-ASCII content correctly.
export function downloadCsv(filename: string, csv: string): void {
  const blob = new Blob(["﻿", csv], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  // Revoke on the next tick so the click has definitely been handled.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

// fetchAllMessages paginates /messages/search until the cursor runs out
// or CSV_EXPORT_CAP rows are collected. Returns the rows plus whether the
// cap truncated the result.
export async function fetchAllMessages(req: {
  range?: string;
  filters: MessageFilter[];
}): Promise<{ rows: TraceSearchResult[]; capped: boolean }> {
  const rows: TraceSearchResult[] = [];
  let cursor: MessageCursor | undefined;
  // Bound the loop independently of the cap as a belt-and-braces guard
  // against a server that never stops returning a cursor.
  for (let page = 0; page < 1_000; page++) {
    const res = await api.searchMessages({
      range: req.range,
      filters: req.filters,
      limit: EXPORT_PAGE,
      cursor,
    });
    rows.push(...(res.results ?? []));
    if (rows.length >= CSV_EXPORT_CAP) {
      return { rows: rows.slice(0, CSV_EXPORT_CAP), capped: true };
    }
    if (!res.next_cursor) break;
    cursor = res.next_cursor;
  }
  return { rows, capped: false };
}

// csvFilename builds a timestamped, filesystem-safe filename like
// "messages-orders-erp-20260608-101530.csv". `scope` is slugified.
export function csvFilename(scope: string): string {
  const slug =
    scope
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "all";
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  const stamp = `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(
    d.getHours(),
  )}${p(d.getMinutes())}${p(d.getSeconds())}`;
  return `messages-${slug}-${stamp}.csv`;
}
