// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Per-entity opt-in for a PUBLIC status badge (like a GitHub build badge).
// Renders a checkbox; when on, shows a live preview + copy-the-markdown for the
// public SVG at /api/v1/badges/<kind>/<id>. Used on the integration, system,
// and service pages — no global setting.

import { useState } from "react";
import { api } from "../api/client";

interface Props {
  kind: "integration" | "system" | "service";
  /** Integration/system id, or the service name. */
  id: string;
  enabled: boolean;
  /** Gates the checkbox — only writers/admins may flip it. */
  canManage: boolean;
  /** Called after a successful toggle so the parent can refresh. */
  onChange?: (enabled: boolean) => void;
}

export default function PublicBadgeControl({ kind, id, enabled, canManage, onChange }: Props) {
  const [on, setOn] = useState(enabled);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const badgeUrl = `${window.location.origin}/api/v1/badges/${kind}/${encodeURIComponent(id)}.svg`;
  const markdown = `![status](${badgeUrl})`;

  const toggle = async (next: boolean) => {
    setBusy(true);
    setError(null);
    try {
      await api.setBadgePublic(kind, id, next);
      setOn(next);
      onChange?.(next);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(markdown);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard blocked — the user can still select the text manually */
    }
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13 }}>
        <input
          type="checkbox"
          checked={on}
          disabled={!canManage || busy}
          onChange={(e) => toggle(e.target.checked)}
        />
        Enable public status badge
      </label>
      <span className="muted" style={{ fontSize: 12 }}>
        {on
          ? `Anyone with the URL can see this ${kind}'s health — no sign-in required.`
          : "Off. When on, a shields-style status badge is served publicly (embed it in a README, like a CI badge)."}
      </span>
      {error && (
        <span className="alert alert--error" style={{ fontSize: 12 }}>
          {error}
        </span>
      )}
      {on && (
        <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 2 }}>
          <img src={badgeUrl} alt={`${kind} status badge`} style={{ height: 20, alignSelf: "flex-start" }} />
          <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
            <code
              className="mono"
              style={{
                fontSize: 12,
                padding: "6px 8px",
                background: "var(--surface-2)",
                border: "1px solid var(--border)",
                borderRadius: 6,
                wordBreak: "break-all",
                flex: 1,
                minWidth: 220,
              }}
            >
              {markdown}
            </code>
            <button type="button" className="btn btn--sm" onClick={copy}>
              {copied ? "Copied ✓" : "Copy markdown"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
