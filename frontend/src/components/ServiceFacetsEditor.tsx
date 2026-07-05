// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ServiceFacetsEditor lets a user manually set which service facets
// apply to a service, for cases the OTLP data can't express. Facets
// are normally auto-detected from telemetry (io.kind / io.role on
// spans, or the attribute-based facet-mapping rules). When that signal
// isn't in the data at all, this editor lets the user add a facet
// directly — or hide one that was wrongly detected.
//
// The editor loads the full facet vocabulary annotated with what's
// auto-detected and what's currently overridden, renders a checkbox per
// facet (pre-checked = effective), and on save diffs the checkbox state
// against auto-detection to compute the include/exclude override set.
// The always-on "core" facet is shown as a fixed, non-editable row.

import { useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import type { FacetOverrideRow } from "../api/types";

interface Props {
  serviceName: string;
  // Called after a successful save so the parent can refresh the
  // service's widgets — adding or hiding a facet changes which widget
  // sections render on the dashboard.
  onChanged?: () => void;
}

// effectiveSlugs returns the set of facet slugs that should start
// checked: every facet that currently resolves as effective.
function effectiveSlugs(rows: FacetOverrideRow[]): Set<string> {
  return new Set(rows.filter((r) => r.effective).map((r) => r.slug));
}

export default function ServiceFacetsEditor({ serviceName, onChanged }: Props) {
  const [rows, setRows] = useState<FacetOverrideRow[]>([]);
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    api
      .getServiceFacetOverrides(serviceName)
      .then((r) => {
        if (cancelled) return;
        setRows(r.facets);
        setChecked(effectiveSlugs(r.facets));
      })
      .catch((e) => {
        if (!cancelled) setError(String((e as Error).message ?? e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [serviceName]);

  // Dirty when any facet's current checkbox state differs from how it
  // resolved on load — that's exactly the set of changes we'd persist.
  const dirty = useMemo(
    () => rows.some((r) => checked.has(r.slug) !== r.effective),
    [rows, checked],
  );

  const toggle = (slug: string, removable: boolean) => {
    if (!removable || saving) return;
    setChecked((prev) => {
      const next = new Set(prev);
      if (next.has(slug)) next.delete(slug);
      else next.add(slug);
      return next;
    });
  };

  const reset = () => setChecked(effectiveSlugs(rows));

  const save = async () => {
    // Diff the checkbox state against auto-detection: a checked facet
    // that isn't auto-detected becomes an include; an unchecked facet
    // that is auto-detected becomes an exclude. Everything else needs
    // no override. The core facet (removable=false) is never sent.
    const include: string[] = [];
    const exclude: string[] = [];
    for (const r of rows) {
      if (!r.removable) continue;
      const isChecked = checked.has(r.slug);
      if (isChecked && !r.auto_detected) include.push(r.slug);
      else if (!isChecked && r.auto_detected) exclude.push(r.slug);
    }
    setSaving(true);
    setError(null);
    try {
      const resp = await api.updateServiceFacetOverrides(serviceName, {
        include,
        exclude,
      });
      setRows(resp.facets);
      setChecked(effectiveSlugs(resp.facets));
      onChanged?.();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  // A short, live hint describing what each row will resolve to given
  // the current checkbox state — so the user sees the effect of a
  // pending change before saving.
  const hintFor = (r: FacetOverrideRow): { label: string; tone: string } | null => {
    const isChecked = checked.has(r.slug);
    if (isChecked && !r.auto_detected) return { label: "manual", tone: "var(--accent, #8b5cf6)" };
    if (!isChecked && r.auto_detected) return { label: "hidden", tone: "var(--muted)" };
    if (isChecked && r.auto_detected) return { label: "detected", tone: "var(--muted)" };
    return null;
  };

  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="card__header">
        Service facets
        <span
          className="muted"
          style={{ marginLeft: 8, fontWeight: 400, fontSize: 13 }}
        >
          · manually assign or hide facets
        </span>
      </div>
      <div style={{ padding: "12px 16px" }}>
        <p className="muted" style={{ margin: "0 0 12px", fontSize: 13 }}>
          Facets are normally detected from this service's telemetry. When the
          signal isn't in the data, tick a facet to assign it manually, or
          untick a detected one to hide it. Manually-assigned facets still get
          their dashboard section — its widgets read empty until matching
          telemetry arrives.
        </p>

        {error && (
          <div className="alert alert--error" style={{ marginBottom: 12 }}>
            {error}
          </div>
        )}

        {loading ? (
          <div className="muted" style={{ fontSize: 12 }}>
            Loading…
          </div>
        ) : (
          <>
            <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
              {rows.map((r) => {
                const isChecked = checked.has(r.slug);
                const hint = hintFor(r);
                return (
                  <label
                    key={r.slug}
                    title={r.removable ? undefined : "Always on — every service has the Overview facet"}
                    style={{
                      display: "grid",
                      gridTemplateColumns: "auto 1fr auto",
                      alignItems: "baseline",
                      gap: 10,
                      padding: "6px 4px",
                      borderRadius: 4,
                      cursor: r.removable ? "pointer" : "default",
                      opacity: r.removable ? 1 : 0.7,
                    }}
                  >
                    <input
                      type="checkbox"
                      checked={isChecked}
                      disabled={!r.removable || saving}
                      onChange={() => toggle(r.slug, r.removable)}
                    />
                    <span>
                      <span style={{ fontSize: 14 }}>{r.name}</span>
                      {r.description && (
                        <span
                          className="muted"
                          style={{ display: "block", fontSize: 12 }}
                        >
                          {r.description}
                        </span>
                      )}
                    </span>
                    {hint && (
                      <span
                        style={{
                          fontSize: 11,
                          textTransform: "uppercase",
                          letterSpacing: 0.4,
                          color: hint.tone,
                          alignSelf: "center",
                        }}
                      >
                        {hint.label}
                      </span>
                    )}
                  </label>
                );
              })}
            </div>

            <div
              style={{
                marginTop: 12,
                display: "flex",
                gap: 8,
                alignItems: "center",
              }}
            >
              <button
                className="btn btn--primary"
                type="button"
                disabled={!dirty || saving}
                onClick={save}
              >
                {saving ? "Saving…" : "Save facets"}
              </button>
              {dirty && !saving && (
                <button className="btn btn--link" type="button" onClick={reset}>
                  Reset
                </button>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
