// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The top-bar "since last visit" digest: a bell with a count badge that opens
// a panel of services registered + alerts fired since you last marked it seen.
// Everything is RBAC-filtered server-side. "Mark all as seen" moves the
// watermark so next time only newer items show.

import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { DigestResponse } from "../api/types";
import { formatRelative } from "../lib/format";

export default function DigestBell() {
  const [open, setOpen] = useState(false);
  const [digest, setDigest] = useState<DigestResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  const load = () => {
    setLoading(true);
    api
      .getDigest()
      .then(setDigest)
      .catch(() => setDigest(null))
      .finally(() => setLoading(false));
  };
  useEffect(() => load(), []);

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

  const count = (digest?.counts.new_services ?? 0) + (digest?.counts.failures ?? 0) + (digest?.counts.shared ?? 0);

  const markSeen = async () => {
    try {
      await api.markDigestSeen();
      load(); // watermark moved to now → badge clears
    } catch {
      /* non-fatal */
    }
  };

  const failureTo = (f: DigestResponse["failures"][number]) =>
    f.service_name
      ? `/services/${encodeURIComponent(f.service_name)}`
      : f.integration_id
        ? `/integrations/${f.integration_id}`
        : "/stuck";

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="What's new since your last visit"
        title="Since your last visit"
        onClick={() => setOpen((v) => !v)}
        className="relative grid h-7 w-7 place-items-center rounded-md focus:outline-none focus-visible:ring-2"
        style={{ color: "var(--ink-2)" }}
      >
        <svg viewBox="0 0 16 16" width={16} height={16} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" aria-hidden>
          <path d="M8 2a3.5 3.5 0 00-3.5 3.5c0 3-1.5 4-1.5 4h10s-1.5-1-1.5-4A3.5 3.5 0 008 2z" />
          <path d="M6.5 13a1.5 1.5 0 003 0" />
        </svg>
        {count > 0 && (
          <span
            aria-hidden
            style={{
              position: "absolute",
              top: -3,
              right: -3,
              minWidth: 15,
              height: 15,
              padding: "0 3px",
              borderRadius: 999,
              background: "var(--primary)",
              color: "var(--on-primary)",
              fontSize: 9.5,
              fontWeight: 700,
              lineHeight: "15px",
              textAlign: "center",
            }}
          >
            {count > 99 ? "99+" : count}
          </span>
        )}
      </button>

      {open && (
        <div
          role="menu"
          className="absolute right-0 top-9 z-50 overflow-hidden rounded-lg border shadow-lg"
          style={{
            width: 360,
            maxHeight: "70vh",
            display: "flex",
            flexDirection: "column",
            background: "var(--surface-2)",
            borderColor: "var(--border)",
            boxShadow: "var(--shadow-pop, 0 8px 24px rgba(15, 23, 42, 0.18))",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "10px 12px", borderBottom: "1px solid var(--border)" }}>
            <strong style={{ fontSize: 13 }}>Since your last visit</strong>
            {count > 0 && (
              <button type="button" className="btn btn--sm" onClick={markSeen}>Mark all as seen</button>
            )}
          </div>

          <div style={{ overflow: "auto", minHeight: 0 }}>
            {loading && !digest ? (
              <div className="placeholder" style={{ margin: 10 }}>Loading…</div>
            ) : count === 0 ? (
              <div className="placeholder" style={{ margin: 10 }}>You're all caught up.</div>
            ) : (
              <>
                {(digest?.new_services.length ?? 0) > 0 && (
                  <div>
                    <div className="muted" style={{ fontSize: 11, textTransform: "uppercase", letterSpacing: "0.04em", padding: "8px 12px 4px" }}>
                      New services · {digest!.new_services.length}
                    </div>
                    {digest!.new_services.map((s) => (
                      <Link
                        key={s.service_name}
                        to={`/services/${encodeURIComponent(s.service_name)}`}
                        onClick={() => setOpen(false)}
                        className="block"
                        style={{ padding: "6px 12px", borderTop: "1px solid var(--border)" }}
                      >
                        <div style={{ fontSize: 13, fontWeight: 600, color: "var(--ink)" }}>{s.service_name}</div>
                        <div style={{ fontSize: 11.5 }}>
                          <span className="muted">registered {formatRelative(s.first_seen_at)}</span>
                          {s.suggested_label && (
                            <span className="badge-brand" style={{ marginLeft: 6 }}>
                              ⚙ looks like {s.suggested_label} — set up monitoring
                            </span>
                          )}
                        </div>
                      </Link>
                    ))}
                  </div>
                )}

                {(digest?.failures.length ?? 0) > 0 && (
                  <div>
                    <div className="muted" style={{ fontSize: 11, textTransform: "uppercase", letterSpacing: "0.04em", padding: "8px 12px 4px" }}>
                      Failures since · {digest!.failures.length}
                    </div>
                    {digest!.failures.map((f, i) => (
                      <Link
                        key={i}
                        to={failureTo(f)}
                        onClick={() => setOpen(false)}
                        className="block"
                        style={{ padding: "6px 12px", borderTop: "1px solid var(--border)" }}
                      >
                        <div style={{ fontSize: 13, fontWeight: 600, color: "var(--ink)", display: "flex", alignItems: "center", gap: 8 }}>
                          <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{f.rule_name}</span>
                          <span className={`m-rule-badge sev-${f.severity}`} style={{ fontWeight: 500, flex: "none" }}>{f.severity}</span>
                        </div>
                        <div className="muted" style={{ fontSize: 11.5 }}>
                          {f.service_name || f.integration_name || "org-wide"} · {f.state} · {formatRelative(f.started_at)}
                        </div>
                      </Link>
                    ))}
                  </div>
                )}

                {(digest?.shared?.length ?? 0) > 0 && (
                  <div>
                    <div className="muted" style={{ fontSize: 11, textTransform: "uppercase", letterSpacing: "0.04em", padding: "8px 12px 4px" }}>
                      Shared with you · {digest!.shared!.length}
                    </div>
                    {digest!.shared!.map((sh, i) => (
                      <Link
                        key={i}
                        to={sh.resource_kind === "integration" ? `/integrations/${sh.resource_id}` : `/systems/${sh.resource_id}`}
                        onClick={() => setOpen(false)}
                        className="block"
                        style={{ padding: "6px 12px", borderTop: "1px solid var(--border)" }}
                      >
                        <div style={{ fontSize: 13, fontWeight: 600, color: "var(--ink)" }}>
                          {sh.resource_name || sh.resource_kind}
                        </div>
                        <div className="muted" style={{ fontSize: 11.5 }}>
                          {sh.resource_kind}{sh.shared_by ? ` · shared by ${sh.shared_by}` : ""} · {formatRelative(sh.shared_at)}
                        </div>
                      </Link>
                    ))}
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
