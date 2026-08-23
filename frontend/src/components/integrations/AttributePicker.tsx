// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The attribute picker shared by the message COLUMNS editor and the
// message FILTER FIELDS editor.
//
// Both screens ask the same question of the same person about the same
// attributes: "out of everything this integration emits, which handful
// matters?" They sat next to each other answering it differently, one
// with this searchable panel and one with a plain <select> of every key
// in emission order. On an integration emitting a hundred attributes a
// bare <select> is not a picker but a haystack, which is the very thing
// both features exist to fix.
//
// Attributes are ranked by how many spans carry the key, because a key
// seen on three spans out of thousands makes a mostly-empty column or a
// filter that matches almost nothing, and the editor should be able to
// see that coming. The count is a SPAN count with no denominator, so it
// is shown as a raw figure rather than a percentage we cannot honestly
// compute.

import { useMemo, useState } from "react";

export interface PickableAttribute {
  key: string;
  source: string;
  useCount: number;
}

interface Props {
  options: PickableAttribute[];
  onPick: (key: string) => void;
  onCancel: () => void;
  /**
   * Standard fields offered above the search box, unfiltered. They are a
   * few known things, and the common reason to reopen this picker is "I
   * removed one and want it back" — making that a search would be a
   * worse answer than a list. Omit where the screen offers its
   * built-ins some other way.
   */
  builtins?: { key: string; label: string }[];
  onPickBuiltin?: (key: string) => void;
  /** Heading over the attribute search. */
  attributeLabel?: string;
  /**
   * Set when the caller's own cap is reached: the search is replaced by
   * this message, so the editor learns why rather than finding a picker
   * that silently refuses.
   */
  full?: boolean;
  fullMessage?: string;
  /** Shown when the integration has emitted nothing to pick from. */
  emptyMessage?: string;
}

export default function AttributePicker({
  options,
  onPick,
  onCancel,
  builtins = [],
  onPickBuiltin,
  attributeLabel = "Span attribute",
  full = false,
  fullMessage,
  emptyMessage = "No attributes seen on this integration in the last 30 days.",
}: Props) {
  const [q, setQ] = useState("");
  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return [...options]
      .filter((o) => !needle || o.key.toLowerCase().includes(needle))
      .sort((a, b) => b.useCount - a.useCount || a.key.localeCompare(b.key))
      .slice(0, 40);
  }, [options, q]);

  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: 8, padding: 10 }}>
      {builtins.length > 0 && onPickBuiltin && (
        <div style={{ marginBottom: 10 }}>
          <div className="muted" style={{ fontSize: 11, marginBottom: 4 }}>Built-in</div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
            {builtins.map((b) => (
              <button
                key={b.key}
                type="button"
                className="btn btn--sm"
                onClick={() => onPickBuiltin(b.key)}
              >
                {b.label}
              </button>
            ))}
          </div>
        </div>
      )}

      <div className="muted" style={{ fontSize: 11, marginBottom: 4 }}>{attributeLabel}</div>
      {full ? (
        <div className="muted" style={{ fontSize: 12.5, padding: 6 }}>
          {fullMessage}
        </div>
      ) : (
        <input
          className="search__input"
          style={{ width: "100%", fontSize: 13 }}
          autoFocus
          placeholder="Filter attributes…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
      )}
      <div style={{ maxHeight: 220, overflowY: "auto", marginTop: 8 }}>
        {!full && shown.length === 0 && (
          <div className="muted" style={{ fontSize: 12.5, padding: 6 }}>
            {options.length === 0 ? emptyMessage : "Nothing matches that filter."}
          </div>
        )}
        {shown.map((o) => (
          <button
            key={o.key}
            type="button"
            className="btn btn--sm"
            style={{
              display: "flex",
              width: "100%",
              justifyContent: "space-between",
              marginBottom: 3,
              textAlign: "left",
            }}
            onClick={() => onPick(o.key)}
          >
            <span className="mono" style={{ fontSize: 12 }}>{o.key}</span>
            <span className="muted" style={{ fontSize: 11 }}>
              {o.source} · {o.useCount.toLocaleString()} spans
            </span>
          </button>
        ))}
      </div>
      <button type="button" className="btn btn--sm" style={{ marginTop: 6 }} onClick={onCancel}>
        Cancel
      </button>
    </div>
  );
}
