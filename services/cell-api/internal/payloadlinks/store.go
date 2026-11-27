// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package payloadlinks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store keeps each organization's link attributes.
type Store struct {
	pool *pgxpool.Pool

	// Every message list asks for the list, so it is cached briefly per
	// organization; a save clears its entry.
	mu    sync.Mutex
	cache map[uuid.UUID]cached
}

type cached struct {
	attrs []Attribute
	at    time.Time
}

const cacheFor = 30 * time.Second

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, cache: map[uuid.UUID]cached{}}
}

// Get returns the organization's list. An organization that never saved
// one has the defaults; one that removed every attribute has none.
func (s *Store) Get(ctx context.Context, orgID uuid.UUID) ([]Attribute, error) {
	s.mu.Lock()
	if c, ok := s.cache[orgID]; ok && time.Since(c.at) < cacheFor {
		s.mu.Unlock()
		return append([]Attribute(nil), c.attrs...), nil
	}
	s.mu.Unlock()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT attributes FROM org_payload_links WHERE organization_id = $1`, orgID).Scan(&raw)
	var attrs []Attribute
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		attrs = Defaults()
	case err != nil:
		return nil, fmt.Errorf("payloadlinks: get: %w", err)
	default:
		if err := json.Unmarshal(raw, &attrs); err != nil {
			return nil, fmt.Errorf("payloadlinks: decode: %w", err)
		}
	}
	s.mu.Lock()
	s.cache[orgID] = cached{attrs: attrs, at: time.Now()}
	s.mu.Unlock()
	return append([]Attribute(nil), attrs...), nil
}

// Put replaces the organization's list. The caller normalizes it first.
func (s *Store) Put(ctx context.Context, orgID uuid.UUID, attrs []Attribute) error {
	if attrs == nil {
		attrs = []Attribute{}
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO org_payload_links (organization_id, attributes) VALUES ($1, $2)
		ON CONFLICT (organization_id) DO UPDATE SET attributes = $2, updated_at = now()`,
		orgID, raw); err != nil {
		return fmt.Errorf("payloadlinks: put: %w", err)
	}
	s.mu.Lock()
	delete(s.cache, orgID)
	s.mu.Unlock()
	return nil
}

// Forget drops the organization's cached list, after a write that did not
// go through Put (a configuration import).
func (s *Store) Forget(orgID uuid.UUID) {
	s.mu.Lock()
	delete(s.cache, orgID)
	s.mu.Unlock()
}
