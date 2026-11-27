// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// System settings → Payload links: the span attributes shown as links to a
// message's payload, each optionally prefixed with a base URL. The list is
// the organization's and applies to every message list; the server
// validates and resolves, this page only edits the list and previews the
// address an example value would open.

import { useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import type { PayloadLinkAttribute } from "../api/types";
import SearchableSelect from "./SearchableSelect";
import { resetPayloadLinkAttributes } from "../lib/payloadLinks";
import { previewPayloadLink } from "../lib/payloadLinkPreview";

const MAX = 10;
const DEFAULT_ATTRIBUTE = "io.payload.url";

interface Row {
  attribute: string;
  label: string;
  prefixed: boolean;
  base: string;
}

const toRows = (a: PayloadLinkAttribute[]): Row[] =>
  a.map((x) => ({ attribute: x.attribute, label: x.label ?? "", prefixed: !!x.base_url, base: x.base_url ?? "" }));

const toWire = (rows: Row[]): PayloadLinkAttribute[] =>
  rows.map((r) => ({
    attribute: r.attribute.trim(),
    ...(r.label.trim() ? { label: r.label.trim() } : {}),
    ...(r.prefixed && r.base.trim() ? { base_url: r.base.trim() } : {}),
  }));

export default function PayloadLinkSettings({ isAdmin }: { isAdmin: boolean }) {
  const [loaded, setLoaded] = useState<PayloadLinkAttribute[] | null>(null);
  const [rows, setRows] = useState<Row[]>([]);
  const [keys, setKeys] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api
      .getPayloadLinkAttributes()
      .then((r) => {
        setLoaded(r.attributes ?? []);
        setRows(toRows(r.attributes ?? []));
      })
      .catch((e) => setError(String((e as Error).message ?? e)));
    // Span attribute keys seen this week, as suggestions for the picker.
    api
      .messageFields("7d")
      .then((r) => {
        const seen = new Set<string>();
        for (const f of r.fields ?? []) for (const k of f.attributeKeys ?? []) if (k.source === "span") seen.add(k.key);
        setKeys([...seen].sort());
      })
      .catch(() => setKeys([]));
  }, []);

  const wire = useMemo(() => toWire(rows), [rows]);
  if (!loaded) return error ? <div className="alert alert--error">{error}</div> : null;

  const dirty = JSON.stringify(wire) !== JSON.stringify(toWire(toRows(loaded)));
  const problems = rows.map((r, i) => {
    const a = r.attribute.trim();
    if (!a) return "Pick the attribute.";
    if (rows.findIndex((x) => x.attribute.trim() === a) !== i) return "Listed twice.";
    if (r.prefixed && !r.base.trim()) return "Enter the base URL the value goes after.";
    if (r.prefixed && !/^https?:\/\/[^/\s#]+/i.test(r.base.trim())) return "The base URL starts with https:// and names a host.";
    return null;
  });
  const update = (i: number, patch: Partial<Row>) => {
    setRows((cur) => cur.map((r, j) => (j === i ? { ...r, ...patch } : r)));
    setSaved(false);
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const r = await api.putPayloadLinkAttributes(wire);
      setLoaded(r.attributes ?? []);
      setRows(toRows(r.attributes ?? []));
      resetPayloadLinkAttributes(r.attributes ?? []);
      setSaved(true);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div style={{ maxWidth: 720 }} data-testid="payload-link-settings">
      <p className="muted" style={{ fontSize: 13, marginBottom: 18, lineHeight: 1.55 }}>
        Span attributes that say where a message&rsquo;s payload is kept, such as the file that was picked up or the
        invoice that was posted. Each becomes a column of links in every message list, shown when a message on the
        page carries it, and a link on the span in the trace. Sluicio never opens the address itself: the link opens in
        the reader&rsquo;s browser with their own access to the archive.
      </p>

      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        {rows.length === 0 && (
          <span className="muted" style={{ fontSize: 13 }}>
            No link attributes. Messages show no payload links.
          </span>
        )}
        {rows.map((r, i) => {
          const example = r.prefixed ? "2026/INV_42.pdf" : "https://archive.example.com/2026/INV_42.pdf";
          const preview = previewPayloadLink(r.prefixed ? r.base.trim() : "", example);
          return (
            <div
              key={i}
              className="rounded-lg border bg-surface-2"
              style={{ borderColor: "var(--border)", padding: 12 }}
              data-testid="payload-link-row"
            >
              <div style={{ display: "flex", gap: 10, alignItems: "flex-end", flexWrap: "wrap" }}>
                <label className="form__label" style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                  Attribute
                  <SearchableSelect
                    variant="field"
                    value={r.attribute}
                    onChange={(v) => update(i, { attribute: v })}
                    options={keys}
                    withoutAll
                    creatable
                    disabled={!isAdmin}
                    allLabel="Pick an attribute…"
                    placeholder="Type an attribute key…"
                    createLabel={(typed) => `Use “${typed}”`}
                  />
                </label>
                <label className="form__label" style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                  Column label
                  <input
                    className="search__input"
                    value={r.label}
                    onChange={(e) => update(i, { label: e.target.value })}
                    placeholder={r.attribute || "Payload"}
                    disabled={!isAdmin}
                    maxLength={60}
                    style={{ width: 200 }}
                  />
                </label>
                {isAdmin && (
                  <button
                    type="button"
                    className="btn"
                    style={{ marginLeft: "auto" }}
                    onClick={() => {
                      setRows((cur) => cur.filter((_, j) => j !== i));
                      setSaved(false);
                    }}
                    aria-label={`Remove ${r.attribute || "this link"}`}
                  >
                    Remove
                  </button>
                )}
              </div>
              <label style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 10, fontSize: 13.5 }}>
                <input
                  type="checkbox"
                  checked={r.prefixed}
                  disabled={!isAdmin}
                  onChange={(e) => update(i, { prefixed: e.target.checked })}
                />
                The value is a path or id: put a base URL in front of it
              </label>
              {r.prefixed && (
                <label className="form__label" style={{ marginTop: 8 }}>
                  Base URL
                  <input
                    className="search__input"
                    value={r.base}
                    onChange={(e) => update(i, { base: e.target.value })}
                    placeholder="https://archive.example.com/files"
                    disabled={!isAdmin}
                    style={{ width: "100%" }}
                  />
                </label>
              )}
              <p className="muted" style={{ fontSize: 12, margin: "8px 0 0" }}>
                {problems[i] ? (
                  <span style={{ color: "var(--err-ink)" }}>{problems[i]}</span>
                ) : preview ? (
                  <>
                    A value of <code>{example}</code> opens <code>{preview}</code>
                  </>
                ) : (
                  <>Values that are not an http or https address show as text.</>
                )}
              </p>
            </div>
          );
        })}
      </div>

      {isAdmin && rows.length < MAX && (
        <button
          type="button"
          className="btn"
          style={{ marginTop: 12 }}
          onClick={() => {
            const fresh = rows.some((r) => r.attribute === DEFAULT_ATTRIBUTE) ? "" : DEFAULT_ATTRIBUTE;
            setRows((cur) => [...cur, { attribute: fresh, label: "", prefixed: false, base: "" }]);
            setSaved(false);
          }}
        >
          Add a link attribute
        </button>
      )}

      {error && <div className="alert alert--error" style={{ marginTop: 12 }}>{error}</div>}
      {isAdmin && (
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: 14 }}>
          <button
            type="button"
            className="btn btn--primary"
            disabled={!dirty || saving || problems.some(Boolean)}
            onClick={save}
          >
            {saving ? "Saving…" : "Save"}
          </button>
          {saved && !dirty && <span className="muted" style={{ fontSize: 12 }}>Saved.</span>}
        </div>
      )}
    </div>
  );
}
