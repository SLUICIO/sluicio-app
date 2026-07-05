// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// SaveAsViewDialog — modal for saving the current filter set as a
// named view. Used by both the global Search page and any scoped
// Messages tab (e.g. /integrations/:id/messages). When the page is
// scoped, the dialog shows a "🔒 scope" line so the user knows the
// view will be pinned to that entity.
//
// The dialog is pure-presentational: it collects the user's input and
// invokes onSubmit with the values; the caller is responsible for
// turning that into a server call.

import { useEffect, useMemo, useRef, useState } from "react";
import type { Filter } from "./FilterEditor";
import type { SavedViewScope } from "./types";

export type SaveAsViewVisibility = "private" | "team" | "org";

export interface SaveAsViewValues {
  name: string;
  description: string;
  visibility: SaveAsViewVisibility;
  pinned: boolean;
}

interface Props {
  open: boolean;
  filters: Filter[];
  // scope: the entity this view will be pinned to (if any). Drives
  // the "🔒 scope" line in the summary and the suggested name.
  scope?: SavedViewScope;
  // suggestedName overrides the auto-generated default. Pass undefined
  // to let the dialog compute one from the filters.
  suggestedName?: string;
  onClose: () => void;
  onSubmit: (values: SaveAsViewValues) => void | Promise<void>;
}

const FIELD_LABELS: Record<string, string> = {
  payload: "payload",
  time: "time",
  integration: "integration",
  status: "status",
  service: "service",
  errorType: "error type",
};

export default function SaveAsViewDialog({
  open,
  filters,
  scope,
  suggestedName,
  onClose,
  onSubmit,
}: Props) {
  const autoName = useMemo(
    () => suggestedName ?? buildAutoName(filters, scope),
    [filters, scope, suggestedName],
  );

  const [name, setName] = useState(autoName);
  const [description, setDescription] = useState("");
  const [visibility, setVisibility] = useState<SaveAsViewVisibility>("private");
  const [pinned, setPinned] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const nameRef = useRef<HTMLInputElement>(null);

  // Reset the form when the dialog is reopened — otherwise the
  // previously typed values bleed into the next save.
  useEffect(() => {
    if (open) {
      setName(autoName);
      setDescription("");
      setVisibility("private");
      setPinned(false);
      setSubmitting(false);
      // Defer the focus until the dialog has mounted.
      window.setTimeout(() => nameRef.current?.select(), 0);
    }
  }, [open, autoName]);

  // Close on Escape.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !submitting) onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, submitting, onClose]);

  if (!open) return null;

  const handleSubmit = async () => {
    const trimmed = name.trim();
    if (!trimmed) {
      nameRef.current?.focus();
      return;
    }
    setSubmitting(true);
    try {
      await onSubmit({
        name: trimmed,
        description: description.trim(),
        visibility,
        pinned,
      });
    } finally {
      setSubmitting(false);
    }
  };

  // Filters to show in the "what's being saved" summary. Locked rows
  // are surfaced with the lock icon so the user knows they're part of
  // the saved view's scope.
  const filterRows = filters
    .filter((f) => f.value || f.locked)
    .map((f) => ({
      key: f.id,
      label:
        f.field === "payload" && f.fieldPath
          ? `payload.${f.fieldPath}`
          : FIELD_LABELS[f.field] ?? f.field,
      value: f.value || "—",
      locked: !!f.locked,
      optional: !!f.optional,
    }));

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="save-as-view-title"
      className="fixed inset-0 z-50 flex items-center justify-center"
      style={{ background: "rgba(0,0,0,0.45)" }}
      onClick={(e) => {
        if (e.target === e.currentTarget && !submitting) onClose();
      }}
    >
      <div
        className="w-full max-w-md rounded-lg border bg-surface-2 shadow-lg"
        style={{ borderColor: "var(--border)" }}
      >
        <header
          className="flex items-center justify-between border-b px-4 py-3"
          style={{ borderColor: "var(--border)" }}
        >
          <h2 id="save-as-view-title" className="text-base font-semibold">
            Save as view
          </h2>
          <button
            type="button"
            className="text-muted hover:text-foreground"
            onClick={onClose}
            aria-label="Close"
            disabled={submitting}
          >
            ✕
          </button>
        </header>

        <div className="space-y-4 px-4 py-4 text-sm">
          {/* Name */}
          <label className="block">
            <span className="text-xs uppercase tracking-wide text-muted">name</span>
            <input
              ref={nameRef}
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={200}
              className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
              style={{
                borderColor: "var(--border)",
                background: "var(--surface-3)",
                color: "var(--ink)",
              }}
            />
          </label>

          {/* Visibility */}
          <fieldset>
            <legend className="text-xs uppercase tracking-wide text-muted">
              visibility
            </legend>
            <div className="mt-1 flex gap-2">
              {(["private", "team", "org"] as SaveAsViewVisibility[]).map((v) => {
                const selected = visibility === v;
                return (
                  <button
                    key={v}
                    type="button"
                    onClick={() => setVisibility(v)}
                    className="flex-1 rounded-md border px-2 py-1.5 text-sm capitalize transition-colors"
                    style={{
                      borderColor: selected
                        ? "color-mix(in oklab, var(--primary) 35%, transparent)"
                        : "var(--border)",
                      background: selected
                        ? "var(--primary-soft)"
                        : "var(--surface-3)",
                      color: selected ? "var(--primary-ink)" : "var(--ink)",
                      fontWeight: selected ? 600 : 400,
                    }}
                    aria-pressed={selected}
                  >
                    {v === "private"
                      ? "private"
                      : v === "team"
                        ? "team"
                        : "org-wide"}
                  </button>
                );
              })}
            </div>
            <p className="mt-1 text-xs text-muted">
              {visibility === "private"
                ? "Only you can see this view."
                : visibility === "team"
                  ? "Your team can see and load this view."
                  : "Everyone in the org can see this view."}
            </p>
          </fieldset>

          {/* Pin */}
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={pinned}
              onChange={(e) => setPinned(e.target.checked)}
            />
            <span>Pin to the saved-views rail</span>
          </label>

          {/* Description */}
          <label className="block">
            <span className="text-xs uppercase tracking-wide text-muted">
              description (optional)
            </span>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={2}
              placeholder="Shown as a tooltip in the rail."
              className="mt-1 svc-textarea"
              style={{
                borderColor: "var(--border)",
                background: "var(--surface-3)",
                color: "var(--ink)",
              }}
            />
          </label>

          {/* Summary of what's being saved */}
          <div
            className="rounded-md border px-3 py-2"
            style={{ borderColor: "var(--border)", background: "var(--surface-3)" }}
          >
            <p className="text-xs uppercase tracking-wide text-muted">
              filters being saved
            </p>
            <ul className="mt-1 space-y-0.5 text-xs">
              {filterRows.length === 0 && (
                <li className="text-muted">No filters yet.</li>
              )}
              {filterRows.map((row) => (
                <li
                  key={row.key}
                  style={{ opacity: row.optional ? 0.6 : 1 }}
                  className="font-mono"
                >
                  {row.locked && (
                    <span aria-hidden="true" className="text-muted">
                      🔒 scope ·{" "}
                    </span>
                  )}
                  <span className="font-semibold">{row.label}</span>{" "}
                  <span className="text-muted">=</span>{" "}
                  <span>{row.value}</span>
                  {row.optional && (
                    <span className="ml-1 text-[10px] uppercase text-muted">opt.</span>
                  )}
                </li>
              ))}
            </ul>
            {scope?.integrationName && (
              <p className="mt-2 text-xs text-muted">
                This view will be pinned to{" "}
                <span className="font-semibold" style={{ color: "var(--ink)" }}>
                  {scope.integrationName}
                </span>{" "}
                — it will appear here and in the global search rail with an "in{" "}
                {scope.integrationName}" badge.
              </p>
            )}
          </div>
        </div>

        <footer
          className="flex items-center justify-end gap-2 border-t px-4 py-3"
          style={{ borderColor: "var(--border)" }}
        >
          <button
            type="button"
            className="btn"
            onClick={onClose}
            disabled={submitting}
          >
            cancel
          </button>
          <button
            type="button"
            className="btn btn--primary"
            onClick={handleSubmit}
            disabled={submitting || !name.trim()}
          >
            {submitting ? "saving…" : "💾 save view"}
          </button>
        </footer>
      </div>
    </div>
  );
}

// buildAutoName produces a short label from the active filters. Locked
// rows lead ("in <X>"), then up to two interesting free filters. Falls
// back to "untitled view" if there's nothing to summarize. Not exported
// to keep the file's react-refresh boundary clean — callers that want
// a custom name should pass `suggestedName` to the dialog.
function buildAutoName(filters: Filter[], scope?: SavedViewScope): string {
  const effective = filters.filter((f) => !f.optional);
  const parts: string[] = [];

  const free = effective
    .filter((f) => !f.locked)
    .filter((f) => f.field !== "time")
    .filter((f) => f.value && f.value !== "any");
  for (const f of free.slice(0, 2)) {
    const fieldLabel =
      f.field === "payload" && f.fieldPath
        ? f.fieldPath
        : FIELD_LABELS[f.field] ?? f.field;
    parts.push(`${fieldLabel} ${shortValue(f.value)}`);
  }

  let stem = parts.join(" · ");
  const scopeLabel = scope?.integrationName;
  if (scopeLabel) {
    stem = stem ? `${stem} in ${scopeLabel}` : `messages in ${scopeLabel}`;
  }
  return stem || "untitled view";
}

function shortValue(v: string): string {
  const trimmed = v.trim();
  if (trimmed.length <= 24) return trimmed;
  return trimmed.slice(0, 21) + "…";
}
