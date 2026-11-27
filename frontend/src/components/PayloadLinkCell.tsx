// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// One payload-link cell of a message list: the earliest span's link, and
// "+N" when other spans of the message carry other addresses (a transfer
// records the file as picked up and as delivered, a fan-out one file per
// branch). The count opens every link of the message with the span that
// carried it. Links open in a new tab with the reader's own access:
// Sluicio never fetches them.

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { PayloadLink, PayloadLinks, SpanPayloadLink } from "../api/types";
import { KVTable } from "./primitives";
import { formatDateTime } from "../lib/format";

/** The last path segment of an address or value: the file name, mostly. */
export function payloadLinkName(l: { url?: string; value: string }): string {
  const raw = l.url ?? l.value;
  let path = raw;
  try {
    if (l.url) path = decodeURIComponent(new URL(l.url).pathname + new URL(l.url).search);
  } catch {
    // Shown as recorded.
  }
  const trimmed = path.replace(/[/?=]+$/, "");
  const cut = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("="));
  return (cut >= 0 ? trimmed.slice(cut + 1) : trimmed) || raw;
}

export function roleLabel(role?: string): string {
  if (role === "input") return "received";
  if (role === "output") return "sent";
  return "";
}

/** A payload link, or its value as text when it is not an address. */
export function PayloadAnchor({ link, children, className }: { link: { url?: string; value: string }; children: React.ReactNode; className?: string }) {
  if (!link.url) {
    return (
      <span className={className} title={`${link.value} (not an address)`}>
        {children}
      </span>
    );
  }
  return (
    <a
      href={link.url}
      target="_blank"
      rel="noopener noreferrer"
      className={className}
      title={link.url}
      style={{ color: "var(--primary)" }}
      onClick={(e) => e.stopPropagation()}
    >
      {children}
    </a>
  );
}

export default function PayloadLinkCell({ links, loading }: { links?: PayloadLinks; loading: boolean }) {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const popoverRef = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      const t = e.target as Node;
      if (triggerRef.current?.contains(t) || popoverRef.current?.contains(t)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // Portaled and placed from the trigger, so the list's overflow does not
  // clip it; it opens upwards when there is no room below.
  useLayoutEffect(() => {
    if (!open) {
      setPos(null);
      return;
    }
    const place = () => {
      const r = triggerRef.current?.getBoundingClientRect();
      if (!r) return;
      const width = 420;
      const left = Math.max(8, Math.min(r.left, window.innerWidth - width - 8));
      const below = window.innerHeight - r.bottom;
      const height = popoverRef.current?.offsetHeight ?? 240;
      setPos({ top: below < height + 16 ? Math.max(8, r.top - height - 6) : r.bottom + 6, left });
    };
    place();
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [open]);

  if (loading) {
    return (
      <span className="truncate font-mono text-xs text-muted" title="Reading payload links">
        …
      </span>
    );
  }
  const list = links?.links ?? [];
  if (list.length === 0) {
    return (
      <span className="truncate font-mono text-xs text-muted" title="No payload link on this message">
        –
      </span>
    );
  }
  const first = list[0];
  const others = list.length - 1 + (links?.more ?? 0);
  return (
    <span className="flex min-w-0 items-center gap-1">
      <PayloadAnchor link={first} className="truncate font-mono text-xs hover:underline">
        {payloadLinkName(first)}
      </PayloadAnchor>
      {others > 0 && (
        <button
          ref={triggerRef}
          type="button"
          className="badge shrink-0"
          style={{ cursor: "pointer", fontSize: 11, padding: "0 6px" }}
          title={`${others} more link${others === 1 ? "" : "s"} in this message`}
          aria-expanded={open}
          onClick={(e) => {
            e.stopPropagation();
            setOpen((o) => !o);
          }}
        >
          +{others}
        </button>
      )}
      {open &&
        createPortal(
          <div
            ref={popoverRef}
            role="dialog"
            aria-label="Payload links of this message"
            onClick={(e) => e.stopPropagation()}
            style={{
              position: "fixed",
              top: pos?.top ?? -9999,
              left: pos?.left ?? -9999,
              zIndex: 1000,
              width: 420,
              maxHeight: 360,
              overflowY: "auto",
              background: "var(--surface-2)",
              border: "1px solid var(--border)",
              borderRadius: 8,
              boxShadow: "0 10px 24px rgba(0,0,0,0.14)",
              padding: 8,
            }}
          >
            <PayloadLinkList links={list} more={links?.more} />
          </div>,
          document.body,
        )}
    </span>
  );
}

/** Every link of a message, earliest first, with the spans that carried it. */
export function PayloadLinkList({ links, more }: { links: PayloadLink[]; more?: number }) {
  return (
    // minmax(0, 1fr): a grid item otherwise grows to its longest line and
    // the span lines never truncate.
    <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "grid", gridTemplateColumns: "minmax(0, 1fr)", gap: 6 }}>
      {links.map((l) => (
        <li key={l.url ?? `v:${l.value}`} style={{ padding: "4px 6px", borderRadius: 6 }} className="hover:bg-surface-3">
          <PayloadAnchor link={l} className="block truncate font-mono text-xs hover:underline">
            {payloadLinkName(l)}
          </PayloadAnchor>
          {l.spans.map((s) => (
            <div key={s.span_id} className="truncate text-xs text-muted" title={`${s.service} · ${s.span_name}`}>
              {/* The time before the names: the names are what may be cut. */}
              {roleLabel(s.role) && `${roleLabel(s.role)} · `}
              <span className="font-mono">{formatDateTime(s.time)}</span> · {s.service} · {s.span_name}
            </div>
          ))}
        </li>
      ))}
      {more ? <li className="px-1.5 text-xs text-muted">and {more} more</li> : null}
    </ul>
  );
}

/** A span's own payload links, as trace detail and the drawer show them. */
export function SpanPayloadLinks({ links }: { links?: SpanPayloadLink[] }) {
  if (!links || links.length === 0) return null;
  return (
    <div className="mb-3">
      <div className="kv__section">Payload</div>
      <KVTable
        rows={links.map((l) => ({
          rowKey: l.attribute,
          k: l.label,
          keyTitle: l.attribute,
          v: (
            <PayloadAnchor link={l} className="break-all hover:underline">
              {l.url ?? l.value}
              {l.url ? " ↗" : ""}
            </PayloadAnchor>
          ),
          copyValue: l.url ?? l.value,
        }))}
      />
    </div>
  );
}
