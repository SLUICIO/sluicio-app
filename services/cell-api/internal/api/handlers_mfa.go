// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/pkg/totp"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
)

const totpIssuer = "Sluicio"
const backupCodeCount = 10

// mfaStatus: GET /api/v1/account/mfa — the current user's MFA state, plus
// whether the server can do MFA at all (key configured).
func (h *Handlers) mfaStatus(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	st, err := h.Identity.MFAStatus(r.Context(), *p.UserID)
	if err != nil {
		h.Logger.Error("mfa status failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled":   st.Enabled,
		"pending":   st.Enrolled && !st.Enabled,
		"available": h.Identity.HasMFAKey(),
	})
}

// mfaSetup: POST /api/v1/account/mfa/setup — start enrollment. Generates a
// secret, stores it pending, returns it + the otpauth URI for the QR code.
func (h *Handlers) mfaSetup(w http.ResponseWriter, r *http.Request) {
	if h.demoAccountGuard(w, r) {
		return
	}
	p := middleware.Principal(r)
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !h.Identity.HasMFAKey() {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "MFA is not available — the server has no encryption key configured")
		return
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		h.Logger.Error("mfa gen secret failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not start enrollment")
		return
	}
	if err := h.Identity.StartMFAEnrollment(r.Context(), *p.UserID, secret); err != nil {
		if errors.Is(err, identity.ErrMFAUnavailable) {
			httpserver.WriteError(w, http.StatusServiceUnavailable, "MFA is not available")
			return
		}
		h.Logger.Error("mfa start enrollment failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not start enrollment")
		return
	}
	account := p.Email
	if account == "" {
		account = p.UserID.String()
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"secret":      secret,
		"otpauth_uri": totp.ProvisioningURI(secret, account, totpIssuer),
	})
}

// mfaEnable: POST /api/v1/account/mfa/enable {code} — confirm enrollment by
// validating a code against the pending secret. Returns one-time backup
// codes on success (shown to the user exactly once).
func (h *Handlers) mfaEnable(w http.ResponseWriter, r *http.Request) {
	if h.demoAccountGuard(w, r) {
		return
	}
	p := middleware.Principal(r)
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	secret, enabled, err := h.Identity.MFASecret(r.Context(), *p.UserID)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "start enrollment first")
		return
	}
	if enabled {
		httpserver.WriteError(w, http.StatusConflict, "MFA is already enabled")
		return
	}
	if !totp.Validate(secret, body.Code, time.Now()) {
		httpserver.WriteError(w, http.StatusBadRequest, "that code is incorrect — check your authenticator app's clock and try again")
		return
	}
	codes, hashes, err := identity.GenerateBackupCodes(backupCodeCount)
	if err != nil {
		h.Logger.Error("mfa backup codes failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not enable MFA")
		return
	}
	if err := h.Identity.EnableMFA(r.Context(), *p.UserID, hashes); err != nil {
		h.Logger.Error("mfa enable failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not enable MFA")
		return
	}
	h.recordAudit(r, "mfa.enable", "user", p.UserID.String(), nil)
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"enabled": true, "backup_codes": codes})
}

// mfaDisable: POST /api/v1/account/mfa/disable {code} — turn MFA off after
// re-confirming with a current code or a backup code.
func (h *Handlers) mfaDisable(w http.ResponseWriter, r *http.Request) {
	if h.demoAccountGuard(w, r) {
		return
	}
	p := middleware.Principal(r)
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	secret, enabled, err := h.Identity.MFASecret(r.Context(), *p.UserID)
	if err != nil || !enabled {
		httpserver.WriteError(w, http.StatusBadRequest, "MFA is not enabled")
		return
	}
	ok := totp.Validate(secret, body.Code, time.Now())
	if !ok {
		if used, _ := h.Identity.ConsumeBackupCode(r.Context(), *p.UserID, body.Code); used {
			ok = true
		}
	}
	if !ok {
		httpserver.WriteError(w, http.StatusBadRequest, "incorrect code")
		return
	}
	if err := h.Identity.DisableMFA(r.Context(), *p.UserID); err != nil {
		h.Logger.Error("mfa disable failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not disable MFA")
		return
	}
	h.recordAudit(r, "mfa.disable", "user", p.UserID.String(), nil)
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// mfaVerify: POST /api/v1/auth/mfa-verify {mfa_token, code} — the login
// second step (public). Exchanges a valid pending token + TOTP/backup code
// for a session.
func (h *Handlers) mfaVerify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	userID, err := h.Identity.VerifyMFAPendingToken(strings.TrimSpace(body.MFAToken))
	if err != nil {
		httpserver.WriteError(w, http.StatusUnauthorized, "this sign-in attempt expired — start again")
		return
	}
	secret, enabled, err := h.Identity.MFASecret(r.Context(), userID)
	if err != nil || !enabled {
		httpserver.WriteError(w, http.StatusUnauthorized, "MFA not enabled")
		return
	}
	ok := totp.Validate(secret, body.Code, time.Now())
	if !ok {
		if used, _ := h.Identity.ConsumeBackupCode(r.Context(), userID, body.Code); used {
			ok = true
		}
	}
	if !ok {
		// Audited: a wrong second factor after a correct password is a
		// stronger compromise signal than a plain failed login.
		if user, err := h.Identity.GetUserByID(r.Context(), userID); err == nil {
			h.recordAuthAudit(r.Context(), user, nil, "mfa.verify_failed", clientIP(r), nil)
		}
		httpserver.WriteError(w, http.StatusUnauthorized, "incorrect code")
		return
	}
	user, err := h.Identity.GetUserByID(r.Context(), userID)
	if err != nil {
		h.Logger.Error("mfa verify: get user failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "login failed")
		return
	}
	h.finishLogin(w, r, user)
}
