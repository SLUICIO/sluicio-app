// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// CRUD for group_access_policies — the policy rows that grant a
// group access to one of:
//   - a specific service             (kind='service')
//   - all services in an integration (kind='integration')
//   - services with matching attrs   (kind='attributes')
//   - the AND of the above           (kind='compound')
//   - everything in the org          (kind='all_org')
//   - all flagged systems            (kind='system')
//   - an arbitrary AND/OR/NOT tree   (kind='expression')
//
// All mutating routes require org admin via RequireRole (wired in
// handlers.go). Listing is open to any authed user in the org.

package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// listGroupPolicies: GET /api/v1/settings/groups/{id}/policies
func (h *Handlers) listGroupPolicies(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	// Sanity: belongs to caller's org.
	if _, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), groupID); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	policies, err := h.Identity.ListPoliciesForGroup(r.Context(), groupID)
	if err != nil {
		h.Logger.Error("list policies failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"policies": policies})
}

// createGroupPolicy: POST /api/v1/settings/groups/{id}/policies  (admin)
func (h *Handlers) createGroupPolicy(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	if _, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), groupID); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	var body identity.AccessPolicyInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	policy, err := h.Identity.CreatePolicy(r.Context(), groupID, body)
	if err != nil {
		// Re-posting an identical policy is a conflict, not an
		// idempotent success — consistent with the other
		// already-exists surfaces in this API.
		if errors.Is(err, identity.ErrPolicyExists) {
			httpserver.WriteError(w, http.StatusConflict, "an identical policy already exists on this group")
			return
		}
		// CreatePolicy's own validation errors go straight back as 400.
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.recordAudit(r, "group_policy.create", "group", groupID.String(), map[string]any{"kind": body.Kind})
	httpserver.WriteJSON(w, http.StatusCreated, policy)
}

// deleteGroupPolicy: DELETE /api/v1/settings/groups/{id}/policies/{policy_id}  (admin)
func (h *Handlers) deleteGroupPolicy(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	policyID, err := uuid.Parse(r.PathValue("policy_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid policy id")
		return
	}
	// Sanity: group is in caller's org. (DeletePolicy doesn't enforce
	// org scoping; the route's group_id path arg + GetGroup call does.)
	if _, err := h.Identity.GetGroup(r.Context(), middleware.OrgID(r), groupID); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	if err := h.Identity.DeletePolicy(r.Context(), policyID); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "policy not found")
			return
		}
		h.Logger.Error("delete policy failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "group_policy.delete", "group", groupID.String(), map[string]any{"policy_id": policyID.String()})
	w.WriteHeader(http.StatusNoContent)
}
