// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The cell's mark, for partners running Sluicio under their own brand
// (issue #29).
//
// Lives on the operator page rather than in organization settings,
// because a partner's brand is a property of the deployment: every org
// on the cell is theirs, and setting it per org could only ever produce
// an inconsistency.

import { useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import type { Branding } from "../api/types";
import { refreshBranding } from "../lib/useBranding";

const MAX_ASSET_KB = 256;

/** Read a picked file as a data URI, which is how assets are stored. */
function readAsDataURI(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const fr = new FileReader();
    fr.onload = () => resolve(String(fr.result));
    fr.onerror = () => reject(new Error("could not read the file"));
    fr.readAsDataURL(file);
  });
}

function AssetField({
  label,
  hint,
  value,
  disabled,
  onChange,
}: {
  label: string;
  hint: string;
  value: string;
  disabled: boolean;
  onChange: (v: string) => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [err, setErr] = useState<string | null>(null);
  return (
    <div className="svc-field" style={{ minWidth: 0 }}>
      <span className="svc-field-label">
        {label}
        <span className="hint">{hint}</span>
      </span>
      <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
        {/* The current mark, on the surface it will actually sit on, so
            a logo that vanishes against the chrome is obvious here
            rather than after saving. */}
        {value ? (
          <img
            src={value}
            alt=""
            style={{
              height: 28,
              width: "auto",
              maxWidth: 120,
              padding: 4,
              borderRadius: 6,
              background: "var(--surface-2)",
              border: "1px solid var(--border)",
            }}
          />
        ) : (
          <span className="muted" style={{ fontSize: 12 }}>
            Not set — the Sluicio mark is used.
          </span>
        )}
        <input
          ref={input}
          type="file"
          accept="image/svg+xml,image/png,image/jpeg,image/webp,image/x-icon"
          style={{ display: "none" }}
          onChange={async (e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            setErr(null);
            if (file.size > MAX_ASSET_KB * 1024) {
              setErr(`That file is ${Math.round(file.size / 1024)} KB; the limit is ${MAX_ASSET_KB} KB.`);
              return;
            }
            try {
              onChange(await readAsDataURI(file));
            } catch (e2) {
              setErr(String((e2 as Error).message));
            }
            if (input.current) input.current.value = "";
          }}
        />
        <button className="btn btn--sm" disabled={disabled} onClick={() => input.current?.click()}>
          {value ? "Replace" : "Upload"}
        </button>
        {value && (
          <button className="btn btn--link btn--sm" disabled={disabled} onClick={() => onChange("")}>
            Clear
          </button>
        )}
      </div>
      {err && (
        <span className="muted" style={{ fontSize: 12, color: "var(--err-ink)" }}>
          {err}
        </span>
      )}
    </div>
  );
}

export default function BrandingCard() {
  const [b, setB] = useState<Branding | null>(null);
  const [draft, setDraft] = useState<Branding | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .getBranding()
      .then((r) => {
        setB(r);
        setDraft(r);
      })
      .catch((e) => setErr(String((e as Error).message ?? e)));
  }, []);

  if (!draft || !b) {
    return null;
  }

  const dirty = JSON.stringify(draft) !== JSON.stringify(b);

  const save = async () => {
    setSaving(true);
    setErr(null);
    setSaved(false);
    try {
      const next = await api.setBranding({
        logo: draft.logo,
        logo_dark: draft.logo_dark,
        wordmark: draft.wordmark,
        favicon: draft.favicon,
      });
      setB(next);
      setDraft(next);
      setSaved(true);
      // Repaint the shell without a reload, so the operator sees the
      // result of what they just did.
      refreshBranding();
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="card" style={{ marginBottom: 16, padding: 16 }}>
      <h2 style={{ fontSize: 16, fontWeight: 600, margin: "0 0 4px" }}>Branding</h2>
      <p className="muted" style={{ fontSize: 12, marginTop: 0, marginBottom: 12 }}>
        Replace the Sluicio mark with your own. Applies to every organization on this
        cell, which is why it is set here rather than per organization.
      </p>

      {/* The gate, stated rather than implied by an inert form. An
          operator who cannot see why nothing saves is left guessing at
          a licensing question. */}
      {!b.entitled && (
        <div className="alert alert--warn" style={{ marginBottom: 12, fontSize: 13 }}>
          White-labelling needs an Enterprise license carrying the{" "}
          <span className="mono">white_label</span> entitlement. Anything set here is kept
          but not applied until the license carries it, so renewing restores your brand
          without uploading it again.
        </div>
      )}

      {err && (
        <div className="alert alert--error" style={{ marginBottom: 12 }}>
          {err}
        </div>
      )}

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 320px), 1fr))",
          gap: "14px 18px",
        }}
      >
        <AssetField
          label="Mark"
          hint="SVG or PNG, up to 256 KB"
          value={draft.logo ?? ""}
          disabled={!b.entitled || saving}
          onChange={(v) => setDraft({ ...draft, logo: v })}
        />
        <AssetField
          label="Mark for dark chrome"
          hint="optional — falls back to the mark"
          value={draft.logo_dark ?? ""}
          disabled={!b.entitled || saving}
          onChange={(v) => setDraft({ ...draft, logo_dark: v })}
        />
        <div className="svc-field" style={{ minWidth: 0 }}>
          <label className="svc-field-label" htmlFor="brand-wordmark">
            Wordmark
            <span className="hint">shown beside the mark and in the tab title</span>
          </label>
          <input
            id="brand-wordmark"
            className="svc-input"
            placeholder="Sluicio"
            maxLength={40}
            value={draft.wordmark ?? ""}
            disabled={!b.entitled || saving}
            onChange={(e) => setDraft({ ...draft, wordmark: e.target.value })}
          />
        </div>
        <AssetField
          label="Favicon"
          hint="the browser tab icon"
          value={draft.favicon ?? ""}
          disabled={!b.entitled || saving}
          onChange={(v) => setDraft({ ...draft, favicon: v })}
        />
      </div>

      <div style={{ marginTop: 16, display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
        <button className="btn btn--primary" onClick={save} disabled={!b.entitled || !dirty || saving}>
          {saving ? "Saving…" : "Save"}
        </button>
        {dirty && (
          <button className="btn btn--link" onClick={() => setDraft(b)} disabled={saving}>
            Reset
          </button>
        )}
        {saved && (
          <span className="muted" style={{ fontSize: 12.5 }}>
            Saved. Everyone on this cell sees it on their next page load.
          </span>
        )}
      </div>
    </div>
  );
}
