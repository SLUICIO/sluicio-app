// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package ingestkeys manages per-organization OTLP ingest API keys. A
// collector presents a key (Authorization: Bearer <key>) to cell-ingest,
// which resolves it to an organization and stamps that org onto every
// telemetry row. Keys are high-entropy random strings; we store only a
// SHA-256 hash (fast — cell-ingest verifies one per OTLP batch, so the
// deliberately-slow argon2id used for passwords/PATs is the wrong tool).
package ingestkeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// keyPrefix labels the token kind ("Sluicio ingest key") and lets the
// UI show a recognisable, non-secret stub.
const keyPrefix = "slk_"

// ErrNotFound is returned when a key id doesn't exist (or isn't the
// caller org's, or is already revoked).
var ErrNotFound = errors.New("ingestkeys: not found")

// Store wraps Postgres operations for ingest keys.
type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Key is the read-side projection (no hash) returned to the UI.
type Key struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Name           string     `json:"name"`
	Prefix         string     `json:"prefix"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
}

// HashKey returns the hex SHA-256 of a full key — the value stored in
// key_hash and the lookup key cell-ingest uses.
func HashKey(fullKey string) string {
	sum := sha256.Sum256([]byte(fullKey))
	return hex.EncodeToString(sum[:])
}

// generate mints a new key: "slk_" + 32 random URL-safe bytes. Returns
// the full key (shown to the user once), its display prefix, and hash.
func generate() (full, prefix, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("ingestkeys: rand: %w", err)
	}
	full = keyPrefix + base64.RawURLEncoding.EncodeToString(b)
	prefix = full[:12] // "slk_" + first 8 chars of the secret
	hash = HashKey(full)
	return full, prefix, hash, nil
}

// Create mints and persists a key for the org. The returned plaintext
// is the only time the full key is available — the caller must surface
// it once and never store it.
func (s *Store) Create(ctx context.Context, orgID uuid.UUID, name string, createdBy *uuid.UUID) (plaintext string, key Key, err error) {
	full, prefix, hash, err := generate()
	if err != nil {
		return "", Key{}, err
	}
	const q = `
		INSERT INTO ingest_keys (organization_id, name, prefix, key_hash, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, organization_id, name, prefix, created_at, last_used_at, revoked_at`
	var k Key
	if err := s.pool.QueryRow(ctx, q, orgID, name, prefix, hash, createdBy).Scan(
		&k.ID, &k.OrganizationID, &k.Name, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt,
	); err != nil {
		return "", Key{}, fmt.Errorf("ingestkeys: create: %w", err)
	}
	return full, k, nil
}

// List returns the org's non-revoked keys, newest first.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Key, error) {
	const q = `
		SELECT id, organization_id, name, prefix, created_at, last_used_at, revoked_at
		FROM ingest_keys
		WHERE organization_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("ingestkeys: list: %w", err)
	}
	defer rows.Close()
	out := make([]Key, 0)
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.ID, &k.OrganizationID, &k.Name, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Revoke soft-deletes a key (sets revoked_at). Scoped to the org so a
// leaked id can't revoke another tenant's key. Returns ErrNotFound if
// there's no live key with that id in the org.
func (s *Store) Revoke(ctx context.Context, orgID, id uuid.UUID) error {
	const q = `
		UPDATE ingest_keys SET revoked_at = now()
		WHERE id = $1 AND organization_id = $2 AND revoked_at IS NULL`
	tag, err := s.pool.Exec(ctx, q, id, orgID)
	if err != nil {
		return fmt.Errorf("ingestkeys: revoke: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Resolved is one live key's (hash → org) mapping, loaded by cell-ingest.
type Resolved struct {
	KeyID          uuid.UUID
	OrganizationID uuid.UUID
	Hash           string
}

// LoadLive returns every non-revoked key's hash→org mapping. cell-ingest
// calls this to (re)build its in-memory validation cache.
func (s *Store) LoadLive(ctx context.Context) ([]Resolved, error) {
	const q = `
		SELECT id, organization_id, key_hash
		FROM ingest_keys
		WHERE revoked_at IS NULL`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("ingestkeys: load live: %w", err)
	}
	defer rows.Close()
	out := make([]Resolved, 0)
	for rows.Next() {
		var r Resolved
		if err := rows.Scan(&r.KeyID, &r.OrganizationID, &r.Hash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LookupByHash resolves a single live key by its hash — the cache-miss
// path so a freshly-created key works without waiting for a refresh.
// Returns ErrNotFound if no live key matches.
func (s *Store) LookupByHash(ctx context.Context, hash string) (Resolved, error) {
	const q = `
		SELECT id, organization_id, key_hash
		FROM ingest_keys
		WHERE key_hash = $1 AND revoked_at IS NULL`
	var r Resolved
	if err := s.pool.QueryRow(ctx, q, hash).Scan(&r.KeyID, &r.OrganizationID, &r.Hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Resolved{}, ErrNotFound
		}
		return Resolved{}, fmt.Errorf("ingestkeys: lookup: %w", err)
	}
	return r, nil
}

// TouchLastUsed records that a key was used (best-effort; callers
// throttle so this isn't written on every batch).
func (s *Store) TouchLastUsed(ctx context.Context, keyID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE ingest_keys SET last_used_at = now() WHERE id = $1`, keyID)
	return err
}
