// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// sessionTTL is the lifetime granted to a brand new session at
// login. We don't refresh it on every request (that'd be a hot row
// write); the sliding update is just last_used_at. Set this to a
// week to start — easy to dial later.
const sessionTTL = 7 * 24 * time.Hour

// loginBody is the JSON shape POST /api/v1/auth/login expects.
type loginBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginResponse is what the handler returns on success. The
// must_reset_password flag is carried up so the frontend can route
// straight into a "set a new password" wizard on first login of
// the seeded admin.
type loginResponse struct {
	User        identity.User         `json:"user"`
	Memberships []identity.Membership `json:"memberships"`
	// MustResetPassword is also on User; we duplicate it here for
	// frontend convenience (it's the one field the frontend will
	// most often switch on post-login).
	MustResetPassword bool `json:"must_reset_password"`
}

// login: POST /api/v1/auth/login
//
// Verifies the email + password against the local users table,
// creates a session row, and sets the Sluicio-Session cookie. The
// response body is the user + memberships so the SPA can populate
// its store without an immediate follow-up /me round-trip.
//
// Failure modes all return 401 with the same body so an attacker
// can't distinguish "no such user" from "wrong password" from "no
// local password set". Timing equalisation lives in
// identity.AuthenticatePassword (see the dummyHash branch).
func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	if h.Identity == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "auth not initialised")
		return
	}
	var body loginBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := strings.TrimSpace(body.Email)
	if email == "" || body.Password == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := h.Identity.AuthenticatePassword(r.Context(), email, body.Password)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			// Best-effort: bump the per-account failed-login counter for the
			// Members security stat, and audit the failure when the account
			// exists. Fire-and-forget on a detached context so it never
			// affects the 401 response timing (preserving the user-
			// enumeration dodge) and is a no-op for an unknown email.
			go func(email, ip string) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := h.Identity.RecordFailedLoginByEmail(ctx, email); err != nil {
					h.Logger.Warn("login: record failed-login failed", "err", err)
				}
				if user, err := h.Identity.GetUserByEmail(ctx, email); err == nil {
					h.recordAuthAudit(ctx, user, nil, "login.failed", ip, nil)
				}
			}(email, clientIP(r))
			httpserver.WriteError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		h.Logger.Error("login: authenticate failed", "err", err, "email", email)
		httpserver.WriteError(w, http.StatusInternalServerError, "login failed")
		return
	}

	// Password is correct. If the user has MFA enabled, don't mint a
	// session yet — return a short-lived pending token and require the
	// second factor via /auth/mfa-verify.
	if st, err := h.Identity.MFAStatus(r.Context(), user.ID); err == nil && st.Enabled {
		token, err := h.Identity.IssueMFAPendingToken(user.ID)
		if err != nil {
			h.Logger.Error("login: issue mfa token failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "login failed")
			return
		}
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{
			"mfa_required": true,
			"mfa_token":    token,
		})
		return
	}

	h.finishLogin(w, r, user)
}

// finishLogin mints a session for an authenticated user and writes the
// standard login response. Shared by password-only login and the MFA
// second step.
func (h *Handlers) finishLogin(w http.ResponseWriter, r *http.Request, user identity.User) {
	sess, err := h.Identity.CreateSession(r.Context(), user.ID, sessionTTL, r.UserAgent())
	if err != nil {
		h.Logger.Error("login: create session failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "login failed")
		return
	}
	if err := h.Identity.TouchLastLogin(r.Context(), user.ID); err != nil {
		h.Logger.Warn("login: touch last_login_at failed", "err", err)
	}
	memberships, err := h.Identity.ListMemberships(r.Context(), user.ID)
	if err != nil {
		h.Logger.Warn("login: list memberships failed", "err", err)
		memberships = nil
	}
	h.recordAuthAudit(r.Context(), user, memberships, "login.succeeded", clientIP(r), nil)
	http.SetCookie(w, sessionCookie(sess.ID, sess.ExpiresAt))
	httpserver.WriteJSON(w, http.StatusOK, loginResponse{
		User:              user,
		Memberships:       memberships,
		MustResetPassword: user.MustResetPassword,
	})
}

// logout: POST /api/v1/auth/logout
//
// Deletes the session row and instructs the browser to clear the
// cookie. Gated by Require — anonymous calls return 401, but the
// frontend can also just drop the cookie locally on a 401.
func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	if p := middleware.Principal(r); p.UserID != nil {
		h.recordAudit(r, "session.logout", "user", p.UserID.String(), nil)
	}
	// Honour either the new or legacy cookie name (rebrand grace
	// window). Delete the underlying session row either way.
	cookie, err := r.Cookie(middleware.SessionCookieName)
	if err != nil || cookie.Value == "" {
		cookie, err = r.Cookie(middleware.SessionCookieLegacyName)
	}
	if err == nil && cookie != nil && cookie.Value != "" {
		if err := h.Identity.DeleteSession(r.Context(), cookie.Value); err != nil {
			// Logged but not surfaced — the cookie's about to be
			// cleared either way.
			h.Logger.Warn("logout: delete session failed", "err", err)
		}
	}
	// Clear both cookies so a mid-migration browser doesn't keep
	// resurrecting the dead session via the legacy name.
	http.SetCookie(w, sessionCookieClear())
	http.SetCookie(w, sessionCookieClearLegacy())
	w.WriteHeader(http.StatusNoContent)
}

// me: GET /api/v1/me
//
// Returns the authenticated user + all their memberships. The
// frontend hits this on app boot to figure out who's logged in
// and which orgs to show in the switcher.
func (h *Handlers) me(w http.ResponseWriter, r *http.Request) {
	p, ok := middleware.PrincipalFromContext(r.Context())
	if !ok || p.UserID == nil {
		// Require should have caught this. Belt-and-braces.
		httpserver.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	user, err := h.Identity.GetUserByID(r.Context(), *p.UserID)
	if err != nil {
		h.Logger.Error("me: get user failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	memberships, err := h.Identity.ListMemberships(r.Context(), user.ID)
	if err != nil {
		h.Logger.Warn("me: list memberships failed", "err", err)
		memberships = nil
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"user":        user,
		"memberships": memberships,
		// Demo accounts are exempt (they can't enrol — the demo guard
		// blocks MFA setup); mirrors EnforceMFAEnrollment.
		"mfa_enrollment_required": !user.IsDemo && h.mfaEnrollmentRequired(r.Context(), user.ID),
		"principal":               p,
	})
}

// updateMeBody is the JSON for PATCH /api/v1/me. Either field can be
// omitted / empty to leave that value alone — the store treats empty
// strings as "no change".
type updateMeBody struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// updateMe: PATCH /api/v1/me
//
// Lets the authenticated user edit their own display name and/or
// email. Validation is shallow (non-empty if provided + basic email
// shape); the heavy lifting is the COALESCE-NULLIF in the store so
// either field can be sent or omitted independently.
//
// Email changes have a real blast radius (logins, IdP matching) but
// no extra confirmation step yet — we trust the cookie-gated session.
// Sessions on other devices keep working because they key on the user
// id, not the email.
func (h *Handlers) updateMe(w http.ResponseWriter, r *http.Request) {
	p, ok := middleware.PrincipalFromContext(r.Context())
	if !ok || p.UserID == nil {
		httpserver.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.demoAccountGuard(w, r) {
		return
	}
	var body updateMeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := strings.TrimSpace(body.Email)
	if email != "" && !strings.ContainsRune(email, '@') {
		httpserver.WriteError(w, http.StatusBadRequest, "email must contain @")
		return
	}
	if err := h.Identity.UpdateUserProfile(r.Context(), *p.UserID, body.Name, email); err != nil {
		if errors.Is(err, identity.ErrUserExists) {
			httpserver.WriteError(w, http.StatusConflict, "a user with that email already exists")
			return
		}
		h.Logger.Error("updateMe failed", "err", err, "user_id", *p.UserID)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	// Audit the identity change itself — historical entries keep the name
	// that was true when they were written (they're hash-chained; rewriting
	// them would be tampering), so this entry is the link between the old
	// and new identity. Recorded under the identity that performed the
	// action (the old one), per membership like other account-level events.
	if meta := profileChangeMeta(p.Name, body.Name, p.Email, email); meta != nil {
		h.recordAuthAudit(r.Context(),
			identity.User{ID: *p.UserID, Name: p.Name, Email: p.Email},
			nil, "user.profile_updated", clientIP(r), meta)
	}
	// Return the fresh row so the SPA can swap state without a /me
	// round-trip.
	user, err := h.Identity.GetUserByID(r.Context(), *p.UserID)
	if err != nil {
		h.Logger.Warn("updateMe: re-read failed", "err", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, user)
}

// changePasswordBody — POST /api/v1/me/password input.
type changePasswordBody struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// changeMyPassword: POST /api/v1/me/password
//
// Requires the caller to prove they know the current password before
// setting a new one — even though the session is already authenticated.
// This is the standard pattern (block account-takeover if a session is
// hijacked).
//
// Side effects on success:
//   - password_hash is replaced with a fresh argon2id digest
//   - must_reset_password is cleared (the first-login wizard relies on
//     this to bounce the seeded admin into here once)
//
// Sessions on other devices are NOT invalidated — that's a bigger
// surgery (delete all but the current session row) and lands as a
// follow-up if a customer asks. Tokens are unaffected; revoke them
// individually if the new password is being set because of compromise.
func (h *Handlers) changeMyPassword(w http.ResponseWriter, r *http.Request) {
	p, ok := middleware.PrincipalFromContext(r.Context())
	if !ok || p.UserID == nil {
		httpserver.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.demoAccountGuard(w, r) {
		return
	}
	var body changePasswordBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.NewPassword) < 8 {
		httpserver.WriteError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	user, err := h.Identity.GetUserByID(r.Context(), *p.UserID)
	if err != nil {
		h.Logger.Error("changeMyPassword: load user failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	// Verify current password — unless this user has no local password
	// (SSO-only), in which case any local-password set is acceptable
	// after authenticating via OIDC. For now SSO isn't wired, so we
	// always require the current password.
	if user.PasswordHash == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "no local password set; sign in via SSO instead")
		return
	}
	ok2, err := identity.VerifyPassword(body.CurrentPassword, user.PasswordHash)
	if err != nil {
		h.Logger.Error("changeMyPassword: verify failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "verification failed")
		return
	}
	if !ok2 {
		httpserver.WriteError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	newHash, err := identity.HashPassword(body.NewPassword)
	if err != nil {
		h.Logger.Error("changeMyPassword: hash failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not set password")
		return
	}
	if err := h.Identity.SetPasswordHash(r.Context(), *p.UserID, newHash); err != nil {
		h.Logger.Error("changeMyPassword: persist failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not set password")
		return
	}
	if user.MustResetPassword {
		if err := h.Identity.ClearMustResetPassword(r.Context(), *p.UserID); err != nil {
			h.Logger.Warn("changeMyPassword: clear must_reset failed", "err", err)
		}
	}
	h.recordAudit(r, "password.changed", "user", p.UserID.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// profileChangeMeta builds the old→new metadata for user.profile_updated,
// including only the fields that actually changed. Empty request fields
// mean "keep current" (the store treats them that way), so they're not
// changes. Returns nil when nothing changed — no entry then.
func profileChangeMeta(oldName, newName, oldEmail, newEmail string) map[string]any {
	m := map[string]any{}
	if n := strings.TrimSpace(newName); n != "" && n != oldName {
		m["old_name"] = oldName
		m["new_name"] = n
	}
	if e := strings.TrimSpace(newEmail); e != "" && e != oldEmail {
		m["old_email"] = oldEmail
		m["new_email"] = e
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// demoAccountGuard blocks the self-service account surface for shared
// demo accounts (users.is_demo): profile, password, MFA, tokens. One
// visitor must not be able to sabotage the login for everyone else.
// Returns true when the request was rejected.
func (h *Handlers) demoAccountGuard(w http.ResponseWriter, r *http.Request) bool {
	if middleware.Principal(r).IsDemo {
		httpserver.WriteError(w, http.StatusForbidden,
			"this is a shared demo account — profile and security settings are disabled")
		return true
	}
	return false
}

// blockDemo wraps org-destructive admin routes (org lifecycle, member
// management, SSO config, ingest keys): a shared demo account may hold an
// admin role to showcase the admin surfaces, but must not be able to break
// the shared environment or other users' access. Runs after the role gate,
// so ordinary members still get their usual 403 first.
func (h *Handlers) blockDemo(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if middleware.Principal(r).IsDemo {
			httpserver.WriteError(w, http.StatusForbidden,
				"this is a shared demo account — organization administration is disabled")
			return
		}
		next(w, r)
	}
}

// installState: GET /api/v1/auth/install-state
//
// Public (no auth required) — the Login page calls this before
// anyone has a session. Returns whether this Sluicio install is
// still in its fresh, no-one-has-logged-in-yet state.
//
// Used to gate the "ships with a default admin account" hint on the
// login page: helpful once on first boot, an information leak once
// the install is in real use.
//
// "Fresh" is defined as: no user row has last_login_at set. The
// seeded admin's first login flips that, so the hint disappears
// immediately after the first sign-in.
//
// The response also carries an optional login pre-fill for public demo
// cells: when SLUICIO_LOGIN_PREFILL_EMAIL is set (only ever on a demo
// deployment — the credentials are public by design there), the login
// form seeds its fields from it. Unset (every normal install) the key
// is absent and the frontend behaves as before.
func (h *Handlers) installState(w http.ResponseWriter, r *http.Request) {
	fresh := true // safer default — show the hint on backend errors
	if h.Identity != nil {
		anyLoggedIn, err := h.Identity.AnyUserHasLoggedIn(r.Context())
		if err != nil {
			h.Logger.Warn("installState: query failed", "err", err)
		} else {
			fresh = !anyLoggedIn
		}
	}
	resp := map[string]any{"fresh": fresh}
	if email := strings.TrimSpace(os.Getenv("SLUICIO_LOGIN_PREFILL_EMAIL")); email != "" {
		resp["prefill"] = map[string]string{
			"email":    email,
			"password": os.Getenv("SLUICIO_LOGIN_PREFILL_PASSWORD"),
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

// bootstrapAdmin: POST /api/v1/auth/bootstrap-admin
//
// Public but self-sealing: the first-run "create your admin account"
// screen posts here to personalize the seeded admin (email + name +
// password) before anyone has ever logged in. Once any login exists the
// endpoint answers 409 forever — no information beyond what the public
// install-state endpoint already reveals, and nothing an attacker can't
// do anyway on a cell still exposing the documented seed credentials.
func (h *Handlers) bootstrapAdmin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if email == "" || !strings.Contains(email, "@") {
		httpserver.WriteError(w, http.StatusBadRequest, "a valid email is required")
		return
	}
	if len(body.Password) < 8 {
		httpserver.WriteError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Admin"
	}
	if h.Identity == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "identity store unavailable")
		return
	}
	switch err := h.Identity.BootstrapAdmin(r.Context(), email, name, body.Password); {
	case errors.Is(err, identity.ErrInstallNotFresh):
		httpserver.WriteError(w, http.StatusConflict, "this install is already set up — sign in instead")
		return
	case errors.Is(err, identity.ErrUserExists):
		httpserver.WriteError(w, http.StatusConflict, "a user with that email already exists")
		return
	case err != nil:
		h.Logger.Error("bootstrap admin failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not set up the admin account")
		return
	}
	h.Logger.Info("first-run bootstrap: admin account personalized", "email", email)
	w.WriteHeader(http.StatusNoContent)
}

// sessionCookie returns the Set-Cookie value used on login. We set:
//
//   - Path=/ so every cell-api path receives it.
//   - HttpOnly so JS can't read it (XSS doesn't grant the session).
//   - SameSite=Lax to allow top-level navigations from the frontend
//     origin while blocking CSRF from third-party form posts. Lax is
//     the right floor for an SPA that hits /api/v1 on the same
//     origin (via Vite's proxy in dev, same host in prod).
//   - Secure is OFF in dev (the cell-api listens on plain http);
//     production should toggle this on via the auth config.
//   - Max-Age derived from expires_at so the browser drops it on
//     time even without a manual logout.
func sessionCookie(sessionID string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure: true,  // ← enable in prod via auth config
	}
}

// sessionCookieClear is the Set-Cookie value used on logout. Empty
// value + MaxAge=-1 instructs the browser to discard whatever was
// there.
func sessionCookieClear() *http.Cookie {
	return &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// sessionCookieClearLegacy clears the pre-rebrand "Conduit-Session"
// cookie. Issued on logout in addition to the new-name clear so a
// browser that still carries the legacy cookie doesn't resurrect the
// session via the read-path grace window. Remove once the legacy
// constants are removed (one release post-rebrand, per issue #23).
func sessionCookieClearLegacy() *http.Cookie {
	return &http.Cookie{
		Name:     middleware.SessionCookieLegacyName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}
