// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Facets page. A service has many facets — one per input or output it
// touches (file-input, queue-output, db-output, etc.) plus an always-
// on `core`. This page lists every facet defined in the cell so users
// can browse which one drives which dashboard widgets, and click
// through to "every service currently carrying this facet".
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { ServiceFacetShape } from "../api/types";
import { EditDrawer, SortableTh } from "../components/primitives";
import { useCurrentUser } from "../lib/useCurrentUser";
import { usePageTitle } from "../lib/usePageTitle";
import { useTableSort } from "../lib/useTableSort";

type FacetSortKey = "name" | "description" | "widgets";

export default function ServiceTypes() {
  usePageTitle("Service facets");
  const [items, setItems] = useState<ServiceFacetShape[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const { sortedRows, sort, toggleSort } = useTableSort<ServiceFacetShape, FacetSortKey>(
    items ?? [],
    {
      name: (t) => t.name,
      description: (t) => t.description,
      widgets: (t) => t.widgets.length,
    },
  );

  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  const [creating, setCreating] = useState(false);
  const [draftName, setDraftName] = useState("");
  const [draftDesc, setDraftDesc] = useState("");
  const [editSlug, setEditSlug] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editDesc, setEditDesc] = useState("");
  const [actionErr, setActionErr] = useState<string | null>(null);

  const refresh = () => {
    setLoading(true);
    api
      .listServiceFacets()
      .then((r) => setItems(r.facets ?? []))
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  };
  useEffect(refresh, []);

  const msg = (e: unknown) => String((e as Error)?.message ?? e);
  const createFacet = async () => {
    if (!draftName.trim()) return;
    setActionErr(null);
    try {
      await api.createServiceFacet({ name: draftName.trim(), description: draftDesc.trim() });
      setCreating(false);
      setDraftName("");
      setDraftDesc("");
      refresh();
    } catch (e) {
      setActionErr(msg(e));
    }
  };
  const saveEdit = async (slug: string) => {
    if (!editName.trim()) return;
    setActionErr(null);
    try {
      await api.updateServiceFacet(slug, { name: editName.trim(), description: editDesc.trim() });
      setEditSlug(null);
      refresh();
    } catch (e) {
      setActionErr(msg(e));
    }
  };
  const deleteFacet = async (slug: string, name: string) => {
    if (!window.confirm(`Delete the custom facet "${name}"? Services assigned it will lose the label.`)) return;
    setActionErr(null);
    try {
      await api.deleteServiceFacet(slug);
      refresh();
    } catch (e) {
      setActionErr(msg(e));
    }
  };

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Service facets</h1>
          <p className="page__subtitle">
            A service is described by every input it reads from and every output
            it writes to. Each of these roles is a "facet"; a service can carry
            many at once (a file-to-queue bridge has file-input + queue-output).
            Facet widgets stack on the service's dashboard. Add your own custom
            facets to label and group services your way.
          </p>
        </div>
        {canWrite && !creating && (
          <button className="btn btn--primary" onClick={() => setCreating(true)}>+ New facet</button>
        )}
      </div>

      {actionErr && !creating && <div className="alert alert--error" style={{ marginBottom: 12 }}>{actionErr}</div>}
      {creating && (
        <EditDrawer
          title="New facet"
          width="narrow"
          onClose={() => { setCreating(false); setDraftName(""); setDraftDesc(""); setActionErr(null); }}
        >
          <form className="form" onSubmit={(e) => { e.preventDefault(); createFacet(); }}>
            <label className="form__label">
              Name
              <input className="search__input" value={draftName} onChange={(e) => setDraftName(e.target.value)} placeholder="e.g. Billing" autoFocus required />
            </label>
            <label className="form__label">
              Description
              <input className="search__input" value={draftDesc} onChange={(e) => setDraftDesc(e.target.value)} placeholder="optional" />
              <span className="form__hint">Optional — shown next to the facet in the list.</span>
            </label>
            {actionErr && <div className="alert alert--error">{actionErr}</div>}
            <div className="form__actions">
              <button type="button" className="btn" onClick={() => { setCreating(false); setDraftName(""); setDraftDesc(""); setActionErr(null); }}>Cancel</button>
              <button type="submit" className="btn btn--primary" disabled={!draftName.trim()}>Create facet</button>
            </div>
          </form>
        </EditDrawer>
      )}

      <div className="card" style={{ marginBottom: 16 }}>
        <div className="card__header">How Sluicio detects facets</div>
        <div style={{ padding: 16, display: "flex", flexDirection: "column", gap: 12, fontSize: 13.5 }}>
          <p className="muted" style={{ margin: 0 }}>
            Sluicio classifies a service from the{" "}
            <span className="mono">io.kind</span> (file / queue / stream / http / db / email) and{" "}
            <span className="mono">io.role</span> (input / output) attributes on the spans that
            cross a system boundary. Every matching pair adds a facet (and its dashboard widgets);
            a service with only internal work is a <strong>Worker</strong>, and every service gets
            the always-on <strong>Overview</strong>. Open a facet below to see the exact attributes
            it looks for.
          </p>
          <div>
            <div className="muted" style={{ marginBottom: 6, textTransform: "uppercase", fontSize: 11, letterSpacing: 0.5 }}>
              Two ways to land on the right facets
            </div>
            <ul style={{ margin: 0, paddingLeft: 18, display: "flex", flexDirection: "column", gap: 6 }}>
              <li>
                <strong>Emit the attributes</strong> — set <span className="mono">io.kind</span> /{" "}
                <span className="mono">io.role</span> (plus each facet's key attributes) on the spans
                that cross each boundary.
              </li>
              <li>
                <strong>Remap what you already emit</strong> — if your spans carry a different
                attribute (e.g. <span className="mono">messaging.system</span>), add an{" "}
                <strong>Attribute mapping</strong> on a service's <strong>Advanced</strong> tab:
                "when attribute X matches Y, treat the span as{" "}
                <span className="mono">io.kind</span>=…, <span className="mono">io.role</span>=…".
                Native <span className="mono">io.kind</span> / <span className="mono">io.role</span>{" "}
                always win when present.
              </li>
            </ul>
          </div>
        </div>
      </div>

      {error && <div className="alert alert--error">Failed to load: {error}</div>}
      {loading && !items && <div className="placeholder">Loading…</div>}

      {items && (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <SortableTh sortKey="name" state={sort} onSort={toggleSort}>Facet</SortableTh>
                <SortableTh sortKey="description" state={sort} onSort={toggleSort}>Description</SortableTh>
                <SortableTh sortKey="widgets" state={sort} onSort={toggleSort} className="num">Widgets</SortableTh>
                <th />
              </tr>
            </thead>
            <tbody>
              {sortedRows.map((t) =>
                editSlug === t.slug ? (
                  <tr key={t.slug}>
                    <td><input className="input" value={editName} onChange={(e) => setEditName(e.target.value)} autoFocus /></td>
                    <td><input className="input" value={editDesc} onChange={(e) => setEditDesc(e.target.value)} placeholder="optional" /></td>
                    <td className="num">—</td>
                    <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                      <button className="btn btn--sm btn--primary" onClick={() => saveEdit(t.slug)} disabled={!editName.trim()}>Save</button>{" "}
                      <button className="btn btn--sm" onClick={() => setEditSlug(null)}>Cancel</button>
                    </td>
                  </tr>
                ) : (
                  <tr key={t.slug}>
                    <td>
                      <Link to={`/service-facets/${encodeURIComponent(t.slug)}`}>{t.name}</Link>
                      {t.custom && <span className="badge" style={{ marginLeft: 8, fontSize: 10 }}>custom</span>}
                      <div className="muted mono" style={{ fontSize: 12 }}>{t.slug}</div>
                    </td>
                    <td className="muted">{t.description}</td>
                    <td className="num">{t.widgets.length}</td>
                    <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                      {canWrite && t.custom && (
                        <>
                          <button className="btn btn--sm" onClick={() => { setEditSlug(t.slug); setEditName(t.name); setEditDesc(t.description); }}>Edit</button>{" "}
                          <button className="btn btn--sm btn--danger" onClick={() => deleteFacet(t.slug, t.name)}>Delete</button>
                        </>
                      )}
                    </td>
                  </tr>
                )
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
