// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// resetTokenTTL bounds how long a reset link is valid.
const resetTokenTTL = time.Hour

// minPasswordLen is the floor for a new password set via reset.
const minPasswordLen = 8

// forgotPassword: POST /api/v1/auth/forgot-password  (public)
//
// Always responds 200 with the same body whether or not the email exists —
// the response must never reveal which addresses are registered. When the
// address does map to a local-password account and SMTP is configured, a
// single-use reset link is emailed out-of-band.
//
// The person asking can't be told why no mail came, so the operator must
// be able to: every outcome leaves a log line, by this one message.
func (h *Handlers) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))
	const ok = `{"status":"ok"}` // identical for every outcome

	if email != "" {
		// Everything past the lookup happens after the response. Waiting
		// for the mail server here would answer a real address seconds
		// later than an unknown one, which says which addresses exist
		// just as plainly as a different body would, and would leave the
		// person looking at a spinner for as long as the server stalls.
		// Detached, so the send outlives the request that asked for it.
		ctx := context.WithoutCancel(r.Context())
		baseURL := h.appBaseURL(r)
		ip := clientIP(r)
		if user, err := h.Identity.GetUserByEmail(r.Context(), email); errors.Is(err, identity.ErrNotFound) {
			h.Logger.Info("password reset requested: no mail sent, no such account", "email", email)
		} else if err != nil {
			h.Logger.Error("password reset requested: no mail sent, account lookup failed", "email", email, "err", err)
		} else if user.IsDemo {
			h.Logger.Info("password reset requested: no mail sent, demo accounts cannot reset", "email", email)
		} else if user.PasswordHash == "" {
			h.Logger.Info("password reset requested: no mail sent, the account has no password (single sign-on)", "email", email)
		} else {
			go h.sendPasswordReset(ctx, user, baseURL, ip)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(ok))
}

// sendPasswordReset mints the token and mails the link, after the
// response. baseURL and ip are read from the request before it ends.
func (h *Handlers) sendPasswordReset(ctx context.Context, user identity.User, baseURL, ip string) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	h.recordAuthAudit(ctx, user, nil, "password.reset_requested", ip, nil)
	if h.Mail == nil || !h.Mail.Configured(ctx) {
		h.Logger.Warn("password reset requested but SMTP not configured", "email", user.Email)
		return
	}
	raw, err := h.Identity.CreatePasswordResetToken(ctx, user.ID, resetTokenTTL)
	if err != nil {
		h.Logger.Error("create reset token failed", "err", err)
		return
	}
	link := fmt.Sprintf("%s/reset-password?token=%s", baseURL, raw)
	subject := "Reset your Sluicio password"
	bodyText := fmt.Sprintf(
		"We received a request to reset the password for your Sluicio account.\n\n"+
			"Reset it here (the link expires in 1 hour):\n%s\n\n"+
			"If you didn't request this, you can ignore this email — your password is unchanged.",
		link)
	if err := h.Mail.Send(ctx, []string{user.Email}, subject, bodyText); err != nil {
		h.Logger.Error("send reset email failed", "email", user.Email, "err", err)
		return
	}
	// Handed to the mail server: from here, a mail that never arrives is
	// that server's or the recipient's to explain.
	h.Logger.Info("password reset email handed to the mail server", "email", user.Email)
}

// resetPassword: POST /api/v1/auth/reset-password  (public)
//
// Consumes a reset token and sets a new password. On success every session
// for the user is revoked (an attacker who had one is kicked) and any other
// outstanding reset tokens are invalidated.
func (h *Handlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.NewPassword) < minPasswordLen {
		httpserver.WriteError(w, http.StatusBadRequest, fmt.Sprintf("password must be at least %d characters", minPasswordLen))
		return
	}
	if strings.TrimSpace(body.Token) == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "missing token")
		return
	}

	userID, err := h.Identity.ConsumePasswordResetToken(r.Context(), strings.TrimSpace(body.Token))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "This reset link is invalid or has expired. Request a new one.")
		return
	}

	if u, uerr := h.Identity.GetUserByID(r.Context(), userID); uerr == nil && u.IsDemo {
		httpserver.WriteError(w, http.StatusForbidden, "demo accounts cannot change their password")
		return
	}
	hash, err := identity.HashPassword(body.NewPassword)
	if err != nil {
		h.Logger.Error("hash password failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not set password")
		return
	}
	if err := h.Identity.SetPasswordHash(r.Context(), userID, hash); err != nil {
		h.Logger.Error("set password failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not set password")
		return
	}
	// Best-effort cleanup — a failure here doesn't undo the reset.
	if err := h.Identity.DeleteSessionsForUser(r.Context(), userID); err != nil {
		h.Logger.Warn("revoke sessions after reset failed", "err", err)
	}
	if err := h.Identity.InvalidateResetTokensForUser(r.Context(), userID); err != nil {
		h.Logger.Warn("invalidate reset tokens failed", "err", err)
	}
	if user, err := h.Identity.GetUserByID(r.Context(), userID); err == nil {
		h.recordAuthAudit(r.Context(), user, nil, "password.reset_completed", clientIP(r), nil)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// appBaseURL returns the public origin to build links against: the
// SLUICIO_APP_URL env if set, else the request's Origin header, else the
// request scheme + host. Trailing slash trimmed.
func (h *Handlers) appBaseURL(r *http.Request) string {
	if u := strings.TrimSpace(os.Getenv("SLUICIO_APP_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	if o := strings.TrimSpace(r.Header.Get("Origin")); o != "" {
		return strings.TrimRight(o, "/")
	}
	scheme := "https"
	if r.TLS == nil && !strings.Contains(r.Host, ":443") {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}
