// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/facetoverrides"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/servicetypes"
)

// facetOverrideRow is one facet in the editor's view of a service: the
// facet identity plus how it currently resolves. The UI renders one
// checkbox per row, pre-checked when Effective is true, and badges rows
// whose Override is set. Removable is false for the always-on core
// facet so the UI can show it as a fixed, non-editable row.
type facetOverrideRow struct {
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	AutoDetected bool    `json:"auto_detected"`
	Override     *string `json:"override"` // "include" | "exclude" | null
	Effective    bool    `json:"effective"`
	Removable    bool    `json:"removable"`
}

// facetOverridesResponse is the body of GET/PUT
// /api/v1/services/{name}/facet-overrides.
type facetOverridesResponse struct {
	ServiceName string             `json:"service_name"`
	Window      WindowSummary      `json:"window"`
	Facets      []facetOverrideRow `json:"facets"`
}

// putFacetOverridesRequest is the body of PUT
// /api/v1/services/{name}/facet-overrides. Both lists hold facet slugs.
// Sending the whole desired set (rather than per-row deltas) keeps the
// endpoint idempotent: it replaces the service's entire override set.
type putFacetOverridesRequest struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

// getFacetOverrides: GET /api/v1/services/{name}/facet-overrides
//
// Returns the full facet vocabulary annotated with what's auto-detected
// for the service over the window, which overrides are set, and the
// resulting effective state — everything the editor needs to render.
func (h *Handlers) getFacetOverrides(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	tr := ParseRange(r, time.Hour)
	httpserver.WriteJSON(w, http.StatusOK, h.buildFacetOverridesResponse(r.Context(), name, tr))
}

// putFacetOverrides: PUT /api/v1/services/{name}/facet-overrides
//
// Replaces the service's entire override set with the include/exclude
// slugs in the body, then returns the recomputed resolution so the UI
// can update without a second round trip.
func (h *Handlers) putFacetOverrides(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	var req putFacetOverridesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate slugs against the registry and keep include/exclude
	// disjoint. `core` is always on, so any override naming it is
	// silently dropped rather than rejected — the editor renders it as a
	// fixed row and shouldn't be punished for echoing it back.
	seen := map[string]string{}
	clean := func(list []string, action string) ([]string, error) {
		out := make([]string, 0, len(list))
		for _, raw := range list {
			slug := strings.TrimSpace(raw)
			if slug == "" {
				continue
			}
			if h.ServiceFacets.Get(slug) == nil {
				return nil, fmt.Errorf("unknown facet slug: %q", slug)
			}
			if slug == servicetypes.CoreSlug {
				continue
			}
			if prev, ok := seen[slug]; ok {
				if prev != action {
					return nil, fmt.Errorf("facet %q cannot be both included and excluded", slug)
				}
				continue
			}
			seen[slug] = action
			out = append(out, slug)
		}
		return out, nil
	}

	includes, err := clean(req.Include, "include")
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	excludes, err := clean(req.Exclude, "exclude")
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.FacetOverrides.ReplaceForService(r.Context(), middleware.OrgID(r), name, includes, excludes); err != nil {
		h.Logger.Error("replace facet overrides failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "facet_overrides.updated", "service", name, map[string]any{"include": len(includes), "exclude": len(excludes)})

	tr := ParseRange(r, time.Hour)
	httpserver.WriteJSON(w, http.StatusOK, h.buildFacetOverridesResponse(r.Context(), name, tr))
}

// buildFacetOverridesResponse computes the annotated facet list for a
// service. A profile (auto-detection) failure is logged and treated as
// "nothing auto-detected" so the editor still works when ClickHouse is
// briefly unavailable — the user's manual includes/excludes don't depend
// on telemetry.
func (h *Handlers) buildFacetOverridesResponse(ctx context.Context, name string, tr TimeRange) facetOverridesResponse {
	resolver := h.ioResolverFor(ctx, name)
	prof, err := h.Store.ServiceProfile(ctx, resolver, name, tr.From, tr.To)
	if err != nil {
		h.Logger.Warn("service profile failed during facet-overrides resolve", "err", err, "service", name)
	}
	profile := toProfile(name, prof)
	auto := make(map[string]bool)
	for _, f := range h.ServiceFacets.MatchAll(profile) {
		auto[f.Slug] = true
	}
	ov := h.facetOverridesFor(ctx, name)

	rows := make([]facetOverrideRow, 0)
	for _, f := range h.mergedFacets(ctx, middleware.OrgIDFromContext(ctx)) {
		removable := f.Slug != servicetypes.CoreSlug
		isAuto := auto[f.Slug]
		included := ov.Include[f.Slug]
		excluded := ov.Exclude[f.Slug] && removable

		var override *string
		switch {
		case included:
			s := string(facetoverrides.ActionInclude)
			override = &s
		case excluded:
			s := string(facetoverrides.ActionExclude)
			override = &s
		}

		rows = append(rows, facetOverrideRow{
			Slug:         f.Slug,
			Name:         f.Name,
			Description:  f.Description,
			AutoDetected: isAuto,
			Override:     override,
			Effective:    (isAuto || included) && !excluded,
			Removable:    removable,
		})
	}
	return facetOverridesResponse{ServiceName: name, Window: tr.Window(), Facets: rows}
}
