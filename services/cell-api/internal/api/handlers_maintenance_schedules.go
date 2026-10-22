// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Recurring maintenance windows (issue #9): "every Sunday 02:00-04:00,
// patch night, don't page anyone".
//
// A schedule is the series; each occurrence is an ordinary maintenance
// window row that MaterializeSchedules creates when it is next, linked by
// schedule_id. The alert engine, suppression stamping, announcements and
// "end early" are therefore untouched: an occurrence IS a window. Only
// one occurrence per series exists ahead of time, so the window list
// shows what is coming next, and cancelling it before it starts skips
// that occurrence (the store records the skip; see EndWindow).
//
// Permissions are the one-shot window's: editors, and an org admin for a
// series that silences the whole organization.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/cellhealth"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/maintenance"
)

type scheduleBody struct {
	Name       string                  `json:"name"`
	Reason     string                  `json:"reason"`
	Scope      maintenance.WindowScope `json:"scope"`
	Recurrence maintenance.Recurrence  `json:"recurrence"`
	Announce   bool                    `json:"announce"`
}

// validateSchedule checks the body. The scope is checked as a window's,
// but its system members are NOT snapshotted here: each occurrence takes
// its own snapshot when it is created, so a series running for months
// follows the system as it changes.
func (h *Handlers) validateSchedule(r *http.Request, body scheduleBody) (maintenance.Schedule, error) {
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return maintenance.Schedule{}, errors.New("name is required")
	}
	if err := body.Recurrence.Validate(); err != nil {
		return maintenance.Schedule{}, err
	}
	orgID := middleware.OrgID(r)
	scope, err := h.normalizeWindowScope(r.Context(), orgID, body.Scope)
	if err != nil {
		return maintenance.Schedule{}, err
	}
	scope.ServiceNamesExpanded = nil
	return maintenance.Schedule{
		OrgID:      orgID,
		Name:       name,
		Reason:     strings.TrimSpace(body.Reason),
		Scope:      scope,
		Recurrence: body.Recurrence.Normalized(),
		Announce:   body.Announce,
	}, nil
}

// listMaintenanceSchedules: GET /api/v1/maintenance-schedules (viewer+)
func (h *Handlers) listMaintenanceSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := h.Maintenance.ListSchedules(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list maintenance schedules failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not load recurring maintenance windows")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"schedules": list})
}

// createMaintenanceSchedule: POST /api/v1/maintenance-schedules (editor+;
// all_org scope needs admin)
func (h *Handlers) createMaintenanceSchedule(w http.ResponseWriter, r *http.Request) {
	var body scheduleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sc, err := h.validateSchedule(r, body)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	p := middleware.Principal(r)
	if sc.Scope.Kind == "all_org" && !p.Role.CanAdmin() {
		httpserver.WriteError(w, http.StatusForbidden, "silencing the whole organization requires an org admin")
		return
	}
	sc.CreatedBy = p.UserID
	created, err := h.Maintenance.CreateSchedule(r.Context(), sc)
	if err != nil {
		h.Logger.Error("create maintenance schedule failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not create recurring maintenance window")
		return
	}
	// The first occurrence exists as soon as the series does, so the
	// window list shows it and it can be skipped right away.
	if err := h.materializeSchedule(r.Context(), created); err != nil {
		h.Logger.Warn("first occurrence not created; the loop will retry", "schedule", created.ID, "err", err)
	}
	h.recordAudit(r, "maintenance_schedule.created", "maintenance_schedule", created.ID.String(),
		map[string]any{"name": created.Name, "scope": created.Scope, "recurrence": created.Recurrence})
	httpserver.WriteJSON(w, http.StatusCreated, created)
}

// updateMaintenanceSchedule: PATCH /api/v1/maintenance-schedules/{id}
//
// An occurrence that has not started is replaced by one under the new
// definition. One in progress is left to finish as it was set up: moving
// the goalposts of a window someone is working inside would either cut
// their silence short or stretch it without them knowing.
func (h *Handlers) updateMaintenanceSchedule(w http.ResponseWriter, r *http.Request) {
	existing, ok := h.scheduleForWrite(w, r)
	if !ok {
		return
	}
	var body scheduleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sc, err := h.validateSchedule(r, body)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if sc.Scope.Kind == "all_org" && !middleware.Principal(r).Role.CanAdmin() {
		httpserver.WriteError(w, http.StatusForbidden, "org-wide windows require an org admin")
		return
	}
	sc.ID = existing.ID
	updated, err := h.Maintenance.UpdateSchedule(r.Context(), existing.OrgID, sc)
	if err != nil {
		h.Logger.Error("update maintenance schedule failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not update recurring maintenance window")
		return
	}
	h.removeUpcomingOccurrences(r.Context(), updated.OrgID, updated.ID)
	if err := h.materializeSchedule(r.Context(), updated); err != nil {
		h.Logger.Warn("occurrence not recreated; the loop will retry", "schedule", updated.ID, "err", err)
	}
	h.recordAudit(r, "maintenance_schedule.updated", "maintenance_schedule", updated.ID.String(),
		map[string]any{"name": updated.Name, "scope": updated.Scope, "recurrence": updated.Recurrence})
	httpserver.WriteJSON(w, http.StatusOK, updated)
}

// deleteMaintenanceSchedule: DELETE /api/v1/maintenance-schedules/{id}
// ends the series: an occurrence in progress ends now (its row stays, as
// for a window ended early), one not yet started is removed, and no
// further ones are created.
func (h *Handlers) deleteMaintenanceSchedule(w http.ResponseWriter, r *http.Request) {
	existing, ok := h.scheduleForWrite(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	wins, err := h.Maintenance.ScheduleWindows(ctx, existing.OrgID, existing.ID)
	if err != nil {
		h.Logger.Error("delete maintenance schedule: occurrences", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not end recurring maintenance window")
		return
	}
	for _, win := range wins {
		if win.Active {
			if _, err := h.Maintenance.EndWindow(ctx, existing.OrgID, win.ID); err != nil {
				h.Logger.Error("delete maintenance schedule: end occurrence", "err", err)
				httpserver.WriteError(w, http.StatusInternalServerError, "could not end recurring maintenance window")
				return
			}
		} else if err := h.Maintenance.DeleteWindowRow(ctx, existing.OrgID, win.ID); err != nil {
			h.Logger.Error("delete maintenance schedule: remove occurrence", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "could not end recurring maintenance window")
			return
		}
		h.removeWindowAnnouncement(ctx, win)
	}
	if err := h.Maintenance.DeleteSchedule(ctx, existing.OrgID, existing.ID); err != nil {
		h.Logger.Error("delete maintenance schedule failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not end recurring maintenance window")
		return
	}
	h.recordAudit(r, "maintenance_schedule.deleted", "maintenance_schedule", existing.ID.String(),
		map[string]any{"name": existing.Name})
	w.WriteHeader(http.StatusNoContent)
}

// scheduleForWrite loads the series named in the path and checks the
// caller may change it. It writes the error response itself.
func (h *Handlers) scheduleForWrite(w http.ResponseWriter, r *http.Request) (maintenance.Schedule, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid schedule id")
		return maintenance.Schedule{}, false
	}
	sc, err := h.Maintenance.GetSchedule(r.Context(), middleware.OrgID(r), id)
	if errors.Is(err, maintenance.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "recurring maintenance window not found")
		return maintenance.Schedule{}, false
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not load recurring maintenance window")
		return maintenance.Schedule{}, false
	}
	if sc.Scope.Kind == "all_org" && !middleware.Principal(r).Role.CanAdmin() {
		httpserver.WriteError(w, http.StatusForbidden, "org-wide windows require an org admin")
		return maintenance.Schedule{}, false
	}
	return sc, true
}

// removeUpcomingOccurrences deletes a series' occurrences that have not
// started, so they can be created again from an edited definition.
func (h *Handlers) removeUpcomingOccurrences(ctx context.Context, orgID, scheduleID uuid.UUID) {
	wins, err := h.Maintenance.ScheduleWindows(ctx, orgID, scheduleID)
	if err != nil {
		h.Logger.Warn("upcoming occurrences not loaded", "schedule", scheduleID, "err", err)
		return
	}
	now := time.Now()
	for _, win := range wins {
		if !now.Before(win.StartsAt) {
			continue
		}
		if err := h.Maintenance.DeleteWindowRow(ctx, orgID, win.ID); err != nil {
			h.Logger.Warn("upcoming occurrence not removed", "window", win.ID, "err", err)
			continue
		}
		h.removeWindowAnnouncement(ctx, win)
	}
}

func (h *Handlers) removeWindowAnnouncement(ctx context.Context, win maintenance.Window) {
	if win.AnnouncementID == nil {
		return
	}
	orgID := win.OrgID
	if err := h.Maintenance.DeleteAnnouncement(ctx, *win.AnnouncementID, &orgID); err != nil &&
		!errors.Is(err, maintenance.ErrNotFound) {
		h.Logger.Warn("window announcement cleanup failed", "window", win.ID, "err", err)
	}
}

// materializeSchedule makes sure a series' current or next occurrence
// exists as a window. It walks the occurrences in order: one already
// created and still running or upcoming means there is nothing to do; one
// created and ended early is passed over; the first one not created is
// created. Idempotent, and safe against a concurrent run through the
// unique (schedule_id, starts_at) index.
func (h *Handlers) materializeSchedule(ctx context.Context, sc maintenance.Schedule) error {
	now := time.Now()
	for _, occ := range sc.Recurrence.Next(now, 8, sc.SkippedStarts) {
		existing, found, err := h.Maintenance.OccurrenceWindow(ctx, sc.ID, occ.StartsAt)
		if err != nil {
			return err
		}
		if found {
			if existing.EndsAt.After(now) {
				return nil
			}
			continue // ended early; the next one is due
		}
		scope, err := h.normalizeWindowScope(ctx, sc.OrgID, sc.Scope)
		if err != nil {
			return err
		}
		id := sc.ID
		created, err := h.Maintenance.CreateWindow(ctx, maintenance.Window{
			OrgID:      sc.OrgID,
			Name:       sc.Name,
			Reason:     sc.Reason,
			StartsAt:   occ.StartsAt,
			EndsAt:     occ.EndsAt,
			Scope:      scope,
			ScheduleID: &id,
			CreatedBy:  sc.CreatedBy,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return nil // another run created it first
			}
			return err
		}
		if sc.Announce {
			h.attachWindowAnnouncementCtx(ctx, created)
		}
		return nil
	}
	return nil
}

// MaterializeSchedules creates the due occurrence of every series, in
// every organization. One bad series does not stop the others.
func (h *Handlers) MaterializeSchedules(ctx context.Context) {
	if h.Maintenance == nil {
		return
	}
	all, err := h.Maintenance.AllSchedules(ctx)
	if err != nil {
		h.Logger.Warn("maintenance schedules not loaded", "err", err)
		return
	}
	for _, sc := range all {
		if err := h.materializeSchedule(ctx, sc); err != nil {
			h.Logger.Warn("maintenance occurrence not created", "schedule", sc.ID, "err", err)
		}
	}
}

// maintenanceScheduleInterval is how often occurrences are created. An
// occurrence is created as soon as the previous one ends, normally days
// ahead, so this only bounds the delay after an end-early or a restart.
const maintenanceScheduleInterval = time.Minute

// RunMaintenanceSchedules runs MaterializeSchedules until ctx ends.
func (h *Handlers) RunMaintenanceSchedules(ctx context.Context) {
	cellhealth.Register("maintenance-schedules", maintenanceScheduleInterval)
	t := time.NewTicker(maintenanceScheduleInterval)
	defer t.Stop()
	for {
		h.MaterializeSchedules(ctx)
		// End of cycle, not start: a loop wedged inside its own body is
		// what the heartbeat catches.
		cellhealth.Beat("maintenance-schedules")
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
