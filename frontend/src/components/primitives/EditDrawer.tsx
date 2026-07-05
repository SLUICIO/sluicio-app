// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// EditDrawer is a right-side overlay drawer for "open a form without
// disrupting the page underneath" — used by the admin pages (Tags,
// Metadata fields, Schemas, Maps) to create / edit a row while the
// table on the left stays visible for context.
//
// Distinct from the existing `.drawer` class in styles.css, which is
// laid out inside the page grid (occupies space, the main column
// shrinks). EditDrawer renders to a portal at document.body level,
// floats on top of everything with a backdrop, and slides in from
// the right. The caller never has to reserve layout for it.
//
// Width comes in three sizes so a Tags-sized form and a Maps-sized
// form (with a 420px CodeMirror block inside) can share the same
// primitive without one feeling too cramped or the other too sparse.

import { useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import type { ReactNode } from "react";

interface EditDrawerProps {
  // Always rendered; toggle by mounting/unmounting the component.
  // Keeps the caller's state model simple ("editing != null" controls
  // mount), and means animations on open/close are predictable.
  title: ReactNode;
  // narrow ≈ 480px (Tags), medium ≈ 640px (Metadata fields, simple),
  // wide ≈ 880px (Schemas, Maps — needs room for CodeMirror).
  width?: "narrow" | "medium" | "wide";
  // Called when the user hits Esc, clicks the backdrop, or clicks the
  // header close button. If `dirty` is true, we confirm before
  // dismissing — see below.
  onClose: () => void;
  // When true, Esc / backdrop-click / close-button trigger a
  // window.confirm() so the user doesn't silently lose typed changes.
  // Save buttons inside the children bypass this (they call onClose
  // themselves AFTER a successful save).
  dirty?: boolean;
  children: ReactNode;
}

const WIDTHS: Record<NonNullable<EditDrawerProps["width"]>, number> = {
  narrow: 480,
  medium: 640,
  wide: 880,
};

export default function EditDrawer({
  title,
  width = "medium",
  onClose,
  dirty = false,
  children,
}: EditDrawerProps) {
  const panelRef = useRef<HTMLDivElement | null>(null);

  // Dismiss-with-guard helper. We use the latest `dirty` value via a
  // ref so the keydown handler captures changes without re-binding
  // the listener on every dirty-flag flip.
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const requestClose = () => {
    if (dirtyRef.current) {
      const ok = window.confirm("Discard unsaved changes?");
      if (!ok) return;
    }
    closeRef.current();
  };

  // Esc-to-close. Bound at the document level so it fires even if
  // focus is inside CodeMirror (which intercepts most key events but
  // lets Escape bubble).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        requestClose();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
    // requestClose closes over refs (always current) — safe to omit
  }, []);

  // Lock body scroll while the drawer is open. Restored on unmount.
  useEffect(() => {
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, []);

  // Auto-focus the first focusable element inside the panel on mount
  // so keyboard users land in the form, not on the document body.
  useEffect(() => {
    const el = panelRef.current;
    if (!el) return;
    const focusable = el.querySelector<HTMLElement>(
      'input, textarea, select, button, [tabindex]:not([tabindex="-1"])',
    );
    focusable?.focus();
  }, []);

  return createPortal(
    <div className="edit-drawer-root" role="dialog" aria-modal="true" aria-label={typeof title === "string" ? title : undefined}>
      <div
        className="edit-drawer-backdrop"
        onClick={requestClose}
      />
      <aside
        className={`edit-drawer-panel edit-drawer-panel--${width}`}
        ref={panelRef}
        style={{ width: WIDTHS[width] }}
      >
        <header className="edit-drawer-head">
          <h2 className="edit-drawer-title">{title}</h2>
          <button
            type="button"
            className="drawer__close"
            onClick={requestClose}
            aria-label="Close"
            title="Close (Esc)"
          >
            ×
          </button>
        </header>
        <div className="edit-drawer-body">{children}</div>
      </aside>
    </div>,
    document.body,
  );
}
