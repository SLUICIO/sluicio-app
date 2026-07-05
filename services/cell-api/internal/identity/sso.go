// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// SSO/OIDC data layer: per-org auth providers, claim→role/team mappings, the
// external-subject ↔ user linkage, the transient PKCE/CSRF login state, and the
// on-login access re-sync (org role + team memberships from IdP claims). The
// OIDC protocol itself lives in the api layer (handlers_sso.go); this file is
// pure persistence + the mapping resolution. See docs/sso.md.

package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/secretcrypto"
	"github.com/jackc/pgx/v5"
)

const authProviderCols = `id, org_id, name, kind, issuer_url, client_id, client_secret,
	claim_email, claim_name, claim_sub, scopes, claim_groups, default_role,
	jit_provisioning, enabled, created_at, updated_at`

// encryptSecret protects a replayable credential (the OIDC client secret)
// before it's written. With no key configured it returns the value unchanged
// (legacy plaintext behavior); the mfaKey doubles as the cell secret key.
func (s *Store) encryptSecret(plain string) (string, error) {
	if len(s.mfaKey) != 32 {
		return plain, nil
	}
	return secretcrypto.Encrypt(s.mfaKey, plain)
}

// decryptSecret reverses encryptSecret; legacy plaintext passes through.
func (s *Store) decryptSecret(stored string) (string, error) {
	return secretcrypto.Decrypt(s.mfaKey, stored)
}

// scanAuthProvider is the single read choke point: every provider load runs
// through it, so decrypting the client secret here covers admin CRUD, the
// login start, and the callback alike. ClientSecret is json:"-", so the
// cleartext value stays server-side.
func (s *Store) scanAuthProvider(row pgx.Row) (AuthProvider, error) {
	var p AuthProvider
	err := row.Scan(&p.ID, &p.OrgID, &p.Name, &p.Kind, &p.IssuerURL, &p.ClientID, &p.ClientSecret,
		&p.ClaimEmail, &p.ClaimName, &p.ClaimSub, &p.Scopes, &p.ClaimGroups, &p.DefaultRole,
		&p.JITProvisioning, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	secret, derr := s.decryptSecret(p.ClientSecret)
	if derr != nil {
		return p, fmt.Errorf("identity: decrypt client secret: %w", derr)
	}
	p.ClientSecret = secret
	return p, nil
}

// CreateAuthProvider inserts a provider, filling claim/scope/role defaults.
func (s *Store) CreateAuthProvider(ctx context.Context, p AuthProvider) (AuthProvider, error) {
	if p.Scopes == "" {
		p.Scopes = "openid email profile"
	}
	if p.ClaimGroups == "" {
		p.ClaimGroups = "groups"
	}
	if p.ClaimEmail == "" {
		p.ClaimEmail = "email"
	}
	if p.ClaimName == "" {
		p.ClaimName = "name"
	}
	if p.ClaimSub == "" {
		p.ClaimSub = "sub"
	}
	if !p.DefaultRole.IsValid() {
		p.DefaultRole = RoleViewer
	}
	secret, err := s.encryptSecret(p.ClientSecret)
	if err != nil {
		return AuthProvider{}, fmt.Errorf("identity: encrypt client secret: %w", err)
	}
	p.ClientSecret = secret
	return s.scanAuthProvider(s.pool.QueryRow(ctx,
		`INSERT INTO auth_providers
		   (org_id, name, kind, issuer_url, client_id, client_secret,
		    claim_email, claim_name, claim_sub, scopes, claim_groups,
		    default_role, jit_provisioning, enabled)
		 VALUES ($1,$2,'oidc',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 RETURNING `+authProviderCols,
		p.OrgID, p.Name, p.IssuerURL, p.ClientID, p.ClientSecret,
		p.ClaimEmail, p.ClaimName, p.ClaimSub, p.Scopes, p.ClaimGroups,
		p.DefaultRole, p.JITProvisioning, p.Enabled))
}

// UpdateAuthProvider updates a provider (org-scoped). An empty ClientSecret
// keeps the stored one, so the secret never has to round-trip to the client.
func (s *Store) UpdateAuthProvider(ctx context.Context, orgID uuid.UUID, p AuthProvider) (AuthProvider, error) {
	if !p.DefaultRole.IsValid() {
		p.DefaultRole = RoleViewer
	}
	// Encrypt only when a new secret was supplied; an empty value stays
	// empty so NULLIF(...) below preserves the stored (encrypted) secret.
	secret, err := s.encryptSecret(p.ClientSecret)
	if err != nil {
		return AuthProvider{}, fmt.Errorf("identity: encrypt client secret: %w", err)
	}
	p.ClientSecret = secret
	return s.scanAuthProvider(s.pool.QueryRow(ctx,
		`UPDATE auth_providers SET
		    name=$3, issuer_url=$4, client_id=$5,
		    client_secret=COALESCE(NULLIF($6,''), client_secret),
		    claim_email=$7, claim_name=$8, claim_sub=$9, scopes=$10,
		    claim_groups=$11, default_role=$12, jit_provisioning=$13,
		    enabled=$14, updated_at=now()
		 WHERE id=$1 AND org_id=$2
		 RETURNING `+authProviderCols,
		p.ID, orgID, p.Name, p.IssuerURL, p.ClientID, p.ClientSecret,
		p.ClaimEmail, p.ClaimName, p.ClaimSub, p.Scopes, p.ClaimGroups,
		p.DefaultRole, p.JITProvisioning, p.Enabled))
}

// EncryptProviderSecretsAtRest is the one-time migration that re-stores any
// plaintext client secret encrypted. Idempotent (skips already-encrypted /
// empty rows) and a no-op when no key is configured. Returns the number of
// rows migrated. It reads the raw column directly — not via scanAuthProvider,
// which would decrypt — so it can tell encrypted from legacy plaintext.
func (s *Store) EncryptProviderSecretsAtRest(ctx context.Context) (int, error) {
	if len(s.mfaKey) != 32 {
		return 0, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, client_secret FROM auth_providers`)
	if err != nil {
		return 0, fmt.Errorf("identity: migrate sso secrets: read: %w", err)
	}
	type pending struct {
		id     uuid.UUID
		secret string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.secret); err != nil {
			rows.Close()
			return 0, fmt.Errorf("identity: migrate sso secrets: scan: %w", err)
		}
		if p.secret != "" && !secretcrypto.IsEncrypted(p.secret) {
			todo = append(todo, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("identity: migrate sso secrets: rows: %w", err)
	}
	n := 0
	for _, p := range todo {
		enc, err := secretcrypto.Encrypt(s.mfaKey, p.secret)
		if err != nil {
			return n, fmt.Errorf("identity: migrate sso secrets: encrypt: %w", err)
		}
		if _, err := s.pool.Exec(ctx,
			`UPDATE auth_providers SET client_secret=$2 WHERE id=$1`, p.id, enc); err != nil {
			return n, fmt.Errorf("identity: migrate sso secrets: write: %w", err)
		}
		n++
	}
	return n, nil
}

func (s *Store) ListAuthProviders(ctx context.Context, orgID uuid.UUID) ([]AuthProvider, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+authProviderCols+` FROM auth_providers WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthProvider
	for rows.Next() {
		p, err := s.scanAuthProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListEnabledAuthProviders is the public login-page list (enabled only).
func (s *Store) ListEnabledAuthProviders(ctx context.Context, orgID uuid.UUID) ([]AuthProvider, error) {
	all, err := s.ListAuthProviders(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, p := range all {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out, nil
}

// ListAllEnabledProviders returns every enabled provider across the cell — for
// the pre-auth login page, which has no org context. (Single-org cells are the
// common case; multi-tenant scoping by hostname is a later refinement.)
func (s *Store) ListAllEnabledProviders(ctx context.Context) ([]AuthProvider, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+authProviderCols+` FROM auth_providers WHERE enabled=true ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthProvider
	for rows.Next() {
		p, err := s.scanAuthProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetAuthProvider is the org-scoped admin lookup.
func (s *Store) GetAuthProvider(ctx context.Context, orgID, id uuid.UUID) (AuthProvider, error) {
	p, err := s.scanAuthProvider(s.pool.QueryRow(ctx, `SELECT `+authProviderCols+` FROM auth_providers WHERE id=$1 AND org_id=$2`, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthProvider{}, ErrNotFound
	}
	return p, err
}

// GetAuthProviderByID is the un-scoped lookup the public callback needs (it only
// has the provider id from the signed login state).
func (s *Store) GetAuthProviderByID(ctx context.Context, id uuid.UUID) (AuthProvider, error) {
	p, err := s.scanAuthProvider(s.pool.QueryRow(ctx, `SELECT `+authProviderCols+` FROM auth_providers WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthProvider{}, ErrNotFound
	}
	return p, err
}

func (s *Store) DeleteAuthProvider(ctx context.Context, orgID, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM auth_providers WHERE id=$1 AND org_id=$2`, id, orgID)
	return err
}

// ── claim mappings ──────────────────────────────────────────────────────

func (s *Store) ListClaimMappings(ctx context.Context, providerID uuid.UUID) ([]ClaimMapping, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, provider_id, claim_value, org_role, group_id, group_role, created_at
		 FROM auth_provider_claim_mappings WHERE provider_id=$1 ORDER BY claim_value`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClaimMapping
	for rows.Next() {
		var m ClaimMapping
		var orgRole *string
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.ClaimValue, &orgRole, &m.GroupID, &m.GroupRole, &m.CreatedAt); err != nil {
			return nil, err
		}
		if orgRole != nil {
			m.OrgRole = Role(*orgRole)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) CreateClaimMapping(ctx context.Context, m ClaimMapping) (ClaimMapping, error) {
	if !m.GroupRole.IsValid() {
		m.GroupRole = RoleViewer
	}
	var orgRole *string
	if m.OrgRole != "" {
		v := string(m.OrgRole)
		orgRole = &v
	}
	var out ClaimMapping
	var scannedOrg *string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO auth_provider_claim_mappings (provider_id, claim_value, org_role, group_id, group_role)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, provider_id, claim_value, org_role, group_id, group_role, created_at`,
		m.ProviderID, m.ClaimValue, orgRole, m.GroupID, m.GroupRole).
		Scan(&out.ID, &out.ProviderID, &out.ClaimValue, &scannedOrg, &out.GroupID, &out.GroupRole, &out.CreatedAt)
	if err != nil {
		return ClaimMapping{}, err
	}
	if scannedOrg != nil {
		out.OrgRole = Role(*scannedOrg)
	}
	return out, nil
}

func (s *Store) DeleteClaimMapping(ctx context.Context, providerID, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM auth_provider_claim_mappings WHERE id=$1 AND provider_id=$2`, id, providerID)
	return err
}

// ── subject linkage ─────────────────────────────────────────────────────

// FindUserBySubject returns the linked user for (provider, sub), or ok=false.
func (s *Store) FindUserBySubject(ctx context.Context, providerID uuid.UUID, sub string) (uuid.UUID, bool, error) {
	var uid uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT user_id FROM oidc_subjects WHERE provider_id=$1 AND external_sub=$2`, providerID, sub).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE oidc_subjects SET last_used_at=now() WHERE provider_id=$1 AND external_sub=$2`, providerID, sub)
	return uid, true, nil
}

// LinkSubject records (or refreshes) the external-subject ↔ user linkage.
func (s *Store) LinkSubject(ctx context.Context, providerID uuid.UUID, sub string, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO oidc_subjects (provider_id, external_sub, user_id, last_used_at)
		 VALUES ($1,$2,$3, now())
		 ON CONFLICT (provider_id, external_sub) DO UPDATE SET last_used_at=now()`,
		providerID, sub, userID)
	return err
}

// ── transient login state ───────────────────────────────────────────────

func (s *Store) CreateSSOLoginState(ctx context.Context, st SSOLoginState) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sso_login_states (state, provider_id, nonce, code_verifier, redirect_to, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		st.State, st.ProviderID, st.Nonce, st.CodeVerifier, st.RedirectTo, st.ExpiresAt)
	return err
}

// ConsumeSSOLoginState atomically deletes + returns an unexpired state (single
// use). Returns ErrNotFound if missing/expired/replayed.
func (s *Store) ConsumeSSOLoginState(ctx context.Context, state string) (SSOLoginState, error) {
	var st SSOLoginState
	err := s.pool.QueryRow(ctx,
		`DELETE FROM sso_login_states WHERE state=$1 AND expires_at > now()
		 RETURNING state, provider_id, nonce, code_verifier, redirect_to, expires_at`, state).
		Scan(&st.State, &st.ProviderID, &st.Nonce, &st.CodeVerifier, &st.RedirectTo, &st.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SSOLoginState{}, ErrNotFound
	}
	return st, err
}

// PurgeExpiredSSOState clears stale rows (best-effort housekeeping).
func (s *Store) PurgeExpiredSSOState(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sso_login_states WHERE expires_at < now()`)
	return err
}

// ── on-login access re-sync ──────────────────────────────────────────────

// ApplyClaimMappings sets the user's org role and team memberships from the
// IdP groups claim, treating the IdP as authoritative. Org role = the highest
// org_role across matched mappings, else the provider default. Team membership
// is synced for the groups this provider manages (those referenced by its
// mappings): matched groups are added/updated, unmatched managed groups are
// removed. Teams the provider doesn't reference are left untouched. Atomic.
func (s *Store) ApplyClaimMappings(ctx context.Context, p AuthProvider, userID uuid.UUID, claimGroups []string) error {
	mappings, err := s.ListClaimMappings(ctx, p.ID)
	if err != nil {
		return err
	}
	have := make(map[string]bool, len(claimGroups))
	for _, g := range claimGroups {
		have[g] = true
	}

	role := p.DefaultRole
	if !role.IsValid() {
		role = RoleViewer
	}
	managed := map[uuid.UUID]bool{}
	matchedGroup := map[uuid.UUID]Role{}
	for _, m := range mappings {
		if m.GroupID != nil {
			managed[*m.GroupID] = true
		}
		if !have[m.ClaimValue] {
			continue
		}
		if m.OrgRole != "" && m.OrgRole.rank() > role.rank() {
			role = m.OrgRole
		}
		if m.GroupID != nil {
			gr := m.GroupRole
			if !gr.IsValid() {
				gr = RoleViewer
			}
			if cur, ok := matchedGroup[*m.GroupID]; !ok || gr.rank() > cur.rank() {
				matchedGroup[*m.GroupID] = gr
			}
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO org_members (user_id, org_id, role) VALUES ($1,$2,$3)
		 ON CONFLICT (user_id, org_id) DO UPDATE SET role=EXCLUDED.role`,
		userID, p.OrgID, role); err != nil {
		return err
	}
	for gid := range managed {
		if gr, ok := matchedGroup[gid]; ok {
			if _, err := tx.Exec(ctx,
				`INSERT INTO group_members (user_id, group_id, role) VALUES ($1,$2,$3)
				 ON CONFLICT (user_id, group_id) DO UPDATE SET role=EXCLUDED.role`,
				userID, gid, gr); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `DELETE FROM group_members WHERE user_id=$1 AND group_id=$2`, userID, gid); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// SSOExpiry is the lifetime of a transient login-state row.
const SSOExpiry = 10 * time.Minute
