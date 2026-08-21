// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Which attributes an integration may be filtered by (issue #31).
//
// The twin of MessageColumnsEditor, and deliberately its mirror image:
// same picker, same label field, same save shape. They sit next to each
// other, are set by the same person about the same attributes, and the
// name a reader sees in a column header should be the name they see in
// the filter that narrows it.
//
// Where a service emits a hundred attributes the filter picker is not a
// feature but a haystack. This is how an editor names the handful worth
// filtering on, and gives them names a reader recognises: customer.id
// shown as KundId.

import { useEffect, useMemo, useState } from "react";
import { api } from "../../api/client";
import type { MessageAttributeKey } from "../../api/types";

interface MessageFilter {
  kind?: "builtin" | "attribute";
  key: string;
  label: string;
}

// The standard fields an editor can offer, in the order they read.
// `status` first because "show me the failed ones" is the commonest
// question anybody asks; the lookup fields after, because nobody
// explores by them.
const BUILTIN_FIELDS: { key: string; label: string; lookup?: boolean }[] = [
  { key: "status", label: "status" },
  { key: "service", label: "service", lookup: true },
  { key: "errorType", label: "error type", lookup: true },
  { key: "traceId", label: "trace ID", lookup: true },
  { key: "spanId", label: "span ID", lookup: true },
];

interface Props {
  integrationID: string;
  /** The stored list; the editor keeps its own draft until saved. */
  value: MessageFilter[];
  canWrite: boolean;
  onSaved: () => void;
}

const MAX_MESSAGE_FILTERS = 20;

export default function MessageFiltersEditor({ integrationID, value, canWrite, onSaved }: Props) {
  const [draft, setDraft] = useState<MessageFilter[]>(value);
  const [keys, setKeys] = useState<MessageAttributeKey[]>([]);
  const [adding, setAdding] = useState(false);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => setDraft(value), [value]);

  useEffect(() => {
    // A wide window, same reasoning as the columns editor: an
    // integration that runs monthly still needs its vocabulary offered,
    // and this is a key list rather than data.
    api
      .integrationAttributeKeys(integrationID, "30d")
      .then((r) => setKeys(r.attribute_keys ?? []))
      .catch(() => setKeys([]));
  }, [integrationID]);

  const dirty = useMemo(() => JSON.stringify(draft) !== JSON.stringify(value), [draft, value]);
  const chosen = useMemo(() => new Set(draft.map((f) => f.key)), [draft]);
  const attributeDraft = useMemo(() => draft.filter((f) => f.kind !== "builtin"), [draft]);
  const available = useMemo(() => keys.filter((k) => !chosen.has(k.key)), [keys, chosen]);

  const save = async () => {
    setSaving(true);
    setErr(null);
    try {
      await api.setIntegrationMessageFilters(integrationID, draft);
      onSaved();
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="card" style={{ marginTop: 16 }}>
      <div className="card__header">
        Filter fields
        <span className="muted" style={{ marginLeft: 8, fontWeight: 400, fontSize: 13 }}>
          · what this integration can be searched by
        </span>
      </div>
      <div style={{ padding: "12px 16px" }}>
        <p className="muted" style={{ margin: "0 0 12px", fontSize: 13 }}>
          Choose which attributes appear in the filter picker for this integration&rsquo;s
          messages, and what each is called. A service emitting a hundred attributes offers
          a hundred filters; naming the few that matter is what makes the picker usable.
          Up to {MAX_MESSAGE_FILTERS} fields.
        </p>
        {/* Said plainly because it is the part with consequences: this
            is enforced, so removing a field breaks a saved view that
            used it. Better to know here than to find out from a
            colleague. */}
        <p className="muted" style={{ margin: "0 0 12px", fontSize: 12.5 }}>
          <strong>Leave this empty to allow every attribute</strong>, which is how it works
          today. With a list set, a search on any other field is refused — including from
          the API, and including a saved view that used one.
        </p>

        {err && (
          <div className="alert alert--error" style={{ marginBottom: 12 }}>
            {err}
          </div>
        )}

        {/* Standard fields as toggles rather than a picker: there are
            five and they are always the same five, so a list you tick is
            clearer than a dropdown you search. Leaving all five off
            keeps them ALL offered, which is what every integration
            configured before this did. */}
        <div style={{ marginBottom: 14 }}>
          <span className="svc-field-label">Standard fields</span>
          <div style={{ display: "flex", gap: 14, flexWrap: "wrap", marginTop: 6 }}>
            {BUILTIN_FIELDS.map((b) => {
              const on = draft.some((f) => f.kind === "builtin" && f.key === b.key);
              return (
                <label key={b.key} style={{ display: "flex", gap: 6, alignItems: "center", fontSize: 13 }}>
                  <input
                    type="checkbox"
                    checked={on}
                    disabled={!canWrite}
                    onChange={(e) => {
                      if (e.target.checked) {
                        setDraft([...draft, { kind: "builtin", key: b.key, label: b.label }]);
                      } else {
                        setDraft(draft.filter((f) => !(f.kind === "builtin" && f.key === b.key)));
                      }
                    }}
                  />
                  <span>{b.label}</span>
                  {b.lookup && (
                    <span className="muted" style={{ fontSize: 11 }}>
                      lookup
                    </span>
                  )}
                </label>
              );
            })}
          </div>
          <span className="muted" style={{ fontSize: 12, display: "block", marginTop: 6 }}>
            None ticked offers all five. Tick some to offer exactly those. The ones marked
            lookup are only usable by someone who already has an id in hand, so they are
            grouped apart in the picker either way.
          </span>
        </div>

        <span className="svc-field-label">Attributes</span>
        {attributeDraft.length === 0 ? (
          <div className="muted" style={{ fontSize: 12, marginBottom: 12 }}>
            No restriction. Every attribute can be filtered on.
          </div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: 8, marginBottom: 12 }}>
            {attributeDraft.map((f) => (
              <div key={f.key} style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
                <span className="mono" style={{ fontSize: 12.5, minWidth: 180 }}>
                  {f.key}
                </span>
                <span className="muted" aria-hidden>
                  →
                </span>
                <input
                  className="svc-input"
                  style={{ maxWidth: 220 }}
                  value={f.label}
                  disabled={!canWrite}
                  aria-label={`Label shown for ${f.key}`}
                  onChange={(e) =>
                    setDraft(draft.map((x) => (x.key === f.key ? { ...x, label: e.target.value } : x)))
                  }
                />
                {canWrite && (
                  <button
                    className="btn btn--link"
                    onClick={() => setDraft(draft.filter((x) => x.key !== f.key))}
                  >
                    Remove
                  </button>
                )}
              </div>
            ))}
          </div>
        )}

        {canWrite && (
          <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
            {adding ? (
              <select
                className="svc-input"
                style={{ maxWidth: 320 }}
                autoFocus
                defaultValue=""
                onChange={(e) => {
                  const key = e.target.value;
                  if (key) {
                    // The label defaults to the key. The server
                    // humanises a blank one, but showing the raw key
                    // here is honest about what will be stored if the
                    // editor leaves it alone.
                    setDraft([...draft, { kind: "attribute", key, label: key }]);
                  }
                  setAdding(false);
                }}
                onBlur={() => setAdding(false)}
              >
                <option value="">Pick an attribute…</option>
                {available.map((k) => (
                  <option key={k.key} value={k.key}>
                    {k.key}
                  </option>
                ))}
              </select>
            ) : (
              <button
                className="btn"
                onClick={() => setAdding(true)}
                disabled={draft.length >= MAX_MESSAGE_FILTERS || available.length === 0}
              >
                + add a field
              </button>
            )}
            <button className="btn btn--primary" onClick={save} disabled={!dirty || saving}>
              {saving ? "Saving…" : "Save"}
            </button>
            {dirty && (
              <button className="btn btn--link" onClick={() => setDraft(value)} disabled={saving}>
                Reset
              </button>
            )}
          </div>
        )}
      </div>
    </section>
  );
}
