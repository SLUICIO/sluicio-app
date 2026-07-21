// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetmappings"
)

// createFacetMappingRequest is the body of POST
// /api/v1/services/{name}/facet-mappings. The path parameter is the
// canonical source of the service name — the body field is not
// accepted to avoid two-way ambiguity.
type createFacetMappingRequest struct {
	AttributeSource string `json:"attribute_source"`
	AttributeKey    string `json:"attribute_key"`
	MatchOperator   string `json:"match_operator"`
	MatchValue      string `json:"match_value"`
	SetIOKind       string `json:"set_io_kind"`
	SetIORole       string `json:"set_io_role"`
}

// listFacetMappings: GET /api/v1/services/{name}/facet-mappings
//
// Returns every user-defined attribute mapping for the service. The
// mappings are ordered by creation time so the UI can render them in
// the same order they're applied at query time — first match wins
// in the SQL CASE-WHEN cascade.
func (h *Handlers) listFacetMappings(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	rows, err := h.FacetMappings.ListForService(r.Context(), middleware.OrgID(r), name)
	if err != nil {
		h.Logger.Error("list facet mappings failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"service_name": name,
		"mappings":     rows,
	})
}

// createFacetMapping: POST /api/v1/services/{name}/facet-mappings
func (h *Handlers) createFacetMapping(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	var req createFacetMappingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Normalise enum-like fields so a caller that sends "Span" or
	// " equals " doesn't get rejected. Validate() does the rest.
	m := facetmappings.Mapping{
		OrganizationID:  middleware.OrgID(r),
		ServiceName:     name,
		AttributeSource: facetmappings.AttributeSource(strings.ToLower(strings.TrimSpace(req.AttributeSource))),
		AttributeKey:    strings.TrimSpace(req.AttributeKey),
		MatchOperator:   facetmappings.Operator(strings.ToLower(strings.TrimSpace(req.MatchOperator))),
		MatchValue:      req.MatchValue,
		SetIOKind:       strings.ToLower(strings.TrimSpace(req.SetIOKind)),
		SetIORole:       strings.ToLower(strings.TrimSpace(req.SetIORole)),
	}
	if err := m.Validate(); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := h.FacetMappings.Create(r.Context(), m)
	if err != nil {
		h.Logger.Error("create facet mapping failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.recordAudit(r, "facet_mapping.created", "facet_mapping", created.ID.String(), map[string]any{"service_name": name})
	httpserver.WriteJSON(w, http.StatusCreated, created)
}

// deleteFacetMapping: DELETE /api/v1/services/{name}/facet-mappings/{id}
//
// The path includes the service name even though the ID alone is
// enough to find the row — keeping the service in the URL makes the
// route discoverable from the service detail page and matches the
// pattern used by service-tag endpoints.
func (h *Handlers) deleteFacetMapping(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid mapping id")
		return
	}
	if err := h.FacetMappings.Delete(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, facetmappings.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "facet mapping not found")
			return
		}
		h.Logger.Error("delete facet mapping failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "facet_mapping.deleted", "facet_mapping", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}
