// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Notification message templates (issue #5): the FORMAT side of
// alerting's org→team ladder. Org default set + per-team overrides, all
// fields Liquid, empty = inherit (resolution is per field, in
// alerting/context.go). The variable palette the editors show is served
// here too — reflected from AlertContext so it cannot drift.
//
// Permissions: org default needs an org admin. A team's override needs
// an org admin/editor, or — EE rbac_advanced — an editor of that group
// (configuration within their scope, exactly the scoped-manage tier).
// The entitlement gates configuration, never enforcement: an EE→CE
// downgrade keeps stored team templates rendering.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/notifytemplates"
)

// ── delivery-side resolver (alerting.SetMessageTemplateResolver) ─────

// ladderCacheEntry caches one (org, group) resolution briefly so a
// delivery batch does one query per scope, not one per notification.
type ladderCacheEntry struct {
	at  time.Time
	val alerting.MessageTemplates
}

var (
	ladderCacheMu  sync.Mutex
	ladderCache    = map[string]ladderCacheEntry{}
	ladderCacheTTL = 5 * time.Second
)

// MessageTemplateLadder is the alerting-side resolver: the stored
// (team over org) per-field merge for a rule's owning group. Errors
// resolve to the zero value — a template lookup must never block an
// alert; the ladder just falls through to the cell/built-in rungs.
func (h *Handlers) MessageTemplateLadder(ctx context.Context, orgID uuid.UUID, groupID *uuid.UUID) alerting.MessageTemplates {
	if h.NotifyTemplates == nil || orgID == uuid.Nil {
		return alerting.MessageTemplates{}
	}
	key := orgID.String()
	if groupID != nil {
		key += "/" + groupID.String()
	}
	ladderCacheMu.Lock()
	if e, ok := ladderCache[key]; ok && time.Since(e.at) < ladderCacheTTL {
		ladderCacheMu.Unlock()
		return e.val
	}
	ladderCacheMu.Unlock()

	res, err := h.NotifyTemplates.Resolve(ctx, orgID, groupID)
	if err != nil {
		h.Logger.Warn("message template resolve failed; falling through", "err", err)
		return alerting.MessageTemplates{}
	}
	val := alerting.MessageTemplates{
		EmailSubject: res.EmailSubject,
		EmailBody:    res.EmailBody,
		SlackTitle:   res.SlackTitle,
		SlackBody:    res.SlackBody,
	}
	ladderCacheMu.Lock()
	ladderCache[key] = ladderCacheEntry{at: time.Now(), val: val}
	ladderCacheMu.Unlock()
	return val
}

// invalidateLadderCache drops the cached resolution for a scope after a
// PUT so edits apply to the next delivery immediately.
func invalidateLadderCache(orgID uuid.UUID, groupID *uuid.UUID) {
	key := orgID.String()
	if groupID != nil {
		key += "/" + groupID.String()
	}
	ladderCacheMu.Lock()
	delete(ladderCache, key)
	ladderCacheMu.Unlock()
}

// ── HTTP surface ─────────────────────────────────────────────────────

type templateSetBody struct {
	EmailSubject string `json:"email_subject"`
	EmailBody    string `json:"email_body"`
	SlackTitle   string `json:"slack_title"`
	SlackBody    string `json:"slack_body"`
}

// validate rejects any malformed Liquid field at save time (the render
// path would fall through on it, but a 400 with the parse error is the
// kind of feedback an editor needs).
func (b templateSetBody) validate() (string, bool) {
	for _, f := range []struct{ name, tmpl string }{
		{"email_subject", b.EmailSubject},
		{"email_body", b.EmailBody},
		{"slack_title", b.SlackTitle},
		{"slack_body", b.SlackBody},
	} {
		if err := alerting.ValidateLiquid(f.tmpl); err != nil {
			return f.name + ": " + err.Error(), false
		}
	}
	return "", true
}

// getOrgNotificationTemplates: GET /api/v1/settings/notification-templates  (admin)
func (h *Handlers) getOrgNotificationTemplates(w http.ResponseWriter, r *http.Request) {
	set, err := h.NotifyTemplates.Get(r.Context(), middleware.OrgID(r), nil)
	if err != nil {
		h.Logger.Error("get org notification templates failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, set)
}

// putOrgNotificationTemplates: PUT /api/v1/settings/notification-templates  (admin, not demo)
func (h *Handlers) putOrgNotificationTemplates(w http.ResponseWriter, r *http.Request) {
	var body templateSetBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg, ok := body.validate(); !ok {
		httpserver.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	orgID := middleware.OrgID(r)
	set, err := h.NotifyTemplates.Upsert(r.Context(), orgID, nil, notifytemplates.TemplateSet{
		EmailSubject: body.EmailSubject, EmailBody: body.EmailBody,
		SlackTitle: body.SlackTitle, SlackBody: body.SlackBody,
	})
	if err != nil {
		h.Logger.Error("put org notification templates failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	invalidateLadderCache(orgID, nil)
	h.recordAudit(r, "notification_template.update", "notification_template", set.ID.String(),
		map[string]any{"scope": "org"})
	httpserver.WriteJSON(w, http.StatusOK, set)
}

// canEditGroupTemplates: org admins/editors always; the group's own
// editors when EE rbac_advanced. Scope-capped tokens never escalate.
func (h *Handlers) canEditGroupTemplates(r *http.Request, groupID uuid.UUID) bool {
	orgWrite, capped, roles := h.dashboardAccessCtx(r)
	if orgWrite {
		return true
	}
	if capped {
		return false
	}
	role, member := roles[groupID]
	return member && role.CanWrite()
}

// groupNotificationTemplates handles GET and PUT of one team's override:
// /api/v1/settings/groups/{id}/notification-template
func (h *Handlers) getGroupNotificationTemplates(w http.ResponseWriter, r *http.Request) {
	gid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	orgID := middleware.OrgID(r)
	if _, err := h.Identity.GetGroup(r.Context(), orgID, gid); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	set, err := h.NotifyTemplates.Get(r.Context(), orgID, &gid)
	if err != nil {
		h.Logger.Error("get group notification templates failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// The editors show inherited values as placeholders — ship the org
	// default alongside so the UI needs one request.
	orgSet, err := h.NotifyTemplates.Get(r.Context(), orgID, nil)
	if err != nil {
		h.Logger.Error("get org notification templates failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"template":    set,
		"org_default": orgSet,
		"can_edit":    h.canEditGroupTemplates(r, gid),
	})
}

func (h *Handlers) putGroupNotificationTemplates(w http.ResponseWriter, r *http.Request) {
	gid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	orgID := middleware.OrgID(r)
	if _, err := h.Identity.GetGroup(r.Context(), orgID, gid); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	if !h.canEditGroupTemplates(r, gid) {
		// Group-editor manage is the EE scoped-manage tier; without the
		// entitlement (or the role) this is a plain 403 — same posture as
		// team dashboards.
		httpserver.WriteError(w, http.StatusForbidden, "you need an org editor role, or an editor role in this team (Enterprise)")
		return
	}
	var body templateSetBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg, ok := body.validate(); !ok {
		httpserver.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	set, err := h.NotifyTemplates.Upsert(r.Context(), orgID, &gid, notifytemplates.TemplateSet{
		EmailSubject: body.EmailSubject, EmailBody: body.EmailBody,
		SlackTitle: body.SlackTitle, SlackBody: body.SlackBody,
	})
	if err != nil {
		h.Logger.Error("put group notification templates failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	invalidateLadderCache(orgID, &gid)
	h.recordAudit(r, "notification_template.update", "notification_template", set.ID.String(),
		map[string]any{"scope": "group", "group_id": gid.String()})
	httpserver.WriteJSON(w, http.StatusOK, set)
}

// templateContextSchema: GET /api/v1/alerting/template-context-schema
// (any authed) — the editors' variable palette, reflected from
// AlertContext. Paths are a public contract: additive only.
func (h *Handlers) templateContextSchema(w http.ResponseWriter, r *http.Request) {
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"variables": alerting.TemplateContextSchema(),
	})
}
