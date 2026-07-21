// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Settings surface: org members, personal access tokens, and (later)
// auth-provider configuration. Routes mounted under
//   /api/v1/settings/members   — admin manages who is in the org
//   /api/v1/settings/tokens    — current user manages their PATs
// All Settings routes inherit the auth gate from the mux Wrap;
// admin-only routes additionally call middleware.RequireRole.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// ── members ───────────────────────────────────────────────────────────

// listMembers: GET /api/v1/settings/members
// Returns the members of the principal's active org.
func (h *Handlers) listMembers(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgID(r)
	rows, err := h.Identity.ListOrgMembers(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("list members failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"members": rows})
}

// addMemberBody is the JSON shape POST /settings/members expects.
// Admin enters an email + name + initial password + role; we create
// the user row if it doesn't exist, then add the membership. The
// new user is flagged `must_reset_password=true` so a future
// password-change wizard can force a rotation.
type addMemberBody struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// addMember: POST /api/v1/settings/members  (admin only)
func (h *Handlers) addMember(w http.ResponseWriter, r *http.Request) {
	var body addMemberBody
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
	if len(body.Password) < 8 {
		httpserver.WriteError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	role := identity.Role(body.Role)
	if !role.IsValid() {
		httpserver.WriteError(w, http.StatusBadRequest, "role must be admin, editor, or viewer")
		return
	}

	// Reuse an existing user with this email if present (e.g. the same
	// person is being added to a second org). Otherwise create.
	user, err := h.Identity.GetUserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, identity.ErrNotFound) {
		h.Logger.Error("add member: user lookup failed", "err", err)
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
			h.Logger.Error("add member: create user failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
			return
		}
		// Set the initial password the admin entered. must_reset_password
		// stays whatever the migration default is (false here, but we
		// want true for invited users so they're forced to rotate). Bump
		// it explicitly.
		hash, err := identity.HashPassword(body.Password)
		if err != nil {
			h.Logger.Error("add member: password hash failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
			return
		}
		if err := h.Identity.SetPasswordHash(r.Context(), user.ID, hash); err != nil {
			h.Logger.Error("add member: password write failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
			return
		}
	}

	if err := h.Identity.AddMember(r.Context(), user.ID, middleware.OrgID(r), role); err != nil {
		if errors.Is(err, identity.ErrAlreadyMember) {
			httpserver.WriteError(w, http.StatusConflict, "user is already a member of this org")
			return
		}
		h.Logger.Error("add member failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "member.added", "user", user.ID.String(), map[string]any{"email": user.Email, "role": string(role)})
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{
		"user": user,
		"role": role,
	})
}

// updateMemberRole: PATCH /api/v1/settings/members/{user_id}  (admin only)
func (h *Handlers) updateMemberRole(w http.ResponseWriter, r *http.Request) {
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
	// Forbid the last admin downgrading themselves into a non-admin
	// role — that'd lock everyone out of admin operations. Counted
	// against the org's current admin list; if this user is the only
	// admin AND the target role isn't admin, refuse.
	if err := h.refuseLastAdminDowngrade(r, userID, role); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Identity.UpdateMemberRole(r.Context(), userID, middleware.OrgID(r), role); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "user is not a member of this org")
			return
		}
		h.Logger.Error("update role failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "member.role_changed", "user", userID.String(), map[string]any{"role": string(role)})
	w.WriteHeader(http.StatusNoContent)
}

// removeMember: DELETE /api/v1/settings/members/{user_id}  (admin only)
func (h *Handlers) removeMember(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.refuseLastAdminDowngrade(r, userID, identity.RoleViewer); err != nil {
		// Same guard as role-change: removing the last admin would
		// orphan the org. The role parameter here is irrelevant
		// (anything non-admin triggers the guard); we pass viewer
		// for clarity.
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Identity.RemoveMember(r.Context(), userID, middleware.OrgID(r)); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "user is not a member of this org")
			return
		}
		h.Logger.Error("remove member failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "member.removed", "user", userID.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// refuseLastAdminDowngrade returns a non-nil error if the operation
// would leave the org with zero admins. Used as a guard on both
// role change and member removal.
func (h *Handlers) refuseLastAdminDowngrade(r *http.Request, targetUserID uuid.UUID, newRole identity.Role) error {
	orgID := middleware.OrgID(r)
	currentRole, err := h.Identity.GetMembership(r.Context(), targetUserID, orgID)
	if err != nil {
		// Not currently a member → no downgrade possible.
		return nil
	}
	if currentRole != identity.RoleAdmin || newRole == identity.RoleAdmin {
		return nil
	}
	members, err := h.Identity.ListOrgMembers(r.Context(), orgID)
	if err != nil {
		// Soft-fail: if we can't count admins, allow the op rather
		// than block.
		return nil
	}
	admins := 0
	for _, m := range members {
		if m.Role == identity.RoleAdmin {
			admins++
		}
	}
	if admins <= 1 {
		return errors.New("can't downgrade the last admin — promote someone else first")
	}
	return nil
}

// ── tokens ─────────────────────────────────────────────────────────────

// listTokens: GET /api/v1/settings/tokens
// Returns the calling user's non-revoked personal access tokens.
func (h *Handlers) listTokens(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusForbidden, "service accounts cannot list user tokens")
		return
	}
	rows, err := h.Identity.ListAPITokensForUser(r.Context(), *p.UserID)
	if err != nil {
		h.Logger.Error("list tokens failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"tokens": rows})
}

// createToken: POST /api/v1/settings/tokens
//
// Mints a fresh PAT for the calling user. The plaintext is returned
// EXACTLY ONCE in the response body; the caller must copy it before
// dismissing the dialog — we only ever store the prefix + hash.
func (h *Handlers) createToken(w http.ResponseWriter, r *http.Request) {
	if h.demoAccountGuard(w, r) {
		return
	}
	p := middleware.Principal(r)
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusForbidden, "service accounts cannot mint user tokens")
		return
	}
	var body struct {
		Name          string `json:"name"`
		ScopeRole     string `json:"scope_role"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	scope := strings.TrimSpace(body.ScopeRole)
	if scope != "" && !identity.Role(scope).IsValid() {
		httpserver.WriteError(w, http.StatusBadRequest, "scope_role must be admin, editor, viewer, or empty")
		return
	}
	expiresAt := expiryFromDays(body.ExpiresInDays)
	tok, err := identity.NewToken(identity.TokenKindPersonal)
	if err != nil {
		h.Logger.Error("mint token failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "mint failed")
		return
	}
	row, err := h.Identity.CreateAPIToken(r.Context(), "user", *p.UserID, name, scope, expiresAt, tok)
	if err != nil {
		h.Logger.Error("create token failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "token.created", "user", p.UserID.String(), map[string]any{"token_name": name, "scope_role": scope})
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{
		"token": row,
		// Returned ONCE. Frontend dialog shows + offers copy; never
		// stored anywhere on our side. Re-issued by revoke + create.
		"plaintext": tok.Plaintext,
	})
}

// revokeToken: DELETE /api/v1/settings/tokens/{id}
func (h *Handlers) revokeToken(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusForbidden, "service accounts cannot revoke user tokens")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid token id")
		return
	}
	// We don't enforce ownership at the SQL layer (api_tokens doesn't
	// have a per-row owner guard beyond owner_id). Verify here that
	// the token belongs to the calling user before revoking, so one
	// user can't revoke another's tokens via guessed id.
	toks, err := h.Identity.ListAPITokensForUser(r.Context(), *p.UserID)
	if err != nil {
		h.Logger.Error("revoke token: ownership check failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	owns := false
	for _, t := range toks {
		if t.ID == id {
			owns = true
			break
		}
	}
	if !owns {
		httpserver.WriteError(w, http.StatusNotFound, "token not found")
		return
	}
	if err := h.Identity.RevokeAPIToken(r.Context(), id); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "token not found")
			return
		}
		h.Logger.Error("revoke token failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	h.recordAudit(r, "token.revoked", "user", p.UserID.String(), map[string]any{"token_id": id.String()})
	w.WriteHeader(http.StatusNoContent)
}

// adminResetMemberPassword: POST /api/v1/settings/members/{user_id}/password
// (admin only, demo-guarded). Sets a temporary password for another member
// and, when require_change is true (default), forces them to change it on
// next login via must_reset_password + revoking their sessions.
func (h *Handlers) adminResetMemberPassword(w http.ResponseWriter, r *http.Request) {
	targetID, err := uuid.Parse(r.PathValue("user_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	p := middleware.Principal(r)
	if p.UserID != nil && *p.UserID == targetID {
		httpserver.WriteError(w, http.StatusBadRequest, "use Account → Password to change your own password")
		return
	}
	var body struct {
		NewPassword   string `json:"new_password"`
		RequireChange *bool  `json:"require_change"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.NewPassword) < minPasswordLen {
		httpserver.WriteError(w, http.StatusBadRequest, fmt.Sprintf("password must be at least %d characters", minPasswordLen))
		return
	}
	// The target must be a member of the admin's active org — an admin can't
	// reach into another tenant.
	if _, err := h.Identity.GetMembership(r.Context(), targetID, middleware.OrgID(r)); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "user not found in this organization")
			return
		}
		h.Logger.Error("reset member password: membership lookup failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	requireChange := true
	if body.RequireChange != nil {
		requireChange = *body.RequireChange
	}
	hash, err := identity.HashPassword(body.NewPassword)
	if err != nil {
		h.Logger.Error("reset member password: hash failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not set password")
		return
	}
	if err := h.Identity.SetPasswordHashForced(r.Context(), targetID, hash, requireChange); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		h.Logger.Error("reset member password: persist failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not set password")
		return
	}
	// Revoke the target's sessions so the temporary password takes effect
	// immediately — any open session is kicked and must re-authenticate.
	if err := h.Identity.DeleteSessionsForUser(r.Context(), targetID); err != nil {
		h.Logger.Warn("reset member password: revoke sessions failed", "err", err)
	}
	h.recordAudit(r, "member.password_reset", "user", targetID.String(),
		map[string]any{"require_change": requireChange})
	w.WriteHeader(http.StatusNoContent)
}
