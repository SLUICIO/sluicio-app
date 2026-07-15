// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Settings → Groups handlers. Filename suffix `_admin` is to avoid
// colliding with the pre-existing handlers_groups.go which is about
// log grouping (an entirely different concept that predates this).
//
// Routes:
//   GET    /api/v1/settings/groups                          — list (any authed user; mutations admin-only)
//   POST   /api/v1/settings/groups                          — create (org admin)
//   GET    /api/v1/settings/groups/{id}                     — detail
//   PATCH  /api/v1/settings/groups/{id}                     — update name/description (group admin or org admin)
//   DELETE /api/v1/settings/groups/{id}                     — delete (org admin)
//   GET    /api/v1/settings/groups/{id}/members             — list members
//   POST   /api/v1/settings/groups/{id}/members             — add (org admin)
//   PATCH  /api/v1/settings/groups/{id}/members/{user_id}   — change role
//   DELETE /api/v1/settings/groups/{id}/members/{user_id}   — remove
//
// Plus the service-side assignment surface:
//   GET    /api/v1/services/{name}/groups                   — which groups is this service in
//   PUT    /api/v1/services/{name}/groups                   — replace the set (org admin)

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
)

// listGroups: GET /api/v1/settings/groups
func (h *Handlers) listGroupsAdmin(w http.ResponseWriter, r *http.Request) {
	out, err := h.Identity.ListGroups(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list groups failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"groups": out})
}

// createGroupAdmin: POST /api/v1/settings/groups  (admin only)
func (h *Handlers) createGroupAdmin(w http.ResponseWriter, r *http.Request) {
	var body identity.GroupInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	g, err := h.Identity.CreateGroup(r.Context(), middleware.OrgID(r), body)
	if err != nil {
		if errors.Is(err, identity.ErrGroupSlugExists) {
			httpserver.WriteError(w, http.StatusConflict, "a group with that slug already exists")
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.recordAudit(r, "group.created", "group", g.ID.String(), map[string]any{"name": g.Name})
	httpserver.WriteJSON(w, http.StatusCreated, g)
}

// getGroupAdmin: GET /api/v1/settings/groups/{id}
func (h *Handlers) getGroupAdmin(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	g, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "group not found")
			return
		}
		h.Logger.Error("get group failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, g)
}

// updateGroupAdmin: PATCH /api/v1/settings/groups/{id}  (admin only)
func (h *Handlers) updateGroupAdmin(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	var body identity.GroupInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.Identity.UpdateGroup(r.Context(), middleware.OrgID(r), id, body); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "group not found")
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.recordAudit(r, "group.updated", "group", id.String(), map[string]any{"name": body.Name})
	w.WriteHeader(http.StatusNoContent)
}

// deleteGroupAdmin: DELETE /api/v1/settings/groups/{id}  (admin only)
func (h *Handlers) deleteGroupAdmin(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	if err := h.Identity.DeleteGroup(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "group not found")
			return
		}
		h.Logger.Error("delete group failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "group.deleted", "group", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// listGroupMembersAdmin: GET /api/v1/settings/groups/{id}/members
func (h *Handlers) listGroupMembersAdmin(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	// Sanity: belongs to caller's org.
	if _, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), id); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	out, err := h.Identity.ListGroupMembers(r.Context(), id)
	if err != nil {
		h.Logger.Error("list group members failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"members": out})
}

// addGroupMemberAdmin: POST /api/v1/settings/groups/{id}/members  (admin only)
//
// Body: { user_id XOR service_account_id, role }. A user must already
// be a member of the org (no auto-invite — invite via Settings →
// Members first); a service account must belong to the org. SA
// membership is how scoped service accounts gain visibility
// (docs/service-account-scoping-design.md).
func (h *Handlers) addGroupMemberAdmin(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	if _, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), groupID); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	var body struct {
		UserID           string `json:"user_id"`
		ServiceAccountID string `json:"service_account_id"`
		Role             string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	role := identity.Role(body.Role)
	if !role.IsValid() {
		httpserver.WriteError(w, http.StatusBadRequest, "role must be admin, editor, or viewer")
		return
	}
	hasUser := strings.TrimSpace(body.UserID) != ""
	hasSA := strings.TrimSpace(body.ServiceAccountID) != ""
	if hasUser == hasSA {
		httpserver.WriteError(w, http.StatusBadRequest, "provide exactly one of user_id or service_account_id")
		return
	}

	if hasSA {
		saID, err := uuid.Parse(strings.TrimSpace(body.ServiceAccountID))
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid service_account_id")
			return
		}
		// Sanity: the SA belongs to the caller's org.
		sa, err := h.Identity.GetServiceAccount(r.Context(), saID)
		if err != nil || sa.OrgID != middleware.OrgID(r) {
			httpserver.WriteError(w, http.StatusBadRequest, "service account not found in this org")
			return
		}
		if err := h.Identity.AddGroupServiceAccount(r.Context(), saID, groupID, role); err != nil {
			if errors.Is(err, identity.ErrAlreadyMember) {
				httpserver.WriteError(w, http.StatusConflict, "service account is already a member of this group")
				return
			}
			h.Logger.Error("add group service account failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
			return
		}
		h.recordAudit(r, "group_member.added", "group", groupID.String(), map[string]any{"service_account_id": saID.String(), "role": string(role)})
		w.WriteHeader(http.StatusNoContent)
		return
	}

	userID, err := uuid.Parse(strings.TrimSpace(body.UserID))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	// Sanity: target is a member of the org.
	if _, err := h.Identity.GetMembership(r.Context(), userID, middleware.OrgID(r)); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "user is not a member of this org")
		return
	}
	if err := h.Identity.AddGroupMember(r.Context(), userID, groupID, role); err != nil {
		if errors.Is(err, identity.ErrAlreadyMember) {
			httpserver.WriteError(w, http.StatusConflict, "user is already a member of this group")
			return
		}
		h.Logger.Error("add group member failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "group_member.added", "group", groupID.String(), map[string]any{"user_id": userID.String(), "role": string(role)})
	w.WriteHeader(http.StatusNoContent)
}

// updateGroupMemberRoleAdmin: PATCH /api/v1/settings/groups/{id}/members/{user_id}
func (h *Handlers) updateGroupMemberRoleAdmin(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	userID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if _, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), groupID); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	role := identity.Role(body.Role)
	if !role.IsValid() {
		httpserver.WriteError(w, http.StatusBadRequest, "role must be admin, editor, or viewer")
		return
	}
	if err := h.Identity.UpdateGroupMemberRole(r.Context(), userID, groupID, role); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "user is not a member of this group")
			return
		}
		h.Logger.Error("update group role failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "group_member.role_changed", "group", groupID.String(), map[string]any{"user_id": userID.String(), "role": string(role)})
	w.WriteHeader(http.StatusNoContent)
}

// removeGroupMemberAdmin: DELETE /api/v1/settings/groups/{id}/members/{user_id}
func (h *Handlers) removeGroupMemberAdmin(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	userID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if _, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), groupID); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	if err := h.Identity.RemoveGroupMember(r.Context(), userID, groupID); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "user is not a member of this group")
			return
		}
		h.Logger.Error("remove group member failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "group_member.removed", "group", groupID.String(), map[string]any{"user_id": userID.String()})
	w.WriteHeader(http.StatusNoContent)
}

// groupAndSAFromPath parses + org-checks the {id} group and {sa_id}
// service account of the SA-membership routes. Writes the error
// response itself on failure.
func (h *Handlers) groupAndSAFromPath(w http.ResponseWriter, r *http.Request) (groupID, saID uuid.UUID, ok bool) {
	groupID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return groupID, saID, false
	}
	saID, err = uuid.Parse(r.PathValue("sa_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid service account id")
		return groupID, saID, false
	}
	if _, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), groupID); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "group not found")
		return groupID, saID, false
	}
	sa, err := h.Identity.GetServiceAccount(r.Context(), saID)
	if err != nil || sa.OrgID != middleware.OrgID(r) {
		httpserver.WriteError(w, http.StatusNotFound, "service account not found")
		return groupID, saID, false
	}
	return groupID, saID, true
}

// updateGroupServiceAccountRoleAdmin: PATCH /api/v1/settings/groups/{id}/service-accounts/{sa_id}
func (h *Handlers) updateGroupServiceAccountRoleAdmin(w http.ResponseWriter, r *http.Request) {
	groupID, saID, ok := h.groupAndSAFromPath(w, r)
	if !ok {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	role := identity.Role(body.Role)
	if !role.IsValid() {
		httpserver.WriteError(w, http.StatusBadRequest, "role must be admin, editor, or viewer")
		return
	}
	if err := h.Identity.UpdateGroupServiceAccountRole(r.Context(), saID, groupID, role); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "service account is not a member of this group")
			return
		}
		h.Logger.Error("update group service-account role failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "group_member.role_changed", "group", groupID.String(), map[string]any{"service_account_id": saID.String(), "role": string(role)})
	w.WriteHeader(http.StatusNoContent)
}

// removeGroupServiceAccountAdmin: DELETE /api/v1/settings/groups/{id}/service-accounts/{sa_id}
func (h *Handlers) removeGroupServiceAccountAdmin(w http.ResponseWriter, r *http.Request) {
	groupID, saID, ok := h.groupAndSAFromPath(w, r)
	if !ok {
		return
	}
	if err := h.Identity.RemoveGroupServiceAccount(r.Context(), saID, groupID); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "service account is not a member of this group")
			return
		}
		h.Logger.Error("remove group service account failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "group_member.removed", "group", groupID.String(), map[string]any{"service_account_id": saID.String()})
	w.WriteHeader(http.StatusNoContent)
}

// ── service ↔ group assignment ─────────────────────────────────────────

// listServiceGroupsHandler: GET /api/v1/services/{name}/groups
func (h *Handlers) listServiceGroupsHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name required")
		return
	}
	groups, err := h.Identity.ListServiceGroups(r.Context(), middleware.OrgID(r), name)
	if err != nil {
		h.Logger.Error("list service groups failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// putServiceGroupsHandler: PUT /api/v1/services/{name}/groups  (admin only)
//
// Body: { group_ids: ["<uuid>", …] }. Replaces the full set in a
// single transaction.
func (h *Handlers) putServiceGroupsHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name required")
		return
	}
	var body struct {
		GroupIDs []string `json:"group_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ids := make([]uuid.UUID, 0, len(body.GroupIDs))
	for _, s := range body.GroupIDs {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid group id: "+s)
			return
		}
		ids = append(ids, id)
	}
	if err := h.Identity.SetServiceGroups(r.Context(), middleware.OrgID(r), name, ids); err != nil {
		h.Logger.Error("set service groups failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "service_groups.updated", "service", name, map[string]any{"group_ids_count": len(ids)})
	w.WriteHeader(http.StatusNoContent)
}
