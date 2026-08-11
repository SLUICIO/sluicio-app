// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// "Show this as a column" — the in-context half of issue #23.
//
// This exists because the settings list answers the wrong question when
// you do not yet know the key. Standing on a real span looking at
// `documents.exported = 7` you can point at the answer; on a settings
// page it is a string among forty.
//
// The one thing this surface can do that no other can: show the value.
// The popover previews the column with the reading in front of you,
// which is the strongest possible confirmation that the key is the one
// you meant.
//
// Feedback deliberately lives on the ROW rather than in a transient
// toast. The effect is a persistent state of this attribute, not an
// event, so a control that reflects that state answers "did it work"
// for as long as the drawer is open — and doubles as the undo.

import { useState } from "react";
import { api } from "../../api/client";
import { MAX_MESSAGE_COLUMNS, type MessageColumn } from "../../api/types";
import { humanizeAttributeKey } from "../../lib/humanizeAttributeKey";

interface Props {
  attrKey: string;
  /** The value on the span being looked at, for the preview. */
  value: string;
  integrationID: string;
  columns: MessageColumn[];
  onChanged: (cols: MessageColumn[]) => void;
}

export default function PromoteAttributeButton({
  attrKey,
  value,
  integrationID,
  columns,
  onChanged,
}: Props) {
  const [open, setOpen] = useState(false);
  const [label, setLabel] = useState(() => humanizeAttributeKey(attrKey));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const promoted = columns.some((c) => c.key === attrKey);
  const full = columns.length >= MAX_MESSAGE_COLUMNS;

  const write = async (next: MessageColumn[]) => {
    setBusy(true);
    setErr(null);
    try {
      const res = await api.setMessageColumns(integrationID, next);
      onChanged(res.message_columns);
      setOpen(false);
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  if (promoted) {
    return (
      <button
        type="button"
        className="badge"
        style={{ fontSize: 10, marginLeft: 6, cursor: "pointer" }}
        disabled={busy}
        title="Shown as a column in this integration's Messages list — click to remove"
        onClick={() => void write(columns.filter((c) => c.key !== attrKey))}
      >
        column ×
      </button>
    );
  }

  if (!open) {
    return (
      <button
        type="button"
        className="btn btn--sm"
        style={{ fontSize: 10, marginLeft: 6, padding: "0 5px" }}
        disabled={full}
        title={
          full
            ? `This integration already shows ${MAX_MESSAGE_COLUMNS} columns — remove one first`
            : "Show this attribute as a column in the Messages list"
        }
        onClick={() => setOpen(true)}
      >
        + column
      </button>
    );
  }

  return (
    <div
      style={{
        marginTop: 6,
        padding: 10,
        border: "1px solid var(--border)",
        borderRadius: 8,
        background: "var(--surface-2)",
      }}
    >
      <div style={{ fontWeight: 600, fontSize: 12.5, marginBottom: 6 }}>Show as a column</div>
      <label style={{ display: "block", fontSize: 11.5 }} className="muted">
        Heading
        <input
          className="search__input"
          style={{ width: "100%", marginTop: 3, fontSize: 12.5 }}
          autoFocus
          maxLength={40}
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && label.trim()) {
              void write([...columns, { key: attrKey, label: label.trim() }]);
            }
            if (e.key === "Escape") setOpen(false);
          }}
        />
      </label>

      {/* The reason this belongs in the drawer and not in settings. */}
      <div style={{ marginTop: 8, fontSize: 11.5 }}>
        <span className="muted">Preview: </span>
        <span style={{ fontWeight: 600 }}>{label.trim() || humanizeAttributeKey(attrKey)}</span>
        <span className="muted"> │ </span>
        <span className="mono">{value}</span>
      </div>

      {err && (
        <div className="alert alert--error" style={{ margin: "8px 0 0", fontSize: 11.5 }}>
          {err}
        </div>
      )}

      <div style={{ display: "flex", gap: 6, marginTop: 9, alignItems: "center" }}>
        <button
          type="button"
          className="btn btn--sm btn--primary"
          disabled={busy || !label.trim()}
          onClick={() => void write([...columns, { key: attrKey, label: label.trim() }])}
        >
          {busy ? "Adding…" : "Add"}
        </button>
        <button type="button" className="btn btn--sm" disabled={busy} onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
      <div className="muted" style={{ fontSize: 11, marginTop: 6, lineHeight: 1.45 }}>
        Adds a column to this integration&rsquo;s Messages list. The value is read from any span in
        the message, so every message gets one even when the attribute sits on a different step.
      </div>
    </div>
  );
}
