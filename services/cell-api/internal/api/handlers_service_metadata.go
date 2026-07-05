// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/servicemeta"
)

// getServiceMetadata: GET /api/v1/services/{name}/metadata
//
// Returns BOTH the built-in fields (description, owner, …) and the
// user-defined metadata: the applicable field defs + the service's
// saved values. The frontend renders the two side by side.
func (h *Handlers) getServiceMetadata(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	m, err := h.ServiceMeta.Get(r.Context(), middleware.OrgID(r), name)
	if err != nil {
		h.Logger.Error("get service metadata failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	allMetadataFields, mfErr := h.Metadata.ListFields(r.Context(), middleware.OrgID(r))
	if mfErr != nil {
		h.Logger.Warn("list metadata fields failed", "err", mfErr)
	}
	serviceFields := scopedFields(allMetadataFields, false)
	values, mvErr := h.serviceMetadataValues(r.Context(), name)
	if mvErr != nil {
		h.Logger.Warn("service metadata values failed", "err", mvErr)
		values = map[string]string{}
	}
	// Linked In-Schema / Out-Schema. Returned with the rest of the
	// service metadata so the detail page can render them in one
	// request. Either side may be null.
	pair, psErr := h.Schemas.ForService(r.Context(), middleware.OrgID(r), name)
	if psErr != nil {
		h.Logger.Warn("service schemas read failed", "err", psErr)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"service_name":    m.ServiceName,
		"description":     m.Description,
		"owner":           m.Owner,
		"on_call":         m.OnCall,
		"team":            m.Team,
		"repository":      m.Repository,
		"runbook_url":     m.RunbookURL,
		"updated_at":      m.UpdatedAt,
		"metadata_fields": serviceFields,
		"metadata_values": values,
		"in_schema":       pair.In,
		"out_schema":      pair.Out,
	})
}

// putServiceMetadata: PUT /api/v1/services/{name}/metadata
func (h *Handlers) putServiceMetadata(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	var req servicemeta.Metadata
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	req.Owner = strings.TrimSpace(req.Owner)
	req.OnCall = strings.TrimSpace(req.OnCall)
	req.Team = strings.TrimSpace(req.Team)
	req.Repository = strings.TrimSpace(req.Repository)
	req.RunbookURL = strings.TrimSpace(req.RunbookURL)
	if req.RunbookURL != "" && !strings.HasPrefix(req.RunbookURL, "http://") && !strings.HasPrefix(req.RunbookURL, "https://") {
		httpserver.WriteError(w, http.StatusBadRequest, "runbook_url must be an http(s) URL")
		return
	}
	out, err := h.ServiceMeta.Upsert(r.Context(), middleware.OrgID(r), name, req)
	if err != nil {
		h.Logger.Error("upsert service metadata failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "service_metadata.updated", "service", name, nil)
	httpserver.WriteJSON(w, http.StatusOK, out)
}
