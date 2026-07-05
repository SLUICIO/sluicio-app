// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/schemas"
)

// listSchemas: GET /api/v1/schemas
func (h *Handlers) listSchemas(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Schemas.List(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list schemas failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"schemas": rows})
}

// getSchema: GET /api/v1/schemas/{id}
func (h *Handlers) getSchema(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid schema id")
		return
	}
	sch, err := h.Schemas.Get(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "schema not found")
			return
		}
		h.Logger.Error("get schema failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	usage, err := h.Schemas.UsageFor(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		h.Logger.Warn("schema usage failed", "err", err)
		usage = nil
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"schema": sch,
		"usage":  usage,
	})
}

// createSchema: POST /api/v1/schemas
func (h *Handlers) createSchema(w http.ResponseWriter, r *http.Request) {
	var in schemas.Input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sch, err := h.Schemas.Create(r.Context(), middleware.OrgID(r), in)
	if err != nil {
		if writeSchemaValidationError(w, err) {
			return
		}
		h.Logger.Error("create schema failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "schema.created", "schema", sch.ID.String(), map[string]any{"name": sch.Name})
	httpserver.WriteJSON(w, http.StatusCreated, sch)
}

// updateSchema: PATCH /api/v1/schemas/{id}
func (h *Handlers) updateSchema(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid schema id")
		return
	}
	var in schemas.Input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sch, err := h.Schemas.Update(r.Context(), middleware.OrgID(r), id, in)
	if err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "schema not found")
			return
		}
		if writeSchemaValidationError(w, err) {
			return
		}
		h.Logger.Error("update schema failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "schema.updated", "schema", sch.ID.String(), map[string]any{"name": sch.Name})
	httpserver.WriteJSON(w, http.StatusOK, sch)
}

// deleteSchema: DELETE /api/v1/schemas/{id}
func (h *Handlers) deleteSchema(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid schema id")
		return
	}
	if err := h.Schemas.Delete(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "schema not found")
			return
		}
		h.Logger.Error("delete schema failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "schema.deleted", "schema", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// putServiceSchemas: PUT /api/v1/services/{name}/schemas
//
// Body: {"in_schema_id": "...", "out_schema_id": "..."} — either may
// be null/empty to clear the link in that direction.
func (h *Handlers) putServiceSchemas(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	var req struct {
		InSchemaID  *string `json:"in_schema_id"`
		OutSchemaID *string `json:"out_schema_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	in, err := parseOptionalUUID(req.InSchemaID)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "in_schema_id: "+err.Error())
		return
	}
	out, err := parseOptionalUUID(req.OutSchemaID)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "out_schema_id: "+err.Error())
		return
	}
	if err := h.Schemas.SetServiceSchemas(r.Context(), middleware.OrgID(r), name, in, out); err != nil {
		// FK violation on services(organization_id, service_name) means
		// the service isn't in the catalog yet. Surface that clearly.
		if strings.Contains(err.Error(), "service_schemas_organization_id_service_name_fkey") {
			httpserver.WriteError(w, http.StatusBadRequest,
				"service is not in the catalog yet (no telemetry seen) — try again after the reconciler picks it up")
			return
		}
		h.Logger.Error("set service schemas failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "service_schemas.updated", "service", name, nil)
	// Echo the now-saved pair back to the caller.
	pair, err := h.Schemas.ForService(r.Context(), middleware.OrgID(r), name)
	if err != nil {
		h.Logger.Warn("re-read service schemas failed", "err", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, pair)
}

func parseOptionalUUID(s *string) (*uuid.UUID, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	u, err := uuid.Parse(strings.TrimSpace(*s))
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func writeSchemaValidationError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, schemas.ErrNameExists):
		httpserver.WriteError(w, http.StatusConflict, "a schema with that name already exists")
		return true
	case errors.Is(err, schemas.ErrValidation),
		errors.Is(err, schemas.ErrBadDirection),
		errors.Is(err, schemas.ErrBadKind):
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return true
	}
	return false
}
