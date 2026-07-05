// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shared UI for Enterprise-gated features: a small badge and an upgrade
// notice shown where a feature is locked behind a license. Kept generic so
// every gated surface (SSO, audit, retention, advanced RBAC) reads the same.

import type { ReactNode } from "react";

// A compact "Enterprise" pill to tag a tab, section header, or control.
export function EnterpriseBadge({ title }: { title?: string }) {
  return (
    <span
      title={title ?? "Sluicio Enterprise feature"}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        fontSize: 10.5,
        fontWeight: 700,
        letterSpacing: "0.04em",
        textTransform: "uppercase",
        padding: "2px 7px",
        borderRadius: 999,
        color: "var(--accent, #4c9aff)",
        border: "1px solid color-mix(in oklab, var(--accent, #4c9aff) 45%, transparent)",
        background: "color-mix(in oklab, var(--accent, #4c9aff) 12%, transparent)",
        whiteSpace: "nowrap",
      }}
    >
      ★ Enterprise
    </span>
  );
}

// A full-width notice shown in place of (or above) a locked feature. When the
// license has expired vs. never been present, the copy adapts.
export function UpgradeNotice({
  title,
  expired,
  children,
}: {
  title: string;
  expired?: boolean;
  children?: ReactNode;
}) {
  return (
    <div
      className="card"
      style={{
        padding: 20,
        borderColor: "color-mix(in oklab, var(--accent, #4c9aff) 35%, var(--border))",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 8 }}>
        <EnterpriseBadge />
        <strong style={{ fontSize: 15 }}>{title}</strong>
      </div>
      <p className="muted" style={{ margin: "0 0 12px", fontSize: 13.5, lineHeight: 1.6 }}>
        {expired
          ? "Your Sluicio Enterprise license has expired. Renew it to restore this feature."
          : "This is a Sluicio Enterprise feature. It's available with a valid license key — even when self-hosted."}
      </p>
      {children}
      <p className="muted" style={{ margin: "12px 0 0", fontSize: 12.5 }}>
        Add or update your license key under <strong>Settings → License</strong>.
      </p>
    </div>
  );
}
