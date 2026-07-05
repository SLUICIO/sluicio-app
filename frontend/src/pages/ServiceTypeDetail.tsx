// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Facet detail page: shows one facet's widget definitions plus every
// service currently carrying that facet in the selected window.
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import ServicesTable from "../components/ServicesTable";
import { SortableTh } from "../components/primitives";
import type { ServiceFacetDetailResponse, ServiceFacetShape } from "../api/types";
import { usePageTitle } from "../lib/usePageTitle";
import { useTableSort } from "../lib/useTableSort";
import { useTimeWindow } from "../lib/useTimeWindow";

type FacetWidgetSortKey = "name" | "kind" | "description";

export default function ServiceTypeDetail() {
  const { slug = "" } = useParams();
  const [windowVal] = useTimeWindow();
  const [data, setData] = useState<ServiceFacetDetailResponse | null>(null);
  usePageTitle(data?.facet.name ?? slug);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const widgets = data?.facet.widgets ?? [];
  const {
    sortedRows: sortedWidgets,
    sort: widgetSort,
    toggleSort: toggleWidgetSort,
  } = useTableSort<(typeof widgets)[number], FacetWidgetSortKey>(widgets, {
    name: (w) => w.name,
    kind: (w) => w.kind,
    description: (w) => w.description,
  });

  useEffect(() => {
    setLoading(true);
    setError(null);
    api
      .getServiceFacet(slug, windowVal)
      .then(setData)
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  }, [slug, windowVal]);

  return (
    <div>
      <div className="page__header">
        <div>
          <p className="breadcrumb">
            <Link to="/service-facets">Service facets</Link> / {data?.facet.name ?? slug}
          </p>
          <h1 className="page__title">{data?.facet.name ?? "Loading…"}</h1>
          {data && <p className="page__subtitle">{data.facet.description}</p>}
        </div>
      </div>

      {error && <div className="alert alert--error">{error}</div>}
      {loading && !data && <div className="placeholder">Loading…</div>}

      {data && (
        <>
          <DetectionCard facet={data.facet} />

          <div className="card" style={{ marginTop: 16 }}>
            <div className="card__header">Widgets this facet provides</div>
            <table className="table">
              <thead>
                <tr>
                  <SortableTh sortKey="name" state={widgetSort} onSort={toggleWidgetSort}>Name</SortableTh>
                  <SortableTh sortKey="kind" state={widgetSort} onSort={toggleWidgetSort}>Kind</SortableTh>
                  <SortableTh sortKey="description" state={widgetSort} onSort={toggleWidgetSort}>Description</SortableTh>
                </tr>
              </thead>
              <tbody>
                {sortedWidgets.map((w, i) => (
                  <tr key={i}>
                    <td>{w.name}</td>
                    <td className="muted mono">{w.kind}</td>
                    <td className="muted">{w.description}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="card" style={{ marginTop: 16 }}>
            <div className="card__header">
              Services currently carrying {data.facet.name} ({data.services.length})
            </div>
            <ServicesTable
              services={data.services}
              showType={true}
              showIntegrations={false}
              showFirstSeen={false}
              showTags={false}
              emptyState={
                <div className="placeholder">
                  No services currently carry this facet in the selected window.
                </div>
              }
            />
          </div>
        </>
      )}
    </div>
  );
}

// DetectionCard explains the OTel instrumentation a producer must emit
// for Sluicio to recognise this facet — the match attributes/span kinds
// it classifies on, plus the optional attributes that enrich the
// dashboards. Without these, Sluicio can't tell what the service is.
function DetectionCard({ facet }: { facet: ServiceFacetShape }) {
  const m = facet.match ?? {};
  const attrs = m.span_attributes ?? [];
  const kinds = m.span_kinds ?? [];
  return (
    <div className="card">
      <div className="card__header">How Sluicio detects this facet</div>
      <div style={{ padding: 16, display: "flex", flexDirection: "column", gap: 16 }}>
        {m.always ? (
          <p className="muted" style={{ margin: 0, fontSize: 13.5 }}>
            Applies to <strong>every service</strong> — no specific attributes required.
          </p>
        ) : (
          <div>
            <p className="muted" style={{ margin: "0 0 8px", fontSize: 13.5 }}>
              Sluicio applies this facet to a service that emits spans with:
            </p>
            <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
              {attrs.map((a, i) => (
                <span key={a.key} style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                  {i > 0 && <span className="muted" style={{ fontSize: 12 }}>AND</span>}
                  <span className="badge mono">{a.key} = {a.value}</span>
                </span>
              ))}
              {kinds.map((k) => (
                <span key={k} className="badge mono">span kind = {k}</span>
              ))}
            </div>
            {m.note && (
              <p className="muted" style={{ margin: "8px 0 0", fontSize: 12.5 }}>…{m.note}.</p>
            )}
          </div>
        )}

        {facet.key_attributes.length > 0 && (
          <div>
            <p className="muted" style={{ margin: "0 0 8px", fontSize: 13.5 }}>
              Emit these attributes too — they power this facet's widgets and are highlighted on
              its spans (optional, but recommended):
            </p>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
              {facet.key_attributes.map((k) => (
                <span key={k} className="badge mono">{k}</span>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
