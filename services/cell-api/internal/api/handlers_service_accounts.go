// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Service accounts — machine identities for programmatic access (docs/api.md).
// A service account has its own role (admin/editor/viewer) in one org and owns
// api_tokens (owner_type='service_account') used as Authorization: Bearer. All
// management here is org-admin only; the personal-token flow lives in
// handlers_settings.go. Tokens are returned in plaintext exactly once at mint.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
)

// auditActor identifies who performed an action, for audit log lines.
func auditActor(r *http.Request) string {
	p := middleware.Principal(r)
	if p.Email != "" {
		return p.Email
	}
	if p.UserID != nil {
		return p.UserID.String()
	}
	return "service-account"
}

// expiryFromDays converts an "expires in N days" request value into an absolute
// timestamp; 0 or negative means no expiry (nil). Shared by the personal- and
// service-account token mint handlers.
func expiryFromDays(days int) *time.Time {
	if days <= 0 {
		return nil
	}
	t := time.Now().AddDate(0, 0, days)
	return &t
}

func validServiceAccountRole(r string) bool {
	switch identity.Role(r) {
	case identity.RoleAdmin, identity.RoleEditor, identity.RoleViewer:
		return true
	}
	return false
}

// orgServiceAccount loads a service account and confirms it belongs to the
// caller's org. Returns ok=false (and writes 404) otherwise.
func (h *Handlers) orgServiceAccount(w http.ResponseWriter, r *http.Request, id uuid.UUID) (identity.ServiceAccount, bool) {
	sa, err := h.Identity.GetServiceAccount(r.Context(), id)
	if err != nil || sa.OrgID != middleware.OrgID(r) {
		httpserver.WriteError(w, http.StatusNotFound, "service account not found")
		return identity.ServiceAccount{}, false
	}
	return sa, true
}

// listServiceAccounts: GET /api/v1/settings/service-accounts  (admin)
func (h *Handlers) listServiceAccounts(w http.ResponseWriter, r *http.Request) {
	sas, err := h.Identity.ListServiceAccounts(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list service accounts failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"service_accounts": sas})
}

// createServiceAccount: POST /api/v1/settings/service-accounts  (admin)
func (h *Handlers) createServiceAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Role        string `json:"role"`
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
	if !validServiceAccountRole(body.Role) {
		httpserver.WriteError(w, http.StatusBadRequest, "role must be admin, editor, or viewer")
		return
	}
	sa, err := h.Identity.CreateServiceAccount(r.Context(), middleware.OrgID(r), name, strings.TrimSpace(body.Description), identity.Role(body.Role), middleware.Principal(r).UserID)
	if err != nil {
		if isUniqueViolation(err) {
			httpserver.WriteError(w, http.StatusConflict, "a service account with that name already exists")
			return
		}
		h.Logger.Error("create service account failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	if sa.Role == identity.RoleAdmin {
		h.Logger.Warn("audit: admin-role service account created",
			"name", sa.Name, "service_account_id", sa.ID, "by", auditActor(r), "org", sa.OrgID)
	}
	h.recordAudit(r, "service_account.created", "service_account", sa.ID.String(), map[string]any{"name": sa.Name, "role": string(sa.Role)})
	httpserver.WriteJSON(w, http.StatusCreated, sa)
}

// updateServiceAccount: PUT /api/v1/settings/service-accounts/{id}  (admin)
func (h *Handlers) updateServiceAccount(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, ok := h.orgServiceAccount(w, r, id); !ok {
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Role        string `json:"role"`
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
	if !validServiceAccountRole(body.Role) {
		httpserver.WriteError(w, http.StatusBadRequest, "role must be admin, editor, or viewer")
		return
	}
	sa, err := h.Identity.UpdateServiceAccount(r.Context(), middleware.OrgID(r), id, name, strings.TrimSpace(body.Description), identity.Role(body.Role))
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "service account not found")
			return
		}
		if isUniqueViolation(err) {
			httpserver.WriteError(w, http.StatusConflict, "a service account with that name already exists")
			return
		}
		h.Logger.Error("update service account failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "service_account.updated", "service_account", sa.ID.String(), map[string]any{"name": sa.Name, "role": string(sa.Role)})
	httpserver.WriteJSON(w, http.StatusOK, sa)
}

// deleteServiceAccount: DELETE /api/v1/settings/service-accounts/{id}  (admin)
func (h *Handlers) deleteServiceAccount(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, ok := h.orgServiceAccount(w, r, id); !ok {
		return
	}
	if err := h.Identity.DeleteServiceAccount(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "service account not found")
			return
		}
		h.Logger.Error("delete service account failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "service_account.deleted", "service_account", id.String(), nil)
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// listServiceAccountTokens: GET /api/v1/settings/service-accounts/{id}/tokens
func (h *Handlers) listServiceAccountTokens(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, ok := h.orgServiceAccount(w, r, id); !ok {
		return
	}
	toks, err := h.Identity.ListAPITokensForServiceAccount(r.Context(), id)
	if err != nil {
		h.Logger.Error("list service-account tokens failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"tokens": toks})
}

// createServiceAccountToken: POST /api/v1/settings/service-accounts/{id}/tokens
// Mints a service-account token; the plaintext is returned EXACTLY ONCE.
func (h *Handlers) createServiceAccountToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	sa, ok := h.orgServiceAccount(w, r, id)
	if !ok {
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
	tok, err := identity.NewToken(identity.TokenKindServiceAccount)
	if err != nil {
		h.Logger.Error("mint service-account token failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "mint failed")
		return
	}
	row, err := h.Identity.CreateAPIToken(r.Context(), "service_account", id, name, scope, expiryFromDays(body.ExpiresInDays), tok)
	if err != nil {
		h.Logger.Error("create service-account token failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	// Audit: a token whose effective access is admin is a durable admin credential.
	if sa.Role == identity.RoleAdmin && (scope == "" || scope == string(identity.RoleAdmin)) {
		h.Logger.Warn("audit: admin-capable service-account token minted",
			"service_account", sa.Name, "token_prefix", row.Prefix, "by", auditActor(r), "org", sa.OrgID)
	}
	h.recordAudit(r, "service_account_token.created", "service_account", id.String(), map[string]any{"token_name": name, "scope_role": scope})
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{
		"token":     row,
		"plaintext": tok.Plaintext, // shown once; never stored in plaintext
	})
}

// revokeServiceAccountToken: DELETE /api/v1/settings/service-accounts/{id}/tokens/{tid}
func (h *Handlers) revokeServiceAccountToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tid, err := uuid.Parse(r.PathValue("tid"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid token id")
		return
	}
	if _, ok := h.orgServiceAccount(w, r, id); !ok {
		return
	}
	// Confirm the token belongs to this service account before revoking.
	toks, err := h.Identity.ListAPITokensForServiceAccount(r.Context(), id)
	if err != nil {
		h.Logger.Error("revoke service-account token: list failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	owns := false
	for _, t := range toks {
		if t.ID == tid {
			owns = true
			break
		}
	}
	if !owns {
		httpserver.WriteError(w, http.StatusNotFound, "token not found")
		return
	}
	if err := h.Identity.RevokeAPIToken(r.Context(), tid); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "token not found")
			return
		}
		h.Logger.Error("revoke service-account token failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	h.recordAudit(r, "service_account_token.revoked", "service_account", id.String(), map[string]any{"token_id": tid.String()})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"revoked": true})
}
