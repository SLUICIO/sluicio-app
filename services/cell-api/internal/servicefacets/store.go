// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package servicefacets stores an org's user-defined ("custom") service facets
// — classification labels created in the UI, merged with the built-in
// code-defined facets (servicetypes.Registry) on read. Custom facets carry no
// widgets; they're labels you assign to services via facet overrides. Slug is
// immutable after create (matching the groups model).
package servicefacets

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a custom facet lookup misses.
var ErrNotFound = errors.New("servicefacets: not found")

// Facet is one org-owned custom service facet.
type Facet struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, slug, name, description, created_at, updated_at`

func scan(row pgx.Row) (Facet, error) {
	var f Facet
	err := row.Scan(&f.ID, &f.Slug, &f.Name, &f.Description, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Facet, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM service_facets WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Facet
	for rows.Next() {
		f, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, orgID uuid.UUID, slug, name, description string) (Facet, error) {
	return scan(s.pool.QueryRow(ctx,
		`INSERT INTO service_facets (org_id, slug, name, description) VALUES ($1,$2,$3,$4) RETURNING `+cols,
		orgID, slug, name, description))
}

func (s *Store) UpdateBySlug(ctx context.Context, orgID uuid.UUID, slug, name, description string) (Facet, error) {
	f, err := scan(s.pool.QueryRow(ctx,
		`UPDATE service_facets SET name=$3, description=$4, updated_at=now() WHERE org_id=$1 AND slug=$2 RETURNING `+cols,
		orgID, slug, name, description))
	if errors.Is(err, pgx.ErrNoRows) {
		return Facet{}, ErrNotFound
	}
	return f, err
}

func (s *Store) DeleteBySlug(ctx context.Context, orgID uuid.UUID, slug string) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM service_facets WHERE org_id=$1 AND slug=$2`, orgID, slug)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
