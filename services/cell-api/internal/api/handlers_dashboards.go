// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Dashboards — group-scopable since RBAC v2 §5.2 A′ (decision
// 2026-07-04): group_id NULL = org-wide (everyone sees it; org editors
// manage it — the historical behaviour); group_id set = the dashboard
// belongs to that team (its members see it; its group-editors + org
// editors manage it). The team is immutable after create.

package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/license"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/dashboards"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// callerGroupRoles returns the caller's group_id → role map. Users and
// service accounts both resolve through their group memberships
// (docs/service-account-scoping-design.md); internal callers get none.
func (h *Handlers) callerGroupRoles(r *http.Request) map[uuid.UUID]identity.Role {
	p := middleware.Principal(r)
	out := map[uuid.UUID]identity.Role{}
	var ref identity.MemberRef
	switch {
	case p.UserID != nil:
		ref = identity.UserRef(*p.UserID)
	case p.Kind == identity.PrincipalServiceAccount && p.ServiceAccountID != nil:
		ref = identity.ServiceAccountRef(*p.ServiceAccountID)
	default:
		return out
	}
	groups, err := h.Identity.ListMemberGroups(r.Context(), ref, p.OrgID)
	if err != nil {
		h.Logger.Warn("caller group roles lookup failed", "err", err)
		return out
	}
	for _, g := range groups {
		out[g.GroupID] = g.Role
	}
	return out
}

// canSeeDashboard: org-wide dashboards are visible to everyone in the
// org; team dashboards only to members (org admins/editors see all).
func canSeeDashboard(d dashboards.Dashboard, orgWrite bool, roles map[uuid.UUID]identity.Role) bool {
	if d.GroupID == nil || orgWrite {
		return true
	}
	_, member := roles[*d.GroupID]
	return member
}

// canManageDashboard: org editors/admins manage everything; team
// dashboards are additionally manageable by that team's group-editors.
// Scope-capped tokens never escalate via group roles.
func canManageDashboard(d dashboards.Dashboard, orgWrite, scopeCapped bool, roles map[uuid.UUID]identity.Role) bool {
	if orgWrite {
		return true
	}
	if scopeCapped || d.GroupID == nil {
		return false
	}
	role, member := roles[*d.GroupID]
	return member && role.CanWrite()
}

// dashboardAccessCtx snapshots the caller's role facts once per request.
func (h *Handlers) dashboardAccessCtx(r *http.Request) (orgWrite, scopeCapped bool, roles map[uuid.UUID]identity.Role) {
	if h.AuthMW == nil {
		return true, false, nil // no-auth dev mode: everything manageable
	}
	p := middleware.Principal(r)
	roles = h.callerGroupRoles(r)
	if !h.featureEntitled(license.FeatureRBACAdvanced) {
		// CE: membership still drives VISIBILITY of team dashboards, but
		// group roles grant no manage — demote all to viewer.
		for k := range roles {
			roles[k] = identity.RoleViewer
		}
	}
	return p.Role.CanWrite(), p.ScopeCapped(), roles
}

// listDashboards: GET /api/v1/dashboards
//
// Returns every dashboard visible to the CALLER: org-wide ones always,
// team ones only when they're a member. canManage is stamped per row.
func (h *Handlers) listDashboards(w http.ResponseWriter, r *http.Request) {
	views, err := h.Dashboards.List(r.Context(), middleware.OrgID(r), nil)
	if err != nil {
		h.Logger.Error("list dashboards failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	orgWrite, capped, roles := h.dashboardAccessCtx(r)
	out := make([]dashboards.Dashboard, 0, len(views))
	for _, d := range views {
		if !canSeeDashboard(d, orgWrite, roles) {
			continue
		}
		d.CanManage = canManageDashboard(d, orgWrite, capped, roles)
		out = append(out, d)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"dashboards": out})
}

// getDashboard: GET /api/v1/dashboards/{id}
func (h *Handlers) getDashboard(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid dashboard id")
		return
	}
	d, err := h.Dashboards.Get(r.Context(), middleware.OrgID(r), id, nil)
	if errors.Is(err, dashboards.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	if err != nil {
		h.Logger.Error("get dashboard failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	orgWrite, capped, roles := h.dashboardAccessCtx(r)
	if !canSeeDashboard(d, orgWrite, roles) {
		// Invisible team dashboards read as nonexistent.
		httpserver.WriteError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	d.CanManage = canManageDashboard(d, orgWrite, capped, roles)
	httpserver.WriteJSON(w, http.StatusOK, d)
}

// createDashboard: POST /api/v1/dashboards
//
// Org-wide dashboards (no groupId) need an org editor. Team dashboards
// need org editor OR editor role in the target team.
func (h *Handlers) createDashboard(w http.ResponseWriter, r *http.Request) {
	var req dashboards.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	orgWrite, capped, roles := h.dashboardAccessCtx(r)
	if !orgWrite {
		if req.GroupID == nil {
			httpserver.WriteError(w, http.StatusForbidden, "org-wide dashboards require an org-wide editor role")
			return
		}
		role, member := roles[*req.GroupID]
		if capped || !member || !role.CanWrite() {
			httpserver.WriteError(w, http.StatusForbidden, "you need an editor role in that team")
			return
		}
	}
	if req.GroupID != nil {
		// Team must exist in the caller's org (FK alone would 500).
		if _, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), *req.GroupID); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "unknown group")
			return
		}
	}
	d, err := h.Dashboards.Create(r.Context(), middleware.OrgID(r), nil, req)
	if err != nil {
		if dashboards.IsValidationError(err) {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("create dashboard failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	d.CanManage = true
	httpserver.WriteJSON(w, http.StatusCreated, d)
}

// requireManageDashboard loads the dashboard and enforces the manage
// rule; returns (dashboard, true) on success.
func (h *Handlers) requireManageDashboard(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dashboards.Dashboard, bool) {
	d, err := h.Dashboards.Get(r.Context(), middleware.OrgID(r), id, nil)
	if errors.Is(err, dashboards.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "dashboard not found")
		return dashboards.Dashboard{}, false
	}
	if err != nil {
		h.Logger.Error("load dashboard failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return dashboards.Dashboard{}, false
	}
	orgWrite, capped, roles := h.dashboardAccessCtx(r)
	if !canSeeDashboard(d, orgWrite, roles) {
		httpserver.WriteError(w, http.StatusNotFound, "dashboard not found")
		return dashboards.Dashboard{}, false
	}
	if !canManageDashboard(d, orgWrite, capped, roles) {
		httpserver.WriteError(w, http.StatusForbidden, "you don't manage this dashboard")
		return dashboards.Dashboard{}, false
	}
	return d, true
}

// updateDashboard: PUT /api/v1/dashboards/{id}
//
// Full replace — every mutable field plus the items list. Items behave
// as a set: anything not in the payload is dropped, anything in it is
// upserted by (dashboard, integration). The team (groupId) is immutable
// and ignored here.
func (h *Handlers) updateDashboard(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid dashboard id")
		return
	}
	if _, ok := h.requireManageDashboard(w, r, id); !ok {
		return
	}
	var req dashboards.UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	d, err := h.Dashboards.Update(r.Context(), middleware.OrgID(r), id, nil, req)
	if errors.Is(err, dashboards.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	if err != nil {
		if dashboards.IsValidationError(err) {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("update dashboard failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	d.CanManage = true
	httpserver.WriteJSON(w, http.StatusOK, d)
}

// deleteDashboard: DELETE /api/v1/dashboards/{id}
func (h *Handlers) deleteDashboard(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid dashboard id")
		return
	}
	if _, ok := h.requireManageDashboard(w, r, id); !ok {
		return
	}
	err = h.Dashboards.Delete(r.Context(), middleware.OrgID(r), id)
	if errors.Is(err, dashboards.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	if err != nil {
		h.Logger.Error("delete dashboard failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
