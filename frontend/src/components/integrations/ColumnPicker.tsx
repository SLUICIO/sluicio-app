// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ColumnPicker — a dropdown checkbox list that toggles which columns
// are rendered in the integrations table. The parent owns the visible
// set and persists it (we use URL params one level up).

import { useEffect, useRef, useState } from "react";

export interface ColumnDef {
  id: string;
  label: string;
  // group lets us section the list in the popover (e.g. "Operational"
  // vs "Metadata") so it stays scannable as more user-defined fields
  // get added.
  group: string;
}

interface Props {
  columns: ColumnDef[];
  visible: Set<string>;
  onChange: (next: Set<string>) => void;
}

export default function ColumnPicker({ columns, visible, onChange }: Props) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement | null>(null);

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

  const toggle = (id: string) => {
    const next = new Set(visible);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    onChange(next);
  };

  // Group columns into sections for display, preserving the
  // declaration order within each group.
  const groups = new Map<string, ColumnDef[]>();
  for (const c of columns) {
    const list = groups.get(c.group) ?? [];
    list.push(c);
    groups.set(c.group, list);
  }

  const total = columns.length;
  const shown = columns.filter((c) => visible.has(c.id)).length;

  const allOn = () => onChange(new Set(columns.map((c) => c.id)));
  const allOff = () => onChange(new Set());

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
            minWidth: 260,
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
            <span className="text-muted">Show columns</span>
            <span>
              <button type="button" className="text-muted hover:text-foreground" onClick={allOn}>
                all
              </button>
              <span className="text-muted mx-1">·</span>
              <button type="button" className="text-muted hover:text-foreground" onClick={allOff}>
                none
              </button>
            </span>
          </div>

          {[...groups.entries()].map(([group, cols]) => (
            <div key={group} className="mt-2 first:mt-1">
              <div className="px-1 py-0.5 text-[10px] uppercase tracking-wide text-muted">
                {group}
              </div>
              {cols.map((c) => (
                <label
                  key={c.id}
                  className="flex cursor-pointer items-center gap-2 rounded px-1.5 py-1 text-sm hover:bg-surface-3"
                >
                  <input
                    type="checkbox"
                    checked={visible.has(c.id)}
                    onChange={() => toggle(c.id)}
                  />
                  <span>{c.label}</span>
                </label>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
