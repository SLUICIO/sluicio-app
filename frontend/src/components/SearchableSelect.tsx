// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A single-select dropdown with a typeahead filter — for choosing one
// value out of potentially hundreds (e.g. the service filter on the
// Logs page) where a plain <select> is unusable. Options are filtered
// client-side; the empty value ("") is the "all / none" choice shown
// with allLabel. Closes on outside click or Escape; arrow keys + Enter
// navigate the filtered list.

import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";

interface Props {
  value: string;
  onChange: (v: string) => void;
  options: string[];
  placeholder?: string;
  allLabel?: string;
  // Maps an option value to its display label (defaults to the value
  // itself). Use when the value is an id but the user sees a name — the
  // filter matches against the label, not the value.
  labelFor?: (v: string) => string;
  // Which edge of the trigger the popover aligns to. Default "left";
  // use "right" when the control sits near the right edge of the window
  // so the popover doesn't overflow off-screen.
  align?: "left" | "right";
}

export default function SearchableSelect({
  value,
  onChange,
  options,
  placeholder = "Search…",
  allLabel = "All",
  labelFor = (v) => v,
  align = "left",
}: Props) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const btnRef = useRef<HTMLButtonElement | null>(null);
  const popRef = useRef<HTMLDivElement | null>(null);
  // Viewport coords for the portaled popover (position: fixed), so it escapes
  // any ancestor overflow:hidden (cards) / transform (drawers).
  const [coords, setCoords] = useState<{ top: number; left: number; minWidth: number } | null>(null);

  // The selectable list always starts with the "all" entry (empty value).
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const matches = q ? options.filter((o) => labelFor(o).toLowerCase().includes(q)) : options;
    return ["", ...matches];
  }, [options, query, labelFor]);

  useEffect(() => {
    if (!open) return;
    const onPointer = (e: MouseEvent) => {
      const t = e.target as Node;
      // The popover is portaled to <body>, so it's outside wrapRef — check it too.
      if (wrapRef.current?.contains(t) || popRef.current?.contains(t)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // Position the portaled popover under the trigger and keep it there while
  // open (scroll in capture phase catches scrolling in any ancestor).
  useEffect(() => {
    if (!open) return;
    const place = () => {
      const el = btnRef.current;
      if (!el) return;
      const r = el.getBoundingClientRect();
      setCoords({
        top: r.bottom + 4,
        left: align === "right" ? r.right : r.left,
        minWidth: Math.max(r.width, 240),
      });
    };
    place();
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [open, align]);

  useEffect(() => {
    if (open) {
      setQuery("");
      setActive(0);
      // Focus the filter input once the popover is mounted.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  const choose = (v: string) => {
    onChange(v);
    setOpen(false);
  };

  const label = (v: string) => (v === "" ? allLabel : labelFor(v));

  const onInputKey = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => Math.min(a + 1, filtered.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (filtered[active] !== undefined) choose(filtered[active]);
    }
  };

  return (
    <div ref={wrapRef} style={{ position: "relative", display: "inline-block" }}>
      <button
        ref={btnRef}
        type="button"
        className="toolbar__select"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        style={{ minWidth: 180, textAlign: "left" }}
      >
        {value === "" ? <span className="muted">{allLabel}</span> : label(value)}
        <span aria-hidden style={{ float: "right", color: "var(--muted)" }}>▾</span>
      </button>

      {open && coords && createPortal(
        <div
          ref={popRef}
          role="listbox"
          style={{
            position: "fixed",
            // Above .edit-drawer-root (z 2000) so the popover shows when the
            // select is used inside an edit drawer, not just on a plain card.
            zIndex: 2100,
            top: coords.top,
            left: coords.left,
            // Right-align: anchor the popover's right edge to the trigger's.
            transform: align === "right" ? "translateX(-100%)" : undefined,
            minWidth: coords.minWidth,
            maxWidth: 360,
            background: "var(--surface-2)",
            border: "1px solid var(--border)",
            borderRadius: 6,
            boxShadow: "var(--shadow-pop, 0 8px 24px rgba(15, 23, 42, 0.18))",
            overflow: "hidden",
          }}
        >
          <div style={{ padding: 8, borderBottom: "1px solid var(--border)" }}>
            <input
              ref={inputRef}
              type="search"
              className="search__input"
              placeholder={placeholder}
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setActive(0);
              }}
              onKeyDown={onInputKey}
              style={{ width: "100%" }}
            />
          </div>
          <div style={{ maxHeight: 280, overflowY: "auto" }}>
            {filtered.map((opt, i) => (
              <button
                key={opt || "__all__"}
                type="button"
                role="option"
                aria-selected={opt === value}
                onMouseEnter={() => setActive(i)}
                onClick={() => choose(opt)}
                className="searchable-select__option"
                style={{
                  display: "block",
                  width: "100%",
                  textAlign: "left",
                  padding: "6px 10px",
                  fontSize: 13,
                  border: "none",
                  cursor: "pointer",
                  background: i === active ? "var(--surface-3)" : "transparent",
                  color: opt === "" ? "var(--muted)" : "var(--ink-2)",
                  fontWeight: opt === value ? 600 : 400,
                }}
              >
                {opt === "" ? allLabel : labelFor(opt)}
              </button>
            ))}
            {filtered.length === 1 && query.trim() !== "" && (
              <div className="muted" style={{ padding: "6px 10px", fontSize: 12 }}>
                No matches
              </div>
            )}
          </div>
        </div>,
        document.body,
      )}
    </div>
  );
}
