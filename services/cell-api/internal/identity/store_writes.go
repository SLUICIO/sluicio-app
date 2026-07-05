// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Mutation paths against the auth tables — the write counterparts to
// the read methods in store.go. Lives in its own file to keep the
// hot-path read store browsable; the auth-management UI lives on
// these.

package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── members ───────────────────────────────────────────────────────────

// AddMember inserts a new (user, org) → role row. Caller is the
// admin doing the inviting; the row's joined_at defaults to now.
// Returns ErrAlreadyMember if the user is already a member of the
// org. Caller is responsible for validating Role.IsValid().
func (s *Store) AddMember(ctx context.Context, userID, orgID uuid.UUID, role Role) error {
	if !role.IsValid() {
		return fmt.Errorf("identity: invalid role: %s", role)
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO org_members (user_id, org_id, role) VALUES ($1, $2, $3)`,
		userID, orgID, role)
	if err != nil {
		if strings.Contains(err.Error(), "org_members_pkey") {
			return ErrAlreadyMember
		}
		return fmt.Errorf("identity: add member: %w", err)
	}
	return nil
}

// UpdateMemberRole changes the role of an existing membership.
// Returns ErrNotFound if the user isn't a member of the org.
func (s *Store) UpdateMemberRole(ctx context.Context, userID, orgID uuid.UUID, role Role) error {
	if !role.IsValid() {
		return fmt.Errorf("identity: invalid role: %s", role)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE org_members SET role = $3 WHERE user_id = $1 AND org_id = $2`,
		userID, orgID, role)
	if err != nil {
		return fmt.Errorf("identity: update role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveMember deletes one (user, org) row. Returns ErrNotFound if
// the user wasn't a member. Caller is expected to refuse removing
// the last admin from an org (we don't enforce that here — the
// handler does, where it can be more graceful about messaging).
func (s *Store) RemoveMember(ctx context.Context, userID, orgID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM org_members WHERE user_id = $1 AND org_id = $2`,
		userID, orgID)
	if err != nil {
		return fmt.Errorf("identity: remove member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MemberRow is the join of a user + their role in one org — what the
// Members tab on the Settings page renders one row per.
type MemberRow struct {
	User     User      `json:"user"`
	Role     Role      `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
	// HasPassword is true when the user can sign in with a local password
	// (password_hash is never returned, so this exposes just the fact).
	HasPassword bool `json:"has_password"`
	// SSOProviders are the names of the IdPs this user has signed in through
	// (linked via oidc_subjects). Empty for password-only users; the admin
	// members list renders it as the user's login method.
	SSOProviders []string `json:"sso_providers"`
}

// ListOrgMembers returns every user that belongs to the given org,
// joined with their role. Order is alphabetical-by-email for stable
// rendering.
func (s *Store) ListOrgMembers(ctx context.Context, orgID uuid.UUID) ([]MemberRow, error) {
	const q = `
		SELECT u.id, u.email, u.name,
		       COALESCE(u.password_hash, ''), u.must_reset_password,
		       u.is_operator,
		       u.last_login_at, u.created_at, u.updated_at,
		       u.login_count, u.failed_login_count, u.last_active_at,
		       (mfa.enabled_at IS NOT NULL) AS mfa_enabled,
		       (u.password_hash IS NOT NULL AND u.password_hash <> '') AS has_password,
		       COALESCE((
		           SELECT array_agg(DISTINCT ap.name ORDER BY ap.name)
		           FROM oidc_subjects os
		           JOIN auth_providers ap ON ap.id = os.provider_id
		           WHERE os.user_id = u.id
		       ), ARRAY[]::text[]) AS sso_providers,
		       m.role, m.joined_at
		FROM org_members m
		JOIN users u ON u.id = m.user_id
		LEFT JOIN user_mfa mfa ON mfa.user_id = u.id
		WHERE m.org_id = $1
		ORDER BY lower(u.email)`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: list org members: %w", err)
	}
	defer rows.Close()
	out := make([]MemberRow, 0)
	for rows.Next() {
		var mr MemberRow
		if err := rows.Scan(
			&mr.User.ID, &mr.User.Email, &mr.User.Name,
			&mr.User.PasswordHash, &mr.User.MustResetPassword,
			&mr.User.IsOperator,
			&mr.User.LastLoginAt, &mr.User.CreatedAt, &mr.User.UpdatedAt,
			&mr.User.LoginCount, &mr.User.FailedLoginCount, &mr.User.LastActiveAt,
			&mr.User.MFAEnabled,
			&mr.HasPassword, &mr.SSOProviders,
			&mr.Role, &mr.JoinedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, mr)
	}
	return out, rows.Err()
}

// CreateUser inserts a new users row. The admin "Add member" flow
// creates the user first, then adds an org_members row. Returns
// ErrUserExists if the email already maps to another user; the
// handler should suggest "did you mean to invite the existing user
// instead?".
func (s *Store) CreateUser(ctx context.Context, email, name string) (User, error) {
	email = strings.TrimSpace(email)
	name = strings.TrimSpace(name)
	if email == "" {
		return User{}, fmt.Errorf("identity: email is required")
	}
	const q = `
		INSERT INTO users (email, name)
		VALUES ($1, $2)
		RETURNING id, email, name,
		          COALESCE(password_hash, ''), must_reset_password,
		          last_login_at, created_at, updated_at`
	row := s.pool.QueryRow(ctx, q, email, name)
	var u User
	if err := row.Scan(
		&u.ID, &u.Email, &u.Name,
		&u.PasswordHash, &u.MustResetPassword,
		&u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		if strings.Contains(err.Error(), "users_email_key") || strings.Contains(err.Error(), "idx_users_email") {
			return User{}, ErrUserExists
		}
		return User{}, fmt.Errorf("identity: create user: %w", err)
	}
	return u, nil
}

// ── tokens ───────────────────────────────────────────────────────────

// ApiToken is the read-side projection of an api_tokens row, with
// the (always-secret) token_hash omitted. Returned to the UI for
// listing.
type ApiToken struct {
	ID         uuid.UUID  `json:"id"`
	OwnerType  string     `json:"owner_type"`
	OwnerID    uuid.UUID  `json:"owner_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	// ScopeRole caps the token below its owner's role ("" = no cap).
	ScopeRole string `json:"scope_role,omitempty"`
	// ExpiresAt is when the token stops working (nil = never).
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// CreateAPIToken persists the prefix + hash of a freshly-minted
// token (see NewToken). Returns the persisted row id. The token
// kind on owner_type ('user' or 'service_account') tells the
// middleware which lookup path to use.
func (s *Store) CreateAPIToken(ctx context.Context, ownerType string, ownerID uuid.UUID, name string, scopeRole string, expiresAt *time.Time, tok MintedToken) (ApiToken, error) {
	if ownerType != "user" && ownerType != "service_account" {
		return ApiToken{}, fmt.Errorf("identity: bad owner_type %q", ownerType)
	}
	const q = `
		INSERT INTO api_tokens (owner_type, owner_id, name, prefix, token_hash, scope_role, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, owner_type, owner_id, name, prefix,
		          last_used_at, created_at, revoked_at, scope_role, expires_at`
	row := s.pool.QueryRow(ctx, q, ownerType, ownerID, name, tok.Prefix, tok.Hash, scopeRole, expiresAt)
	var t ApiToken
	if err := row.Scan(
		&t.ID, &t.OwnerType, &t.OwnerID, &t.Name, &t.Prefix,
		&t.LastUsedAt, &t.CreatedAt, &t.RevokedAt, &t.ScopeRole, &t.ExpiresAt,
	); err != nil {
		return ApiToken{}, fmt.Errorf("identity: create token: %w", err)
	}
	return t, nil
}

// ListAPITokensForUser returns the user's non-revoked tokens, newest
// first. The Settings → Tokens tab consumes this directly.
func (s *Store) ListAPITokensForUser(ctx context.Context, userID uuid.UUID) ([]ApiToken, error) {
	const q = `
		SELECT id, owner_type, owner_id, name, prefix,
		       last_used_at, created_at, revoked_at, scope_role, expires_at
		FROM api_tokens
		WHERE owner_type = 'user' AND owner_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: list user tokens: %w", err)
	}
	defer rows.Close()
	out := make([]ApiToken, 0)
	for rows.Next() {
		var t ApiToken
		if err := rows.Scan(
			&t.ID, &t.OwnerType, &t.OwnerID, &t.Name, &t.Prefix,
			&t.LastUsedAt, &t.CreatedAt, &t.RevokedAt, &t.ScopeRole, &t.ExpiresAt,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListAPITokensForServiceAccount returns a service account's non-revoked
// tokens, newest first — the service-account management UI consumes this.
func (s *Store) ListAPITokensForServiceAccount(ctx context.Context, saID uuid.UUID) ([]ApiToken, error) {
	const q = `
		SELECT id, owner_type, owner_id, name, prefix,
		       last_used_at, created_at, revoked_at, scope_role, expires_at
		FROM api_tokens
		WHERE owner_type = 'service_account' AND owner_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, saID)
	if err != nil {
		return nil, fmt.Errorf("identity: list service-account tokens: %w", err)
	}
	defer rows.Close()
	out := make([]ApiToken, 0)
	for rows.Next() {
		var t ApiToken
		if err := rows.Scan(
			&t.ID, &t.OwnerType, &t.OwnerID, &t.Name, &t.Prefix,
			&t.LastUsedAt, &t.CreatedAt, &t.RevokedAt, &t.ScopeRole, &t.ExpiresAt,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken marks the token as revoked. Returns ErrNotFound if
// the id is unknown or already revoked. The caller is expected to
// already have authorised the operation (a user can revoke their
// own tokens; an org admin can revoke any service-account token in
// the org).
func (s *Store) RevokeAPIToken(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`,
		id)
	if err != nil {
		return fmt.Errorf("identity: revoke token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TokenLookup is the result of resolving a bearer token: the row id
// (for last_used_at bump), the owner identity, and whether the owner
// is a user or service account. The middleware composes a Principal
// from this + the user/service-account row.
type TokenLookup struct {
	ID        uuid.UUID
	OwnerType string
	OwnerID   uuid.UUID
	// ScopeRole caps the resolved principal's role ("" = no cap).
	ScopeRole string
}

// ResolveAPIToken looks up a token by its prefix (indexed) and, if
// found, verifies the full hash against the supplied plaintext.
// Returns ErrInvalidCredentials on any mismatch / revocation /
// unknown-prefix; the middleware translates to 401.
func (s *Store) ResolveAPIToken(ctx context.Context, plaintext string) (TokenLookup, error) {
	prefix := PrefixOf(plaintext)
	const q = `
		SELECT id, owner_type, owner_id, token_hash, scope_role
		FROM api_tokens
		WHERE prefix = $1 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())`
	row := s.pool.QueryRow(ctx, q, prefix)
	var t TokenLookup
	var storedHash string
	if err := row.Scan(&t.ID, &t.OwnerType, &t.OwnerID, &storedHash, &t.ScopeRole); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TokenLookup{}, ErrInvalidCredentials
		}
		return TokenLookup{}, err
	}
	if !VerifyTokenHash(plaintext, storedHash) {
		return TokenLookup{}, ErrInvalidCredentials
	}
	// Best-effort last_used_at bump; failure is logged by caller.
	_, _ = s.pool.Exec(ctx,
		`UPDATE api_tokens SET last_used_at = now() WHERE id = $1`, t.ID)
	return t, nil
}

// ── self-service profile updates ─────────────────────────────────────

// UpdateUserProfile lets a user change their own display name and/or
// email. Empty strings are treated as "no change" (per-field). Returns
// ErrUserExists if the new email collides with another user's row.
// Bumps updated_at.
func (s *Store) UpdateUserProfile(ctx context.Context, userID uuid.UUID, name, email string) error {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" && email == "" {
		return nil // nothing to do
	}
	// COALESCE on NULLIF lets us send either field without conditional
	// SQL: an empty string falls through to the existing value.
	const q = `
		UPDATE users
		SET name       = COALESCE(NULLIF($2, ''), name),
		    email      = COALESCE(NULLIF($3, ''), email),
		    updated_at = now()
		WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, userID, name, email)
	if err != nil {
		if strings.Contains(err.Error(), "users_email_key") || strings.Contains(err.Error(), "idx_users_email") {
			return ErrUserExists
		}
		return fmt.Errorf("identity: update user profile: %w", err)
	}
	return nil
}

// ClearMustResetPassword flips the first-login flag off. Called from
// the change-password handler the first time a seeded admin sets their
// own password.
func (s *Store) ClearMustResetPassword(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET must_reset_password = FALSE, updated_at = now() WHERE id = $1`,
		userID)
	return err
}

// ── orgs ─────────────────────────────────────────────────────────────

// GetOrgByID returns one Org or ErrNotFound. Used by the org-settings
// surface to render the editable name + slug.
func (s *Store) GetOrgByID(ctx context.Context, orgID uuid.UUID) (Org, error) {
	const q = `SELECT id, slug, name, created_at, updated_at FROM orgs WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, orgID)
	var o Org
	if err := row.Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Org{}, ErrNotFound
		}
		return Org{}, fmt.Errorf("identity: get org: %w", err)
	}
	return o, nil
}

// UpdateOrg renames or re-slugs an org. Either field empty = "no
// change". Returns ErrSlugTaken if the new slug collides with another
// org. Bumps updated_at.
func (s *Store) UpdateOrg(ctx context.Context, orgID uuid.UUID, name, slug string) error {
	name = strings.TrimSpace(name)
	slug = strings.TrimSpace(slug)
	if name == "" && slug == "" {
		return nil
	}
	const q = `
		UPDATE orgs
		SET name       = COALESCE(NULLIF($2, ''), name),
		    slug       = COALESCE(NULLIF($3, ''), slug),
		    updated_at = now()
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, orgID, name, slug)
	if err != nil {
		if strings.Contains(err.Error(), "orgs_slug_key") || strings.Contains(err.Error(), "idx_orgs_slug") {
			return ErrSlugTaken
		}
		return fmt.Errorf("identity: update org: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOrg removes an org and (via ON DELETE CASCADE on the auth
// schema's foreign keys) everything inside it: members, groups,
// policies, service accounts, tokens owned by service accounts, etc.
// User accounts themselves are NOT deleted — a user can still log in
// and operate on their other orgs.
//
// The handler is expected to refuse this when the org is the user's
// only org (CountOrgsForUser drops to 0 → soft-bricked account).
func (s *Store) DeleteOrg(ctx context.Context, orgID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgID)
	if err != nil {
		return fmt.Errorf("identity: delete org: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountOrgsForUser returns how many orgs the user is currently a
// member of. Used by the delete-org gate so a user can't strand
// themselves with zero orgs.
func (s *Store) CountOrgsForUser(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM org_members WHERE user_id = $1`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("identity: count user orgs: %w", err)
	}
	return n, nil
}

// AnyUserHasLoggedIn returns true once any user row has its
// last_login_at set — i.e. someone has signed in at least once. The
// public install-state endpoint uses this to decide whether to show
// the "ships with a default admin" hint on the login page; once anyone
// has been here, that hint is noise (and worse, it advertises the
// default credentials on a deployed app).
func (s *Store) AnyUserHasLoggedIn(ctx context.Context) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE last_login_at IS NOT NULL)`).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("identity: any user logged in: %w", err)
	}
	return ok, nil
}

// ── extra error sentinels ────────────────────────────────────────────

var (
	ErrAlreadyMember = errors.New("identity: already a member of this org")
	ErrUserExists    = errors.New("identity: a user with that email already exists")
	ErrSlugTaken     = errors.New("identity: an org with that slug already exists")
)
