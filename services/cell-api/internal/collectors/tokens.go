// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tokenPrefix labels a collector token ("Sluicio collector token") so a
// leaked one is recognisable, and gives the UI a non-secret stub.
const tokenPrefix = "sct_"

// ErrNotFound is returned for a token or collector that does not exist in
// the caller's organization (or team), or a token already revoked.
var ErrNotFound = errors.New("collectors: not found")

// ErrUnauthenticated is returned for a token that is unknown or revoked.
var ErrUnauthenticated = errors.New("collectors: invalid token")

// Store wraps the Postgres side of collector management.
type Store struct {
	pool *pgxpool.Pool
	keys *Keys
}

// New returns a store. keys may be nil: collectors still register, but no
// configuration is stored or shown until the cell has a key.
func New(pool *pgxpool.Pool, keys *Keys) *Store { return &Store{pool: pool, keys: keys} }

// ConfigStorage reports whether configurations are stored.
func (s *Store) ConfigStorage() bool { return s.keys != nil }

// Token is the read-side projection of a team's token (never the hash).
type Token struct {
	ID         uuid.UUID  `json:"id"`
	GroupID    uuid.UUID  `json:"group_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// TokenRef is what a valid token resolves to: the team it assigns a
// collector to.
type TokenRef struct {
	TokenID uuid.UUID
	OrgID   uuid.UUID
	GroupID uuid.UUID
}

// HashToken is the SHA-256 the store keeps. Tokens are 256 random bits,
// so a fast hash is enough; the slow KDF is for low-entropy passwords.
func HashToken(full string) string {
	sum := sha256.Sum256([]byte(full))
	return hex.EncodeToString(sum[:])
}

func mintToken() (full, prefix, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("collectors: rand: %w", err)
	}
	full = tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return full, full[:12], HashToken(full), nil
}

const tokenCols = `id, group_id, name, prefix, created_at, last_used_at`

func scanToken(row pgx.Row) (Token, error) {
	var t Token
	err := row.Scan(&t.ID, &t.GroupID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt)
	return t, err
}

// CreateToken mints a token for the team. The plaintext is returned once
// and never stored. The team must belong to the organization.
func (s *Store) CreateToken(ctx context.Context, orgID, groupID uuid.UUID, name string, createdBy *uuid.UUID) (string, Token, error) {
	full, prefix, hash, err := mintToken()
	if err != nil {
		return "", Token{}, err
	}
	t, err := scanToken(s.pool.QueryRow(ctx, `
		INSERT INTO collector_tokens (organization_id, group_id, name, prefix, token_hash, created_by)
		SELECT $1, g.id, $3, $4, $5, $6 FROM groups g WHERE g.id = $2 AND g.org_id = $1
		RETURNING `+tokenCols, orgID, groupID, strings.TrimSpace(name), prefix, hash, createdBy))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Token{}, ErrNotFound
	}
	if err != nil {
		return "", Token{}, fmt.Errorf("collectors: create token: %w", err)
	}
	return full, t, nil
}

// ListTokens returns the team's live tokens, newest first.
func (s *Store) ListTokens(ctx context.Context, orgID, groupID uuid.UUID) ([]Token, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+tokenCols+` FROM collector_tokens
		WHERE organization_id = $1 AND group_id = $2 AND revoked_at IS NULL
		ORDER BY created_at DESC`, orgID, groupID)
	if err != nil {
		return nil, fmt.Errorf("collectors: list tokens: %w", err)
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken stops a token from authenticating. Collectors already
// registered with it stay in the registry; the caller also disconnects
// any live connection that authenticated with it.
func (s *Store) RevokeToken(ctx context.Context, orgID, groupID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE collector_tokens SET revoked_at = now()
		WHERE id = $1 AND organization_id = $2 AND group_id = $3 AND revoked_at IS NULL`, id, orgID, groupID)
	if err != nil {
		return fmt.Errorf("collectors: revoke token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RegenerateToken gives an existing token a new secret. The old secret
// stops working at once; a rotation without an outage uses a second token
// instead (create, switch the collectors, revoke the old one).
func (s *Store) RegenerateToken(ctx context.Context, orgID, groupID, id uuid.UUID) (string, Token, error) {
	full, prefix, hash, err := mintToken()
	if err != nil {
		return "", Token{}, err
	}
	t, err := scanToken(s.pool.QueryRow(ctx, `
		UPDATE collector_tokens SET prefix = $4, token_hash = $5, created_at = now(), last_used_at = NULL
		WHERE id = $1 AND organization_id = $2 AND group_id = $3 AND revoked_at IS NULL
		RETURNING `+tokenCols, id, orgID, groupID, prefix, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Token{}, ErrNotFound
	}
	if err != nil {
		return "", Token{}, fmt.Errorf("collectors: regenerate token: %w", err)
	}
	return full, t, nil
}

// Authenticate resolves a presented token to its team. Unknown, revoked
// and malformed tokens are all ErrUnauthenticated: the caller answers 401
// without saying which.
func (s *Store) Authenticate(ctx context.Context, presented string) (TokenRef, error) {
	if !strings.HasPrefix(presented, tokenPrefix) {
		return TokenRef{}, ErrUnauthenticated
	}
	var ref TokenRef
	err := s.pool.QueryRow(ctx, `
		SELECT t.id, t.organization_id, t.group_id
		FROM collector_tokens t JOIN groups g ON g.id = t.group_id AND g.org_id = t.organization_id
		WHERE t.token_hash = $1 AND t.revoked_at IS NULL`, HashToken(presented)).
		Scan(&ref.TokenID, &ref.OrgID, &ref.GroupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenRef{}, ErrUnauthenticated
	}
	if err != nil {
		return TokenRef{}, fmt.Errorf("collectors: authenticate: %w", err)
	}
	return ref, nil
}

// TouchToken records use. Callers throttle it; HTTP agents authenticate
// on every poll.
func (s *Store) TouchToken(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE collector_tokens SET last_used_at = now() WHERE id = $1`, id)
	return err
}
