// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package facetoverrides

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed layer for manual facet overrides. Every
// method scopes to (organization_id, service_name) because overrides
// are always read or replaced for one specific service.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by the given Postgres pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ListForService returns every override for the given service, ordered
// by facet slug for a stable presentation.
func (s *Store) ListForService(ctx context.Context, orgID uuid.UUID, serviceName string) ([]Override, error) {
	const q = `
		SELECT id, organization_id, service_name, facet_slug, action, created_at, updated_at
		FROM service_facet_overrides
		WHERE organization_id = $1 AND service_name = $2
		ORDER BY facet_slug
	`
	rows, err := s.pool.Query(ctx, q, orgID, serviceName)
	if err != nil {
		return nil, fmt.Errorf("list facet overrides: %w", err)
	}
	defer rows.Close()
	out := make([]Override, 0)
	for rows.Next() {
		var o Override
		var action string
		if err := rows.Scan(
			&o.ID, &o.OrganizationID, &o.ServiceName, &o.FacetSlug,
			&action, &o.CreatedAt, &o.UpdatedAt,
		); err != nil {
			return nil, err
		}
		o.Action = Action(action)
		out = append(out, o)
	}
	return out, rows.Err()
}

// ReplaceForService atomically replaces the entire override set for a
// service: the existing rows are deleted and the given include/exclude
// slugs reinserted, all in one transaction. This matches the editor's
// "edit the whole list and save" interaction and makes re-saves
// idempotent. Callers are responsible for validating the slugs against
// the facet registry first. Slugs appearing in both lists resolve to
// the last write via the upsert, but the API layer keeps them disjoint.
func (s *Store) ReplaceForService(ctx context.Context, orgID uuid.UUID, serviceName string, includes, excludes []string) error {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return fmt.Errorf("service_name must not be empty")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	if _, err := tx.Exec(ctx,
		`DELETE FROM service_facet_overrides WHERE organization_id = $1 AND service_name = $2`,
		orgID, serviceName,
	); err != nil {
		return fmt.Errorf("clear facet overrides: %w", err)
	}

	insert := func(slug string, action Action) error {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			return nil
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO service_facet_overrides (organization_id, service_name, facet_slug, action)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (organization_id, service_name, facet_slug)
			DO UPDATE SET action = EXCLUDED.action, updated_at = now()
		`, orgID, serviceName, slug, string(action))
		return err
	}
	for _, slug := range includes {
		if err := insert(slug, ActionInclude); err != nil {
			return fmt.Errorf("insert include override: %w", err)
		}
	}
	for _, slug := range excludes {
		if err := insert(slug, ActionExclude); err != nil {
			return fmt.Errorf("insert exclude override: %w", err)
		}
	}
	return tx.Commit(ctx)
}
