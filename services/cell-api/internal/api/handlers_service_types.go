// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"time"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/servicetypes"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/store"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/tags"
)

type widgetResponse struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Data        any    `json:"data"`
}

// facetWidgetsResponse is one facet section on a service's dashboard:
// the facet identity plus the computed widgets that belong to it.
// Source is "auto" or "manual" (see ServiceFacetRef.Source) so the UI
// can badge a manually-assigned section — whose widgets may read empty
// when the service emits no matching telemetry.
type facetWidgetsResponse struct {
	Slug        string           `json:"slug"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Source      string           `json:"source"`
	Widgets     []widgetResponse `json:"widgets"`
}

type serviceWidgetsResponse struct {
	ServiceName string                 `json:"service_name"`
	Window      WindowSummary          `json:"window"`
	Facets      []facetWidgetsResponse `json:"facets"`
}

// listServiceFacets: GET /api/v1/service-facets
func (h *Handlers) listServiceFacets(w http.ResponseWriter, r *http.Request) {
	all := h.mergedFacets(r.Context(), middleware.OrgID(r))
	out := make([]servicetypes.JSONShape, 0, len(all))
	for _, f := range all {
		out = append(out, f.ToJSON())
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"facets": out,
	})
}

// getServiceFacet: GET /api/v1/service-facets/{slug}
//
// Returns the facet's definition plus the services currently carrying
// it. Lets the Service Facets page double as "show me everything
// currently classified as a File Input".
func (h *Handlers) getServiceFacet(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	allFacets := h.mergedFacets(r.Context(), middleware.OrgID(r))
	var f *servicetypes.ServiceFacet
	for i := range allFacets {
		if allFacets[i].Slug == slug {
			f = &allFacets[i]
			break
		}
	}
	if f == nil {
		httpserver.WriteError(w, http.StatusNotFound, "service facet not found")
		return
	}
	tr := ParseRange(r, time.Hour)

	allServices, err := h.Store.ListServices(r.Context(), tr.From, tr.To)
	if err != nil {
		h.Logger.Error("list services for facet failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	firingServices, err := h.Alerts.FiringHealthServices(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("firing health services failed", "err", err)
		firingServices = map[string]bool{}
	}

	matched := make([]ServiceSummary, 0)
	for _, s := range allServices {
		resolver := h.ioResolverFor(r.Context(), s.ServiceName)
		prof, err := h.Store.ServiceProfile(r.Context(), resolver, s.ServiceName, tr.From, tr.To)
		if err != nil {
			h.Logger.Warn("profile failed", "service", s.ServiceName, "err", err)
			continue
		}
		profile := toProfile(s.ServiceName, prof)
		// Resolve the effective facet set (auto-detection + manual
		// overrides) so this page agrees with the services list: a
		// service manually assigned this facet shows up here, and one
		// that excluded it does not.
		auto := h.ServiceFacets.MatchAll(profile)
		resolved := h.resolveFacets(allFacets, auto, h.facetOverridesFor(r.Context(), s.ServiceName))
		// Show the full facet set on each row so a user looking at "all
		// services that have File Input" can also see that some of
		// them also have Queue Output, etc.
		refs := make([]ServiceFacetRef, 0, len(resolved))
		hasFacet := false
		for _, rf := range resolved {
			if rf.facet.Slug == slug {
				hasFacet = true
			}
			refs = append(refs, ServiceFacetRef{Slug: rf.facet.Slug, Name: rf.facet.Name, Source: rf.source})
		}
		if !hasFacet {
			continue
		}
		status := "ok"
		if s.ErrorTraceCount > 0 {
			status = "errors"
		}
		if firingServices[s.ServiceName] {
			status = "unhealthy"
		}
		matched = append(matched, ServiceSummary{
			ServiceName:      s.ServiceName,
			ServiceNamespace: s.ServiceNamespace,
			LastSeen:         s.LastSeen,
			TraceCount:       s.TraceCount,
			ErrorTraceCount:  s.ErrorTraceCount,
			ServiceFacets:    refs,
			Tags:             []tags.Tag{},
			Status:           status,
		})
	}

	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"facet":    f.ToJSON(),
		"services": matched,
		"window":   tr.Window(),
	})
}

// serviceWidgets: GET /api/v1/services/{name}/widgets
//
// Classifies the service via its profile, then computes the widgets
// for *every* matching facet, returning them grouped by facet.
func (h *Handlers) serviceWidgets(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	tr := ParseRange(r, time.Hour)

	// One resolver per request — built from the service's mappings
	// and threaded through both profile classification and every
	// widget below. Cheap enough that we don't bother caching
	// across requests; the Postgres fetch is keyed on a tight
	// index over (organization_id, service_name).
	resolver := h.ioResolverFor(r.Context(), name)

	prof, err := h.Store.ServiceProfile(r.Context(), resolver, name, tr.From, tr.To)
	if err != nil {
		h.Logger.Error("service profile failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	profile := toProfile(name, prof)
	auto := h.ServiceFacets.MatchAll(profile)
	// Effective facets = auto-detected, plus any the user manually
	// included, minus any they excluded. A manually-included facet still
	// renders its full widget set; with no matching telemetry those
	// widgets simply read empty, which is the honest answer.
	resolved := h.resolveFacets(h.mergedFacets(r.Context(), middleware.OrgID(r)), auto, h.facetOverridesFor(r.Context(), name))

	out := make([]facetWidgetsResponse, 0, len(resolved))
	for _, rf := range resolved {
		facet := rf.facet
		widgets := make([]widgetResponse, 0, len(facet.Widgets))
		for _, widget := range facet.Widgets {
			data, err := widget.Compute(r.Context(), h.ClickHouseConn, resolver, name, tr.From, tr.To)
			if err != nil {
				h.Logger.Error("widget compute failed",
					"err", err, "facet", facet.Slug, "kind", widget.Kind(), "name", widget.Name())
				// One broken widget shouldn't kill the whole dashboard.
				data = nil
			}
			widgets = append(widgets, widgetResponse{
				Kind:        widget.Kind(),
				Name:        widget.Name(),
				Description: widget.Description(),
				Data:        data,
			})
		}
		out = append(out, facetWidgetsResponse{
			Slug:        facet.Slug,
			Name:        facet.Name,
			Description: facet.Description,
			Source:      rf.source,
			Widgets:     widgets,
		})
	}

	httpserver.WriteJSON(w, http.StatusOK, serviceWidgetsResponse{
		ServiceName: name,
		Window:      tr.Window(),
		Facets:      out,
	})
}

// toProfile bridges the store's row shape and the servicetypes
// package's profile shape (which uses maps for O(1) lookup).
func toProfile(serviceName string, row store.ServiceProfileRow) servicetypes.ServiceProfile {
	kinds := make(map[string]bool, len(row.SpanKinds))
	for _, k := range row.SpanKinds {
		kinds[k] = true
	}
	resourceKeys := make(map[string]bool, len(row.ResourceAttrKeys))
	for _, k := range row.ResourceAttrKeys {
		resourceKeys[k] = true
	}
	spanKeys := make(map[string]bool, len(row.SpanAttrKeys))
	for _, k := range row.SpanAttrKeys {
		spanKeys[k] = true
	}
	ioFacets := make(map[string]bool, len(row.IOFacets))
	for _, k := range row.IOFacets {
		ioFacets[k] = true
	}
	return servicetypes.ServiceProfile{
		ServiceName:      serviceName,
		SpanKinds:        kinds,
		ResourceAttrKeys: resourceKeys,
		SpanAttrKeys:     spanKeys,
		IOFacets:         ioFacets,
	}
}
