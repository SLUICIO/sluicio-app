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
import {
  BUILTIN_COLUMNS,
  MAX_MESSAGE_COLUMNS,
  isAttributeColumn,
  type BuiltinColumnID,
  type MessageColumn,
} from "../../api/types";
import { humanizeAttributeKey } from "../../lib/humanizeAttributeKey";
import AttributePicker from "./AttributePicker";

// Mirrors builtinLabels in the Go package. The server fills a blank
// label from the same table, so a column added here and one added by
// PUTting a bare key come out identically.
const BUILTIN_LABELS: Record<BuiltinColumnID, string> = {
  msg_id: "msg id",
  service: "service",
  step: "step",
  duration: "duration",
};

interface Props {
  /** Whose attribute vocabulary to offer in the picker. */
  integrationID: string;
  /** The stored list; the editor keeps its own draft until saved. */
  value: MessageColumn[];
  canWrite: boolean;
  onSaved: (cols: MessageColumn[]) => void;
  /**
   * Where a save goes. Defaults to the integration's own column set.
   *
   * The Messages page passes a saver that writes to the ACTIVE VIEW
   * instead, so you can change columns where you are looking rather
   * than having to go to settings and come back. Same editor either
   * way — the difference is only what the list belongs to.
   */
  save?: (cols: MessageColumn[]) => Promise<MessageColumn[]>;
  /** Overrides for the framing when the target is not the integration. */
  title?: string;
  hint?: React.ReactNode;
}

export default function MessageColumnsEditor({
  integrationID,
  value,
  canWrite,
  onSaved,
  save: saveOverride,
  title,
  hint,
}: Props) {
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
      // Render what was STORED, not what was sent: the server fills
      // blank labels and collapses duplicate keys, whichever target
      // the list belongs to.
      const stored = saveOverride
        ? await saveOverride(next)
        : (await api.setMessageColumns(integrationID, next)).message_columns;
      setDraft(stored);
      onSaved(stored);
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

  const usedAttrs = new Set(draft.filter(isAttributeColumn).map((c) => c.key));
  const usedBuiltins = new Set(draft.filter((c) => c.kind === "builtin").map((c) => c.key));
  const available = keys.filter((k) => !usedAttrs.has(k.key));
  const missingBuiltins = BUILTIN_COLUMNS.filter((b) => !usedBuiltins.has(b));
  // Built-ins do not count: the cap exists to keep the table readable
  // and the built-ins are a fixed set of four, not an open-ended budget.
  const full = draft.filter(isAttributeColumn).length >= MAX_MESSAGE_COLUMNS;

  return (
    <section className="card" style={{ marginTop: 16 }}>
      <div className="card__header">{title ?? "Message columns"}</div>
      <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 12 }}>
        <p className="muted" style={{ fontSize: 13, lineHeight: 1.55, margin: 0 }}>
          {hint ?? (
            <>
              The columns of this integration&rsquo;s Messages list, left to right. Remove the ones
              you don&rsquo;t read, and add span attributes that matter — how many documents a run
              exported, which month an archive covered. An attribute&rsquo;s value is read from{" "}
              <strong>any span in the message</strong>, so it does not have to sit on the span the
              matchers select. Up to {MAX_MESSAGE_COLUMNS} attribute columns.
            </>
          )}
        </p>
        <p className="muted" style={{ fontSize: 12, margin: 0 }}>
          The status dot, the timestamp and <span className="mono">open ›</span> are always shown: a
          message list with no time can&rsquo;t be read, and one with no way in can&rsquo;t be used.
        </p>

        {draft.length === 0 && (
          <div className="placeholder" style={{ padding: 10, fontSize: 13 }}>
            No columns — every message will show only its time. Add one below.
          </div>
        )}

        {draft.map((c, i) => (
          <div key={`${c.kind ?? "attribute"}:${c.key}`} style={{ display: "flex", gap: 8, alignItems: "center" }}>
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
            <span style={{ flex: 1, fontSize: 12, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>
              {c.kind === "builtin" ? (
                <span className="badge" style={{ fontSize: 10 }}>built-in</span>
              ) : (
                <span className="mono muted">{c.key}</span>
              )}
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
            options={full ? [] : available}
            builtins={missingBuiltins.map((b) => ({
              key: b,
              label: BUILTIN_LABELS[b as BuiltinColumnID],
            }))}
            full={full}
            fullMessage={`Already showing ${MAX_MESSAGE_COLUMNS} attribute columns — remove one to add another.`}
            onCancel={() => setAdding(false)}
            onPickBuiltin={(key) => {
              setAdding(false);
              setDraft([
                ...draft,
                { kind: "builtin", key, label: BUILTIN_LABELS[key as BuiltinColumnID] },
              ]);
            }}
            onPick={(key) => {
              setAdding(false);
              setDraft([...draft, { kind: "attribute", key, label: humanizeAttributeKey(key) }]);
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
                disabled={full && missingBuiltins.length === 0}
                title={
                  full && missingBuiltins.length === 0
                    ? `At most ${MAX_MESSAGE_COLUMNS} attribute columns, and every built-in is already shown`
                    : undefined
                }
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
