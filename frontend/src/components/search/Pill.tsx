// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Pill — a value you click to change, with its editor in a popover.
//
// Extracted from the Messages filter editor so the integration matcher
// editor can speak the same language. Two screens that both say "this
// field, this operator, this value" had two vocabularies: pills on one,
// dropdowns on the other, and a reader who learned one learned nothing
// about the other.

import { useEffect, useRef, useState } from "react";

export interface PillProps {
  kind: "field" | "op" | "value";
  label: string;
  accent?: boolean;
  locked?: boolean;
  dashed?: boolean;
  /** Muted (an optional row). Applies to the pill itself; the popover it
   *  opens stays fully opaque. */
  dimmed?: boolean;
  showLockIcon?: boolean;
  /** What this pill IS, for a screen reader and for a test: the label
   *  alone is its current value, which changes as somebody edits it. */
  ariaLabel?: string;
  editor: (props: { close: () => void }) => React.ReactNode;
}

export default function Pill({
  kind,
  label,
  accent,
  locked,
  dashed,
  dimmed,
  showLockIcon,
  ariaLabel,
  editor,
}: PillProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open || locked) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, locked]);

  // Locked pills get --surface-3 background, solid border (not dashed),
  // no dropdown arrow, default cursor, and clicking does nothing. The
  // tabIndex of -1 keeps keyboard nav from focusing the read-only
  // control.
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => {
          if (locked) return;
          setOpen((o) => !o);
        }}
        tabIndex={locked ? -1 : 0}
        aria-disabled={locked || undefined}
        aria-label={ariaLabel}
        className="inline-flex items-center gap-1 rounded-full border px-3 py-1 text-sm transition-colors"
        style={{
          // On the TRIGGER, not on the row. Opacity applies to a whole
          // subtree and a child cannot climb back out of it, so fading
          // the row faded the picker this pill opens: the popover went
          // see-through and the table underneath read straight through
          // its text.
          opacity: dimmed ? 0.55 : 1,
          borderColor: locked
            ? "var(--border)"
            : accent
              ? "color-mix(in oklab, var(--primary) 35%, transparent)"
              : "var(--border)",
          borderStyle: dashed ? "dashed" : "solid",
          background: locked
            ? "var(--surface-3)"
            : accent
              ? "var(--primary-soft)"
              : "var(--surface-3)",
          color: locked
            ? "var(--ink)"
            : accent
              ? "var(--primary-ink)"
              : "var(--ink)",
          fontFamily: kind === "value" ? "'JetBrains Mono', ui-monospace, monospace" : undefined,
          cursor: locked ? "default" : "pointer",
        }}
      >
        {showLockIcon && (
          <span aria-hidden="true" className="text-[10px] text-muted">
            🔒
          </span>
        )}
        <span>{label}</span>
        {!locked && <span className="text-muted">▾</span>}
      </button>
      {open && !locked && (
        <div
          className="absolute left-0 top-full z-20 mt-1 min-w-[260px] rounded-lg border p-3 shadow"
          style={{ background: "var(--surface-2)", borderColor: "var(--border)" }}
        >
          {editor({ close: () => setOpen(false) })}
        </div>
      )}
    </div>
  );
}
