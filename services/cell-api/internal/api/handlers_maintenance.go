// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Announcements + maintenance windows. Design:
// docs/maintenance-and-announcements-design.md.
//
// Announcements are organizational communication — the read path is
// every authenticated user in the org (plus cell-wide rows) and is
// deliberately NOT filtered by group visibility policies. Management is
// org-admin (org rows) / operator (cell-wide rows).
//
// Maintenance windows silence alert *delivery* for a scope while active.
// Editors create/edit/end windows for entities/group scopes; all_org
// windows need an org admin. Everything is audited.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/maintenance"
)

// ── announcements: user-facing read + dismiss ────────────────────────

// listMyAnnouncements: GET /api/v1/announcements
func (h *Handlers) listMyAnnouncements(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	if h.Maintenance == nil || p.UserID == nil {
		// Service accounts have no banner surface; empty is correct.
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"announcements": []any{}})
		return
	}
	list, err := h.Maintenance.ActiveForUser(r.Context(), p.OrgID, *p.UserID)
	if err != nil {
		h.Logger.Error("list announcements failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not load announcements")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"announcements": list})
}

// dismissAnnouncement: POST /api/v1/announcements/{id}/dismiss
func (h *Handlers) dismissAnnouncement(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusBadRequest, "only user sessions can dismiss announcements")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid announcement id")
		return
	}
	switch err := h.Maintenance.Dismiss(r.Context(), id, *p.UserID); {
	case errors.Is(err, maintenance.ErrNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "announcement not found")
	case errors.Is(err, maintenance.ErrNotDismissible):
		httpserver.WriteError(w, http.StatusBadRequest, "this announcement can't be dismissed")
	case err != nil:
		h.Logger.Error("dismiss announcement failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not dismiss")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── announcements: org-admin management ──────────────────────────────

type announcementBody struct {
	Message     string     `json:"message"`
	Severity    string     `json:"severity"`
	EndsAt      *time.Time `json:"ends_at"`
	Dismissible *bool      `json:"dismissible"`
}

func (b announcementBody) validate() (maintenance.Announcement, error) {
	msg := strings.TrimSpace(b.Message)
	if msg == "" {
		return maintenance.Announcement{}, errors.New("message is required")
	}
	if len(msg) > 500 {
		return maintenance.Announcement{}, errors.New("message must be at most 500 characters")
	}
	sev := b.Severity
	if sev == "" {
		sev = "info"
	}
	if sev != "info" && sev != "warning" && sev != "critical" {
		return maintenance.Announcement{}, errors.New("severity must be info, warning, or critical")
	}
	dismissible := true
	if b.Dismissible != nil {
		dismissible = *b.Dismissible
	}
	return maintenance.Announcement{
		Message: msg, Severity: sev, EndsAt: b.EndsAt, Dismissible: dismissible,
	}, nil
}

// listOrgAnnouncements: GET /api/v1/settings/announcements  (admin)
func (h *Handlers) listOrgAnnouncements(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgID(r)
	list, err := h.Maintenance.List(r.Context(), &orgID)
	if err != nil {
		h.Logger.Error("list org announcements failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not load announcements")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"announcements": list})
}

// createOrgAnnouncement: POST /api/v1/settings/announcements  (admin, not demo)
func (h *Handlers) createOrgAnnouncement(w http.ResponseWriter, r *http.Request) {
	var body announcementBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	a, err := body.validate()
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	orgID := middleware.OrgID(r)
	a.OrgID = &orgID
	a.CreatedBy = middleware.Principal(r).UserID
	created, err := h.Maintenance.CreateAnnouncement(r.Context(), a)
	if err != nil {
		h.Logger.Error("create announcement failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not create announcement")
		return
	}
	h.recordAudit(r, "announcement.created", "announcement", created.ID.String(),
		map[string]any{"message": created.Message, "severity": created.Severity})
	httpserver.WriteJSON(w, http.StatusCreated, created)
}

// deleteOrgAnnouncement: DELETE /api/v1/settings/announcements/{id}  (admin, not demo)
func (h *Handlers) deleteOrgAnnouncement(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid announcement id")
		return
	}
	orgID := middleware.OrgID(r)
	switch err := h.Maintenance.DeleteAnnouncement(r.Context(), id, &orgID); {
	case errors.Is(err, maintenance.ErrNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "announcement not found")
	case err != nil:
		h.Logger.Error("delete announcement failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not delete announcement")
	default:
		h.recordAudit(r, "announcement.deleted", "announcement", id.String(), nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── announcements: operator (cell-wide) ──────────────────────────────

// listCellAnnouncements: GET /api/v1/operator/announcements
func (h *Handlers) listCellAnnouncements(w http.ResponseWriter, r *http.Request) {
	list, err := h.Maintenance.List(r.Context(), nil)
	if err != nil {
		h.Logger.Error("list cell announcements failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not load announcements")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"announcements": list})
}

// createCellAnnouncement: POST /api/v1/operator/announcements
func (h *Handlers) createCellAnnouncement(w http.ResponseWriter, r *http.Request) {
	var body announcementBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	a, err := body.validate()
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.CreatedBy = middleware.Principal(r).UserID // org_id stays nil = cell-wide
	created, err := h.Maintenance.CreateAnnouncement(r.Context(), a)
	if err != nil {
		h.Logger.Error("create cell announcement failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not create announcement")
		return
	}
	h.recordAudit(r, "announcement.created", "announcement", created.ID.String(),
		map[string]any{"message": created.Message, "severity": created.Severity, "cell_wide": true})
	httpserver.WriteJSON(w, http.StatusCreated, created)
}

// deleteCellAnnouncement: DELETE /api/v1/operator/announcements/{id}
func (h *Handlers) deleteCellAnnouncement(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid announcement id")
		return
	}
	switch err := h.Maintenance.DeleteAnnouncement(r.Context(), id, nil); {
	case errors.Is(err, maintenance.ErrNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "announcement not found")
	case err != nil:
		h.Logger.Error("delete cell announcement failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not delete announcement")
	default:
		h.recordAudit(r, "announcement.deleted", "announcement", id.String(), map[string]any{"cell_wide": true})
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── maintenance windows ──────────────────────────────────────────────

type windowBody struct {
	Name     string                  `json:"name"`
	Reason   string                  `json:"reason"`
	StartsAt *time.Time              `json:"starts_at"`
	EndsAt   *time.Time              `json:"ends_at"`
	Scope    maintenance.WindowScope `json:"scope"`
	Announce bool                    `json:"announce"`
}

// validateWindow normalizes the body into a Window, expanding system
// scope to a service-name snapshot. Bounds enforced here: ends_at is
// required and at most 7 days after starts_at.
func (h *Handlers) validateWindow(r *http.Request, body windowBody) (maintenance.Window, error) {
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return maintenance.Window{}, errors.New("name is required")
	}
	starts := time.Now()
	if body.StartsAt != nil {
		starts = *body.StartsAt
	}
	if body.EndsAt == nil {
		return maintenance.Window{}, errors.New("ends_at is required — windows are bounded by design")
	}
	ends := *body.EndsAt
	if !ends.After(starts) {
		return maintenance.Window{}, errors.New("ends_at must be after starts_at")
	}
	if ends.Sub(starts) > maintenance.MaxWindowDuration {
		return maintenance.Window{}, fmt.Errorf("a window may last at most %d days — extend it later if needed", int(maintenance.MaxWindowDuration.Hours()/24))
	}
	scope := body.Scope
	orgID := middleware.OrgID(r)
	switch scope.Kind {
	case "all_org":
		// no selectors
		scope = maintenance.WindowScope{Kind: "all_org"}
	case "group":
		if scope.GroupID == nil {
			return maintenance.Window{}, errors.New("scope.group_id is required for a team window")
		}
		scope = maintenance.WindowScope{Kind: "group", GroupID: scope.GroupID}
	case "entities":
		if len(scope.IntegrationIDs) == 0 && len(scope.SystemIDs) == 0 && len(scope.ServiceNames) == 0 {
			return maintenance.Window{}, errors.New("an entities scope needs at least one integration, system, or service")
		}
		// Snapshot system membership to concrete service names at write
		// time. Windows are short-lived; a snapshot fails toward less
		// silence (same posture as scoped manage).
		expanded := map[string]struct{}{}
		for _, sid := range scope.SystemIDs {
			id := sid
			names, err := h.systemExpander(r.Context(), orgID, "", &id)
			if err != nil {
				return maintenance.Window{}, fmt.Errorf("resolving system members: %w", err)
			}
			for _, n := range names {
				expanded[n] = struct{}{}
			}
		}
		scope.ServiceNamesExpanded = scope.ServiceNamesExpanded[:0]
		for n := range expanded {
			scope.ServiceNamesExpanded = append(scope.ServiceNamesExpanded, n)
		}
	default:
		return maintenance.Window{}, errors.New("scope.kind must be all_org, entities, or group")
	}
	return maintenance.Window{
		OrgID:    orgID,
		Name:     name,
		Reason:   strings.TrimSpace(body.Reason),
		StartsAt: starts,
		EndsAt:   ends,
		Scope:    scope,
	}, nil
}

// listMaintenanceWindows: GET /api/v1/maintenance-windows  (viewer+)
func (h *Handlers) listMaintenanceWindows(w http.ResponseWriter, r *http.Request) {
	list, err := h.Maintenance.ListWindows(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list maintenance windows failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not load maintenance windows")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"windows": list})
}

// createMaintenanceWindow: POST /api/v1/maintenance-windows  (editor+;
// all_org scope needs admin)
func (h *Handlers) createMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	var body windowBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	win, err := h.validateWindow(r, body)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	p := middleware.Principal(r)
	if win.Scope.Kind == "all_org" && !p.Role.CanAdmin() {
		httpserver.WriteError(w, http.StatusForbidden, "silencing the whole organization requires an org admin")
		return
	}
	win.CreatedBy = p.UserID
	created, err := h.Maintenance.CreateWindow(r.Context(), win)
	if err != nil {
		h.Logger.Error("create maintenance window failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not create maintenance window")
		return
	}
	if body.Announce {
		created = h.attachWindowAnnouncement(r, created)
	}
	h.recordAudit(r, "maintenance_window.created", "maintenance_window", created.ID.String(),
		map[string]any{"name": created.Name, "scope": created.Scope, "starts_at": created.StartsAt, "ends_at": created.EndsAt})
	httpserver.WriteJSON(w, http.StatusCreated, created)
}

// attachWindowAnnouncement creates the linked banner for a window. The
// announcement carries the window's bounds so it appears and expires with
// it — no cleanup job needed. Best-effort: a failed banner never fails
// the window.
func (h *Handlers) attachWindowAnnouncement(r *http.Request, win maintenance.Window) maintenance.Window {
	msg := fmt.Sprintf("Maintenance: %s — alerts are silenced until %s.",
		win.Name, win.EndsAt.UTC().Format("2006-01-02 15:04 MST"))
	if win.Reason != "" {
		msg = fmt.Sprintf("Maintenance: %s — %s. Alerts are silenced until %s.",
			win.Name, win.Reason, win.EndsAt.UTC().Format("2006-01-02 15:04 MST"))
	}
	orgID := win.OrgID
	ends := win.EndsAt
	ann, err := h.Maintenance.CreateAnnouncement(r.Context(), maintenance.Announcement{
		OrgID: &orgID, Message: msg, Severity: "warning",
		StartsAt: win.StartsAt, EndsAt: &ends, Dismissible: true,
		CreatedBy: win.CreatedBy,
	})
	if err != nil {
		h.Logger.Warn("window announcement create failed", "window", win.ID, "err", err)
		return win
	}
	if err := h.Maintenance.SetWindowAnnouncement(r.Context(), win.OrgID, win.ID, &ann.ID); err != nil {
		h.Logger.Warn("window announcement link failed", "window", win.ID, "err", err)
		return win
	}
	win.AnnouncementID = &ann.ID
	return win
}

// updateMaintenanceWindow: PATCH /api/v1/maintenance-windows/{id}
func (h *Handlers) updateMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid window id")
		return
	}
	orgID := middleware.OrgID(r)
	existing, err := h.Maintenance.GetWindow(r.Context(), orgID, id)
	if errors.Is(err, maintenance.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "maintenance window not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not load window")
		return
	}
	var body windowBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	win, err := h.validateWindow(r, body)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	p := middleware.Principal(r)
	if (win.Scope.Kind == "all_org" || existing.Scope.Kind == "all_org") && !p.Role.CanAdmin() {
		httpserver.WriteError(w, http.StatusForbidden, "org-wide windows require an org admin")
		return
	}
	win.ID = id
	updated, err := h.Maintenance.UpdateWindow(r.Context(), orgID, win)
	if err != nil {
		h.Logger.Error("update maintenance window failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not update maintenance window")
		return
	}
	h.recordAudit(r, "maintenance_window.updated", "maintenance_window", id.String(),
		map[string]any{"name": updated.Name, "scope": updated.Scope, "starts_at": updated.StartsAt, "ends_at": updated.EndsAt})
	httpserver.WriteJSON(w, http.StatusOK, updated)
}

// endMaintenanceWindow: DELETE /api/v1/maintenance-windows/{id} — ends an
// active window now (the row stays for suppression history) or cancels a
// scheduled one outright. The linked announcement is removed either way.
func (h *Handlers) endMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid window id")
		return
	}
	orgID := middleware.OrgID(r)
	existing, err := h.Maintenance.GetWindow(r.Context(), orgID, id)
	if errors.Is(err, maintenance.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "maintenance window not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not load window")
		return
	}
	if existing.Scope.Kind == "all_org" && !middleware.Principal(r).Role.CanAdmin() {
		httpserver.WriteError(w, http.StatusForbidden, "org-wide windows require an org admin")
		return
	}
	if _, err := h.Maintenance.EndWindow(r.Context(), orgID, id); err != nil {
		h.Logger.Error("end maintenance window failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not end maintenance window")
		return
	}
	if existing.AnnouncementID != nil {
		if err := h.Maintenance.DeleteAnnouncement(r.Context(), *existing.AnnouncementID, &orgID); err != nil &&
			!errors.Is(err, maintenance.ErrNotFound) {
			h.Logger.Warn("window announcement cleanup failed", "window", id, "err", err)
		}
	}
	h.recordAudit(r, "maintenance_window.ended", "maintenance_window", id.String(),
		map[string]any{"name": existing.Name})
	w.WriteHeader(http.StatusNoContent)
}
