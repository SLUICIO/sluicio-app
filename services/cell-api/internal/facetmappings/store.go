// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package facetmappings

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed CRUD layer for facet attribute
// mappings. Each method scopes to (organization_id, service_name)
// because mappings are always queried for a specific service when
// building the IO attribute resolver at request time.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by the given Postgres pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ListForService returns every mapping for the given service, ordered
// by creation time so the SQL CASE-WHEN cascade applies rules in a
// stable, predictable order ("first match wins" reads consistent with
// how the user added them).
func (s *Store) ListForService(ctx context.Context, orgID uuid.UUID, serviceName string) ([]Mapping, error) {
	const q = `
		SELECT id, organization_id, service_name,
		       attribute_source, attribute_key, match_operator, match_value,
		       set_io_kind, set_io_role, created_at, updated_at
		FROM service_facet_mappings
		WHERE organization_id = $1 AND service_name = $2
		ORDER BY created_at ASC
	`
	rows, err := s.pool.Query(ctx, q, orgID, serviceName)
	if err != nil {
		return nil, fmt.Errorf("list facet mappings: %w", err)
	}
	defer rows.Close()
	out := make([]Mapping, 0)
	for rows.Next() {
		var m Mapping
		var source, op string
		if err := rows.Scan(
			&m.ID, &m.OrganizationID, &m.ServiceName,
			&source, &m.AttributeKey, &op, &m.MatchValue,
			&m.SetIOKind, &m.SetIORole, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		m.AttributeSource = AttributeSource(source)
		m.MatchOperator = Operator(op)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Get returns one mapping by ID, scoped to the org.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Mapping, error) {
	const q = `
		SELECT id, organization_id, service_name,
		       attribute_source, attribute_key, match_operator, match_value,
		       set_io_kind, set_io_role, created_at, updated_at
		FROM service_facet_mappings
		WHERE organization_id = $1 AND id = $2
	`
	var m Mapping
	var source, op string
	err := s.pool.QueryRow(ctx, q, orgID, id).Scan(
		&m.ID, &m.OrganizationID, &m.ServiceName,
		&source, &m.AttributeKey, &op, &m.MatchValue,
		&m.SetIOKind, &m.SetIORole, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Mapping{}, ErrNotFound
	}
	if err != nil {
		return Mapping{}, fmt.Errorf("get facet mapping: %w", err)
	}
	m.AttributeSource = AttributeSource(source)
	m.MatchOperator = Operator(op)
	return m, nil
}

// Create inserts a new mapping. The caller is responsible for calling
// Validate(); the DB CHECKs are a backstop, not the primary defence.
// match_value is forced to "" when the operator is "exists" so the
// DB CHECK never fires for that case.
func (s *Store) Create(ctx context.Context, m Mapping) (Mapping, error) {
	if m.MatchOperator == OperatorExists {
		m.MatchValue = ""
	}
	const q = `
		INSERT INTO service_facet_mappings (
		    organization_id, service_name,
		    attribute_source, attribute_key, match_operator, match_value,
		    set_io_kind, set_io_role
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, organization_id, service_name,
		          attribute_source, attribute_key, match_operator, match_value,
		          set_io_kind, set_io_role, created_at, updated_at
	`
	var out Mapping
	var source, op string
	err := s.pool.QueryRow(ctx, q,
		m.OrganizationID, m.ServiceName,
		string(m.AttributeSource), m.AttributeKey, string(m.MatchOperator), m.MatchValue,
		m.SetIOKind, m.SetIORole,
	).Scan(
		&out.ID, &out.OrganizationID, &out.ServiceName,
		&source, &out.AttributeKey, &op, &out.MatchValue,
		&out.SetIOKind, &out.SetIORole, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return Mapping{}, fmt.Errorf("insert facet mapping: %w", err)
	}
	out.AttributeSource = AttributeSource(source)
	out.MatchOperator = Operator(op)
	return out, nil
}

// Delete removes one mapping, scoped to the org.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM service_facet_mappings WHERE organization_id = $1 AND id = $2`,
		orgID, id,
	)
	if err != nil {
		return fmt.Errorf("delete facet mapping: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
