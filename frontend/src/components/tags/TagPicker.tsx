// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// TagPicker is the controlled chip-with-popover used on the
// integration create / edit screens. It owns nothing about the
// underlying selection — callers pass `selectedIds` and `onChange`,
// plus the full org vocabulary in `available`.
//
// Inline tag creation is supported: when the typed query doesn't
// match an existing tag, a "Create new tag" affordance opens a small
// form. The parent decides what to do on create (typically: POST to
// /api/v1/tags, then call onChange with the new id appended).

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { CreateTagRequest, Tag } from "../../api/types";
import TagChip from "./TagChip";

interface TagPickerProps {
  available: Tag[];
  selectedIds: string[];
  onChange: (next: string[]) => void;
  // Called when the user asks to create a tag that doesn't exist
  // yet. The parent owns the POST and should resolve with the newly
  // created tag (the picker will both append it to the selection and
  // expect the caller to refresh `available`).
  onCreate?: (req: CreateTagRequest) => Promise<Tag>;
  // Read-only mode hides the "+" affordance and remove buttons.
  readOnly?: boolean;
  placeholder?: string;
}

// A small palette of pleasant, theme-agnostic colors used as the
// default rotation when the user creates a new tag without picking
// one. Kept short on purpose so chips stay visually distinct.
const DEFAULT_PALETTE = [
  "#3b82f6", // blue
  "#10b981", // green
  "#f59e0b", // amber
  "#ef4444", // red
  "#8b5cf6", // violet
  "#06b6d4", // cyan
  "#ec4899", // pink
  "#64748b", // slate
];

export default function TagPicker({
  available,
  selectedIds,
  onChange,
  onCreate,
  readOnly = false,
  placeholder = "Add a tag…",
}: TagPickerProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // newColor controls the inline-create form. Defaults rotate through
  // the palette so successive new tags don't collide.
  const [newColor, setNewColor] = useState<string>(DEFAULT_PALETTE[0]);
  const rootRef = useRef<HTMLDivElement>(null);
  const popoverRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  // Popover is rendered via a React portal to escape any
  // `overflow: hidden` ancestor (e.g. .svc-form-card on the service
  // edit page). We position it manually from the trigger row's
  // bounding rect and keep it in sync on scroll / resize.
  const [popoverPos, setPopoverPos] = useState<{ top: number; left: number } | null>(null);

  const updatePopoverPos = () => {
    const el = rootRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setPopoverPos({ top: r.bottom + 6, left: r.left });
  };

  // Close on outside click. Because the popover lives in a portal it
  // isn't a DOM descendant of rootRef — check both refs.
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      const t = e.target as Node;
      if (rootRef.current?.contains(t)) return;
      if (popoverRef.current?.contains(t)) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  // Position the popover synchronously before paint so it doesn't
  // flicker at (0,0) on the first frame after open.
  useLayoutEffect(() => {
    if (!open) {
      setPopoverPos(null);
      return;
    }
    updatePopoverPos();
    const onScrollOrResize = () => updatePopoverPos();
    window.addEventListener("scroll", onScrollOrResize, true);
    window.addEventListener("resize", onScrollOrResize);
    return () => {
      window.removeEventListener("scroll", onScrollOrResize, true);
      window.removeEventListener("resize", onScrollOrResize);
    };
  }, [open]);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  const selected = useMemo(
    () => available.filter((t) => selectedIds.includes(t.id)),
    [available, selectedIds],
  );

  // Tags not yet selected, ranked by query match. An empty query
  // returns everything (alphabetical, which `available` already is).
  const candidates = useMemo(() => {
    const q = query.trim().toLowerCase();
    const pool = available.filter((t) => !selectedIds.includes(t.id));
    if (!q) return pool;
    return pool.filter(
      (t) =>
        t.name.toLowerCase().includes(q) || t.slug.toLowerCase().includes(q),
    );
  }, [available, selectedIds, query]);

  const exactMatch = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q && available.some((t) => t.slug === q || t.name.toLowerCase() === q);
  }, [available, query]);

  const canCreate = !!onCreate && !readOnly && query.trim().length > 0 && !exactMatch;

  const handleCreate = async () => {
    if (!onCreate) return;
    const name = query.trim();
    if (!name) return;
    setBusy(true);
    setError(null);
    try {
      const created = await onCreate({
        name,
        slug: slugify(name),
        color: newColor,
      });
      onChange([...selectedIds, created.id]);
      setQuery("");
      // Rotate the default color so back-to-back creates get
      // different palette entries.
      const idx = (DEFAULT_PALETTE.indexOf(newColor) + 1) % DEFAULT_PALETTE.length;
      setNewColor(DEFAULT_PALETTE[idx]);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const toggle = (id: string) => {
    if (selectedIds.includes(id)) {
      onChange(selectedIds.filter((x) => x !== id));
    } else {
      onChange([...selectedIds, id]);
    }
  };

  return (
    <div ref={rootRef} style={{ display: "inline-block" }}>
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: 6,
          alignItems: "center",
          minHeight: 28,
        }}
      >
        {selected.map((t) => (
          <TagChip
            key={t.id}
            tag={t}
            onRemove={
              readOnly
                ? undefined
                : () => onChange(selectedIds.filter((x) => x !== t.id))
            }
          />
        ))}
        {!readOnly && (
          <button
            type="button"
            className="btn"
            style={{ padding: "2px 10px", fontSize: 12 }}
            onClick={() => setOpen((v) => !v)}
          >
            {selected.length === 0 ? placeholder : "+ tag"}
          </button>
        )}
      </div>

      {open && !readOnly && popoverPos && createPortal(
        <div
          ref={popoverRef}
          role="dialog"
          style={{
            position: "fixed",
            top: popoverPos.top,
            left: popoverPos.left,
            zIndex: 1000,
            width: 280,
            background: "var(--surface-2)",
            border: "1px solid var(--border)",
            borderRadius: 8,
            boxShadow: "0 10px 24px rgba(0,0,0,0.14)",
            padding: 8,
          }}
        >
          <input
            ref={inputRef}
            className="search__input"
            type="search"
            placeholder="Search or create…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                if (candidates.length > 0) {
                  toggle(candidates[0].id);
                  setQuery("");
                } else if (canCreate) {
                  void handleCreate();
                }
              } else if (e.key === "Escape") {
                setOpen(false);
              }
            }}
            style={{ width: "100%" }}
          />

          {error && (
            <div className="alert alert--error" style={{ margin: "8px 0" }}>
              {error}
            </div>
          )}

          <div
            style={{
              maxHeight: 200,
              overflowY: "auto",
              marginTop: 6,
              display: "flex",
              flexDirection: "column",
              gap: 4,
            }}
          >
            {candidates.length === 0 && !canCreate && (
              <div className="muted" style={{ fontSize: 12, padding: "6px 4px" }}>
                {available.length === 0
                  ? "No tags yet. Type a name to create the first one."
                  : "All tags are already attached."}
              </div>
            )}
            {candidates.map((t) => (
              <button
                key={t.id}
                type="button"
                onClick={() => {
                  toggle(t.id);
                  setQuery("");
                  inputRef.current?.focus();
                }}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  padding: "6px 4px",
                  background: "transparent",
                  border: 0,
                  borderRadius: 4,
                  cursor: "pointer",
                  textAlign: "left",
                  color: "var(--ink)",
                }}
              >
                <span
                  style={{
                    width: 10,
                    height: 10,
                    borderRadius: 999,
                    background: t.color,
                    flex: "0 0 auto",
                  }}
                />
                <span style={{ fontSize: 13 }}>{t.name}</span>
                <span
                  className="muted mono"
                  style={{ fontSize: 11, marginLeft: "auto" }}
                >
                  {t.slug}
                </span>
              </button>
            ))}
          </div>

          {canCreate && (
            <div
              style={{
                borderTop: "1px solid var(--border)",
                marginTop: 6,
                paddingTop: 8,
                display: "flex",
                flexDirection: "column",
                gap: 8,
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span style={{ fontSize: 12 }} className="muted">
                  color
                </span>
                <div style={{ display: "flex", gap: 4, flexWrap: "wrap" }}>
                  {DEFAULT_PALETTE.map((c) => (
                    <button
                      key={c}
                      type="button"
                      aria-label={`Use color ${c}`}
                      onClick={() => setNewColor(c)}
                      style={{
                        width: 16,
                        height: 16,
                        borderRadius: 999,
                        background: c,
                        border:
                          c === newColor
                            ? "2px solid var(--ink)"
                            : "1px solid rgba(0,0,0,0.15)",
                        cursor: "pointer",
                        padding: 0,
                      }}
                    />
                  ))}
                </div>
              </div>
              {/* Full-width row so a long tag name can't push the button
                  past the popover edge; the label truncates if needed. */}
              <button
                type="button"
                className="btn btn--primary"
                style={{
                  padding: "4px 10px",
                  fontSize: 12,
                  width: "100%",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
                disabled={busy}
                onClick={handleCreate}
              >
                {busy ? "Creating…" : `Create "${query.trim()}"`}
              </button>
            </div>
          )}
        </div>,
        document.body,
      )}
    </div>
  );
}

// slugify is a forgiving lowercase-kebab transform that matches the
// server's slug rules: a-z, 0-9, single hyphens, no leading/trailing
// hyphen, max 64 chars. If the result is empty (e.g. all-emoji name),
// we fall back to "tag" so the request still validates server-side
// and the user can rename via the management screen later.
function slugify(input: string): string {
  const s = input
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64);
  return s || "tag";
}
