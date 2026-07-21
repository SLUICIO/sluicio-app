// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Server-side enforcement of must_reset_password: a human whose password
// was set to a temporary one (by an admin, or first-login rotation) must
// change it before doing anything else. Mirrors EnforceMFAEnrollment —
// runs after the auth middleware (needs the Principal) and in front of
// every handler.

package api

import (
	"net/http"
	"strings"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// pwResetExemptPaths stay reachable while a change is pending: the SPA's
// boot calls plus the change-password endpoint itself.
var pwResetExemptPaths = []string{
	"/api/v1/me",          // boot + the must_reset_password flag itself
	"/api/v1/me/password", // the whole point — let them change it
	"/api/v1/license",     // entitlement display on the boot path
	"/api/v1/auth",        // logout et al (pre-auth anyway; defence in depth)
}

func pwResetExempt(path string) bool {
	for _, p := range pwResetExemptPaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// EnforcePasswordReset wraps the authed mux.
func (h *Handlers) EnforcePasswordReset(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := middleware.Principal(r)
		// Session humans only: API tokens are never gated by a UI flow.
		if p.Kind != identity.PrincipalUser || !p.MustResetPassword || pwResetExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		httpserver.WriteJSON(w, http.StatusForbidden, map[string]any{
			"error":   "password_reset_required",
			"message": "Your password must be changed before you can continue. Set a new password to proceed.",
		})
	})
}
