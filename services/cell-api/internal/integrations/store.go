// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integrations

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when an integration or matcher is not found.
var ErrNotFound = errors.New("integration not found")

// Store is the Postgres-backed CRUD layer.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by the given Postgres pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// List returns every integration for the org, ordered by name.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Integration, error) {
	const q = `
		SELECT id, organization_id, slug, name, COALESCE(description, ''), created_at, updated_at
		FROM integrations
		WHERE organization_id = $1
		ORDER BY name
	`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("list integrations: %w", err)
	}
	defer rows.Close()

	var out []Integration
	for rows.Next() {
		var i Integration
		if err := rows.Scan(&i.ID, &i.OrganizationID, &i.Slug, &i.Name, &i.Description, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Get returns a single integration with its matchers.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (IntegrationWithMatchers, error) {
	const q = `
		SELECT id, organization_id, slug, name, COALESCE(description, ''), badge_public, created_at, updated_at
		FROM integrations
		WHERE organization_id = $1 AND id = $2
	`
	var i Integration
	err := s.pool.QueryRow(ctx, q, orgID, id).Scan(&i.ID, &i.OrganizationID, &i.Slug, &i.Name, &i.Description, &i.BadgePublic, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return IntegrationWithMatchers{}, ErrNotFound
	}
	if err != nil {
		return IntegrationWithMatchers{}, fmt.Errorf("get integration: %w", err)
	}

	matchers, err := s.MatchersForIntegration(ctx, id)
	if err != nil {
		return IntegrationWithMatchers{}, err
	}
	return IntegrationWithMatchers{Integration: i, Matchers: matchers}, nil
}

// SetBadgePublic flips whether this integration exposes a public status badge.
// Org-scoped; ErrNotFound if the integration isn't in the org.
func (s *Store) SetBadgePublic(ctx context.Context, orgID, id uuid.UUID, public bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE integrations SET badge_public = $3, updated_at = now()
		 WHERE organization_id = $1 AND id = $2`, orgID, id, public)
	if err != nil {
		return fmt.Errorf("set integration badge_public: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Create inserts a new integration and its matchers in one transaction.
func (s *Store) Create(ctx context.Context, in IntegrationWithMatchers) (IntegrationWithMatchers, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return IntegrationWithMatchers{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var i Integration
	err = tx.QueryRow(ctx, `
		INSERT INTO integrations (organization_id, slug, name, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, organization_id, slug, name, COALESCE(description, ''), created_at, updated_at
	`, in.OrganizationID, in.Slug, in.Name, in.Description).Scan(
		&i.ID, &i.OrganizationID, &i.Slug, &i.Name, &i.Description, &i.CreatedAt, &i.UpdatedAt,
	)
	if err != nil {
		return IntegrationWithMatchers{}, fmt.Errorf("insert integration: %w", err)
	}

	matchers := make([]Matcher, 0, len(in.Matchers))
	for _, m := range in.Matchers {
		if err := m.Validate(); err != nil {
			return IntegrationWithMatchers{}, err
		}
		var created Matcher
		err := tx.QueryRow(ctx, `
			INSERT INTO integration_matchers (integration_id, attribute, operator, value, match_group)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, integration_id, attribute, operator, value, match_group, created_at
		`, i.ID, m.Attribute, m.Operator, m.Value, m.MatchGroup).Scan(
			&created.ID, &created.IntegrationID, &created.Attribute, &created.Operator, &created.Value, &created.MatchGroup, &created.CreatedAt,
		)
		if err != nil {
			return IntegrationWithMatchers{}, fmt.Errorf("insert matcher: %w", err)
		}
		matchers = append(matchers, created)
	}

	if err := tx.Commit(ctx); err != nil {
		return IntegrationWithMatchers{}, err
	}
	return IntegrationWithMatchers{Integration: i, Matchers: matchers}, nil
}

// Update changes the mutable fields (name, description) of an integration.
// Slug is intentionally immutable to keep URLs stable.
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, name, description string) (Integration, error) {
	var i Integration
	err := s.pool.QueryRow(ctx, `
		UPDATE integrations
		SET name = $3, description = $4, updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING id, organization_id, slug, name, COALESCE(description, ''), created_at, updated_at
	`, orgID, id, name, description).Scan(
		&i.ID, &i.OrganizationID, &i.Slug, &i.Name, &i.Description, &i.CreatedAt, &i.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	}
	if err != nil {
		return Integration{}, fmt.Errorf("update integration: %w", err)
	}
	return i, nil
}

// Delete removes the integration; matchers cascade.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM integrations WHERE organization_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("delete integration: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddMatcher inserts a matcher under the given integration.
func (s *Store) AddMatcher(ctx context.Context, integrationID uuid.UUID, m Matcher) (Matcher, error) {
	if err := m.Validate(); err != nil {
		return Matcher{}, err
	}
	var created Matcher
	err := s.pool.QueryRow(ctx, `
		INSERT INTO integration_matchers (integration_id, attribute, operator, value, match_group)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, integration_id, attribute, operator, value, match_group, created_at
	`, integrationID, m.Attribute, m.Operator, m.Value, m.MatchGroup).Scan(
		&created.ID, &created.IntegrationID, &created.Attribute, &created.Operator, &created.Value, &created.MatchGroup, &created.CreatedAt,
	)
	if err != nil {
		return Matcher{}, fmt.Errorf("insert matcher: %w", err)
	}
	return created, nil
}

// RemoveMatcher deletes a matcher by ID.
func (s *Store) RemoveMatcher(ctx context.Context, matcherID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM integration_matchers WHERE id = $1`, matcherID)
	if err != nil {
		return fmt.Errorf("delete matcher: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveServiceMatchers deletes the exact-match matchers (operator=equals,
// value=serviceName) that tie a service to an integration — the inverse of
// the "add to integration" action. Broad rules (prefix / suffix / contains /
// regex) are left untouched, since deleting those would silently change
// matching for other services. Returns how many were removed (0 means the
// service is matched by a rule, not a direct link).
func (s *Store) RemoveServiceMatchers(ctx context.Context, integrationID uuid.UUID, serviceName string) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM integration_matchers WHERE integration_id = $1 AND operator = 'equals' AND value = $2`,
		integrationID, serviceName)
	if err != nil {
		return 0, fmt.Errorf("integrations: remove service matchers: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MatchersForIntegration returns all matchers for the given integration.
func (s *Store) MatchersForIntegration(ctx context.Context, integrationID uuid.UUID) ([]Matcher, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, integration_id, attribute, operator, value, match_group, created_at
		FROM integration_matchers
		WHERE integration_id = $1
		ORDER BY match_group, created_at
	`, integrationID)
	if err != nil {
		return nil, fmt.Errorf("matchers for integration: %w", err)
	}
	defer rows.Close()

	out := make([]Matcher, 0)
	for rows.Next() {
		var m Matcher
		if err := rows.Scan(&m.ID, &m.IntegrationID, &m.Attribute, &m.Operator, &m.Value, &m.MatchGroup, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AllMatchersWithIntegration returns every matcher in the org joined
// with its integration. Used by the resolver to classify services.
type MatcherWithIntegration struct {
	Matcher     Matcher
	Integration Integration
}

func (s *Store) AllMatchersWithIntegration(ctx context.Context, orgID uuid.UUID) ([]MatcherWithIntegration, error) {
	const q = `
		SELECT
			m.id, m.integration_id, m.attribute, m.operator, m.value, m.match_group, m.created_at,
			i.id, i.organization_id, i.slug, i.name, COALESCE(i.description, ''),
			i.created_at, i.updated_at
		FROM integration_matchers m
		JOIN integrations i ON i.id = m.integration_id
		WHERE i.organization_id = $1
	`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("all matchers: %w", err)
	}
	defer rows.Close()

	var out []MatcherWithIntegration
	for rows.Next() {
		var mi MatcherWithIntegration
		if err := rows.Scan(
			&mi.Matcher.ID, &mi.Matcher.IntegrationID, &mi.Matcher.Attribute, &mi.Matcher.Operator, &mi.Matcher.Value, &mi.Matcher.MatchGroup, &mi.Matcher.CreatedAt,
			&mi.Integration.ID, &mi.Integration.OrganizationID, &mi.Integration.Slug, &mi.Integration.Name, &mi.Integration.Description,
			&mi.Integration.CreatedAt, &mi.Integration.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, mi)
	}
	return out, rows.Err()
}
