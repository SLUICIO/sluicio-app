// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Event-subscription CRUD (issue #4) + the event-type catalog the
// picker shows. Team-scoped subscriptions are the primary model
// (Robert: org-wide rarely makes sense in an enterprise org): a team
// subscription needs an org editor or — EE rbac_advanced — that team's
// editors, and only receives events on entities the team can see.
// Org-wide subscriptions (no group) are admin-only. Destinations are
// WEBHOOK channels only; the channel's format knob picks CloudEvents
// vs canonical JSON.

package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/eventsubs"
)

// eventTypeCatalog is the documented vocabulary the picker offers.
// Operational types are exact; config families are globs over the
// audit-action-derived names (com.sluicio.<action>). The full per-action
// list isn't enumerated — subscriptions filter with globs, and actions
// are additive.
type eventTypeEntry struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	// Kind: "operational" (exact types emitted by engines) or "config"
	// (family glob over audit-derived mutations).
	Kind string `json:"kind"`
}

var eventTypeCatalog = []eventTypeEntry{
	{Type: "com.sluicio.alert.fired", Description: "An alert rule started firing (and produced a notification)", Kind: "operational"},
	{Type: "com.sluicio.alert.resolved", Description: "A firing alert resolved (with a recovery notification)", Kind: "operational"},
	{Type: "com.sluicio.errors.opened", Description: "Unacknowledged error traces opened on a service (the error notifier paged)", Kind: "operational"},
	{Type: "com.sluicio.service.discovered", Description: "A service appeared in the catalog for the first time", Kind: "operational"},
	{Type: "com.sluicio.integration.*", Description: "Integration lifecycle and config changes (created, updated, deleted, matchers, groups, profile)", Kind: "config"},
	{Type: "com.sluicio.service.*", Description: "Service-level config changes (system flag, tags, groups, metadata)", Kind: "config"},
	{Type: "com.sluicio.system.*", Description: "System entity changes", Kind: "config"},
	{Type: "com.sluicio.alert_rule.*", Description: "Alert-rule config changes", Kind: "config"},
	{Type: "com.sluicio.group.*", Description: "Group and membership changes (org-wide subscriptions only)", Kind: "config"},
	{Type: "com.sluicio.maintenance_window.*", Description: "Maintenance windows scheduled, updated, ended (org-wide subscriptions only)", Kind: "config"},
	{Type: "com.sluicio.*", Description: "Everything — every event the cell emits", Kind: "config"},
}

// listEventTypes: GET /api/v1/event-types  (any authed)
func (h *Handlers) listEventTypes(w http.ResponseWriter, r *http.Request) {
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"event_types": eventTypeCatalog})
}

type eventSubscriptionBody struct {
	Name         string   `json:"name"`
	GroupID      *string  `json:"group_id"`
	Enabled      *bool    `json:"enabled"`
	EventFilters []string `json:"event_filters"`
	ChannelID    string   `json:"channel_id"`
}

// validateSubscription checks the shared create/update invariants and
// resolves the destination channel (must exist, must be a webhook).
func (h *Handlers) validateSubscription(r *http.Request, body eventSubscriptionBody) (name string, filters []string, channelID uuid.UUID, errMsg string) {
	name = strings.TrimSpace(body.Name)
	if name == "" {
		return "", nil, uuid.Nil, "name is required"
	}
	for _, f := range body.EventFilters {
		if strings.TrimSpace(f) != "" {
			filters = append(filters, strings.TrimSpace(f))
		}
	}
	if len(filters) == 0 {
		// Filters are mandatory — "*" is allowed but must be explicit.
		return "", nil, uuid.Nil, "at least one event filter is required (use * to subscribe to everything)"
	}
	chID, err := uuid.Parse(strings.TrimSpace(body.ChannelID))
	if err != nil {
		return "", nil, uuid.Nil, "channel_id is required"
	}
	ch, err := h.Alerts.GetChannel(r.Context(), middleware.OrgID(r), chID)
	if err != nil {
		return "", nil, uuid.Nil, "channel not found"
	}
	if ch.Kind != alerting.ChannelWebhook {
		return "", nil, uuid.Nil, "event subscriptions deliver to webhook channels only"
	}
	return name, filters, chID, ""
}

// canManageSubscriptionScope: org-wide (nil group) needs an org admin;
// a team subscription needs an org editor or that team's editors (EE) —
// the dashboards permission shape.
func (h *Handlers) canManageSubscriptionScope(r *http.Request, groupID *uuid.UUID) (ok bool, reason string) {
	p := middleware.Principal(r)
	if groupID == nil {
		if !p.Role.CanAdmin() {
			return false, "org-wide subscriptions require an org admin"
		}
		return true, ""
	}
	orgWrite, capped, roles := h.dashboardAccessCtx(r)
	if orgWrite {
		return true, ""
	}
	if capped {
		return false, "scope-capped tokens cannot manage subscriptions"
	}
	role, member := roles[*groupID]
	if !member || !role.CanWrite() {
		return false, "you need an org editor role, or an editor role in that team (Enterprise)"
	}
	return true, ""
}

// listEventSubscriptions: GET /api/v1/event-subscriptions  (authed)
// Everyone sees the org list (like channels — they're configuration,
// not data); manage rights are stamped per row.
func (h *Handlers) listEventSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := h.EventSubs.List(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list event subscriptions failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	type row struct {
		eventsubs.Subscription
		CanManage bool `json:"can_manage"`
	}
	out := make([]row, 0, len(subs))
	for _, s := range subs {
		ok, _ := h.canManageSubscriptionScope(r, s.GroupID)
		out = append(out, row{Subscription: s, CanManage: ok})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"subscriptions": out})
}

// createEventSubscription: POST /api/v1/event-subscriptions  (scope-tiered, not demo)
func (h *Handlers) createEventSubscription(w http.ResponseWriter, r *http.Request) {
	var body eventSubscriptionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var groupID *uuid.UUID
	if body.GroupID != nil && strings.TrimSpace(*body.GroupID) != "" {
		gid, err := uuid.Parse(strings.TrimSpace(*body.GroupID))
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid group_id")
			return
		}
		if _, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), gid); err != nil {
			httpserver.WriteError(w, http.StatusNotFound, "group not found")
			return
		}
		groupID = &gid
	}
	if ok, reason := h.canManageSubscriptionScope(r, groupID); !ok {
		httpserver.WriteError(w, http.StatusForbidden, reason)
		return
	}
	name, filters, chID, errMsg := h.validateSubscription(r, body)
	if errMsg != "" {
		httpserver.WriteError(w, http.StatusBadRequest, errMsg)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	sub, err := h.EventSubs.Create(r.Context(), eventsubs.Subscription{
		OrgID: middleware.OrgID(r), GroupID: groupID, Name: name,
		Enabled: enabled, EventFilters: filters, ChannelID: chID,
		CreatedBy: middleware.Principal(r).UserID,
	})
	if err != nil {
		h.Logger.Error("create event subscription failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.recordAudit(r, "event_subscription.created", "event_subscription", sub.ID.String(),
		map[string]any{"name": sub.Name, "filters": sub.EventFilters})
	httpserver.WriteJSON(w, http.StatusCreated, sub)
}

// updateEventSubscription: PUT /api/v1/event-subscriptions/{id}
func (h *Handlers) updateEventSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid subscription id")
		return
	}
	existing, err := h.EventSubs.Get(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "subscription not found")
		return
	}
	if ok, reason := h.canManageSubscriptionScope(r, existing.GroupID); !ok {
		httpserver.WriteError(w, http.StatusForbidden, reason)
		return
	}
	var body eventSubscriptionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name, filters, chID, errMsg := h.validateSubscription(r, body)
	if errMsg != "" {
		httpserver.WriteError(w, http.StatusBadRequest, errMsg)
		return
	}
	enabled := existing.Enabled
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	sub, err := h.EventSubs.Update(r.Context(), middleware.OrgID(r), id, name, enabled, filters, chID)
	if err != nil {
		h.Logger.Error("update event subscription failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "event_subscription.updated", "event_subscription", sub.ID.String(),
		map[string]any{"name": sub.Name, "enabled": sub.Enabled})
	httpserver.WriteJSON(w, http.StatusOK, sub)
}

// listEventDeliveries: GET /api/v1/event-subscriptions/{id}/deliveries
// The per-subscription delivery ledger — state, attempts, last error —
// so "my webhook is silent" is diagnosable from the UI instead of the
// database. Readable by anyone who can see the subscription list.
func (h *Handlers) listEventDeliveries(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid subscription id")
		return
	}
	deliveries, err := h.EventSubs.RecentDeliveries(r.Context(), middleware.OrgID(r), id, 50)
	if err != nil {
		h.Logger.Error("list event deliveries failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
}

// deleteEventSubscription: DELETE /api/v1/event-subscriptions/{id}
func (h *Handlers) deleteEventSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid subscription id")
		return
	}
	existing, err := h.EventSubs.Get(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "subscription not found")
		return
	}
	if ok, reason := h.canManageSubscriptionScope(r, existing.GroupID); !ok {
		httpserver.WriteError(w, http.StatusForbidden, reason)
		return
	}
	if err := h.EventSubs.Delete(r.Context(), middleware.OrgID(r), id); err != nil {
		h.Logger.Error("delete event subscription failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "event_subscription.deleted", "event_subscription", id.String(),
		map[string]any{"name": existing.Name})
	w.WriteHeader(http.StatusNoContent)
}
