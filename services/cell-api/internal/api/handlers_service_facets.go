// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Custom service-facet management: create / rename / delete the org-defined
// facets that merge with the built-in registry (see mergedFacets). Custom
// facets are classification labels (no widgets) assigned to services via facet
// overrides. Editor+ only.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/servicefacets"
)

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugifyFacet derives a url-safe slug from a display name.
func slugifyFacet(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nonSlugChars.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// createServiceFacet: POST /api/v1/service-facets
func (h *Handlers) createServiceFacet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	slug := slugifyFacet(name)
	if slug == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name must contain a letter or digit")
		return
	}
	// Don't shadow a built-in facet slug.
	if h.ServiceFacets.Get(slug) != nil {
		httpserver.WriteError(w, http.StatusConflict, "a built-in facet already uses that name")
		return
	}
	orgID := middleware.OrgID(r)
	f, err := h.ServiceFacetsCustom.Create(r.Context(), orgID, slug, name, strings.TrimSpace(body.Description))
	if err != nil {
		if strings.Contains(err.Error(), "service_facets_org_id_slug_key") || strings.Contains(err.Error(), "duplicate") {
			httpserver.WriteError(w, http.StatusConflict, "a facet with that name already exists")
			return
		}
		h.Logger.Error("create service facet failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "service_facet.created", "service_facet", f.Slug, map[string]any{"name": f.Name})
	httpserver.WriteJSON(w, http.StatusCreated, f)
}

// updateServiceFacet: PUT /api/v1/service-facets/{slug} — custom facets only.
func (h *Handlers) updateServiceFacet(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if h.ServiceFacets.Get(slug) != nil {
		httpserver.WriteError(w, http.StatusForbidden, "built-in facets can't be edited")
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	f, err := h.ServiceFacetsCustom.UpdateBySlug(r.Context(), middleware.OrgID(r), slug, name, strings.TrimSpace(body.Description))
	if errors.Is(err, servicefacets.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "facet not found")
		return
	}
	if err != nil {
		h.Logger.Error("update service facet failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "service_facet.updated", "service_facet", f.Slug, map[string]any{"name": f.Name})
	httpserver.WriteJSON(w, http.StatusOK, f)
}

// deleteServiceFacet: DELETE /api/v1/service-facets/{slug} — custom facets only.
func (h *Handlers) deleteServiceFacet(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if h.ServiceFacets.Get(slug) != nil {
		httpserver.WriteError(w, http.StatusForbidden, "built-in facets can't be deleted")
		return
	}
	err := h.ServiceFacetsCustom.DeleteBySlug(r.Context(), middleware.OrgID(r), slug)
	if errors.Is(err, servicefacets.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "facet not found")
		return
	}
	if err != nil {
		h.Logger.Error("delete service facet failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "service_facet.deleted", "service_facet", slug, nil)
	w.WriteHeader(http.StatusNoContent)
}
