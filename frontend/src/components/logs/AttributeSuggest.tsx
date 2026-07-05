// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The attribute autocomplete popover: a two-step picker (key → value).
// Step 1 lists indexed attribute keys grouped Recently-used / HTTP /
// Resource / Domain, each with a type chip and approximate cardinality,
// filterable by substring and driveable from the keyboard. Step 2 shows
// an operator picker plus the key's top-N values (or a custom value).
// Backed by /log-fields (passed in) and /log-attributes/{key}/values.

import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../../api/client";
import type { LogAttrFilter, LogAttrOp, LogAttrValue, LogFieldEntry } from "../../api/types";

interface Props {
  fields: LogFieldEntry[];
  recent: string[];
  window: string;
  onPick: (f: LogAttrFilter) => void;
  onClose: () => void;
  // The top-N value fetcher for the second (value) step. Defaults to the
  // logs endpoint; the Metrics explorer passes api.metricAttributeValues
  // so the same picker drives metric attributes.
  fetchValues?: (key: string, window: string) => Promise<{ values: LogAttrValue[] }>;
  // Optional label tweaks for the metric flavour ("attributes are series
  // labels, not points").
  keyPlaceholder?: string;
  footHint?: string;
}

const RESOURCE_PREFIXES = [
  "service.", "k8s.", "host.", "cloud.", "deployment.",
  "telemetry.", "process.", "container.", "os.",
];

const STRING_OPS: { op: LogAttrOp; label: string }[] = [
  { op: "eq", label: "=" },
  { op: "neq", label: "≠" },
  { op: "contains", label: "contains" },
  { op: "not_contains", label: "!contains" },
  { op: "starts_with", label: "starts" },
  { op: "exists", label: "exists" },
];
const NUMBER_OPS: { op: LogAttrOp; label: string }[] = [
  { op: "eq", label: "=" },
  { op: "neq", label: "≠" },
  { op: "gt", label: ">" },
  { op: "gte", label: "≥" },
  { op: "lt", label: "<" },
  { op: "lte", label: "≤" },
  { op: "exists", label: "exists" },
];

function formatCard(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M distinct`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k distinct`;
  return `${n} distinct`;
}
function formatEvents(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M events`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k events`;
  return `${n} events`;
}
function groupFor(key: string): "HTTP" | "Resource" | "Domain" {
  if (key.startsWith("http.")) return "HTTP";
  if (RESOURCE_PREFIXES.some((p) => key.startsWith(p))) return "Resource";
  return "Domain";
}

export default function AttributeSuggest({
  fields,
  recent,
  window,
  onPick,
  onClose,
  fetchValues,
  keyPlaceholder,
  footHint,
}: Props) {
  const valuesFetcher = fetchValues ?? ((key: string, win: string) => api.logAttrValues(key, win));
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const [phase, setPhase] = useState<"key" | "value">("key");
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);

  // value phase state
  const [picked, setPicked] = useState<LogFieldEntry | null>(null);
  const [op, setOp] = useState<LogAttrOp>("eq");
  const [valueQuery, setValueQuery] = useState("");
  const [values, setValues] = useState<LogAttrValue[]>([]);
  const [valuesLoading, setValuesLoading] = useState(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    const onPointer = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) onClose();
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onPointer);
    requestAnimationFrame(() => inputRef.current?.focus());
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onPointer);
    };
  }, [onClose]);

  // ── key step ──────────────────────────────────────────────────
  const byKey = useMemo(() => new Map(fields.map((f) => [f.key, f])), [fields]);

  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    const match = (k: string) => (q ? k.toLowerCase().includes(q) : true);
    const recentItems = recent.map((k) => byKey.get(k)).filter((f): f is LogFieldEntry => !!f && match(f.key));
    const rest = fields.filter((f) => match(f.key) && !recent.includes(f.key));
    const http = rest.filter((f) => groupFor(f.key) === "HTTP");
    const resource = rest.filter((f) => groupFor(f.key) === "Resource");
    const domain = rest.filter((f) => groupFor(f.key) === "Domain");
    const out: { label: string; items: LogFieldEntry[] }[] = [];
    if (recentItems.length) out.push({ label: "Recently used", items: recentItems });
    if (http.length) out.push({ label: "HTTP", items: http });
    if (resource.length) out.push({ label: "Resource", items: resource });
    if (domain.length) out.push({ label: "Domain", items: domain });
    return out;
  }, [fields, recent, byKey, query]);

  const flat = useMemo(() => groups.flatMap((g) => g.items), [groups]);

  const pickKey = (f: LogFieldEntry) => {
    setPicked(f);
    setOp(f.type === "number" ? "gte" : "eq");
    setValueQuery("");
    setValues([]);
    setPhase("value");
  };

  const onKeyInput = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => Math.min(a + 1, flat.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (flat[active]) pickKey(flat[active]);
      // A typed key not in the catalog still goes to the value step so
      // the user can choose an operator and value — not auto-"exists".
      else if (query.trim()) pickKey({ key: query.trim(), type: "string", use_count: 0, cardinality: 0 });
    }
  };

  // ── value step ────────────────────────────────────────────────
  useEffect(() => {
    if (phase !== "value" || !picked) return;
    setValuesLoading(true);
    valuesFetcher(picked.key, window)
      .then((r) => setValues(r.values ?? []))
      .catch(() => setValues([]))
      .finally(() => setValuesLoading(false));
    // valuesFetcher is stable per render of the parent; intentionally omitted.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [phase, picked, window]);

  const ops = picked?.type === "number" ? NUMBER_OPS : STRING_OPS;
  const filteredValues = useMemo(() => {
    const q = valueQuery.trim().toLowerCase();
    return q ? values.filter((v) => v.value.toLowerCase().includes(q)) : values;
  }, [values, valueQuery]);

  const commit = (value: string) => onPick({ key: picked!.key, op, value });

  // exists needs no value — pick immediately when chosen
  const chooseOp = (next: LogAttrOp) => {
    if (next === "exists") onPick({ key: picked!.key, op: "exists", value: "" });
    else setOp(next);
  };

  return (
    <div className="attr-pop" ref={wrapRef} style={{ top: "calc(100% + 8px)", left: 0 }} role="listbox">
      {phase === "key" ? (
        <>
          <div className="attr-pop__head">
            <span aria-hidden style={{ color: "var(--muted)" }}>⌕</span>
            <input
              ref={inputRef}
              placeholder={keyPlaceholder ?? "Filter attributes by name…"}
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setActive(0);
              }}
              onKeyDown={onKeyInput}
            />
            <span className="kbd">esc</span>
          </div>
          <div className="attr-pop__body">
            {flat.length === 0 ? (
              query.trim() ? (
                <button
                  type="button"
                  className="attr-pop__item"
                  onClick={() =>
                    pickKey({ key: query.trim(), type: "string", use_count: 0, cardinality: 0 })
                  }
                >
                  <span className="attr-pop__key">Filter on “{query.trim()}” →</span>
                  <span />
                  <span className="attr-pop__card">custom key</span>
                </button>
              ) : fields.length === 0 ? (
                <div className="attr-pop__empty">
                  No attribute keys indexed in this window. Type a key name
                  (e.g. <span className="mono">service.name</span>) to filter on it.
                </div>
              ) : (
                <div className="attr-pop__empty">Type to filter attribute keys…</div>
              )
            ) : (
              groups.map((g) => (
                <div key={g.label}>
                  <div className="attr-pop__group">{g.label}</div>
                  {g.items.map((f) => {
                    const idx = flat.indexOf(f);
                    return (
                      <button
                        key={f.key}
                        type="button"
                        role="option"
                        aria-selected={idx === active}
                        className={`attr-pop__item ${idx === active ? "is-active" : ""}`}
                        onMouseEnter={() => setActive(idx)}
                        onClick={() => pickKey(f)}
                      >
                        <span className="attr-pop__key">{f.key}</span>
                        <span className={`typechip typechip--${f.type === "number" ? "number" : "string"}`}>
                          {f.type}
                        </span>
                        <span className="attr-pop__card">{formatCard(f.cardinality)}</span>
                      </button>
                    );
                  })}
                </div>
              ))
            )}
          </div>
          <div className="attr-pop__foot">
            <span><span className="kbd">↑↓</span> navigate · <span className="kbd">↵</span> pick</span>
            <span>{footHint ?? `${flat.length} of ${fields.length} indexed`}</span>
          </div>
        </>
      ) : (
        <>
          <div className="attr-pop__head">
            <button
              type="button"
              className="btn btn--link"
              onClick={() => setPhase("key")}
              style={{ padding: 0 }}
            >
              ‹ keys
            </button>
            <span className="attr-pop__key" style={{ flex: 1 }}>{picked?.key}</span>
            <span className="kbd">esc</span>
          </div>
          <div className="attr-pop__op">
            {ops.map((o) => (
              <button
                key={o.op}
                type="button"
                className={`level-seg__btn ${op === o.op ? "is-warn" : ""}`}
                aria-checked={op === o.op}
                style={op === o.op && o.op !== "exists" ? { background: "var(--primary-soft)", color: "var(--primary-ink)", fontWeight: 600 } : undefined}
                onClick={() => chooseOp(o.op)}
              >
                {o.label}
              </button>
            ))}
          </div>
          <div className="attr-pop__head" style={{ borderTop: 0 }}>
            <span aria-hidden style={{ color: "var(--muted)" }}>⌕</span>
            <input
              autoFocus
              placeholder="Filter or type a custom value…"
              value={valueQuery}
              onChange={(e) => setValueQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && valueQuery.trim()) commit(valueQuery.trim());
              }}
            />
          </div>
          <div className="attr-pop__body">
            {valuesLoading ? (
              <div className="attr-pop__empty">Loading values…</div>
            ) : (
              <>
                {valueQuery.trim() && (
                  <button type="button" className="attr-pop__item" onClick={() => commit(valueQuery.trim())}>
                    <span className="attr-pop__key">Add custom value “{valueQuery.trim()}”</span>
                    <span />
                    <span />
                  </button>
                )}
                {filteredValues.map((v) => (
                  <button key={v.value} type="button" className="attr-pop__item" onClick={() => commit(v.value)}>
                    <span className="attr-pop__key">{v.value}</span>
                    <span />
                    <span className="attr-pop__card">{formatEvents(v.events)}</span>
                  </button>
                ))}
                {!valuesLoading && filteredValues.length === 0 && !valueQuery.trim() && (
                  <div className="attr-pop__empty">No indexed values. Type a custom value above.</div>
                )}
              </>
            )}
          </div>
          <div className="attr-pop__foot">
            <span><span className="kbd">↵</span> add filter</span>
            <span>{picked?.type}</span>
          </div>
        </>
      )}
    </div>
  );
}
