// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which span attributes an integration promotes to columns in its
// message list (issue #23).
//
// The list is short and ordered, and the order is the left-to-right
// column order, so every edit rewrites the whole list — there is no
// add/remove/reorder distinction to make in the UI or on the wire.
//
// Reordering is buttons, not drag-and-drop. With a cap of five rows a
// drag target is more code, worse on a keyboard, and worse on touch,
// for an interaction that moves an item one place.

import { useEffect, useMemo, useState } from "react";
import { api } from "../../api/client";
import { MAX_MESSAGE_COLUMNS, type MessageColumn } from "../../api/types";
import { humanizeAttributeKey } from "../../lib/humanizeAttributeKey";

interface Props {
  integrationID: string;
  /** The stored list; the editor keeps its own draft until saved. */
  value: MessageColumn[];
  canWrite: boolean;
  onSaved: (cols: MessageColumn[]) => void;
}

export default function MessageColumnsEditor({ integrationID, value, canWrite, onSaved }: Props) {
  const [draft, setDraft] = useState<MessageColumn[]>(value);
  const [keys, setKeys] = useState<{ key: string; source: string; useCount: number }[]>([]);
  const [adding, setAdding] = useState(false);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Re-sync when the page reloads the integration under us (e.g. after
  // the drawer's promote action writes a new column).
  useEffect(() => setDraft(value), [value]);

  useEffect(() => {
    // A wide window on purpose: an integration that runs monthly still
    // needs its vocabulary offered, and this is a key list, not data.
    api
      .integrationAttributeKeys(integrationID, "30d")
      .then((r) => setKeys(r.attribute_keys ?? []))
      .catch(() => setKeys([]));
  }, [integrationID]);

  const dirty = useMemo(
    () => JSON.stringify(draft) !== JSON.stringify(value),
    [draft, value],
  );

  const save = async (next: MessageColumn[]) => {
    setSaving(true);
    setErr(null);
    try {
      const res = await api.setMessageColumns(integrationID, next);
      // Render what was STORED, not what was sent: the server fills
      // blank labels and collapses duplicate keys.
      setDraft(res.message_columns);
      onSaved(res.message_columns);
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  const move = (i: number, delta: number) => {
    const j = i + delta;
    if (j < 0 || j >= draft.length) return;
    const next = [...draft];
    [next[i], next[j]] = [next[j], next[i]];
    setDraft(next);
  };

  const used = new Set(draft.map((c) => c.key));
  const available = keys.filter((k) => !used.has(k.key));
  const full = draft.length >= MAX_MESSAGE_COLUMNS;

  return (
    <section className="card" style={{ marginTop: 16 }}>
      <div className="card__header">Message columns</div>
      <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 12 }}>
        <p className="muted" style={{ fontSize: 13, lineHeight: 1.55, margin: 0 }}>
          Show a span attribute as its own column in this integration&rsquo;s Messages list — how
          many documents a run exported, which month an archive covered. The value is read from{" "}
          <strong>any span in the message</strong>, so it does not have to sit on the span the
          matchers select. Up to {MAX_MESSAGE_COLUMNS} columns.
        </p>

        {draft.length === 0 && (
          <div className="placeholder" style={{ padding: 10, fontSize: 13 }}>
            No columns yet. The list shows the first few attributes of the matched span, which is
            rarely the interesting part. Add one below, or promote an attribute from a message&rsquo;s
            span detail.
          </div>
        )}

        {draft.map((c, i) => (
          <div key={c.key} style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <div style={{ display: "flex", flexDirection: "column", gap: 1 }}>
              <button
                type="button"
                className="btn btn--sm"
                style={{ padding: "0 6px", lineHeight: 1.2 }}
                disabled={!canWrite || i === 0}
                title="Move left"
                onClick={() => move(i, -1)}
              >
                ↑
              </button>
              <button
                type="button"
                className="btn btn--sm"
                style={{ padding: "0 6px", lineHeight: 1.2 }}
                disabled={!canWrite || i === draft.length - 1}
                title="Move right"
                onClick={() => move(i, 1)}
              >
                ↓
              </button>
            </div>
            <input
              className="search__input"
              style={{ flex: "0 0 200px" }}
              value={c.label}
              disabled={!canWrite}
              maxLength={40}
              aria-label={`Column heading for ${c.key}`}
              onChange={(e) =>
                setDraft(draft.map((d, j) => (j === i ? { ...d, label: e.target.value } : d)))
              }
            />
            <span className="mono muted" style={{ flex: 1, fontSize: 12, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>
              {c.key}
            </span>
            {canWrite && (
              <button
                type="button"
                className="btn btn--sm btn--danger"
                title="Remove this column"
                onClick={() => setDraft(draft.filter((_, j) => j !== i))}
              >
                ×
              </button>
            )}
          </div>
        ))}

        {canWrite && adding && (
          <AttributePicker
            options={available}
            onCancel={() => setAdding(false)}
            onPick={(key) => {
              setAdding(false);
              setDraft([...draft, { key, label: humanizeAttributeKey(key) }]);
            }}
          />
        )}

        {err && <div className="alert alert--error" style={{ margin: 0 }}>{err}</div>}

        {canWrite && (
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            {!adding && (
              <button
                type="button"
                className="btn btn--sm"
                disabled={full}
                title={full ? `At most ${MAX_MESSAGE_COLUMNS} columns` : undefined}
                onClick={() => setAdding(true)}
              >
                + Add column
              </button>
            )}
            <button
              type="button"
              className="btn btn--sm btn--primary"
              disabled={!dirty || saving}
              onClick={() => void save(draft)}
            >
              {saving ? "Saving…" : "Save columns"}
            </button>
            {dirty && !saving && (
              <button type="button" className="btn btn--sm" onClick={() => setDraft(value)}>
                Discard
              </button>
            )}
          </div>
        )}
      </div>
    </section>
  );
}

/**
 * The key picker: substring filter over the attributes this integration
 * has actually emitted.
 *
 * Ranked by how many spans carry the key, because a key seen on three
 * spans out of thousands will render a mostly-empty column and the user
 * should be able to see that coming. The count is a SPAN count with no
 * denominator, so it is shown as a raw figure rather than a percentage
 * we cannot honestly compute.
 */
function AttributePicker({
  options,
  onPick,
  onCancel,
}: {
  options: { key: string; source: string; useCount: number }[];
  onPick: (key: string) => void;
  onCancel: () => void;
}) {
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
      <input
        className="search__input"
        style={{ width: "100%", fontSize: 13 }}
        autoFocus
        placeholder="Filter attributes…"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      <div style={{ maxHeight: 220, overflowY: "auto", marginTop: 8 }}>
        {shown.length === 0 && (
          <div className="muted" style={{ fontSize: 12.5, padding: 6 }}>
            {options.length === 0
              ? "No attributes seen on this integration in the last 30 days."
              : "Nothing matches that filter."}
          </div>
        )}
        {shown.map((o) => (
          <button
            key={o.key}
            type="button"
            className="btn btn--sm"
            style={{ display: "flex", width: "100%", justifyContent: "space-between", marginBottom: 3, textAlign: "left" }}
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
