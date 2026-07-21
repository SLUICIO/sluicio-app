// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Server-side enforcement of the org-wide MFA policy (Enterprise
// mfa_policy). The MFAEnrollmentBanner is the friendly face; this is the
// actual gate: once an admin turns mfa_required on, a signed-in human who
// hasn't enrolled can reach only the endpoints needed to enrol (and to
// find out they must). Service accounts are exempt — they can't do TOTP.
//
// Fails open on lookup errors, like mfaEnrollmentRequired: a database
// blip must never lock the whole org out.

package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/license"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// mfaPolicyCacheTTL bounds how stale the cached mfa_required flag may be.
// Writes through patchSecuritySettings update the cache immediately; the
// TTL only matters for out-of-band changes (another replica, direct SQL).
const mfaPolicyCacheTTL = 30 * time.Second

// mfaPolicyCache avoids a cell_settings query on every request.
type mfaPolicyCache struct {
	mu      sync.Mutex
	value   bool
	fetched time.Time
}

// mfaPolicyOn returns the cached mfa_required flag, refreshing it from the
// settings store when the TTL has lapsed. Fails open (false) on error.
func (h *Handlers) mfaPolicyOn(ctx context.Context) bool {
	h.mfaPolicy.mu.Lock()
	defer h.mfaPolicy.mu.Unlock()
	if time.Since(h.mfaPolicy.fetched) < mfaPolicyCacheTTL {
		return h.mfaPolicy.value
	}
	v, err := h.Settings.GetMFARequired(ctx)
	if err != nil {
		h.Logger.Warn("mfa policy lookup failed", "err", err)
		return h.mfaPolicy.value // keep last known rather than flapping
	}
	h.mfaPolicy.value = v
	h.mfaPolicy.fetched = time.Now()
	return v
}

// setMFAPolicyCache writes through after a successful policy change.
func (h *Handlers) setMFAPolicyCache(v bool) {
	h.mfaPolicy.mu.Lock()
	h.mfaPolicy.value = v
	h.mfaPolicy.fetched = time.Now()
	h.mfaPolicy.mu.Unlock()
}

// mfaEnrollExemptPaths are reachable while enrollment is pending: the SPA's
// boot calls plus the enrollment flow itself. /api/v1/auth/* is already on
// the pre-auth skip list in main.
var mfaEnrollExemptPaths = []string{
	"/api/v1/me",          // boot + the mfa_enrollment_required flag itself
	"/api/v1/account/mfa", // status / setup / enable
	"/api/v1/license",     // entitlement display on the boot path
	"/api/v1/auth",        // logout et al (defence in depth; pre-auth anyway)
}

func mfaEnrollExempt(path string) bool {
	for _, p := range mfaEnrollExemptPaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// EnforceMFAEnrollment wraps the authed mux. Runs after the auth middleware
// (needs the Principal) and before every handler.
func (h *Handlers) EnforceMFAEnrollment(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := middleware.Principal(r)
		if p.UserID == nil || p.Kind != identity.PrincipalUser {
			next.ServeHTTP(w, r)
			return
		}
		// Demo accounts can't enrol (the demo guard blocks MFA setup), so
		// gating them would deadlock. Shared demo logins shouldn't carry
		// TOTP anyway — exempt them.
		if p.IsDemo {
			next.ServeHTTP(w, r)
			return
		}
		if h.Settings == nil || !h.featureEntitled(license.FeatureMFAPolicy) {
			next.ServeHTTP(w, r)
			return
		}
		if !h.mfaPolicyOn(r.Context()) || mfaEnrollExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		st, err := h.Identity.MFAStatus(r.Context(), *p.UserID)
		if err != nil || st.Enabled {
			next.ServeHTTP(w, r) // fail open on error
			return
		}
		httpserver.WriteJSON(w, http.StatusForbidden, map[string]any{
			"error":   "mfa_enrollment_required",
			"message": "Your organization requires two-factor authentication. Set it up under Account → Two-factor to continue.",
		})
	})
}
