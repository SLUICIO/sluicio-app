// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// SavedViewsRail: left rail listing the views the user can see, their
// own private ones, their teams' and the org-wide ones. Active view gets
// an accent left-border + tinted background; the "+ new view" button at
// the bottom appends a draft view to the rail.

import type { SavedView } from "./types";
import { visibilityLabel } from "./viewVisibility";

interface Props {
  views: SavedView[];
  activeId: string | null;
  onSelect: (id: string) => void;
  onCreate: () => void;
  // Delete a view. The rail confirms first; the parent persists the
  // removal (server-side for saved views, local for drafts).
  onDelete?: (id: string) => void;
}

// integrationNameFor lets the rail show "in <Orders → ERP>" badges on
// scoped views. We don't store the human-readable name in the wire
// record (the server only has the id), so the page passes a lookup
// function in.
interface ExtraProps {
  integrationNameFor?: (id: string) => string | undefined;
}

export default function SavedViewsRail({
  views,
  activeId,
  onSelect,
  onCreate,
  onDelete,
  integrationNameFor,
}: Props & ExtraProps) {
  return (
    <aside
      className="flex h-full flex-col rounded-xl border bg-surface-2 shadow-sm"
      style={{ borderColor: "var(--border)" }}
    >
      <header className="border-b px-4 py-3" style={{ borderColor: "var(--border)" }}>
        <h2 className="text-base font-semibold">Saved views</h2>
        <p className="text-xs text-muted">yours, your teams' and org-wide</p>
      </header>
      <ul className="flex-1 overflow-auto">
        {views.length === 0 && (
          <li className="px-4 py-6 text-sm text-muted">No saved views yet.</li>
        )}
        {views.map((v) => (
          <li
            key={v.id}
            className="flex items-stretch border-b"
            style={{
              borderColor: "var(--border)",
              background: v.id === activeId ? "var(--primary-soft)" : undefined,
              borderLeft:
                v.id === activeId
                  ? `3px solid var(--primary)`
                  : "3px solid transparent",
            }}
          >
            <button
              type="button"
              onClick={() => onSelect(v.id)}
              className="flex min-w-0 flex-1 items-center gap-2 px-4 py-3 text-left text-sm hover:bg-surface-elevated"
              style={{
                color: v.id === activeId ? "var(--primary-ink)" : undefined,
              }}
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-baseline gap-2">
                  <span
                    className="truncate"
                    style={{ fontWeight: v.id === activeId ? 600 : 400 }}
                  >
                    {v.name}
                  </span>
                  {v.pinned && <span className="text-xs">★</span>}
                  {v.visibility !== "org" && (
                    <span
                      className="truncate rounded border px-1 text-[10px] uppercase tracking-wide text-muted"
                      style={{ borderColor: "var(--border)" }}
                    >
                      {visibilityLabel(v)}
                    </span>
                  )}
                </div>
                {v.scope?.integrationId && (
                  <div className="mt-0.5 text-xs">
                    <span
                      className="rounded border px-1 py-0.5 text-[10px] uppercase tracking-wide"
                      style={{
                        borderColor:
                          "color-mix(in oklab, var(--primary) 30%, transparent)",
                        background: "var(--primary-soft)",
                        color: "var(--primary-ink)",
                      }}
                      title="This view is scoped to an integration; opening it applies that integration's filter here."
                    >
                      in{" "}
                      {integrationNameFor?.(v.scope.integrationId) ??
                        v.scope.integrationName ??
                        v.scope.integrationId.slice(0, 8) + "…"}
                    </span>
                  </div>
                )}
                {v.resultCount !== undefined && (
                  <div className="text-xs text-muted">
                    {v.resultCount.toLocaleString()} msgs
                  </div>
                )}
              </div>
            </button>
            {onDelete && v.canManage && (
              <button
                type="button"
                aria-label={`Delete view ${v.name}`}
                title="Delete this view"
                onClick={() => {
                  if (
                    window.confirm(
                      `Delete the view "${v.name}"? This can't be undone.`,
                    )
                  ) {
                    onDelete(v.id);
                  }
                }}
                className="flex flex-shrink-0 items-center px-3 text-sm hover:bg-surface-elevated"
                style={{ color: "var(--err)" }}
              >
                🗑
              </button>
            )}
          </li>
        ))}
      </ul>
      <button
        type="button"
        onClick={onCreate}
        className="border-t px-4 py-3 text-center text-sm hover:bg-surface-elevated"
        style={{ borderColor: "var(--border)" }}
      >
        + new view
      </button>
    </aside>
  );
}
