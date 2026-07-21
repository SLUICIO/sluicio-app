// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// Cell-operator surface — the super-admin above the org roles. Every
// route here is gated by RequireOperator at the mux level, so these
// handlers can assume the caller is a cell operator and skip the
// per-org same-active-org check the normal org handlers apply. Operators
// manage the org lifecycle across the whole cell:
//
//   GET    /api/v1/operator/orgs                        — list all orgs
//   POST   /api/v1/operator/orgs                        — create an org
//   PATCH  /api/v1/operator/orgs/{id}                   — rename / re-slug
//   DELETE /api/v1/operator/orgs/{id}                   — delete an org
//   GET    /api/v1/operator/orgs/{id}/members           — list members
//   POST   /api/v1/operator/orgs/{id}/members           — add/assign a member
//   PATCH  /api/v1/operator/orgs/{id}/members/{user_id} — change a member role
//   DELETE /api/v1/operator/orgs/{id}/members/{user_id} — remove a member
//   GET    /api/v1/operator/users                       — list every user
//   PUT    /api/v1/operator/users/{user_id}/operator    — promote/demote operator

type createOrgBody struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// listOperatorOrgs: GET /api/v1/operator/orgs
func (h *Handlers) listOperatorOrgs(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.Identity.ListOrgs(r.Context())
	if err != nil {
		h.Logger.Error("operator: list orgs failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"orgs": orgs})
}

// createOperatorOrg: POST /api/v1/operator/orgs
func (h *Handlers) createOperatorOrg(w http.ResponseWriter, r *http.Request) {
	var body createOrgBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	slug := strings.TrimSpace(body.Slug)
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !orgSlugRe.MatchString(slug) {
		httpserver.WriteError(w, http.StatusBadRequest, "slug must be lowercase letters, digits, and hyphens (1-64 chars)")
		return
	}
	o, err := h.Identity.CreateOrg(r.Context(), name, slug)
	if err != nil {
		if errors.Is(err, identity.ErrSlugTaken) {
			httpserver.WriteError(w, http.StatusConflict, "an org with that slug already exists")
			return
		}
		h.Logger.Error("operator: create org failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.recordOperatorAudit(r, o.ID, "operator.org_created", "org", o.ID.String(), map[string]any{"slug": o.Slug, "name": o.Name})
	httpserver.WriteJSON(w, http.StatusCreated, o)
}

// updateOperatorOrg: PATCH /api/v1/operator/orgs/{id}
func (h *Handlers) updateOperatorOrg(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathOrgID(w, r)
	if !ok {
		return
	}
	var body updateOrgBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	slug := strings.TrimSpace(body.Slug)
	if slug != "" && !orgSlugRe.MatchString(slug) {
		httpserver.WriteError(w, http.StatusBadRequest, "slug must be lowercase letters, digits, and hyphens (1-64 chars)")
		return
	}
	if err := h.Identity.UpdateOrg(r.Context(), orgID, name, slug); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "org not found")
			return
		}
		if errors.Is(err, identity.ErrSlugTaken) {
			httpserver.WriteError(w, http.StatusConflict, "an org with that slug already exists")
			return
		}
		h.Logger.Error("operator: update org failed", "err", err, "org_id", orgID)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	o, err := h.Identity.GetOrgByID(r.Context(), orgID)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.recordOperatorAudit(r, orgID, "operator.org_updated", "org", orgID.String(), map[string]any{"slug": o.Slug, "name": o.Name})
	httpserver.WriteJSON(w, http.StatusOK, o)
}

// deleteOperatorOrg: DELETE /api/v1/operator/orgs/{id}
//
// Destructive — cascades through the org's members, groups, policies,
// service accounts, and tokens. Guardrail: never delete the last org on
// the cell (that would leave nowhere to sign in to).
func (h *Handlers) deleteOperatorOrg(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathOrgID(w, r)
	if !ok {
		return
	}
	orgs, err := h.Identity.ListOrgs(r.Context())
	if err != nil {
		h.Logger.Error("operator: delete org — count failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if len(orgs) <= 1 {
		httpserver.WriteError(w, http.StatusBadRequest, "can't delete the last org on the cell")
		return
	}
	if err := h.Identity.DeleteOrg(r.Context(), orgID); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "org not found")
			return
		}
		h.Logger.Error("operator: delete org failed", "err", err, "org_id", orgID)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "operator.org_deleted", "org", orgID.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// listOperatorOrgMembers: GET /api/v1/operator/orgs/{id}/members
func (h *Handlers) listOperatorOrgMembers(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathOrgID(w, r)
	if !ok {
		return
	}
	rows, err := h.Identity.ListOrgMembers(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("operator: list members failed", "err", err, "org_id", orgID)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"members": rows})
}

type operatorAddMemberBody struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// addOperatorOrgMember: POST /api/v1/operator/orgs/{id}/members
//
// Assigns a user to the target org, creating the user if their email is
// new. Password is optional: supplying one sets an initial local password
// (min 8 chars); omitting it creates an SSO-only user (they sign in via
// the org's IdP or a password reset). Reusing an existing email is how a
// person ends up in more than one org.
func (h *Handlers) addOperatorOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathOrgID(w, r)
	if !ok {
		return
	}
	var body operatorAddMemberBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))
	name := strings.TrimSpace(body.Name)
	if email == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "email is required")
		return
	}
	role := identity.Role(body.Role)
	if !role.IsValid() {
		httpserver.WriteError(w, http.StatusBadRequest, "role must be admin, editor, or viewer")
		return
	}
	if body.Password != "" && len(body.Password) < 8 {
		httpserver.WriteError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	user, err := h.Identity.GetUserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, identity.ErrNotFound) {
		h.Logger.Error("operator: member user lookup failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if errors.Is(err, identity.ErrNotFound) {
		user, err = h.Identity.CreateUser(r.Context(), email, name)
		if err != nil {
			if errors.Is(err, identity.ErrUserExists) {
				httpserver.WriteError(w, http.StatusConflict, "a user with that email already exists")
				return
			}
			h.Logger.Error("operator: create user failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
			return
		}
		if body.Password != "" {
			hash, err := identity.HashPassword(body.Password)
			if err != nil {
				h.Logger.Error("operator: password hash failed", "err", err)
				httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
				return
			}
			if err := h.Identity.SetPasswordHash(r.Context(), user.ID, hash); err != nil {
				h.Logger.Error("operator: password write failed", "err", err)
				httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
				return
			}
		}
	}

	if err := h.Identity.AddMember(r.Context(), user.ID, orgID, role); err != nil {
		if errors.Is(err, identity.ErrAlreadyMember) {
			httpserver.WriteError(w, http.StatusConflict, "user is already a member of this org")
			return
		}
		h.Logger.Error("operator: add member failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordOperatorAudit(r, orgID, "operator.member_added", "user", user.ID.String(), map[string]any{"org_id": orgID.String(), "email": user.Email, "role": string(role)})
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{"user": user, "role": role})
}

// updateOperatorOrgMemberRole: PATCH /api/v1/operator/orgs/{id}/members/{user_id}
func (h *Handlers) updateOperatorOrgMemberRole(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathOrgID(w, r)
	if !ok {
		return
	}
	userID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid user id")
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
	if err := h.Identity.UpdateMemberRole(r.Context(), userID, orgID, role); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "user is not a member of this org")
			return
		}
		h.Logger.Error("operator: update member role failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordOperatorAudit(r, orgID, "operator.member_role_changed", "user", userID.String(), map[string]any{"org_id": orgID.String(), "role": string(role)})
	w.WriteHeader(http.StatusNoContent)
}

// removeOperatorOrgMember: DELETE /api/v1/operator/orgs/{id}/members/{user_id}
func (h *Handlers) removeOperatorOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathOrgID(w, r)
	if !ok {
		return
	}
	userID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.Identity.RemoveMember(r.Context(), userID, orgID); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "user is not a member of this org")
			return
		}
		h.Logger.Error("operator: remove member failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordOperatorAudit(r, orgID, "operator.member_removed", "user", userID.String(), map[string]any{"org_id": orgID.String()})
	w.WriteHeader(http.StatusNoContent)
}

// listOperatorUsers: GET /api/v1/operator/users?q=&limit=&offset=
//
// Filtered + paged so the Operator page stays usable at thousands of
// users: q matches email or name, limit defaults to 50 (cap 200).
func (h *Handlers) listOperatorUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := clampInt(r.URL.Query().Get("limit"), 50, 1, 200)
	offset := clampInt(r.URL.Query().Get("offset"), 0, 0, 1<<30)
	users, total, err := h.Identity.ListUsersPage(r.Context(), q, limit, offset)
	if err != nil {
		h.Logger.Error("operator: list users failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"users":  users,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// clampInt parses raw as an int with a default, clamped to [min, max].
func clampInt(raw string, def, min, max int) int {
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// setOperatorFlag: PUT /api/v1/operator/users/{user_id}/operator
//
// Promotes or demotes a user to/from cell operator. Guard: never demote
// the cell's last operator (that would lock everyone out of the operator
// surface, including org creation and cell-wide settings).
// setDemoFlag: PUT /api/v1/operator/users/{user_id}/demo (operator).
// Marks/unmarks a shared demo account — blocks its self-service surface
// (profile, password, MFA, tokens) so visitors can't sabotage the login.
func (h *Handlers) setDemoFlag(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var body struct {
		IsDemo bool `json:"is_demo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.Identity.SetDemoFlag(r.Context(), userID, body.IsDemo); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		h.Logger.Error("set demo flag failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "operator.demo_flag_set", "user", userID.String(), map[string]any{"is_demo": body.IsDemo})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) setOperatorFlag(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var body struct {
		IsOperator bool `json:"is_operator"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !body.IsOperator {
		// Demote guard: refuse if this is the last operator.
		n, err := h.Identity.CountOperators(r.Context())
		if err != nil {
			h.Logger.Error("operator: count operators failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
			return
		}
		target, err := h.Identity.GetUserByID(r.Context(), userID)
		if err == nil && target.IsOperator && n <= 1 {
			httpserver.WriteError(w, http.StatusBadRequest, "can't demote the last cell operator")
			return
		}
	}
	if err := h.Identity.SetUserOperator(r.Context(), userID, body.IsOperator); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		h.Logger.Error("operator: set operator flag failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "operator.operator_flag_set", "user", userID.String(), map[string]any{"is_operator": body.IsOperator})
	w.WriteHeader(http.StatusNoContent)
}
