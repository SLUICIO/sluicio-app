// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
)

// clearServiceErrors: POST /api/v1/services/{name}/clear-errors
//
// The maintenance team marks a service's current failures as reviewed.
// Sets a watermark (now) + optional comment; service health + error
// count then ignore error traces at or before it, so the service reads
// healthy again until NEW failures arrive. The failed traces themselves
// are untouched — this only moves the "everything before here is
// handled" line.
func (h *Handlers) clearServiceErrors(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	if h.ErrorAcks == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "error acknowledgements unavailable")
		return
	}
	var body struct {
		Comment string `json:"comment"`
	}
	// Body is optional; ignore a decode error on an empty/absent body.
	_ = json.NewDecoder(r.Body).Decode(&body)

	ack, err := h.ErrorAcks.Upsert(r.Context(), middleware.OrgID(r), name, time.Now(),
		strings.TrimSpace(body.Comment), middleware.Principal(r).UserID)
	if err != nil {
		h.Logger.Error("clear service errors failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "clear failed")
		return
	}
	// Clearing errors should also clear a failed-trace health check on this
	// service — the watermark already mutes the built-in open-error signal +
	// the window count, but a firing trace_error instance (sticky/manual
	// resolve) would otherwise keep the service red. The evaluator honours the
	// same watermark, so the check won't immediately re-fire on the cleared
	// errors. Best-effort: a failure here doesn't undo the clear.
	if h.Alerts != nil {
		if n, rerr := h.Alerts.ResolveErrorTraceInstancesForService(r.Context(), middleware.OrgID(r), name); rerr != nil {
			h.Logger.Warn("clear errors: resolve error-trace checks failed", "err", rerr, "service", name)
		} else if n > 0 {
			h.Logger.Info("clear errors: resolved error-trace health checks", "service", name, "count", n)
		}
	}
	if ack.AcknowledgedBy != nil && h.Identity != nil {
		if u, uerr := h.Identity.GetUserByID(r.Context(), *ack.AcknowledgedBy); uerr == nil {
			ack.AcknowledgedByName = u.Name
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, ack)
}

// unclearServiceErrors: DELETE /api/v1/services/{name}/clear-errors
// Removes the watermark, so all in-window errors count again.
func (h *Handlers) unclearServiceErrors(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	if h.ErrorAcks == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "error acknowledgements unavailable")
		return
	}
	if err := h.ErrorAcks.Delete(r.Context(), middleware.OrgID(r), name); err != nil {
		h.Logger.Error("unclear service errors failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "unclear failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serviceReadings: GET /api/v1/services/{name}/readings
//
// The "show on service page" health checks bound to this service, each
// with its latest reading + breach state — the value tiles on the
// service detail page. Health checks are unified with custom metrics:
// a telemetry check's reading is the value the evaluator computed; a
// pushed check's reading is the last value POSTed in.
func (h *Handlers) serviceReadings(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	readings, err := h.Alerts.ServiceReadings(r.Context(), middleware.OrgID(r), name)
	if err != nil {
		h.Logger.Error("service readings failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"service_name": name,
		"readings":     readings,
	})
}

type pushValueRequest struct {
	Value float64 `json:"value"`
}

// pushHealthCheckValue: POST /api/v1/services/{name}/health-checks/{id}/value
//
// External scrapers (e.g. a queue-depth poller) push the current
// observation for a pushed-source health check here. The value is
// recorded as the check's latest reading; the evaluator compares it to
// the threshold on its next tick to drive health + notifications.
func (h *Handlers) pushHealthCheckValue(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	id, err := uuid.Parse(r.PathValue("id"))
	if name == "" || err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "service name and a valid health-check id are required")
		return
	}
	var req pushValueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	rule, err := h.Alerts.GetRule(r.Context(), middleware.OrgID(r), id)
	if errors.Is(err, alerting.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "health check not found")
		return
	}
	if err != nil {
		h.Logger.Error("push value: get rule failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if rule.ServiceName != name {
		httpserver.WriteError(w, http.StatusNotFound, "health check not found for this service")
		return
	}
	if rule.Source != alerting.SourcePushed {
		httpserver.WriteError(w, http.StatusBadRequest, "this health check is telemetry-sourced; values cannot be pushed to it")
		return
	}
	if err := h.Alerts.RecordReading(r.Context(), rule.ID, req.Value); err != nil {
		h.Logger.Error("push value: record reading failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "push failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
