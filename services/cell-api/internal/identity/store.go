// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors callers can match on.
var (
	ErrNotFound           = errors.New("identity: not found")
	ErrInvalidCredentials = errors.New("identity: invalid credentials")
)

// Store is the Postgres-backed read+write layer for the auth tables.
// Methods are grouped by entity (users, sessions, memberships, …);
// the public surface is the union of every read/write the middleware
// and the auth handlers need.
type Store struct {
	pool *pgxpool.Pool
	// mfaKey is the 32-byte AES-GCM key used to encrypt TOTP secrets at
	// rest. Injected at startup via SetMFAKey; nil means MFA enrollment is
	// unavailable (the handlers report a clear error rather than storing a
	// secret in the clear).
	mfaKey []byte
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// SetMFAKey injects the AES-GCM key for TOTP-secret encryption.
func (s *Store) SetMFAKey(key []byte) { s.mfaKey = key }

// ── users ──────────────────────────────────────────────────────────────

// GetUserByID returns the User row by id, or ErrNotFound.
func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (User, error) {
	const q = `
		SELECT id, email, name,
		       COALESCE(password_hash, ''), must_reset_password, is_operator, is_demo,
		       last_login_at, created_at, updated_at
		FROM users WHERE id = $1`
	return s.scanUser(s.pool.QueryRow(ctx, q, id))
}

// GetUserByEmail returns the User row by lower-cased email, or
// ErrNotFound. Email comparison is case-insensitive to match how
// most IdPs / mail systems treat addresses.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	const q = `
		SELECT id, email, name,
		       COALESCE(password_hash, ''), must_reset_password, is_operator, is_demo,
		       last_login_at, created_at, updated_at
		FROM users WHERE lower(email) = lower($1)`
	return s.scanUser(s.pool.QueryRow(ctx, q, email))
}

func (s *Store) scanUser(row pgx.Row) (User, error) {
	var u User
	if err := row.Scan(
		&u.ID, &u.Email, &u.Name,
		&u.PasswordHash, &u.MustResetPassword, &u.IsOperator, &u.IsDemo,
		&u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	return u, nil
}

// SetDemoFlag flips the shared-demo-account marker (see User.IsDemo).
func (s *Store) SetDemoFlag(ctx context.Context, userID uuid.UUID, isDemo bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET is_demo = $2, updated_at = now() WHERE id = $1`, userID, isDemo)
	if err != nil {
		return fmt.Errorf("identity: set demo flag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPasswordHash writes a new PHC-formatted argon2id hash for the
// user. Bumps updated_at; does not touch must_reset_password (caller
// controls that — e.g. the "first login" wizard would set it false).
func (s *Store) SetPasswordHash(ctx context.Context, userID uuid.UUID, hash string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`,
		userID, hash)
	return err
}

// SetPasswordHashForced writes a new hash and sets must_reset_password to
// requireReset in one statement — the admin "reset a member's password"
// path. requireReset=true forces the target to change it on next login
// (EnforcePasswordReset gates everything until they do). Returns
// ErrNotFound when no such user.
func (s *Store) SetPasswordHashForced(ctx context.Context, userID uuid.UUID, hash string, requireReset bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, must_reset_password = $3, updated_at = now() WHERE id = $1`,
		userID, hash, requireReset)
	if err != nil {
		return fmt.Errorf("identity: set password (forced): %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthenticatePassword verifies (email, password) against the local
// users table. Returns the User on success, or ErrInvalidCredentials
// on any failure (no user, no password set, wrong password). We
// deliberately collapse all failure modes into a single sentinel
// error so the handler can give a single "invalid email or password"
// message without leaking which side was wrong.
func (s *Store) AuthenticatePassword(ctx context.Context, email, password string) (User, error) {
	u, err := s.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Run a verify against a known-bad hash anyway so the
			// timing of "no user" looks like "wrong password" to a
			// network observer. Cheap; argon2id is the bottleneck.
			_, _ = VerifyPassword(password, dummyHash)
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	if u.PasswordHash == "" {
		// No local password configured (SSO-only user). Same timing
		// dodge as the missing-user path.
		_, _ = VerifyPassword(password, dummyHash)
		return User{}, ErrInvalidCredentials
	}
	ok, err := VerifyPassword(password, u.PasswordHash)
	if err != nil {
		return User{}, fmt.Errorf("identity: hash check: %w", err)
	}
	if !ok {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

// dummyHash is a static argon2id hash used by AuthenticatePassword's
// timing-equalisation branches. Verifying any password against it
// always returns "not a match" but takes the same time as a real
// verify, so an attacker can't distinguish "no such user" from
// "wrong password" by request latency.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=4$YWFhYWFhYWFhYWFhYWFhYQ$YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE"

// TouchLastLogin records a completed login: stamps last_login_at, bumps
// login_count, and clears the failed-login counter. Called by the login
// handler after a successful credential (and MFA) check. Errors are
// logged but don't fail the login (the session was already created).
func (s *Store) TouchLastLogin(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users
		    SET last_login_at = now(),
		        login_count = login_count + 1,
		        failed_login_count = 0,
		        updated_at = now()
		  WHERE id = $1`,
		userID)
	return err
}

// RecordFailedLoginByEmail increments the per-account failed-login counter
// for the Members security stat. Keyed by email so the login handler doesn't
// have to resolve the user first (which would re-introduce a user-enumeration
// timing signal); a no-op (0 rows) for an unknown email. Case-insensitive to
// match the email index.
func (s *Store) RecordFailedLoginByEmail(ctx context.Context, email string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users
		    SET failed_login_count = failed_login_count + 1,
		        updated_at = now()
		  WHERE lower(email) = lower($1)`,
		email)
	return err
}

// TouchLastActive stamps last_active_at for the per-user "last active" stat,
// throttled in SQL to at most one write per user per 5 minutes so it stays
// cheap on the auth hot path (most calls match 0 rows). Called fire-and-forget
// from the session resolver.
func (s *Store) TouchLastActive(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users
		    SET last_active_at = now()
		  WHERE id = $1
		    AND (last_active_at IS NULL OR last_active_at < now() - interval '5 minutes')`,
		userID)
	return err
}

// DigestSeenAt returns the user's "since last visit" watermark for the
// activity digest (nil if never marked seen).
func (s *Store) DigestSeenAt(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	var t *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT digest_seen_at FROM users WHERE id = $1`, userID).Scan(&t); err != nil {
		return nil, err
	}
	return t, nil
}

// SetDigestSeen bumps the digest watermark to now.
func (s *Store) SetDigestSeen(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET digest_seen_at = now() WHERE id = $1`, userID)
	return err
}

// ── sessions ───────────────────────────────────────────────────────────

// CreateSession inserts a new session row. The returned Session has
// the freshly-minted ID + timestamps; the caller is expected to set
// the cookie with the ID and an expiry matching ExpiresAt.
func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, ttl time.Duration, userAgent string) (Session, error) {
	id, err := NewSessionID()
	if err != nil {
		return Session{}, fmt.Errorf("identity: session id: %w", err)
	}
	now := time.Now()
	const q = `
		INSERT INTO sessions (id, user_id, created_at, last_used_at, expires_at, user_agent)
		VALUES ($1, $2, $3, $3, $4, $5)`
	if _, err := s.pool.Exec(ctx, q, id, userID, now, now.Add(ttl), userAgent); err != nil {
		return Session{}, fmt.Errorf("identity: create session: %w", err)
	}
	return Session{
		ID:         id,
		UserID:     userID,
		CreatedAt:  now,
		LastUsedAt: now,
		ExpiresAt:  now.Add(ttl),
		UserAgent:  userAgent,
	}, nil
}

// GetSession returns the row for the given id, bumping last_used_at
// on the way. Returns ErrNotFound if the id is unknown OR if the
// session has expired (the middleware treats both equivalently).
func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	const q = `
		UPDATE sessions SET last_used_at = now()
		WHERE id = $1 AND expires_at > now()
		RETURNING id, user_id, created_at, last_used_at, expires_at, user_agent`
	var sess Session
	if err := s.pool.QueryRow(ctx, q, id).Scan(
		&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.LastUsedAt, &sess.ExpiresAt, &sess.UserAgent,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	return sess, nil
}

// DeleteSession removes one session by id (logout). Missing ids are
// not an error — the cookie may already be stale.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return err
}

// DeleteExpiredSessions clears rows past their expires_at. Intended
// to be called from a background sweeper; missing the sweep doesn't
// break anything (GetSession already filters by expires_at).
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ── memberships ────────────────────────────────────────────────────────

// ListMemberships returns every org the user belongs to, joined with
// the org row + the user's role per org. Order is alphabetical by
// org name so the org switcher renders deterministically.
func (s *Store) ListMemberships(ctx context.Context, userID uuid.UUID) ([]Membership, error) {
	const q = `
		SELECT o.id, o.slug, o.name, o.created_at,
		       m.role, m.joined_at
		FROM org_members m
		JOIN orgs o ON o.id = m.org_id
		WHERE m.user_id = $1
		ORDER BY lower(o.name), o.id`
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: list memberships: %w", err)
	}
	defer rows.Close()
	out := make([]Membership, 0)
	for rows.Next() {
		var m Membership
		if err := rows.Scan(
			&m.Org.ID, &m.Org.Slug, &m.Org.Name, &m.Org.CreatedAt,
			&m.Role, &m.JoinedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMembership returns one (user, org) → role row, or ErrNotFound
// if the user isn't a member of that org.
func (s *Store) GetMembership(ctx context.Context, userID, orgID uuid.UUID) (Role, error) {
	const q = `SELECT role FROM org_members WHERE user_id = $1 AND org_id = $2`
	var r Role
	if err := s.pool.QueryRow(ctx, q, userID, orgID).Scan(&r); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return r, nil
}

// ── service accounts ───────────────────────────────────────────────────

// GetServiceAccount returns the SA row by id.
func (s *Store) GetServiceAccount(ctx context.Context, id uuid.UUID) (ServiceAccount, error) {
	const q = `
		SELECT id, org_id, name, description, role, scope, created_by, created_at
		FROM service_accounts WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	var sa ServiceAccount
	if err := row.Scan(
		&sa.ID, &sa.OrgID, &sa.Name, &sa.Description, &sa.Role, &sa.Scope, &sa.CreatedBy, &sa.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ServiceAccount{}, ErrNotFound
		}
		return ServiceAccount{}, err
	}
	return sa, nil
}

// ListServiceAccounts returns the org's service accounts, newest first.
func (s *Store) ListServiceAccounts(ctx context.Context, orgID uuid.UUID) ([]ServiceAccount, error) {
	const q = `
		SELECT id, org_id, name, description, role, scope, created_by, created_at
		FROM service_accounts WHERE org_id = $1 ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: list service accounts: %w", err)
	}
	defer rows.Close()
	out := make([]ServiceAccount, 0)
	for rows.Next() {
		var sa ServiceAccount
		if err := rows.Scan(&sa.ID, &sa.OrgID, &sa.Name, &sa.Description, &sa.Role, &sa.Scope, &sa.CreatedBy, &sa.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sa)
	}
	return out, rows.Err()
}

// CreateServiceAccount inserts a new service account in the org.
func (s *Store) CreateServiceAccount(ctx context.Context, orgID uuid.UUID, name, description string, role Role, scope ServiceAccountScope, createdBy *uuid.UUID) (ServiceAccount, error) {
	if !scope.IsValid() {
		return ServiceAccount{}, fmt.Errorf("identity: invalid service-account scope: %s", scope)
	}
	const q = `
		INSERT INTO service_accounts (org_id, name, description, role, scope, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, org_id, name, description, role, scope, created_by, created_at`
	row := s.pool.QueryRow(ctx, q, orgID, name, description, string(role), string(scope), createdBy)
	var sa ServiceAccount
	if err := row.Scan(&sa.ID, &sa.OrgID, &sa.Name, &sa.Description, &sa.Role, &sa.Scope, &sa.CreatedBy, &sa.CreatedAt); err != nil {
		return ServiceAccount{}, fmt.Errorf("identity: create service account: %w", err)
	}
	return sa, nil
}

// UpdateServiceAccount updates a service account's name / description /
// role / scope.
func (s *Store) UpdateServiceAccount(ctx context.Context, orgID, id uuid.UUID, name, description string, role Role, scope ServiceAccountScope) (ServiceAccount, error) {
	if !scope.IsValid() {
		return ServiceAccount{}, fmt.Errorf("identity: invalid service-account scope: %s", scope)
	}
	const q = `
		UPDATE service_accounts SET name = $3, description = $4, role = $5, scope = $6, updated_at = now()
		WHERE org_id = $1 AND id = $2
		RETURNING id, org_id, name, description, role, scope, created_by, created_at`
	row := s.pool.QueryRow(ctx, q, orgID, id, name, description, string(role), string(scope))
	var sa ServiceAccount
	if err := row.Scan(&sa.ID, &sa.OrgID, &sa.Name, &sa.Description, &sa.Role, &sa.Scope, &sa.CreatedBy, &sa.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ServiceAccount{}, ErrNotFound
		}
		return ServiceAccount{}, fmt.Errorf("identity: update service account: %w", err)
	}
	return sa, nil
}

// DeleteServiceAccount removes the SA and its api_tokens. owner_id is not a DB
// foreign key (the owner is polymorphic), so the tokens must be deleted
// explicitly — otherwise they'd keep resolving after the SA is gone.
func (s *Store) DeleteServiceAccount(ctx context.Context, orgID, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("identity: delete service account: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM api_tokens WHERE owner_type = 'service_account' AND owner_id = $1`, id); err != nil {
		return fmt.Errorf("identity: delete service account tokens: %w", err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM service_accounts WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("identity: delete service account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// ── seed bootstrap ─────────────────────────────────────────────────────

// BootstrapSeedAdminPassword sets the admin@conduit.local user's
// password_hash to argon2id("admin") if it is currently empty. Run
// once at cell-api startup so a fresh DB (where migration 0017's
// seed insert left password_hash NULL) gets a usable admin account
// without anyone having to run a CLI. Idempotent — does nothing once
// the column is populated.
//
// This is a dev-bootstrap convenience; production deploys should
// rotate the password on first login (the `must_reset_password`
// flag can drive a forced-rotate flow if we later wire one up).
func (s *Store) BootstrapSeedAdminPassword(ctx context.Context, email, plaintext string) error {
	hash, err := HashPassword(plaintext)
	if err != nil {
		return err
	}
	const q = `
		UPDATE users
		SET password_hash = $2, updated_at = now()
		WHERE lower(email) = lower($1) AND COALESCE(password_hash, '') = ''`
	_, err = s.pool.Exec(ctx, q, email, hash)
	return err
}

// ErrInstallNotFresh means someone has already logged in to this cell,
// so the first-run bootstrap surface is sealed.
var ErrInstallNotFresh = errors.New("identity: install is already set up")

// BootstrapAdmin personalizes the seeded admin on a pristine install —
// the first-run "create your admin account" screen posts here. Email,
// display name, and password land on the seeded operator row in one
// transaction, so the cell still has exactly one bootstrap admin, now
// with credentials the installer chose instead of the public defaults.
//
// Self-sealing: refused with ErrInstallNotFresh once any user has
// logged in (the same freshness signal as the install-state endpoint).
// From then on account changes go through the authenticated surfaces.
func (s *Store) BootstrapAdmin(ctx context.Context, email, name, plaintext string) error {
	hash, err := HashPassword(plaintext)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("identity: bootstrap admin: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var used bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE last_login_at IS NOT NULL)`).Scan(&used); err != nil {
		return fmt.Errorf("identity: bootstrap admin: freshness: %w", err)
	}
	if used {
		return ErrInstallNotFresh
	}

	// The seeded admin is the cell's bootstrap operator (promoted at
	// startup, see cmd/cell-api). Target the oldest operator row.
	var id uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT id FROM users WHERE is_operator ORDER BY created_at ASC LIMIT 1`).Scan(&id); err != nil {
		return fmt.Errorf("identity: bootstrap admin: no operator row: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET email = $2, name = $3, password_hash = $4,
		    must_reset_password = false, updated_at = now()
		WHERE id = $1`, id, email, name, hash); err != nil {
		if strings.Contains(err.Error(), "users_email_key") || strings.Contains(err.Error(), "idx_users_email") {
			return ErrUserExists
		}
		return fmt.Errorf("identity: bootstrap admin: %w", err)
	}
	return tx.Commit(ctx)
}
