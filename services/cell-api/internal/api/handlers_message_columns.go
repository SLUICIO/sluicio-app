// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Promoted message columns (issue #23): which span attributes an
// integration surfaces as columns in its message list.

package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
)

// putIntegrationMessageColumns: PUT /api/v1/integrations/{id}/message-columns
//
// A whole-list PUT rather than add/remove/reorder verbs. Order is the
// column order, so every edit rewrites the list anyway, and one endpoint
// cannot disagree with itself about what position 2 means.
func (h *Handlers) putIntegrationMessageColumns(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Columns []integrations.MessageColumn `json:"columns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.Integrations.SetMessageColumns(r.Context(), middleware.OrgID(r), id, body.Columns); err != nil {
		if errors.Is(err, integrations.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "integration not found")
			return
		}
		// A rejected list is the caller's mistake, not the server's:
		// too many columns, a blank key, an over-long label. Report it
		// verbatim so the UI can show the reason instead of "failed".
		if errors.Is(err, integrations.ErrInvalidMessageColumns) {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("set message columns failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	// Re-read rather than echoing the request: normalisation fills
	// labels and drops duplicates, so what the caller sent is not
	// necessarily what is stored, and the UI must render what is.
	full, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		h.Logger.Error("re-read integration after message columns", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "integration.message_columns_set", "integration", id.String(),
		map[string]any{"count": len(full.MessageColumns)})
	cols := full.MessageColumns
	if len(cols) == 0 {
		// Saving an empty list is how you reset to defaults, so echo
		// what the list will actually render rather than the empty
		// array — the editor shows what it gets back.
		cols = integrations.DefaultMessageColumns()
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"message_columns": cols})
}

// putIntegrationMessageFilters: PUT /api/v1/integrations/{id}/message-filters
//
// Replaces which attributes this integration may be filtered by, and
// what each is labelled (issue #31). An empty list clears the
// restriction and goes back to offering every attribute, which is how
// an editor undoes the whole thing.
func (h *Handlers) putIntegrationMessageFilters(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Filters []integrations.MessageFilter `json:"filters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.Integrations.SetMessageFilters(r.Context(), middleware.OrgID(r), id, body.Filters); err != nil {
		if errors.Is(err, integrations.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "integration not found")
			return
		}
		// Over the cap is the caller's mistake and is reported verbatim,
		// because "at most 20 filter fields" is actionable and "failed"
		// is not.
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.recordAudit(r, "integration.message_filters_updated", "integration", id.String(),
		map[string]any{"count": len(body.Filters)})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"filters": body.Filters})
}
