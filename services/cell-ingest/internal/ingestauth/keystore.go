// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package ingestauth resolves a presented OTLP ingest API key to the
// organization that owns it. Keys live in Postgres (ingest_keys, written
// by cell-api); cell-ingest must validate one on every batch, so we keep
// an in-memory hash→org cache refreshed on a timer, with a DB fallback on
// cache miss so a freshly-minted key works within seconds.
package ingestauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KeyStore caches live ingest keys and resolves them to org ids.
type KeyStore struct {
	pool   *pgxpool.Pool
	logger *slog.Logger

	mu    sync.RWMutex
	cache map[string]uuid.UUID // sha256hex(key) → organization_id
}

// New builds a KeyStore. Call Refresh (or Run) before serving.
func New(pool *pgxpool.Pool, logger *slog.Logger) *KeyStore {
	return &KeyStore{
		pool:   pool,
		logger: logger,
		cache:  map[string]uuid.UUID{},
	}
}

// hash returns the hex SHA-256 of a key — the form stored in
// ingest_keys.key_hash.
func hash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Refresh rebuilds the cache from all non-revoked keys.
func (k *KeyStore) Refresh(ctx context.Context) error {
	rows, err := k.pool.Query(ctx,
		`SELECT key_hash, organization_id FROM ingest_keys WHERE revoked_at IS NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	next := map[string]uuid.UUID{}
	for rows.Next() {
		var h string
		var org uuid.UUID
		if err := rows.Scan(&h, &org); err != nil {
			return err
		}
		next[h] = org
	}
	if err := rows.Err(); err != nil {
		return err
	}
	k.mu.Lock()
	k.cache = next
	k.mu.Unlock()
	return nil
}

// Run does an initial Refresh, then refreshes on `every`. It also
// reflects revocations (a revoked key drops out of the cache on the next
// tick). Blocks until ctx is cancelled.
func (k *KeyStore) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 30 * time.Second
	}
	if err := k.Refresh(ctx); err != nil {
		k.logger.Warn("ingest key cache initial refresh failed", "err", err)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := k.Refresh(ctx); err != nil {
				k.logger.Warn("ingest key cache refresh failed", "err", err)
			}
		}
	}
}

// Resolve maps a presented key to its org. Cache hit is lock-cheap; a
// miss falls back to a direct DB lookup (so a key created seconds ago is
// usable before the next refresh) and is cached on success.
func (k *KeyStore) Resolve(ctx context.Context, presented string) (uuid.UUID, bool) {
	if presented == "" {
		return uuid.Nil, false
	}
	h := hash(presented)

	k.mu.RLock()
	org, ok := k.cache[h]
	k.mu.RUnlock()
	if ok {
		return org, true
	}

	// Cache miss — could be a brand-new key. One scoped DB lookup.
	var dbOrg uuid.UUID
	err := k.pool.QueryRow(ctx,
		`SELECT organization_id FROM ingest_keys WHERE key_hash = $1 AND revoked_at IS NULL`, h).
		Scan(&dbOrg)
	if err != nil {
		if err != pgx.ErrNoRows {
			k.logger.Warn("ingest key lookup failed", "err", err)
		}
		return uuid.Nil, false
	}
	k.mu.Lock()
	k.cache[h] = dbOrg
	k.mu.Unlock()
	return dbOrg, true
}
