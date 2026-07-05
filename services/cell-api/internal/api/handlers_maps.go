// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/mapexec"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/maps"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/schemas"
)

// listMaps: GET /api/v1/maps
func (h *Handlers) listMaps(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Maps.List(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list maps failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"maps": rows})
}

// getMap: GET /api/v1/maps/{id}
func (h *Handlers) getMap(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid map id")
		return
	}
	m, err := h.Maps.Get(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		if errors.Is(err, maps.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "map not found")
			return
		}
		h.Logger.Error("get map failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"map": m})
}

// createMap: POST /api/v1/maps
func (h *Handlers) createMap(w http.ResponseWriter, r *http.Request) {
	var in maps.Input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	m, err := h.Maps.Create(r.Context(), middleware.OrgID(r), in)
	if err != nil {
		if writeMapValidationError(w, err) {
			return
		}
		h.Logger.Error("create map failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "map.created", "map", m.ID.String(), map[string]any{"name": m.Name})
	httpserver.WriteJSON(w, http.StatusCreated, m)
}

// updateMap: PATCH /api/v1/maps/{id}
func (h *Handlers) updateMap(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid map id")
		return
	}
	var in maps.Input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	m, err := h.Maps.Update(r.Context(), middleware.OrgID(r), id, in)
	if err != nil {
		if errors.Is(err, maps.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "map not found")
			return
		}
		if writeMapValidationError(w, err) {
			return
		}
		h.Logger.Error("update map failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "map.updated", "map", m.ID.String(), map[string]any{"name": m.Name})
	httpserver.WriteJSON(w, http.StatusOK, m)
}

// deleteMap: DELETE /api/v1/maps/{id}
func (h *Handlers) deleteMap(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid map id")
		return
	}
	if err := h.Maps.Delete(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, maps.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "map not found")
			return
		}
		h.Logger.Error("delete map failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "map.deleted", "map", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// executeMap: POST /api/v1/maps/{id}/execute
//
// Runs the map's transformation against the request's `input` field
// and optionally validates the input against the from-schema and the
// output against the to-schema, returning a single response shape:
//
//	{
//	  "output": "<transformed>",
//	  "engine_error": "<optional libxslt / liquid diagnostic>",
//	  "input_validation":  { "skipped": ..., "valid": ..., "errors": [...] },
//	  "output_validation": { "skipped": ..., "valid": ..., "errors": [...] }
//	}
//
// The endpoint is intentionally permissive: a malformed input or a
// failing validation isn't an HTTP error — the UI shows the validation
// status inline. We only return 4xx/5xx when the request itself is
// bad (no map id, unknown format) or when the runtime can't run at
// all (xsltproc missing).
func (h *Handlers) executeMap(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid map id")
		return
	}
	var req struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	m, err := h.Maps.Get(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		if errors.Is(err, maps.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "map not found")
			return
		}
		h.Logger.Error("execute map: get failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// Resolve schemas BEFORE running the transformation so an unknown
	// schema id surfaces early (a stale pin is a config error worth
	// reporting up-front).
	fromName, fromFmt, fromContent, ferr := h.fetchSchemaForValidation(r, m.FromSchemaID)
	if ferr != nil {
		h.Logger.Warn("execute map: from-schema fetch failed", "err", ferr)
	}
	toName, toFmt, toContent, terr := h.fetchSchemaForValidation(r, m.ToSchemaID)
	if terr != nil {
		h.Logger.Warn("execute map: to-schema fetch failed", "err", terr)
	}

	// Run the transformation.
	result, runErr := mapexec.Execute(r.Context(), m.Format, m.Content, req.Input)
	if runErr != nil {
		if errors.Is(runErr, mapexec.ErrUnsupportedFormat) {
			httpserver.WriteError(w, http.StatusBadRequest, runErr.Error())
			return
		}
		if errors.Is(runErr, mapexec.ErrToolMissing) {
			httpserver.WriteError(w, http.StatusServiceUnavailable, runErr.Error())
			return
		}
		h.Logger.Error("execute map: runtime failed", "err", runErr)
		httpserver.WriteError(w, http.StatusInternalServerError, "execution failed")
		return
	}

	// Validate input and output against their pinned schemas (if any).
	var inputVal, outputVal mapexec.ValidationResult
	if m.FromSchemaID != nil {
		inputVal = mapexec.Validate(req.Input, fromName, fromFmt, fromContent)
	} else {
		inputVal = mapexec.ValidationResult{
			Skipped:    true,
			SkipReason: "no from-schema pinned on this map",
		}
	}
	if m.ToSchemaID != nil {
		outputVal = mapexec.Validate(result.Output, toName, toFmt, toContent)
	} else {
		outputVal = mapexec.ValidationResult{
			Skipped:    true,
			SkipReason: "no to-schema pinned on this map",
		}
	}

	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"output":            result.Output,
		"engine_error":      result.EngineError,
		"input_validation":  inputVal,
		"output_validation": outputVal,
	})
}

// fetchSchemaForValidation loads a schema by id and returns the
// fields needed for validation, or zero values + nil error when the
// id is nil. A real fetch error returns the error so the caller can
// log it; validation degrades gracefully (treated as no schema).
func (h *Handlers) fetchSchemaForValidation(r *http.Request, schemaID *uuid.UUID) (name, format, content string, err error) {
	if schemaID == nil {
		return "", "", "", nil
	}
	sch, err := h.Schemas.Get(r.Context(), middleware.OrgID(r), *schemaID)
	if err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			return "", "", "", nil // pinned schema was deleted; treat as no schema
		}
		return "", "", "", err
	}
	return sch.Name, sch.Format, sch.Content, nil
}

func writeMapValidationError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, maps.ErrNameExists):
		httpserver.WriteError(w, http.StatusConflict, "a map with that name and version already exists")
		return true
	case errors.Is(err, maps.ErrBadSchemaRef):
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return true
	case errors.Is(err, maps.ErrValidation):
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return true
	}
	return false
}
