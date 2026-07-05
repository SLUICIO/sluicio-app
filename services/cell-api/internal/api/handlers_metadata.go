// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/metadata"
)

// listMetadataFields: GET /api/v1/metadata-fields
func (h *Handlers) listMetadataFields(w http.ResponseWriter, r *http.Request) {
	fields, err := h.Metadata.ListFields(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list metadata fields failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"fields": fields})
}

// createMetadataField: POST /api/v1/metadata-fields
func (h *Handlers) createMetadataField(w http.ResponseWriter, r *http.Request) {
	var in metadata.FieldInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	f, err := h.Metadata.CreateField(r.Context(), middleware.OrgID(r), in)
	if err != nil {
		if writeMetadataValidationError(w, err) {
			return
		}
		h.Logger.Error("create metadata field failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "metadata_field.created", "metadata_field", f.ID.String(), map[string]any{"key": f.Key})
	httpserver.WriteJSON(w, http.StatusCreated, f)
}

// updateMetadataField: PATCH /api/v1/metadata-fields/{id}
func (h *Handlers) updateMetadataField(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid field id")
		return
	}
	var in metadata.FieldInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	f, err := h.Metadata.UpdateField(r.Context(), middleware.OrgID(r), id, in)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "field not found")
			return
		}
		if writeMetadataValidationError(w, err) {
			return
		}
		h.Logger.Error("update metadata field failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "metadata_field.updated", "metadata_field", f.ID.String(), map[string]any{"key": f.Key})
	httpserver.WriteJSON(w, http.StatusOK, f)
}

// deleteMetadataField: DELETE /api/v1/metadata-fields/{id}
func (h *Handlers) deleteMetadataField(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid field id")
		return
	}
	if err := h.Metadata.DeleteField(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "field not found")
			return
		}
		h.Logger.Error("delete metadata field failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "metadata_field.deleted", "metadata_field", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// putIntegrationMetadata: PUT /api/v1/integrations/{id}/metadata
//
// Body is a key → value map. Empty values clear the field.
func (h *Handlers) putIntegrationMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	values, err := decodeValueMap(r)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Metadata.SetIntegrationValues(r.Context(), middleware.OrgID(r), id, values); err != nil {
		if writeMetadataValidationError(w, err) {
			return
		}
		h.Logger.Error("set integration metadata failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "integration_metadata.updated", "integration", id.String(), nil)
	// Echo the saved value map back to the caller in key form.
	out, err := h.integrationMetadataValues(r.Context(), id)
	if err != nil {
		h.Logger.Warn("re-read integration metadata after save failed", "err", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"metadata_values": out})
}

// putServiceMetadataExtras: PUT /api/v1/services/{name}/metadata-extras
func (h *Handlers) putServiceMetadataExtras(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	values, err := decodeValueMap(r)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Metadata.SetServiceValues(r.Context(), middleware.OrgID(r), name, values); err != nil {
		if writeMetadataValidationError(w, err) {
			return
		}
		h.Logger.Error("set service metadata failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "service_metadata_extras.updated", "service", name, nil)
	out, err := h.serviceMetadataValues(r.Context(), name)
	if err != nil {
		h.Logger.Warn("re-read service metadata after save failed", "err", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"metadata_values": out})
}

// ── helpers ───────────────────────────────────────────────────────────

// decodeValueMap accepts {"key": "value", ...} from a request body.
func decodeValueMap(r *http.Request) (map[string]string, error) {
	if r.ContentLength == 0 {
		return map[string]string{}, nil
	}
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, errors.New("invalid JSON body")
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch t := v.(type) {
		case string:
			out[k] = t
		case bool:
			if t {
				out[k] = "true"
			} else {
				out[k] = "false"
			}
		case float64:
			// JSON numbers come in as float64; FormatFloat with -1
			// precision trims trailing zeros automatically (1.0 -> "1",
			// 1.5 -> "1.5").
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		case nil:
			out[k] = ""
		default:
			return nil, errors.New("metadata values must be string / number / boolean")
		}
	}
	return out, nil
}

// integrationMetadataValues returns the saved values for an integration
// keyed by field key (not id) so the frontend can use them directly.
func (h *Handlers) integrationMetadataValues(ctx context.Context, id uuid.UUID) (map[string]string, error) {
	fields, err := h.Metadata.ListFields(ctx, middleware.OrgIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	vals, err := h.Metadata.IntegrationValues(ctx, id)
	if err != nil {
		return nil, err
	}
	return remapByKey(fields, vals), nil
}

// serviceMetadataValues mirrors integrationMetadataValues for services.
func (h *Handlers) serviceMetadataValues(ctx context.Context, name string) (map[string]string, error) {
	fields, err := h.Metadata.ListFields(ctx, middleware.OrgIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	vals, err := h.Metadata.ServiceValues(ctx, middleware.OrgIDFromContext(ctx), name)
	if err != nil {
		return nil, err
	}
	return remapByKey(fields, vals), nil
}

// remapByKey converts a field-id → value map into a key → value map.
func remapByKey(fields []metadata.Field, byID map[uuid.UUID]string) map[string]string {
	keyByID := make(map[uuid.UUID]string, len(fields))
	for _, f := range fields {
		keyByID[f.ID] = f.Key
	}
	out := make(map[string]string, len(byID))
	for id, v := range byID {
		if k, ok := keyByID[id]; ok {
			out[k] = v
		}
	}
	return out
}

// scopedFields filters the org field list to those that apply to the
// given scope (integration or service).
func scopedFields(fields []metadata.Field, integrationScope bool) []metadata.Field {
	out := make([]metadata.Field, 0, len(fields))
	for _, f := range fields {
		if integrationScope && !f.AppliesToIntegration {
			continue
		}
		if !integrationScope && !f.AppliesToService {
			continue
		}
		out = append(out, f)
	}
	return out
}

// writeMetadataValidationError handles the validation-style errors from
// the metadata package and writes the right HTTP status. Returns true if
// it handled the error.
func writeMetadataValidationError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, metadata.ErrFieldExists):
		httpserver.WriteError(w, http.StatusConflict, "a field with that key already exists")
		return true
	case errors.Is(err, metadata.ErrValidation),
		errors.Is(err, metadata.ErrUnknownType),
		errors.Is(err, metadata.ErrNoScope),
		errors.Is(err, metadata.ErrInvalidValue),
		errors.Is(err, metadata.ErrFieldRequired),
		errors.Is(err, metadata.ErrFieldWrongType):
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return true
	}
	return false
}
