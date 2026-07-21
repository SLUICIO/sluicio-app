// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// Wrap gates an http.Handler (typically the cell-api's full mux)
// behind auth. Every request must arrive with a valid session
// cookie or bearer token unless its path matches one of the
// skip entries. On success, the resolved Principal is injected
// into the request context (and recovered later via
// PrincipalFromContext / OrgID).
//
// skip entries are matched against r.URL.Path with an exact or
// prefix match. Trailing "/" on a skip entry means "everything
// under this prefix"; no trailing slash means "exactly this path."
//
// Used by cmd/cell-api/main.go in P3 to gate the entire surface.
// /api/v1/auth/login and /api/v1/auth/logout are typically skipped
// (they ARE the input to auth and bring down their own creds).
func (r *Resolver) Wrap(skip []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if pathMatchesSkip(req.URL.Path, skip) {
			next.ServeHTTP(w, req)
			return
		}
		p, ok, err := r.Resolve(req)
		if err != nil {
			mcpAuthChallenge(w, req)
			httpserver.WriteError(w, http.StatusUnauthorized, errStatusMessage(err))
			return
		}
		if !ok {
			mcpAuthChallenge(w, req)
			httpserver.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, req.WithContext(WithPrincipal(req.Context(), p)))
	})
}

// mcpAuthChallenge adds the RFC 9728 WWW-Authenticate header on an
// unauthenticated MCP request, pointing the client at the protected-resource
// metadata so its OAuth connector flow can discover the authorization server.
func mcpAuthChallenge(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/api/v1/mcp" {
		return
	}
	scheme := req.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if req.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	w.Header().Set("WWW-Authenticate",
		`Bearer resource_metadata="`+scheme+"://"+req.Host+`/.well-known/oauth-protected-resource"`)
}

func pathMatchesSkip(path string, skip []string) bool {
	for _, s := range skip {
		if strings.HasSuffix(s, "/") {
			if strings.HasPrefix(path, s) {
				return true
			}
			continue
		}
		if path == s {
			return true
		}
	}
	return false
}

// Require wraps next in an auth check. On a valid session-cookie or
// bearer-token credential, the Principal is injected into the
// request context and next runs. On any failure path the handler
// returns 401 without invoking next.
//
// In P2 this was the per-route gate; in P3 the mux itself is
// wrapped with Wrap so every route gets auth by default. Require
// stays around for handlers that want to express "this route is
// auth-required" inline as documentation even though Wrap would
// catch it anyway.
func (r *Resolver) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		p, ok, err := r.Resolve(req)
		if err != nil {
			// We deliberately collapse all credential-failure reasons
			// into one response — the client doesn't need to know
			// whether the session was expired vs the org membership
			// was wrong. The detailed reason is on the cell-api logs.
			httpserver.WriteError(w, http.StatusUnauthorized, errStatusMessage(err))
			return
		}
		if !ok {
			httpserver.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, req.WithContext(WithPrincipal(req.Context(), p)))
	}
}

// RequireRole wraps Require with an additional role floor. The
// principal's role on its active org must satisfy the predicate
// `need` (e.g. role.CanWrite or role.CanAdmin) or the request is
// rejected with 403. Use this for mutating endpoints in P3.
func (r *Resolver) RequireRole(need func(identity.Role) bool, next http.HandlerFunc) http.HandlerFunc {
	return r.Require(func(w http.ResponseWriter, req *http.Request) {
		p, _ := PrincipalFromContext(req.Context())
		if !need(p.Role) {
			httpserver.WriteError(w, http.StatusForbidden, "insufficient role for this action")
			return
		}
		next.ServeHTTP(w, req)
	})
}

// RequireWriteAnywhere is the G6 gate for resource-creation routes
// that should be available to any user who has editor+ permission
// SOMEWHERE in the org — either as an org-wide role OR within at
// least one group they belong to. Used on dashboard / alert create
// where the user might be an org-viewer but a group-editor.
//
// Service accounts pass if their (fixed) role is editor or admin.
func (r *Resolver) RequireWriteAnywhere(next http.HandlerFunc) http.HandlerFunc {
	return r.Require(func(w http.ResponseWriter, req *http.Request) {
		p, _ := PrincipalFromContext(req.Context())
		// Fast path on org-wide role.
		if p.Role.CanWrite() {
			next.ServeHTTP(w, req)
			return
		}
		// Service accounts: trust the role on the SA row.
		if p.Kind == identity.PrincipalServiceAccount {
			httpserver.WriteError(w, http.StatusForbidden, "insufficient role for this action")
			return
		}
		// Org-viewer: ask the identity store whether any of their
		// group memberships grants editor+.
		if p.UserID == nil {
			httpserver.WriteError(w, http.StatusForbidden, "insufficient role for this action")
			return
		}
		// A scope-capped token (e.g. a viewer-scoped PAT / the MCP OAuth
		// token) is a hard write ceiling: don't let the user's group
		// memberships re-expand it back to write.
		if p.ScopeCapped() {
			httpserver.WriteError(w, http.StatusForbidden, "insufficient role for this action")
			return
		}
		ok, err := r.Identity.CanUserWriteAnywhere(req.Context(), *p.UserID, p.OrgID)
		if err != nil || !ok {
			httpserver.WriteError(w, http.StatusForbidden, "insufficient role for this action")
			return
		}
		next.ServeHTTP(w, req)
	})
}

// RequireOperator gates the cell-operator surface (org lifecycle +
// cell-wide settings). The principal must be a user flagged is_operator.
// Service accounts are never operators, and a scope-capped token can't
// perform operator actions even if its owning user is an operator — the
// cap is a hard ceiling that operator status must not re-expand.
func (r *Resolver) RequireOperator(next http.HandlerFunc) http.HandlerFunc {
	return r.Require(func(w http.ResponseWriter, req *http.Request) {
		p, _ := PrincipalFromContext(req.Context())
		if !p.IsOperator || p.ScopeCapped() {
			httpserver.WriteError(w, http.StatusForbidden, "operator access required")
			return
		}
		next.ServeHTTP(w, req)
	})
}

// errStatusMessage maps an internal resolution error to a user-
// facing message kept deliberately short on detail. The cell-api
// logs the real one.
func errStatusMessage(err error) string {
	switch {
	case errors.Is(err, errNoMembership):
		return "not a member of the requested org"
	case errors.Is(err, errBadSession),
		errors.Is(err, errBadToken),
		errors.Is(err, errNoCredentials):
		return "authentication required"
	default:
		return "authentication failed"
	}
}
