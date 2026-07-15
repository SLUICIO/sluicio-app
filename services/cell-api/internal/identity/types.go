// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package identity is Conduit's auth+authz data layer. It owns the
// orgs / users / org_members / api_tokens / service_accounts /
// sessions / auth_providers / oidc_subjects tables and the read +
// write paths against them.
//
// Conduit ships with native identity: users sign in with email +
// password stored in `users.password_hash`, sessions live in the
// `sessions` table behind an HTTP-only cookie. Customers who already
// have an IdP can configure per-org OIDC providers via the
// `auth_providers` table; OIDC sign-ins link external `sub`s to
// Conduit users via `oidc_subjects`.
//
// Three principal shapes can be authenticated:
//   - User    (a human, via session cookie or via OIDC then session)
//   - Service (a non-human owned by an org, via api_tokens bearer)
//
// All authorisation downstream operates on a Principal struct that
// resolves both into a (UserOrServiceID, OrgID, Role) tuple.
package identity

import (
	"time"

	"github.com/google/uuid"
)

// Role is the closed enum of org-membership permissions. The same
// values are enforced by CHECK constraints on org_members.role and
// service_accounts.role.
type Role string

const (
	// RoleAdmin can do everything in the org: invite members,
	// manage tokens, edit schemas / maps / alerts, configure SSO.
	RoleAdmin Role = "admin"
	// RoleEditor can mutate org resources (services config, schemas,
	// alerts, etc.) but cannot manage membership, tokens, or SSO.
	RoleEditor Role = "editor"
	// RoleViewer can read everything but mutate nothing. Useful for
	// dashboard-only access (NOC, stakeholders).
	RoleViewer Role = "viewer"
)

// IsValid reports whether r is one of the three known roles. Used by
// the API layer to reject bad input before it hits the DB CHECK.
func (r Role) IsValid() bool {
	switch r {
	case RoleAdmin, RoleEditor, RoleViewer:
		return true
	}
	return false
}

// CanWrite returns true for roles that can mutate org resources
// (everything except viewer).
func (r Role) CanWrite() bool {
	return r == RoleAdmin || r == RoleEditor
}

// rank orders roles for least-privilege comparison (higher = more power).
// An unknown role ranks 0 so it never out-privileges a real one.
func (r Role) rank() int {
	switch r {
	case RoleAdmin:
		return 3
	case RoleEditor:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// Cap returns the more restrictive of r and the cap. An empty / invalid cap
// means "no cap" (return r unchanged). Used to enforce a per-token role cap so
// a token can never exceed — only narrow — its owner's role.
func (r Role) Cap(cap Role) Role {
	if !cap.IsValid() {
		return r
	}
	if cap.rank() < r.rank() {
		return cap
	}
	return r
}

// CanAdmin returns true only for the org admin role. Used for the
// member/token/SSO management surfaces and any destructive operation
// (e.g. org delete) that wants the strongest gate.
func (r Role) CanAdmin() bool {
	return r == RoleAdmin
}

// Org is one row in the orgs table.
type Org struct {
	ID        uuid.UUID `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// User is one row in the users table. password_hash and the OIDC-
// linkage live in separate tables / fields so the same user row can
// represent a local-password account, an SSO-only account, or both.
type User struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Name  string    `json:"name"`
	// PasswordHash is the argon2id PHC-formatted hash. Never returned
	// over the wire — the `-` json tag drops it from API responses.
	// Empty means the user has no local password (SSO-only).
	PasswordHash      string `json:"-"`
	MustResetPassword bool   `json:"must_reset_password"`
	// IsOperator marks a cell operator (super-admin above the org roles):
	// manages orgs + cell-wide settings. Org-independent, so it lives on
	// the user, not org_members.
	IsOperator bool `json:"is_operator"`
	// IsDemo marks a shared demo account: the self-service surface
	// (profile, password, MFA, tokens) is blocked so one visitor can't
	// sabotage the login for the rest. RBAC still governs everything else.
	IsDemo      bool       `json:"is_demo"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at,omitempty"`
	// Per-user activity stats (Settings → Members). Populated by
	// ListOrgMembers; zero-valued on the auth/lookup paths that don't
	// select them. MFAEnabled is derived live from user_mfa.enabled_at.
	LoginCount       int64      `json:"login_count,omitempty"`
	FailedLoginCount int        `json:"failed_login_count,omitempty"`
	LastActiveAt     *time.Time `json:"last_active_at,omitempty"`
	MFAEnabled       bool       `json:"mfa_enabled,omitempty"`
}

// Membership is a single (user, org) → role assignment, joined with
// the org row so the API can return a one-shot list of "what orgs
// can this user see, and as what".
type Membership struct {
	Org      Org       `json:"org"`
	Role     Role      `json:"role"`
	JoinedAt time.Time `json:"joined_at,omitempty"`
}

// Session is one row in the sessions table — backs the HTTP-only
// cookie the cell-api sets on login. ID is the opaque random string
// that the browser carries; everything else is local state.
type Session struct {
	ID         string    `json:"-"`
	UserID     uuid.UUID `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	UserAgent  string    `json:"user_agent"`
}

// ServiceAccount is a non-human principal owned by an org. Its role
// is per-account (not per-org-membership) because a service account
// always lives in exactly one org.
type ServiceAccount struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"org_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Role        Role       `json:"role"`
	// Scope is the visibility model (docs/service-account-scoping-design.md):
	// SAScopeScoped (default) — deny-by-default, visibility resolved from
	// group memberships exactly like a user's; SAScopeOrgWide — explicit,
	// audited opt-in to org-wide reads. Role stays the capability axis
	// either way.
	Scope     ServiceAccountScope `json:"scope"`
	CreatedBy *uuid.UUID          `json:"created_by,omitempty"`
	CreatedAt time.Time           `json:"created_at,omitempty"`
}

// ServiceAccountScope is the service_accounts.scope enum.
type ServiceAccountScope string

const (
	SAScopeScoped  ServiceAccountScope = "scoped"
	SAScopeOrgWide ServiceAccountScope = "org_wide"
)

func (s ServiceAccountScope) IsValid() bool {
	return s == SAScopeScoped || s == SAScopeOrgWide
}

// AuthProvider is one row in the auth_providers table — a per-org
// OIDC configuration. Customers add these via "Settings → SSO" when
// they're ready to plug Conduit into their corporate IdP.
type AuthProvider struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"org_id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"` // "oidc"
	IssuerURL string    `json:"issuer_url"`
	ClientID  string    `json:"client_id"`
	// ClientSecret is never returned over the wire. The `-` tag
	// hides it from API responses; the management endpoints
	// surface it once at create time only.
	ClientSecret string `json:"-"`
	ClaimEmail   string `json:"claim_email"`
	ClaimName    string `json:"claim_name"`
	ClaimSub     string `json:"claim_sub"`
	// ClaimGroups names the ID-token claim that carries the user's IdP
	// groups/roles (default "groups"); Scopes is the OAuth scope string
	// requested at authorize time. DefaultRole is the org role granted to
	// users with no matching claim mapping; JITProvisioning creates a user
	// on first SSO login when no local/linked account exists. See docs/sso.md.
	Scopes          string    `json:"scopes"`
	ClaimGroups     string    `json:"claim_groups"`
	DefaultRole     Role      `json:"default_role"`
	JITProvisioning bool      `json:"jit_provisioning"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

// ClaimMapping maps one IdP groups-claim value to an org role and/or a team
// (group) membership. At least one of OrgRole / GroupID is set. See docs/sso.md.
type ClaimMapping struct {
	ID         uuid.UUID  `json:"id"`
	ProviderID uuid.UUID  `json:"provider_id"`
	ClaimValue string     `json:"claim_value"`
	OrgRole    Role       `json:"org_role,omitempty"`
	GroupID    *uuid.UUID `json:"group_id,omitempty"`
	GroupRole  Role       `json:"group_role,omitempty"`
	CreatedAt  time.Time  `json:"created_at,omitempty"`
}

// SSOLoginState is the transient per-login PKCE/CSRF state (single-use, short
// TTL) stashed between the authorize redirect and the callback.
type SSOLoginState struct {
	State        string
	ProviderID   uuid.UUID
	Nonce        string
	CodeVerifier string
	RedirectTo   string
	ExpiresAt    time.Time
}

// PrincipalKind discriminates "a request authenticates as a User" from
// "a request authenticates as a ServiceAccount". The middleware (P2)
// returns a Principal carrying both this and the relevant id; the
// authorisation helpers below normalise both into a Role per org.
type PrincipalKind string

const (
	PrincipalUser           PrincipalKind = "user"
	PrincipalServiceAccount PrincipalKind = "service_account"
)

// Principal is the resolved identity attached to an authenticated
// request. The middleware fills it from either:
//   - a Conduit session cookie (kind=user, UserID set, OrgID + Role
//     resolved from the active-org header against org_members), or
//   - a bearer API token "con_..." (kind=user or service_account,
//     fields set from the api_tokens / service_accounts join).
//
// All authz checks downstream operate on Principal.
type Principal struct {
	Kind             PrincipalKind `json:"kind"`
	UserID           *uuid.UUID    `json:"user_id,omitempty"`
	ServiceAccountID *uuid.UUID    `json:"service_account_id,omitempty"`
	// SAScope mirrors service_accounts.scope for service-account
	// principals: scoped SAs resolve visibility through group
	// memberships (deny-by-default), org-wide SAs read the whole org.
	// Empty for users.
	SAScope ServiceAccountScope `json:"sa_scope,omitempty"`
	OrgID   uuid.UUID           `json:"org_id"`
	Role    Role                `json:"role"`
	// BaseRole is the identity's role on the active org BEFORE any token
	// scope-cap (ScopeRole). Role may be capped below it for least-privilege;
	// BaseRole is what the identity could do unscoped. Used for READ
	// visibility: scoping a token down removes WRITE capability but must not
	// narrow what the underlying identity is allowed to READ. Empty ⇒ equals
	// Role (no scope-cap in play).
	BaseRole Role `json:"-"`
	// Email + Name are denormalised onto the principal so handlers
	// that want to log "who did this" don't need a second lookup.
	// Empty for service accounts (their Name is on the SA row).
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
	// MustResetPassword mirrors users.must_reset_password: the human must
	// change their password before using anything else (admin set a
	// temporary one, or first-login rotation). Enforced by
	// EnforcePasswordReset. Session path only — API tokens aren't gated.
	MustResetPassword bool `json:"must_reset_password,omitempty"`
	// IsDemo mirrors users.is_demo for the demo-account guard.
	IsDemo bool `json:"is_demo,omitempty"`
	// IsOperator is the cell-operator (super-admin) flag off the user row.
	// Gates the operator surface (org lifecycle + cell-wide settings).
	// Always false for service accounts.
	IsOperator bool `json:"is_operator,omitempty"`
}

// ReadRole is the role to use for READ-visibility decisions. Scoping a token
// down (ScopeRole) narrows write capability via Role, but reads should follow
// the uncapped BaseRole — an admin's read-only token still reads everything.
func (p Principal) ReadRole() Role {
	if p.BaseRole != "" {
		return p.BaseRole
	}
	return p.Role
}

// ScopeCapped reports whether a token scope reduced this principal's effective
// Role below the identity's actual (base) role. A scope-capped token is a hard
// write ceiling: it must NOT regain write capability through side channels such
// as group-level editor membership (see RequireWriteAnywhere).
func (p Principal) ScopeCapped() bool {
	return p.BaseRole != "" && p.BaseRole != p.Role
}
