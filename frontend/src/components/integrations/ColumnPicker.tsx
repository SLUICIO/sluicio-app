// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ColumnPicker — a dropdown that controls which columns render in the
// integrations table AND in what order. The list is shown in the current
// column order; rows reorder by dragging the ⠿ handle or with the ▲/▼
// buttons (keyboard-friendly). The parent owns order + hidden set and
// persists them (per-user server preference, mirrored into ?cols=).

import { useEffect, useRef, useState } from "react";

export interface ColumnDef {
  id: string;
  label: string;
  // group is shown as a muted suffix so the flat, reorderable list keeps
  // the "operational vs metadata" context the old sectioned view had.
  group: string;
}

interface Props {
  // In effective render order.
  columns: ColumnDef[];
  hidden: Set<string>;
  onChange: (order: string[], hidden: Set<string>) => void;
  // Forget the stored layout and return to defaults.
  onReset: () => void;
}

export default function ColumnPicker({ columns, hidden, onChange, onReset }: Props) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const [dragIdx, setDragIdx] = useState<number | null>(null);
  const [overIdx, setOverIdx] = useState<number | null>(null);

  useEffect(() => {
    if (!open) return;
    const onPointer = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false);
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

  const order = columns.map((c) => c.id);

  const toggle = (id: string) => {
    const next = new Set(hidden);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    onChange(order, next);
  };

  const move = (from: number, to: number) => {
    if (to < 0 || to >= order.length || from === to) return;
    const next = [...order];
    const [id] = next.splice(from, 1);
    next.splice(to, 0, id);
    onChange(next, hidden);
  };

  const total = columns.length;
  const shown = columns.filter((c) => !hidden.has(c.id)).length;

  const allOn = () => onChange(order, new Set());
  const allOff = () => onChange(order, new Set(order));

  return (
    <div ref={wrapRef} style={{ position: "relative", display: "inline-block" }}>
      <button
        type="button"
        className="btn"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="true"
        aria-expanded={open}
      >
        Columns · {shown}/{total} ▾
      </button>
      {open && (
        <div
          role="menu"
          style={{
            position: "absolute",
            zIndex: 50,
            top: "calc(100% + 4px)",
            right: 0,
            minWidth: 300,
            maxHeight: 480,
            overflowY: "auto",
            background: "var(--surface-2)",
            border: "1px solid var(--border)",
            borderRadius: 6,
            boxShadow: "var(--shadow-pop, 0 8px 24px rgba(15, 23, 42, 0.18))",
            padding: 8,
          }}
        >
          <div className="flex items-center justify-between border-b pb-2 mb-1 text-xs"
            style={{ borderColor: "var(--border)" }}>
            <span className="text-muted">Show &amp; order columns</span>
            <span>
              <button type="button" className="text-muted hover:text-foreground" onClick={allOn}>
                all
              </button>
              <span className="text-muted mx-1">·</span>
              <button type="button" className="text-muted hover:text-foreground" onClick={allOff}>
                none
              </button>
              <span className="text-muted mx-1">·</span>
              <button type="button" className="text-muted hover:text-foreground" onClick={onReset}
                title="Forget this layout and return to defaults">
                reset
              </button>
            </span>
          </div>

          {columns.map((c, idx) => (
            <div
              key={c.id}
              draggable
              onDragStart={(e) => {
                setDragIdx(idx);
                e.dataTransfer.effectAllowed = "move";
              }}
              onDragOver={(e) => {
                e.preventDefault();
                if (overIdx !== idx) setOverIdx(idx);
              }}
              onDrop={(e) => {
                e.preventDefault();
                if (dragIdx !== null) move(dragIdx, idx);
                setDragIdx(null);
                setOverIdx(null);
              }}
              onDragEnd={() => {
                setDragIdx(null);
                setOverIdx(null);
              }}
              className="flex items-center gap-1.5 rounded px-1 py-1 text-sm hover:bg-surface-3"
              style={{
                opacity: dragIdx === idx ? 0.4 : 1,
                borderTop:
                  overIdx === idx && dragIdx !== null && dragIdx > idx
                    ? "2px solid var(--primary)"
                    : "2px solid transparent",
                borderBottom:
                  overIdx === idx && dragIdx !== null && dragIdx < idx
                    ? "2px solid var(--primary)"
                    : "2px solid transparent",
              }}
            >
              <span
                aria-hidden
                title="Drag to reorder"
                style={{ cursor: "grab", color: "var(--muted)", userSelect: "none" }}
              >
                ⠿
              </span>
              <label className="flex flex-1 cursor-pointer items-center gap-2 min-w-0">
                <input type="checkbox" checked={!hidden.has(c.id)} onChange={() => toggle(c.id)} />
                <span className="truncate">{c.label}</span>
                <span className="text-[10px] uppercase tracking-wide text-muted">{c.group}</span>
              </label>
              <button
                type="button"
                className="text-muted hover:text-foreground disabled:opacity-30"
                aria-label={`Move ${c.label} up`}
                disabled={idx === 0}
                onClick={() => move(idx, idx - 1)}
              >
                ▲
              </button>
              <button
                type="button"
                className="text-muted hover:text-foreground disabled:opacity-30"
                aria-label={`Move ${c.label} down`}
                disabled={idx === columns.length - 1}
                onClick={() => move(idx, idx + 1)}
              >
                ▼
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
