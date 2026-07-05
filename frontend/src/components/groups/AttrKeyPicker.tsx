// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A searchable attribute-key picker for "group by attribute". The key
// list can be large (and the backend already returns the most-used keys
// from recent data, ranked), so this shows a capped, filterable list in
// a popover rather than a giant native <select>.

import { useEffect, useMemo, useRef, useState } from "react";

const SHOWN = 60;

export default function AttrKeyPicker({
  value,
  keys,
  onPick,
}: {
  value: string;
  keys: string[];
  onPick: (k: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const ref = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    requestAnimationFrame(() => inputRef.current?.focus());
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase();
    const matched = s ? keys.filter((k) => k.toLowerCase().includes(s)) : keys;
    return matched.slice(0, SHOWN);
  }, [keys, q]);

  return (
    <div ref={ref} style={{ position: "relative" }}>
      <button
        type="button"
        className="m-rs-sel"
        style={{ minWidth: 150, textAlign: "left" }}
        onClick={() => setOpen((o) => !o)}
        title={value || "choose attribute…"}
      >
        {value || "choose attribute…"}
      </button>
      {open && (
        <div className="attr-pop" style={{ top: "calc(100% + 6px)", right: 0, width: 320 }} role="listbox">
          <div className="attr-pop__head">
            <span aria-hidden style={{ color: "var(--muted)" }}>⌕</span>
            <input
              ref={inputRef}
              placeholder="Filter attributes by name…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
            <span className="kbd">esc</span>
          </div>
          <div className="attr-pop__body">
            {filtered.length === 0 ? (
              <div className="attr-pop__empty">No attributes match.</div>
            ) : (
              filtered.map((k) => (
                <button
                  key={k}
                  type="button"
                  role="option"
                  aria-selected={k === value}
                  className={`attr-pop__item ${k === value ? "is-active" : ""}`}
                  onClick={() => {
                    onPick(k);
                    setOpen(false);
                    setQ("");
                  }}
                >
                  <span className="attr-pop__key">{k}</span>
                </button>
              ))
            )}
          </div>
          <div className="attr-pop__foot">
            <span>{filtered.length} shown{keys.length > SHOWN ? ` of ${keys.length}` : ""}</span>
          </div>
        </div>
      )}
    </div>
  );
}
