// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tags

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a tag or link is missing.
var ErrNotFound = errors.New("tag not found")

// ErrSlugConflict is returned when a tag slug already exists in the org.
var ErrSlugConflict = errors.New("tag slug already exists")

// Store is the Postgres-backed CRUD layer for tags and their links.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by the given Postgres pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ListWithUsage returns every tag in the org enriched with the
// number of integrations and services currently attached. Single
// query (LEFT JOIN + COUNT DISTINCT) so the management page can
// render without one round trip per tag.
func (s *Store) ListWithUsage(ctx context.Context, orgID uuid.UUID) ([]TagWithUsage, error) {
	const q = `
		SELECT t.id, t.organization_id, t.slug, t.name, t.color, t.created_at, t.updated_at,
		       COUNT(DISTINCT it.integration_id) AS integration_count,
		       COUNT(DISTINCT st.service_name)   AS service_count
		FROM tags t
		LEFT JOIN integration_tags it ON it.tag_id = t.id
		LEFT JOIN service_tags     st ON st.tag_id = t.id AND st.organization_id = t.organization_id
		WHERE t.organization_id = $1
		GROUP BY t.id
		ORDER BY t.name
	`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("list tags with usage: %w", err)
	}
	defer rows.Close()
	out := make([]TagWithUsage, 0)
	for rows.Next() {
		var t TagWithUsage
		if err := rows.Scan(
			&t.ID, &t.OrganizationID, &t.Slug, &t.Name, &t.Color, &t.CreatedAt, &t.UpdatedAt,
			&t.IntegrationCount, &t.ServiceCount,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// List returns every tag in the org, ordered by name.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Tag, error) {
	const q = `
		SELECT id, organization_id, slug, name, color, created_at, updated_at
		FROM tags
		WHERE organization_id = $1
		ORDER BY name
	`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer rows.Close()
	out := make([]Tag, 0)
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.Slug, &t.Name, &t.Color, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get returns one tag by ID, scoped to the org.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Tag, error) {
	const q = `
		SELECT id, organization_id, slug, name, color, created_at, updated_at
		FROM tags
		WHERE organization_id = $1 AND id = $2
	`
	var t Tag
	err := s.pool.QueryRow(ctx, q, orgID, id).Scan(
		&t.ID, &t.OrganizationID, &t.Slug, &t.Name, &t.Color, &t.CreatedAt, &t.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tag{}, ErrNotFound
	}
	if err != nil {
		return Tag{}, fmt.Errorf("get tag: %w", err)
	}
	return t, nil
}

// Create inserts a new tag. Slug conflicts surface as ErrSlugConflict.
func (s *Store) Create(ctx context.Context, t Tag) (Tag, error) {
	const q = `
		INSERT INTO tags (organization_id, slug, name, color)
		VALUES ($1, $2, $3, $4)
		RETURNING id, organization_id, slug, name, color, created_at, updated_at
	`
	var out Tag
	err := s.pool.QueryRow(ctx, q, t.OrganizationID, t.Slug, t.Name, t.Color).Scan(
		&out.ID, &out.OrganizationID, &out.Slug, &out.Name, &out.Color, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Tag{}, ErrSlugConflict
		}
		return Tag{}, fmt.Errorf("insert tag: %w", err)
	}
	return out, nil
}

// Update changes the mutable fields (name, color). Slug is immutable
// so chip URLs and saved filters stay stable.
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, name, color string) (Tag, error) {
	const q = `
		UPDATE tags
		SET name = $3, color = $4, updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING id, organization_id, slug, name, color, created_at, updated_at
	`
	var out Tag
	err := s.pool.QueryRow(ctx, q, orgID, id, name, color).Scan(
		&out.ID, &out.OrganizationID, &out.Slug, &out.Name, &out.Color, &out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tag{}, ErrNotFound
	}
	if err != nil {
		return Tag{}, fmt.Errorf("update tag: %w", err)
	}
	return out, nil
}

// Delete removes a tag; integration_tags and service_tags links
// cascade.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM tags WHERE organization_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// integration <-> tag

// AttachToIntegration links a tag to an integration. The integration's
// org must match the tag's org; we enforce this by joining through the
// integrations row when we look the tag up.
func (s *Store) AttachToIntegration(ctx context.Context, orgID, integrationID, tagID uuid.UUID) error {
	if err := s.assertSameOrg(ctx, orgID, tagID, integrationID); err != nil {
		return err
	}
	const q = `
		INSERT INTO integration_tags (integration_id, tag_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`
	if _, err := s.pool.Exec(ctx, q, integrationID, tagID); err != nil {
		return fmt.Errorf("attach tag to integration: %w", err)
	}
	return nil
}

// DetachFromIntegration removes a tag link from an integration.
func (s *Store) DetachFromIntegration(ctx context.Context, orgID, integrationID, tagID uuid.UUID) error {
	if err := s.assertSameOrg(ctx, orgID, tagID, integrationID); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM integration_tags WHERE integration_id = $1 AND tag_id = $2`,
		integrationID, tagID,
	)
	if err != nil {
		return fmt.Errorf("detach tag from integration: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListForIntegration returns every tag attached to an integration.
func (s *Store) ListForIntegration(ctx context.Context, orgID, integrationID uuid.UUID) ([]Tag, error) {
	const q = `
		SELECT t.id, t.organization_id, t.slug, t.name, t.color, t.created_at, t.updated_at
		FROM tags t
		JOIN integration_tags it ON it.tag_id = t.id
		WHERE t.organization_id = $1 AND it.integration_id = $2
		ORDER BY t.name
	`
	return s.queryTags(ctx, q, orgID, integrationID)
}

// ListForIntegrations returns every tag for the given set of
// integrations, grouped by integration ID. Lets list endpoints fetch
// tags for many integrations in one round trip.
func (s *Store) ListForIntegrations(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]Tag, error) {
	out := map[uuid.UUID][]Tag{}
	if len(ids) == 0 {
		return out, nil
	}
	const q = `
		SELECT it.integration_id,
		       t.id, t.organization_id, t.slug, t.name, t.color, t.created_at, t.updated_at
		FROM integration_tags it
		JOIN tags t ON t.id = it.tag_id
		WHERE t.organization_id = $1 AND it.integration_id = ANY($2)
		ORDER BY t.name
	`
	rows, err := s.pool.Query(ctx, q, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("list tags for integrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var integID uuid.UUID
		var t Tag
		if err := rows.Scan(&integID,
			&t.ID, &t.OrganizationID, &t.Slug, &t.Name, &t.Color, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out[integID] = append(out[integID], t)
	}
	return out, rows.Err()
}

// IntegrationIDsForTag returns the integrations tagged with the given
// tag.
func (s *Store) IntegrationIDsForTag(ctx context.Context, orgID, tagID uuid.UUID) ([]uuid.UUID, error) {
	const q = `
		SELECT it.integration_id
		FROM integration_tags it
		JOIN integrations i ON i.id = it.integration_id
		WHERE i.organization_id = $1 AND it.tag_id = $2
	`
	rows, err := s.pool.Query(ctx, q, orgID, tagID)
	if err != nil {
		return nil, fmt.Errorf("integrations for tag: %w", err)
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// service <-> tag

// AttachToService tags the named service. The (org, service_name)
// pair is what we key on, since services live in ClickHouse.
func (s *Store) AttachToService(ctx context.Context, orgID uuid.UUID, serviceName string, tagID uuid.UUID) error {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return errInvalid("service_name must not be empty")
	}
	if err := s.assertTagInOrg(ctx, orgID, tagID); err != nil {
		return err
	}
	const q = `
		INSERT INTO service_tags (organization_id, service_name, tag_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`
	if _, err := s.pool.Exec(ctx, q, orgID, serviceName, tagID); err != nil {
		return fmt.Errorf("attach tag to service: %w", err)
	}
	return nil
}

// DetachFromService removes a tag from a service.
func (s *Store) DetachFromService(ctx context.Context, orgID uuid.UUID, serviceName string, tagID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM service_tags WHERE organization_id = $1 AND service_name = $2 AND tag_id = $3`,
		orgID, serviceName, tagID,
	)
	if err != nil {
		return fmt.Errorf("detach tag from service: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListForService returns every tag attached to a service.
func (s *Store) ListForService(ctx context.Context, orgID uuid.UUID, serviceName string) ([]Tag, error) {
	const q = `
		SELECT t.id, t.organization_id, t.slug, t.name, t.color, t.created_at, t.updated_at
		FROM tags t
		JOIN service_tags st ON st.tag_id = t.id
		WHERE t.organization_id = $1 AND st.organization_id = $1 AND st.service_name = $2
		ORDER BY t.name
	`
	return s.queryTags(ctx, q, orgID, serviceName)
}

// ListForServices returns tags for the given service names, grouped
// by service name. Lets list endpoints fetch in one round trip.
func (s *Store) ListForServices(ctx context.Context, orgID uuid.UUID, names []string) (map[string][]Tag, error) {
	out := map[string][]Tag{}
	if len(names) == 0 {
		return out, nil
	}
	const q = `
		SELECT st.service_name,
		       t.id, t.organization_id, t.slug, t.name, t.color, t.created_at, t.updated_at
		FROM service_tags st
		JOIN tags t ON t.id = st.tag_id
		WHERE st.organization_id = $1 AND st.service_name = ANY($2)
		ORDER BY t.name
	`
	rows, err := s.pool.Query(ctx, q, orgID, names)
	if err != nil {
		return nil, fmt.Errorf("list tags for services: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var t Tag
		if err := rows.Scan(&name,
			&t.ID, &t.OrganizationID, &t.Slug, &t.Name, &t.Color, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out[name] = append(out[name], t)
	}
	return out, rows.Err()
}

// ServicesForTag returns the service names tagged with the given tag.
func (s *Store) ServicesForTag(ctx context.Context, orgID, tagID uuid.UUID) ([]string, error) {
	const q = `
		SELECT service_name FROM service_tags
		WHERE organization_id = $1 AND tag_id = $2
		ORDER BY service_name
	`
	rows, err := s.pool.Query(ctx, q, orgID, tagID)
	if err != nil {
		return nil, fmt.Errorf("services for tag: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// helpers

func (s *Store) queryTags(ctx context.Context, q string, args ...any) ([]Tag, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query tags: %w", err)
	}
	defer rows.Close()
	out := make([]Tag, 0)
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.Slug, &t.Name, &t.Color, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// assertTagInOrg returns ErrNotFound unless the tag belongs to orgID.
func (s *Store) assertTagInOrg(ctx context.Context, orgID, tagID uuid.UUID) error {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tags WHERE id = $1 AND organization_id = $2)`,
		tagID, orgID,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check tag in org: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// assertSameOrg checks that the integration and the tag both belong
// to orgID before linking them. Cheap one-query check to keep the FK
// model honest without enforcing a composite FK across schemas.
func (s *Store) assertSameOrg(ctx context.Context, orgID, tagID, integrationID uuid.UUID) error {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM tags         WHERE id = $1 AND organization_id = $3)
		   AND EXISTS(SELECT 1 FROM integrations WHERE id = $2 AND organization_id = $3)
	`, tagID, integrationID, orgID).Scan(&ok)
	if err != nil {
		return fmt.Errorf("check same org: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// isUniqueViolation returns true for Postgres' duplicate-key error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
