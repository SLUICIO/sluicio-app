// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package middleware holds the cross-cutting HTTP middlewares for
// cell-api. Right now there's only one — auth — but the package
// gives the auth code somewhere to live without bloating internal/api
// and keeps the import cycle clean (handlers depend on middleware,
// not the other way round).
//
// The auth middleware resolves an incoming request to an
// identity.Principal by trying, in order:
//
//  1. A `Conduit-Session` HTTP-only cookie carrying an opaque
//     session id. Validated against the sessions table; the user
//     and per-org role are looked up via org_members + the
//     X-Conduit-Org request header (defaults to the user's first
//     membership).
//  2. An `Authorization: Bearer con_...` API token. Looked up by
//     its plaintext prefix (first 12 chars), verified by argon2id
//     against the stored hash, then resolved to a Principal via
//     api_tokens.owner_type ∈ {user, service_account}.
//
// Either path lands the Principal in the request context via
// WithPrincipal; handlers downstream pull it with PrincipalFromContext.
//
// In P2 the middleware is opt-in per route via `Require` — only the
// auth-management endpoints + the demo handler are wired up. P3 will
// wrap the whole mux once every existing handler has been ported.
package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	imclickhouse "github.com/integration-monitor/integration-monitor/pkg/clickhouse"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
)

// Cookie name that carries the session id. HTTP-only + SameSite=Lax
// is set on creation in the login handler; the middleware just
// reads the value.
//
// Renamed from "Conduit-Session" with the Conduit → Sluicio rebrand
// (issue #23). The legacy name is still accepted on the read path
// (see readSessionCookie) for a one-release grace window so any user
// who was signed in pre-rename stays signed in. New cookies are
// always written with the new name.
const SessionCookieName = "Sluicio-Session"

// SessionCookieLegacyName is the pre-rebrand cookie name. Read-only,
// for the grace window. Remove after one release cycle has passed.
const SessionCookieLegacyName = "Conduit-Session"

// HeaderActiveOrg lets the frontend (or API caller) tell us which
// org-scoped role the principal should be evaluated against on this
// request. Optional — omit and we default to the first membership.
//
// Renamed from "X-Conduit-Org" with the Conduit → Sluicio rebrand.
// Same grace pattern as the cookie: legacy name still read, new name
// is what the SPA sends.
const HeaderActiveOrg = "X-Sluicio-Org"

// HeaderActiveOrgLegacy is the pre-rebrand header name. Read-only,
// for the grace window.
const HeaderActiveOrgLegacy = "X-Conduit-Org"

// ctxKey is a private type so package-external callers can't
// accidentally collide with our context key.
type ctxKey int

const (
	keyPrincipal ctxKey = iota
)

// WithPrincipal returns a derived context carrying p. Handlers should
// pull it back out with PrincipalFromContext rather than looking up the
// key directly. It also stamps the tenant-isolation ClickHouse filter
// (clickhouse.WithOrgFilter) for the principal's org, so EVERY telemetry
// read on this request is automatically scoped to that org — the central
// Phase-2 isolation boundary. A zero org (no membership) → no filter.
func WithPrincipal(ctx context.Context, p identity.Principal) context.Context {
	ctx = context.WithValue(ctx, keyPrincipal, p)
	if p.OrgID != uuid.Nil {
		ctx = imclickhouse.WithOrgFilter(ctx, p.OrgID.String())
	}
	return ctx
}

// PrincipalFromContext returns the Principal attached to ctx by an
// upstream auth middleware, plus a boolean reporting whether one was
// present. Handlers gated by Require can safely ignore the bool.
func PrincipalFromContext(ctx context.Context) (identity.Principal, bool) {
	p, ok := ctx.Value(keyPrincipal).(identity.Principal)
	return p, ok
}

// OrgID is a convenience for handlers that just want "what org is
// this request for?" without unpacking the full Principal. Returns
// the zero UUID if no principal is in context (which shouldn't
// happen on a Wrap-gated route — but never panics).
func OrgID(r *http.Request) uuid.UUID {
	p, _ := PrincipalFromContext(r.Context())
	return p.OrgID
}

// OrgIDFromContext is the same as OrgID but for helpers deep in the
// call stack that only have a context.Context (not the original
// *http.Request). The principal flows through context.WithValue, so
// any ctx derived from the request carries it.
func OrgIDFromContext(ctx context.Context) uuid.UUID {
	p, _ := PrincipalFromContext(ctx)
	return p.OrgID
}

// Principal returns the resolved Principal for handlers that want
// more than just the org id (e.g. for audit logging "who did this").
// Same null-safety guarantee as OrgID.
func Principal(r *http.Request) identity.Principal {
	p, _ := PrincipalFromContext(r.Context())
	return p
}

// Resolver bundles the dependencies the middleware needs. Held by
// the api.Handlers struct and passed into Require / RequireRole at
// route-registration time.
type Resolver struct {
	Identity *identity.Store
}

// errors surfaced internally by the resolver. None of these are
// matched on outside the package — they translate to 401s.
var (
	errNoCredentials = errors.New("middleware: no credentials")
	errBadSession    = errors.New("middleware: bad session")
	errBadToken      = errors.New("middleware: bad token")
	errNoMembership  = errors.New("middleware: not a member of the active org")
)

// Resolve inspects the request for either a session cookie or a
// bearer token and returns the resulting Principal. Returns false
// + nil error when no credentials are present (so callers can choose
// to either 401 or fall through to anonymous behaviour).
func (r *Resolver) Resolve(req *http.Request) (identity.Principal, bool, error) {
	// 1) Bearer token wins if present — it's explicit, the client
	//    asked for it. (Cookies on a programmatic caller would be a
	//    surprise.)
	if tok := bearerToken(req); tok != "" {
		// Only resolve tokens that look like our format. Any other
		// bearer scheme is treated as no-credentials, so callers
		// pairing a misconfigured tool with cookie-auth still
		// degrade gracefully.
		if !identity.LooksLikeToken(tok) {
			return identity.Principal{}, false, errBadToken
		}
		lookup, err := r.Identity.ResolveAPIToken(req.Context(), tok)
		if err != nil {
			return identity.Principal{}, false, errBadToken
		}
		return r.principalFromToken(req, lookup)
	}

	// 2) Session cookie path. Accept either the new name or the
	//    legacy "Conduit-Session" cookie for the rebrand grace
	//    window (see issue #23).
	cookie, err := req.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		cookie, err = req.Cookie(SessionCookieLegacyName)
	}
	if err != nil || cookie == nil || cookie.Value == "" {
		return identity.Principal{}, false, nil
	}
	sess, err := r.Identity.GetSession(req.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			return identity.Principal{}, false, errBadSession
		}
		return identity.Principal{}, false, err
	}
	user, err := r.Identity.GetUserByID(req.Context(), sess.UserID)
	if err != nil {
		return identity.Principal{}, false, err
	}

	// Record human activity for the per-user "last active" stat. Only the
	// session (cookie) path — i.e. an interactive user — not API tokens.
	// Fire-and-forget on a detached context (the request context is
	// cancelled when the response is written) and throttled in SQL to one
	// write per user per 5 min, so it never adds latency to the hot path.
	go func(uid uuid.UUID) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Identity.TouchLastActive(ctx, uid)
	}(user.ID)

	// Pick the active org. Prefer the X-Sluicio-Org header (slug or
	// UUID); fall back to the user's first membership. If the user
	// has no memberships at all, they're authenticated but have no
	// scope — return a Principal with a zero OrgID + viewer role so
	// the /me endpoint can still answer. Other handlers that
	// require an org will check OrgID and 403.
	memberships, err := r.Identity.ListMemberships(req.Context(), user.ID)
	if err != nil {
		return identity.Principal{}, false, err
	}

	active := activeOrgHeader(req)
	var orgID uuid.UUID
	var role identity.Role
	switch {
	case len(memberships) == 0:
		// Authenticated but org-less. Caller decides what to do.
	case active == "":
		orgID = memberships[0].Org.ID
		role = memberships[0].Role
	default:
		// Match by slug first (the friendlier identifier), then by UUID.
		matched := false
		for _, m := range memberships {
			if m.Org.Slug == active || m.Org.ID.String() == active {
				orgID = m.Org.ID
				role = m.Role
				matched = true
				break
			}
		}
		if !matched {
			return identity.Principal{}, false, errNoMembership
		}
	}

	uid := user.ID
	return identity.Principal{
		Kind:              identity.PrincipalUser,
		UserID:            &uid,
		OrgID:             orgID,
		Role:              role,
		BaseRole:          role, // session auth isn't scope-capped
		Email:             user.Email,
		Name:              user.Name,
		IsOperator:        user.IsOperator,
		IsDemo:            user.IsDemo,
		MustResetPassword: user.MustResetPassword,
	}, true, nil
}

// principalFromToken builds a Principal from a successfully verified
// API token. PATs (owner_type='user') inherit the user's role on the
// active org (chosen the same way as cookie auth). Service accounts
// have their org + role fixed on the SA row itself — X-Sluicio-Org
// is ignored.
func (r *Resolver) principalFromToken(req *http.Request, lookup identity.TokenLookup) (identity.Principal, bool, error) {
	switch lookup.OwnerType {
	case "user":
		user, err := r.Identity.GetUserByID(req.Context(), lookup.OwnerID)
		if err != nil {
			return identity.Principal{}, false, err
		}
		memberships, err := r.Identity.ListMemberships(req.Context(), user.ID)
		if err != nil {
			return identity.Principal{}, false, err
		}
		// Same active-org logic as cookie auth — token requests
		// can also pin via X-Sluicio-Org (legacy X-Conduit-Org
		// still accepted for the grace window).
		active := activeOrgHeader(req)
		var orgID uuid.UUID
		var role identity.Role
		switch {
		case len(memberships) == 0:
			// PAT for a user with no memberships → can authenticate
			// to /me only. Most routes will 403.
		case active == "":
			orgID = memberships[0].Org.ID
			role = memberships[0].Role
		default:
			matched := false
			for _, m := range memberships {
				if m.Org.Slug == active || m.Org.ID.String() == active {
					orgID = m.Org.ID
					role = m.Role
					matched = true
					break
				}
			}
			if !matched {
				return identity.Principal{}, false, errNoMembership
			}
		}
		uid := user.ID
		return identity.Principal{
			Kind:   identity.PrincipalUser,
			UserID: &uid,
			OrgID:  orgID,
			// Cap the token below the user's role when scoped (least-privilege).
			Role:       role.Cap(identity.Role(lookup.ScopeRole)),
			BaseRole:   role, // uncapped — for READ visibility (see Principal.ReadRole)
			Email:      user.Email,
			Name:       user.Name,
			IsOperator: user.IsOperator,
			IsDemo:     user.IsDemo,
		}, true, nil

	case "service_account":
		sa, err := r.Identity.GetServiceAccount(req.Context(), lookup.OwnerID)
		if err != nil {
			return identity.Principal{}, false, err
		}
		said := sa.ID
		return identity.Principal{
			Kind:             identity.PrincipalServiceAccount,
			ServiceAccountID: &said,
			SAScope:          sa.Scope, // scoped → group-resolved visibility; org_wide → org reads
			OrgID:            sa.OrgID,
			// Cap the token below the service account's role when scoped.
			Role:     sa.Role.Cap(identity.Role(lookup.ScopeRole)),
			BaseRole: sa.Role, // uncapped — for READ visibility
			Name:     sa.Name,
		}, true, nil

	default:
		return identity.Principal{}, false, errBadToken
	}
}

// bearerToken returns the token from `Authorization: Bearer <tok>`,
// or "" if no such header is present.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// activeOrgHeader reads the X-Sluicio-Org header, falling back to the
// legacy X-Conduit-Org for the rebrand grace window. New is preferred
// when both are present (a client mid-migration that sends both should
// get the new value).
func activeOrgHeader(r *http.Request) string {
	if v := r.Header.Get(HeaderActiveOrg); v != "" {
		return v
	}
	return r.Header.Get(HeaderActiveOrgLegacy)
}
