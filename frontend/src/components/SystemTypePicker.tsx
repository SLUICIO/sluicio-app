// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// SystemTypePicker — choose a system's type from the catalog, or create a
// new type without leaving the form.
//
// This replaces a free-text field. `type_key` is stored verbatim and is
// never validated against the catalog, so typing it from memory was a
// trap: "RabbitMQ", "rabbit" or a trailing space all save happily and
// then match no type at all. The system silently gets no starter checks,
// no monitoring template and no docs link — and nothing anywhere says
// why. A field that behaves like an enum should look like one.
//
// The create-new path exists because the catalog cannot be complete:
// people run things we have never heard of, and "your system isn't in
// our list" is not an acceptable answer. It asks only for key + label
// (all the API requires); detection prefixes and starter checks are the
// System types page's job, and it links there.

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { SystemType } from "../api/types";
import SearchableSelect from "./SearchableSelect";

interface Props {
  value: string;
  onChange: (typeKey: string) => void;
  disabled?: boolean;
  // Rendered above the control. Omitted when the caller supplies its own.
  label?: string;
}

/** Slug a free-typed label the way the API will: lowercase, trimmed. */
function slugify(s: string): string {
  return s
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

export default function SystemTypePicker({ value, onChange, disabled, label }: Props) {
  const [types, setTypes] = useState<SystemType[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [newLabel, setNewLabel] = useState("");
  const [newKey, setNewKey] = useState("");
  const [keyEdited, setKeyEdited] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    api
      .listSystemTypes()
      .then((r) => setTypes(r.system_types ?? []))
      .catch(() => setTypes([]))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const known = types.some((t) => t.key === value);

  const create = async () => {
    const key = slugify(newKey || newLabel);
    const lbl = newLabel.trim();
    if (!key || !lbl) {
      setError("A name and a key are both required.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      // Minimal type: the API needs only key + label. Detection prefixes
      // and starter checks are richer decisions that belong on the System
      // types page, not in the middle of naming a system.
      await api.createSystemType({
        key,
        label: lbl,
        is_system: true,
        detect_prefixes: [],
        checks: [],
      });
      load();
      onChange(key);
      setCreating(false);
      setNewLabel("");
      setNewKey("");
      setKeyEdited(false);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  if (creating) {
    const previewKey = slugify(newKey || newLabel);
    return (
      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        {label && <span style={{ fontSize: 13 }}>{label}</span>}
        <input
          className="search__input"
          style={{ fontSize: 13 }}
          autoFocus
          placeholder="Name — e.g. IBM MQ"
          value={newLabel}
          onChange={(e) => {
            setNewLabel(e.target.value);
            if (!keyEdited) setNewKey(slugify(e.target.value));
          }}
        />
        <input
          className="search__input mono"
          style={{ fontSize: 12.5 }}
          placeholder="key — e.g. ibm-mq"
          value={newKey}
          onChange={(e) => {
            setKeyEdited(true);
            setNewKey(e.target.value);
          }}
        />
        <span className="muted" style={{ fontSize: 11.5 }}>
          Saved as <span className="mono">{previewKey || "…"}</span>. The key is how services are matched to this
          type, so it is lowercased and hyphenated. Add detection prefixes and starter checks later under{" "}
          <Link to="/system-types">System types</Link>.
        </span>
        {error && <div className="alert alert--error" style={{ margin: 0 }}>{error}</div>}
        <div style={{ display: "flex", gap: 8 }}>
          <button className="btn btn--sm primary" type="button" onClick={create} disabled={busy}>
            {busy ? "Creating…" : "Create type"}
          </button>
          <button
            className="btn btn--sm"
            type="button"
            disabled={busy}
            onClick={() => {
              setCreating(false);
              setError(null);
            }}
          >
            Cancel
          </button>
        </div>
      </div>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      {label && <span style={{ fontSize: 13 }}>{label}</span>}
      <SearchableSelect
        value={value}
        onChange={onChange}
        options={types.map((t) => t.key)}
        labelFor={(k) => {
          const t = types.find((x) => x.key === k);
          return t ? `${t.label} (${t.key})` : k;
        }}
        allLabel="No type"
        placeholder={loading ? "Loading types…" : "Pick a system type…"}
        disabled={disabled}
      />
      {/* A saved key that matches nothing is exactly the state this
          component exists to prevent, so say so rather than rendering it
          as if it were a real choice. Pre-existing systems can be in it. */}
      {value && !known && !loading && (
        <span className="muted" style={{ fontSize: 11.5, color: "var(--warn-ink, var(--ink))" }}>
          <span className="mono">{value}</span> isn’t a known system type — no starter checks or template will
          apply. Pick one above, or create it.
        </span>
      )}
      {!disabled && (
        <button
          className="btn btn--link"
          type="button"
          style={{ alignSelf: "flex-start", fontSize: 12 }}
          onClick={() => {
            setCreating(true);
            setError(null);
          }}
        >
          ＋ None of these match — create a type
        </button>
      )}
    </div>
  );
}
