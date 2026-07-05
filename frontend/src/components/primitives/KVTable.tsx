// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// KVTable — the aligned two-column key/value list used wherever the
// UI shows a list of attributes (log details, trace span details,
// integration metadata, …). The grid is the same in every spot so
// keys and values line up across rows regardless of caller.
//
// History: this lived under components/logs/ when only the log
// drawer used it; moved to primitives/ when trace and integration
// detail pages adopted the same look (issue #5).

import { ReactNode, useState } from "react";

export interface KVRow {
  // Key cell content. Use a string for plain code-style attribute
  // names; pass JSX when the key needs a tooltip target, a required
  // marker, or other inline decoration.
  k: ReactNode;
  // Value cell content. Plain strings render mono in the "mono"
  // variant; JSX lets callers render icons (✓/✗) or muted dashes.
  v: ReactNode;
  // Optional tooltip text shown on hover over the key cell — used
  // by metadata fields that carry a human description alongside the
  // label.
  keyTitle?: string;
  // String to copy when the row's copy button is clicked. Omit (or
  // pass null) to hide the copy button — appropriate when the value
  // is a UI affordance ("—", "✓ yes") rather than literal data.
  copyValue?: string | null;
  // Optional React key when `k` is not a string. Falls back to the
  // index if neither is available.
  rowKey?: string;
}

interface Props {
  rows: KVRow[];
  // "mono" — code-style keys + values (attribute names, IDs, raw
  // values). "prose" — sans-serif keys + values (human-readable
  // labels, formatted display values).
  variant?: "mono" | "prose";
  // When true the table draws its own border + background. Default
  // false because most callers already sit inside a card.
  bordered?: boolean;
  // Text shown when `rows` is empty.
  emptyLabel?: string;
  // Column policy. "single" (default) keeps the existing one-row-
  // per-line layout — right for narrow side drawers like the log /
  // trace details. "auto" opts the container into CSS multi-column
  // (column-width ~360px, capped at 2 cols) so a long metadata
  // list reads as two newspaper-style columns on wide pages and
  // collapses to one on narrow ones. Used by the integration detail
  // page where the metadata panel can have 20+ entries.
  columns?: "single" | "auto";
}

export default function KVTable({
  rows,
  variant = "mono",
  bordered = false,
  emptyLabel = "None.",
  columns = "single",
}: Props) {
  const [copied, setCopied] = useState<string | null>(null);
  const copy = (id: string, v: string) => {
    navigator.clipboard?.writeText(v).then(
      () => {
        setCopied(id);
        window.setTimeout(() => setCopied((c) => (c === id ? null : c)), 1200);
      },
      () => {},
    );
  };

  if (rows.length === 0) {
    return (
      <div className="placeholder" style={{ padding: 12, fontSize: 12 }}>
        {emptyLabel}
      </div>
    );
  }

  const cls = [
    "kvtable",
    `kvtable--${variant}`,
    bordered ? "kvtable--bordered" : "",
    columns === "auto" ? "kvtable--columns-auto" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={cls}>
      {rows.map((r, i) => {
        const id = r.rowKey ?? (typeof r.k === "string" ? r.k : String(i));
        const showCopy = typeof r.copyValue === "string";
        return (
          <div className="kvtable__row" key={id}>
            <span className="kvtable__k" title={r.keyTitle}>
              {r.k}
            </span>
            <span className="kvtable__v">{r.v}</span>
            {showCopy ? (
              <button
                className="kvtable__copy"
                type="button"
                title="Copy value"
                onClick={() => copy(id, r.copyValue as string)}
              >
                {copied === id ? "✓" : "⧉"}
              </button>
            ) : (
              <span className="kvtable__copy kvtable__copy--placeholder" />
            )}
          </div>
        );
      })}
    </div>
  );
}

// Convenience: build a KVRow[] from a plain {key:value} attribute
// map sorted by key. Used by the log/trace attribute sections where
// every row is a string attribute and the value is its own copy
// target.
export function attributeRows(m: Record<string, string> | undefined): KVRow[] {
  return Object.entries(m ?? {})
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([k, v]) => ({ k, v, copyValue: v }));
}
