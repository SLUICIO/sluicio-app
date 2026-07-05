// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package oauth backs the small OAuth 2.1 authorization server cell-api runs in
// front of the remote MCP endpoint: dynamically-registered clients + short-
// lived, single-use authorization codes. Access tokens are NOT stored here —
// the token endpoint mints a viewer-scoped api_token, so the MCP endpoint's
// existing Bearer auth + RBAC validate them unchanged.
package oauth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a client or (valid, unconsumed) code lookup misses.
var ErrNotFound = errors.New("oauth: not found")

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Client is a dynamically-registered OAuth client (public; PKCE, no secret).
type Client struct {
	ClientID     string
	ClientName   string
	RedirectURIs []string
	CreatedAt    time.Time
}

func (s *Store) CreateClient(ctx context.Context, c Client) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO oauth_clients (client_id, client_name, redirect_uris) VALUES ($1,$2,$3)`,
		c.ClientID, c.ClientName, c.RedirectURIs)
	return err
}

func (s *Store) GetClient(ctx context.Context, clientID string) (Client, error) {
	var c Client
	err := s.pool.QueryRow(ctx,
		`SELECT client_id, client_name, redirect_uris, created_at FROM oauth_clients WHERE client_id=$1`,
		clientID).Scan(&c.ClientID, &c.ClientName, &c.RedirectURIs, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, ErrNotFound
	}
	return c, err
}

// AuthCode is a pending authorization-code grant (PKCE challenge bound in).
type AuthCode struct {
	Code                string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	UserID              uuid.UUID
	Scope               string
	ExpiresAt           time.Time
}

func (s *Store) CreateCode(ctx context.Context, c AuthCode) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO oauth_auth_codes
		   (code, client_id, redirect_uri, code_challenge, code_challenge_method, user_id, scope, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		c.Code, c.ClientID, c.RedirectURI, c.CodeChallenge, c.CodeChallengeMethod, c.UserID, c.Scope, c.ExpiresAt)
	return err
}

// ConsumeCode atomically marks a valid, unexpired, unconsumed code as consumed
// and returns it. Returns ErrNotFound if missing / already used / expired — the
// UPDATE..WHERE..RETURNING makes single-use atomic against replay.
func (s *Store) ConsumeCode(ctx context.Context, code string) (AuthCode, error) {
	var c AuthCode
	err := s.pool.QueryRow(ctx,
		`UPDATE oauth_auth_codes SET consumed=true
		   WHERE code=$1 AND consumed=false AND expires_at > now()
		 RETURNING code, client_id, redirect_uri, code_challenge, code_challenge_method, user_id, scope, expires_at`,
		code).Scan(&c.Code, &c.ClientID, &c.RedirectURI, &c.CodeChallenge, &c.CodeChallengeMethod, &c.UserID, &c.Scope, &c.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthCode{}, ErrNotFound
	}
	return c, err
}
