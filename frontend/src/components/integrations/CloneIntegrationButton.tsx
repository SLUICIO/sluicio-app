// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Clone, offered on an integration's Overview and on Edit integration:
// the button and the dialog that asks for the new name. After cloning the
// new integration opens on its Edit integration page.

import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../../api/client";
import { slugify } from "../../lib/slugify";

export default function CloneIntegrationButton({ integrationId, name }: { integrationId: string; name: string }) {
  const navigate = useNavigate();
  // The dialog owns its own state so the page behind it does not re-render
  // on every keystroke.
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" className="btn" onClick={() => setOpen(true)}>
        Clone
      </button>
      {open && (
        <CloneIntegrationDialog
          sourceId={integrationId}
          sourceName={name}
          onCancel={() => setOpen(false)}
          onCloned={(newId) => {
            setOpen(false);
            navigate(`/integrations/${encodeURIComponent(newId)}/settings`);
          }}
        />
      )}
    </>
  );
}

// CloneIntegrationDialog asks for the one thing a clone cannot inherit —
// its name — and derives a slug from it, exactly as the new-integration
// form does.
//
// What it does NOT ask is what to copy. That is decided server-side from
// the caller's authority (an editor's clone cannot carry team grants),
// and offering it as a checkbox would imply a choice the caller may not
// have. The result banner reports what actually happened instead.
function CloneIntegrationDialog({
  sourceId,
  sourceName,
  onCancel,
  onCloned,
}: {
  sourceId: string;
  sourceName: string;
  onCancel: () => void;
  onCloned: (newId: string) => void;
}) {
  const [name, setName] = useState(`${sourceName} (copy)`);
  const [slug, setSlug] = useState(slugify(`${sourceName} copy`));
  const [slugTouched, setSlugTouched] = useState(false);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const onName = (v: string) => {
    setName(v);
    // The slug follows the name until the user takes it over, matching
    // the new-integration form.
    if (!slugTouched) setSlug(slugify(v));
  };

  const submit = async () => {
    setSaving(true);
    setErr(null);
    try {
      const res = await api.cloneIntegration(sourceId, {
        name: name.trim(),
        slug: slug.trim(),
      });
      onCloned(res.integration.id);
    } catch (e) {
      setErr(String((e as Error).message ?? e));
      setSaving(false);
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Clone integration"
      onKeyDown={(e) => {
        if (e.key === "Escape") onCancel();
      }}
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 2100,
        background: "rgba(15,23,42,0.45)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 16,
      }}
    >
      <div
        className="card"
        style={{ width: 460, maxWidth: "100%" }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="card__header">Clone “{sourceName}”</div>
        <div
          style={{
            padding: 16,
            display: "flex",
            flexDirection: "column",
            gap: 12,
          }}
        >
          <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
            Copies the matchers, tags, metadata, message columns and filters,
            notification profile, supporting telemetry and health checks. The new
            integration starts with its public status badge off, and team access
            is copied only if you are an admin.
          </p>
          <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span className="m-field-label">Name</span>
            <input
              className="input"
              autoFocus
              value={name}
              onChange={(e) => onName(e.target.value)}
            />
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span className="m-field-label">Slug</span>
            <input
              className="input"
              value={slug}
              onChange={(e) => {
                setSlugTouched(true);
                setSlug(e.target.value);
              }}
            />
          </label>
          {err && (
            <div className="alert alert--error" style={{ margin: 0 }}>
              {err}
            </div>
          )}
          <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <button
              type="button"
              className="btn"
              onClick={onCancel}
              disabled={saving}
            >
              Cancel
            </button>
            <button
              type="button"
              className="btn primary"
              onClick={submit}
              disabled={saving || !name.trim() || !slug.trim()}
            >
              {saving ? "Cloning…" : "Clone integration"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
