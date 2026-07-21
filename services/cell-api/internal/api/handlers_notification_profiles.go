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
	"github.com/sluicio/sluicio-app/services/cell-api/internal/notifyprofiles"
)

// Notification profiles: per-team (or org-wide) bundles of behaviour +
// channels that decide how an alert/error is delivered. An alert resolves
// to one profile (integration's assigned → team default → org default).

type profileBody struct {
	GroupID         string   `json:"group_id"` // "" = org-wide
	Name            string   `json:"name"`
	Grouping        string   `json:"grouping"`
	RenotifyMinutes int      `json:"renotify_minutes"`
	IsDefault       bool     `json:"is_default"`
	ChannelIDs      []string `json:"channel_ids"`
}

func (h *Handlers) listNotificationProfiles(w http.ResponseWriter, r *http.Request) {
	if h.Profiles == nil {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"profiles": []any{}})
		return
	}
	profiles, err := h.Profiles.List(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list notification profiles failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (h *Handlers) createNotificationProfile(w http.ResponseWriter, r *http.Request) {
	if h.Profiles == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "profiles not available")
		return
	}
	var body profileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	p := notifyprofiles.Profile{
		ID:              uuid.New(),
		OrganizationID:  middleware.OrgID(r),
		Name:            strings.TrimSpace(body.Name),
		Grouping:        normalizeGrouping(body.Grouping),
		RenotifyMinutes: maxInt(0, body.RenotifyMinutes),
		IsDefault:       body.IsDefault,
	}
	if g := strings.TrimSpace(body.GroupID); g != "" {
		gid, err := uuid.Parse(g)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid group_id")
			return
		}
		p.GroupID = &gid
	}
	created, err := h.Profiles.Create(r.Context(), p)
	if err != nil {
		h.Logger.Error("create notification profile failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	if len(body.ChannelIDs) > 0 {
		ids, perr := parseUUIDs(body.ChannelIDs)
		if perr != nil {
			httpserver.WriteError(w, http.StatusBadRequest, perr.Error())
			return
		}
		if err := h.Profiles.SetChannels(r.Context(), created.ID, ids); err != nil {
			h.Logger.Error("set profile channels failed", "err", err)
		}
	}
	h.recordAudit(r, "notification_profile.created", "notification_profile", created.ID.String(), map[string]any{"name": created.Name})
	full, _ := h.Profiles.Get(r.Context(), middleware.OrgID(r), created.ID)
	httpserver.WriteJSON(w, http.StatusCreated, full)
}

func (h *Handlers) updateNotificationProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := h.profileID(w, r)
	if !ok {
		return
	}
	var body profileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p := notifyprofiles.Profile{
		ID:              id,
		Name:            strings.TrimSpace(body.Name),
		Grouping:        normalizeGrouping(body.Grouping),
		RenotifyMinutes: maxInt(0, body.RenotifyMinutes),
		IsDefault:       body.IsDefault,
	}
	updated, err := h.Profiles.Update(r.Context(), middleware.OrgID(r), p)
	if errors.Is(err, notifyprofiles.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "profile not found")
		return
	}
	if err != nil {
		h.Logger.Error("update notification profile failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "notification_profile.updated", "notification_profile", updated.ID.String(), map[string]any{"name": updated.Name})
	full, _ := h.Profiles.Get(r.Context(), middleware.OrgID(r), updated.ID)
	httpserver.WriteJSON(w, http.StatusOK, full)
}

func (h *Handlers) deleteNotificationProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := h.profileID(w, r)
	if !ok {
		return
	}
	if err := h.Profiles.Delete(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, notifyprofiles.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "profile not found")
			return
		}
		h.Logger.Error("delete notification profile failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "notification_profile.deleted", "notification_profile", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) setNotificationProfileChannels(w http.ResponseWriter, r *http.Request) {
	id, ok := h.profileID(w, r)
	if !ok {
		return
	}
	var body struct {
		ChannelIDs []string `json:"channel_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ids, perr := parseUUIDs(body.ChannelIDs)
	if perr != nil {
		httpserver.WriteError(w, http.StatusBadRequest, perr.Error())
		return
	}
	if err := h.Profiles.SetChannels(r.Context(), id, ids); err != nil {
		h.Logger.Error("set profile channels failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "notification_profile.channels_updated", "notification_profile", id.String(), map[string]any{"channel_count": len(ids)})
	w.WriteHeader(http.StatusNoContent)
}

// getIntegrationProfile: GET /api/v1/integrations/{id}/notification-profile
func (h *Handlers) getIntegrationProfile(w http.ResponseWriter, r *http.Request) {
	if h.Profiles == nil {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"profile_id": nil})
		return
	}
	iid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	if _, ok := h.gateIntegrationMembers(w, r, iid); !ok {
		return
	}
	pid, err := h.Profiles.IntegrationProfileID(r.Context(), middleware.OrgID(r), iid)
	if err != nil {
		h.Logger.Error("get integration profile failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	var out any
	if pid != nil {
		out = pid.String()
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"profile_id": out})
}

// assignIntegrationProfile: PUT /api/v1/integrations/{id}/notification-profile
func (h *Handlers) assignIntegrationProfile(w http.ResponseWriter, r *http.Request) {
	if h.Profiles == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "profiles not available")
		return
	}
	iid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	var body struct {
		ProfileID string `json:"profile_id"` // "" = inherit (clear)
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var pid *uuid.UUID
	if s := strings.TrimSpace(body.ProfileID); s != "" {
		id, perr := uuid.Parse(s)
		if perr != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid profile_id")
			return
		}
		pid = &id
	}
	if err := h.Profiles.AssignIntegrationProfile(r.Context(), middleware.OrgID(r), iid, pid); err != nil {
		h.Logger.Error("assign integration profile failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	var meta map[string]any
	if pid != nil {
		meta = map[string]any{"profile_id": pid.String()}
	}
	h.recordAudit(r, "integration.profile_assigned", "integration", iid.String(), meta)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) profileID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if h.Profiles == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "profiles not available")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid profile id")
		return uuid.Nil, false
	}
	return id, true
}

func normalizeGrouping(g string) string {
	if g == notifyprofiles.GroupingPerIntegration {
		return notifyprofiles.GroupingPerIntegration
	}
	return notifyprofiles.GroupingPerCheck
}

func parseUUIDs(in []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, errors.New("invalid id: " + s)
		}
		out = append(out, id)
	}
	return out, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
