// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Tag management. One row per tag with a live chip preview, click-
// to-edit name + color, usage counts, and a delete affordance that
// names what cascades. Inline creation from the integration pages is
// the picker; this page is for the deliberate work: renaming,
// recoloring, retiring.

import { FormEvent, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type {
  CreateTagRequest,
  Tag,
  TagWithUsage,
  UpdateTagRequest,
} from "../api/types";
import ColorInput, { DEFAULT_TAG_PALETTE } from "../components/tags/ColorInput";
import TagChip from "../components/tags/TagChip";
import { EditDrawer } from "../components/primitives";
import { useCurrentUser } from "../lib/useCurrentUser";
import { usePageTitle } from "../lib/usePageTitle";

// Re-export the palette under the local name so existing references
// keep working. The hex input on ColorInput means any value is
// reachable; the palette is just the quick-pick row.
const PALETTE = DEFAULT_TAG_PALETTE;

export default function Tags() {
  usePageTitle("Tags");
  const { can } = useCurrentUser();
  // Tag editing piggybacks on the same permission as integration
  // writes — both are "shape the vocabulary" actions, and we don't
  // yet have a finer-grained tag permission.
  const canWrite = can("integration.write");
  const canDelete = can("integration.delete");

  const [items, setItems] = useState<TagWithUsage[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);

  const refresh = () => {
    setLoading(true);
    setError(null);
    api
      .listTagsWithUsage()
      .then((d) => setItems(d.tags ?? []))
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  };

  useEffect(refresh, []);

  // The summary line ("12 tags, 4 unused") is most useful at a glance.
  // Unused means zero attachments anywhere — a candidate for cleanup.
  const summary = useMemo(() => {
    if (!items) return null;
    const total = items.length;
    const unused = items.filter(
      (t) => t.integration_count === 0 && t.service_count === 0,
    ).length;
    return { total, unused };
  }, [items]);

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Tags</h1>
          <p className="page__subtitle">
            The org's tag vocabulary. Attach tags from an{" "}
            <Link to="/integrations">integration's detail page</Link> or via the
            picker on the integration form. Renaming a tag updates it
            everywhere it's used; deleting one untags everything it touches.
          </p>
        </div>
        <div className="toolbar">
          {summary && (
            <span className="muted" style={{ fontSize: 12 }}>
              {summary.total} {summary.total === 1 ? "tag" : "tags"}
              {summary.unused > 0 && (
                <> · {summary.unused} unused</>
              )}
            </span>
          )}
          {canWrite ? (
            <button
              type="button"
              className="btn btn--primary"
              onClick={() => setShowCreate((v) => !v)}
            >
              {showCreate ? "Cancel" : "New tag"}
            </button>
          ) : (
            <button
              type="button"
              className="btn btn--primary"
              disabled
              title="Your role doesn't allow editing tags"
            >
              New tag
            </button>
          )}
        </div>
      </div>

      {error && <div className="alert alert--error">Failed to load: {error}</div>}
      {loading && !items && <div className="placeholder">Loading…</div>}

      {showCreate && canWrite && (
        <EditDrawer
          title="New tag"
          width="narrow"
          onClose={() => setShowCreate(false)}
        >
          <CreateTagPanel
            onCancel={() => setShowCreate(false)}
            onCreated={() => {
              setShowCreate(false);
              refresh();
            }}
          />
        </EditDrawer>
      )}

      {items && items.length === 0 && !loading && (
        <div className="placeholder">
          No tags yet. Click <strong>New tag</strong> to define one — for
          example, <code>hr</code> to group everything owned by HR.
        </div>
      )}

      {items && items.length > 0 && (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 220 }}>Tag</th>
                <th>Slug</th>
                <th>Color</th>
                <th className="num">Integrations</th>
                <th className="num">Services</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {items.map((t) => (
                <TagRow
                  key={t.id}
                  tag={t}
                  canWrite={canWrite}
                  canDelete={canDelete}
                  onChanged={refresh}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ── Row ───────────────────────────────────────────────────────────

interface TagRowProps {
  tag: TagWithUsage;
  canWrite: boolean;
  canDelete: boolean;
  onChanged: () => void;
}

function TagRow({ tag, canWrite, canDelete, onChanged }: TagRowProps) {
  // editingName lets the user click the chip text and replace it
  // with an input, mirroring the inline-edit pattern most label
  // management UIs use. Color editing pops a palette below the
  // current swatch instead of opening a separate dialog.
  const [editingName, setEditingName] = useState(false);
  const [name, setName] = useState(tag.name);
  const [colorOpen, setColorOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  useEffect(() => setName(tag.name), [tag.name]);

  const save = async (patch: UpdateTagRequest) => {
    setSaving(true);
    setError(null);
    try {
      await api.updateTag(tag.id, patch);
      onChanged();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  const commitName = async () => {
    const trimmed = name.trim();
    if (trimmed === "" || trimmed === tag.name) {
      setName(tag.name);
      setEditingName(false);
      return;
    }
    await save({ name: trimmed, color: tag.color });
    setEditingName(false);
  };

  const pickColor = async (color: string) => {
    setColorOpen(false);
    if (color === tag.color) return;
    await save({ name: tag.name, color });
  };

  const onDelete = async () => {
    setSaving(true);
    setError(null);
    try {
      await api.deleteTag(tag.id);
      onChanged();
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setConfirmingDelete(false);
    } finally {
      setSaving(false);
    }
  };

  // The preview chip uses the in-flight name so the user sees what
  // they're typing — even before they commit the edit.
  const previewTag: Tag = { ...tag, name: editingName ? name || tag.name : tag.name };

  return (
    <>
      <tr>
        <td>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <TagChip tag={previewTag} size="md" />
          </div>
          {error && (
            <div className="alert alert--error" style={{ marginTop: 6 }}>
              {error}
            </div>
          )}
        </td>
        <td className="muted mono" title="Slug is immutable so saved filters stay valid">
          {tag.slug}
        </td>
        <td>
          <ColorEditor
            value={tag.color}
            disabled={!canWrite || saving}
            open={colorOpen}
            onToggle={() => setColorOpen((v) => !v)}
            onPick={pickColor}
            onClose={() => setColorOpen(false)}
          />
        </td>
        <td className="num">
          {tag.integration_count > 0 ? (
            <Link to={`/integrations?tags=${encodeURIComponent(tag.slug)}`}>
              {tag.integration_count}
            </Link>
          ) : (
            <span className="muted">0</span>
          )}
        </td>
        <td className="num">
          {tag.service_count > 0 ? (
            tag.service_count
          ) : (
            <span className="muted">0</span>
          )}
        </td>
        <td className="num" style={{ whiteSpace: "nowrap" }}>
          {canWrite ? (
            editingName ? (
              <NameEditor
                value={name}
                onChange={setName}
                onCommit={commitName}
                onCancel={() => {
                  setName(tag.name);
                  setEditingName(false);
                }}
                saving={saving}
              />
            ) : (
              <button
                type="button"
                className="btn btn--link"
                onClick={() => setEditingName(true)}
                disabled={saving}
              >
                Rename
              </button>
            )
          ) : null}
          {canDelete && !editingName && (
            <button
              type="button"
              className="btn btn--link"
              style={{ color: "var(--err)" }}
              onClick={() => setConfirmingDelete(true)}
              disabled={saving}
            >
              Delete
            </button>
          )}
        </td>
      </tr>
      {confirmingDelete && (
        <tr>
          <td colSpan={6} style={{ background: "var(--surface-3)" }}>
            <DeleteConfirm
              tag={tag}
              onCancel={() => setConfirmingDelete(false)}
              onConfirm={onDelete}
              busy={saving}
            />
          </td>
        </tr>
      )}
    </>
  );
}

// ── Sub-components ────────────────────────────────────────────────

interface NameEditorProps {
  value: string;
  onChange: (v: string) => void;
  onCommit: () => void;
  onCancel: () => void;
  saving: boolean;
}

function NameEditor({ value, onChange, onCommit, onCancel, saving }: NameEditorProps) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    ref.current?.focus();
    ref.current?.select();
  }, []);
  return (
    <span style={{ display: "inline-flex", gap: 6, alignItems: "center" }}>
      <input
        ref={ref}
        className="search__input"
        style={{ width: 180, padding: "2px 8px", fontSize: 13 }}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            onCommit();
          } else if (e.key === "Escape") {
            e.preventDefault();
            onCancel();
          }
        }}
        disabled={saving}
      />
      <button
        type="button"
        className="btn"
        style={{ padding: "2px 10px", fontSize: 12 }}
        onClick={onCommit}
        disabled={saving}
      >
        {saving ? "…" : "Save"}
      </button>
      <button
        type="button"
        className="btn btn--link"
        onClick={onCancel}
        disabled={saving}
      >
        Cancel
      </button>
    </span>
  );
}

interface ColorEditorProps {
  value: string;
  disabled: boolean;
  open: boolean;
  onToggle: () => void;
  onPick: (color: string) => void;
  onClose: () => void;
}

const COLOR_PANEL_W = 260;

function ColorEditor({ value, disabled, open, onToggle, onPick, onClose }: ColorEditorProps) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) onClose();
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open, onClose]);

  // Position the palette with fixed viewport coords anchored to the
  // trigger, so the table's `.card { overflow: hidden }` can't clip it.
  // Reposition while open if the page scrolls or resizes.
  useLayoutEffect(() => {
    if (!open) {
      setPos(null);
      return;
    }
    const place = () => {
      const r = triggerRef.current?.getBoundingClientRect();
      if (!r) return;
      const left = Math.max(8, Math.min(r.left, window.innerWidth - COLOR_PANEL_W - 8));
      setPos({ top: r.bottom + 6, left });
    };
    place();
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [open]);

  return (
    <div ref={wrapRef} style={{ position: "relative", display: "inline-block" }}>
      <button
        ref={triggerRef}
        type="button"
        onClick={onToggle}
        disabled={disabled}
        title={disabled ? undefined : "Change color"}
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 8,
          padding: "3px 8px",
          borderRadius: 6,
          border: "1px solid var(--border)",
          background: "var(--surface-2)",
          cursor: disabled ? "default" : "pointer",
        }}
      >
        <span
          style={{
            width: 14,
            height: 14,
            borderRadius: 4,
            background: value,
            border: "1px solid rgba(0,0,0,0.15)",
          }}
        />
        <span className="mono" style={{ fontSize: 12 }}>
          {value}
        </span>
      </button>
      {open && pos && (
        <div
          role="dialog"
          style={{
            position: "fixed",
            top: pos.top,
            left: pos.left,
            zIndex: 50,
            padding: 10,
            background: "var(--surface-2)",
            border: "1px solid var(--border)",
            borderRadius: 8,
            boxShadow: "0 10px 24px rgba(0,0,0,0.14)",
            width: COLOR_PANEL_W,
          }}
        >
          <ColorInput value={value} onChange={onPick} />
        </div>
      )}
    </div>
  );
}

interface DeleteConfirmProps {
  tag: TagWithUsage;
  onConfirm: () => void;
  onCancel: () => void;
  busy: boolean;
}

function DeleteConfirm({ tag, onConfirm, onCancel, busy }: DeleteConfirmProps) {
  const parts: string[] = [];
  if (tag.integration_count > 0) {
    parts.push(
      `${tag.integration_count} ${tag.integration_count === 1 ? "integration" : "integrations"}`,
    );
  }
  if (tag.service_count > 0) {
    parts.push(
      `${tag.service_count} ${tag.service_count === 1 ? "service" : "services"}`,
    );
  }
  const cascade = parts.length === 0 ? "nothing else" : `${parts.join(" and ")}`;

  return (
    <div style={{ padding: "12px 16px", display: "flex", alignItems: "center", gap: 12 }}>
      <span>
        Delete <strong>{tag.name}</strong>? This will untag <strong>{cascade}</strong>.
        Tags can't be undeleted — you'd have to recreate it and re-attach by hand.
      </span>
      <div style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
        <button type="button" className="btn" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button
          type="button"
          className="btn btn--primary"
          onClick={onConfirm}
          disabled={busy}
          style={{ background: "var(--err)", borderColor: "var(--err)" }}
        >
          {busy ? "Deleting…" : "Delete"}
        </button>
      </div>
    </div>
  );
}

// ── Create panel ──────────────────────────────────────────────────

interface CreateTagPanelProps {
  onCreated: () => void;
  onCancel: () => void;
}

function CreateTagPanel({ onCreated, onCancel }: CreateTagPanelProps) {
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [color, setColor] = useState(PALETTE[9]); // blue, the friendliest default
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Auto-suggest slug from name unless the user has typed one
  // explicitly. Same slugify rules as the picker (lowercase kebab,
  // max 64) so the server's CHECK never surprises anyone.
  useEffect(() => {
    if (slugTouched) return;
    setSlug(slugify(name));
  }, [name, slugTouched]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const req: CreateTagRequest = {
        name: name.trim(),
        slug: slug.trim(),
        color,
      };
      await api.createTag(req);
      onCreated();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    // Layout mirrors the SchemasPage / MapsPage editor flow: Name +
    // Slug paired in a 2-col .form__row, Color on its own line, then a
    // .form__actions row pinned right. The .card wrapper used to live
    // here, but the form is now rendered inside an EditDrawer body
    // (which provides the surface + padding), so it's just .form.
    <form onSubmit={submit} className="form">
      <div className="form__row">
        <label className="form__label">
          Name
          <input
            className="search__input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="HR"
            autoFocus
            required
            maxLength={128}
          />
        </label>
        <label className="form__label">
          Slug
          <input
            className="search__input"
            value={slug}
            onChange={(e) => {
              setSlugTouched(true);
              setSlug(e.target.value);
            }}
            pattern="[a-z0-9-]+"
            placeholder="hr"
            required
            maxLength={64}
          />
          <span className="form__hint">Lowercase letters, digits, hyphens. Immutable after creation.</span>
        </label>
      </div>

      <div className="form__label">
        <ColorInput value={color} onChange={setColor} label="Color" />
      </div>

      <div className="form__label">
        <span>Preview</span>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <TagChip
            tag={{
              id: "preview",
              organization_id: "",
              slug: slug || "preview",
              name: name || "preview",
              color,
              created_at: "",
              updated_at: "",
            }}
            size="md"
          />
        </div>
      </div>

      {error && <div className="alert alert--error">{error}</div>}

      <div className="form__actions">
        <button type="button" className="btn" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button
          type="submit"
          className="btn btn--primary"
          disabled={busy || !name.trim() || !slug.trim()}
        >
          {busy ? "Creating…" : "Create tag"}
        </button>
      </div>
    </form>
  );
}

function slugify(input: string): string {
  const s = input
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64);
  return s;
}
