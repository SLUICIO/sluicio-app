// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package schemas owns the data-schema catalogue and the per-service
// In-Schema / Out-Schema links. A schema describes the shape of a
// message that a service consumes or produces; pinning schemas on
// services lets the UI surface a new dependency dimension ("which
// services share a schema", "whose out → whose in").
package schemas

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Direction enumerates the two link directions a schema can have on
// a service.
type Direction string

const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

// Kind enumerates the artifact categories the catalogue holds. It's
// validated by a CHECK constraint in the schema; new values require a
// migration.
//
// Transformations (XSLT, jq, JSONata, Liquid, Mustache, Handlebars)
// used to live here under kind='mapping' / kind='template'. Those
// moved into the dedicated `maps` catalogue in migration 0016 and the
// constants are gone — Schemas is now strictly the shape catalogue.
const (
	KindSchema  = "schema"
	KindExample = "example"
	KindOther   = "other"
)

// Schema is one row in the schemas catalogue.
type Schema struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id,omitempty"`
	Name           string    `json:"name"`
	// Kind tells the UI what category of artifact this is: a
	// shape-description ("schema"), a transformation ("template" /
	// "mapping"), a sample document ("example"), or "other". Format
	// describes the syntax of the content (json/yaml/xml/liquid/…).
	Kind        string    `json:"kind"`
	Version     string    `json:"version"`
	Description string    `json:"description"`
	Format      string    `json:"format"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	// UsageCount is the number of (service, direction) rows pointing
	// to this schema. Populated on list responses; absent on detail
	// (caller can compute via UsageFor).
	UsageCount int `json:"usage_count,omitempty"`
	// Usage is the full per-service link list pointing at this schema,
	// populated on list responses so the table can render clickable
	// service chips without a per-row round-trip.
	Usage []Usage `json:"usage,omitempty"`
}

// Input is the write payload accepted by Create / Update.
type Input struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Format      string `json:"format"`
	Content     string `json:"content"`
}

// Usage describes one service that points at a schema.
type Usage struct {
	ServiceName string    `json:"service_name"`
	Direction   Direction `json:"direction"`
}

// Errors surfaced to callers.
var (
	ErrNotFound     = errors.New("schemas: not found")
	ErrNameExists   = errors.New("schemas: name and version already in use")
	ErrValidation   = errors.New("schemas: validation error")
	ErrBadDirection = errors.New("schemas: direction must be 'in' or 'out'")
	ErrBadKind      = errors.New("schemas: kind must be schema|example|other (transformations belong in Maps)")
)

// validKinds is the allow-list mirrored from the CHECK constraint.
var validKinds = map[string]bool{
	KindSchema:  true,
	KindExample: true,
	KindOther:   true,
}

// Store is the Postgres-backed CRUD + link layer.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ── schemas CRUD ────────────────────────────────────────────────────────

// List returns all schemas for the org, ordered by name, each
// decorated with its usage count.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Schema, error) {
	const q = `
		SELECT s.id, s.organization_id, s.name, s.kind, s.version,
		       s.description, s.format, s.content,
		       s.created_at, s.updated_at,
		       COALESCE(u.cnt, 0) AS usage
		FROM schemas s
		LEFT JOIN (
			SELECT schema_id, COUNT(*) AS cnt FROM service_schemas GROUP BY schema_id
		) u ON u.schema_id = s.id
		WHERE s.organization_id = $1
		ORDER BY lower(s.name) ASC, s.version ASC`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}
	defer rows.Close()
	out := make([]Schema, 0)
	for rows.Next() {
		var sch Schema
		if err := rows.Scan(
			&sch.ID, &sch.OrganizationID, &sch.Name, &sch.Kind, &sch.Version,
			&sch.Description, &sch.Format, &sch.Content,
			&sch.CreatedAt, &sch.UpdatedAt, &sch.UsageCount,
		); err != nil {
			return nil, err
		}
		out = append(out, sch)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Second query: pull every (schema, service, direction) row for the
	// org in one round-trip and fan it out onto each schema. The list is
	// already in memory; this is just so each row carries its clickable
	// service chips without a per-row request.
	usageByID, err := s.usageByOrg(ctx, orgID)
	if err != nil {
		// Don't fail the list — usage decoration is best-effort.
		return out, nil
	}
	for i := range out {
		if u, ok := usageByID[out[i].ID]; ok {
			out[i].Usage = u
		}
	}
	return out, nil
}

// usageByOrg returns every service_schemas row for the org, grouped by
// schema id. Used by List to populate Schema.Usage in one query rather
// than per-row.
func (s *Store) usageByOrg(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID][]Usage, error) {
	const q = `
		SELECT schema_id, service_name, direction
		FROM service_schemas
		WHERE organization_id = $1
		ORDER BY service_name, direction`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("usage by org: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID][]Usage)
	for rows.Next() {
		var sid uuid.UUID
		var u Usage
		if err := rows.Scan(&sid, &u.ServiceName, &u.Direction); err != nil {
			return nil, err
		}
		out[sid] = append(out[sid], u)
	}
	return out, rows.Err()
}

// Get returns one schema by id.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Schema, error) {
	const q = `
		SELECT id, organization_id, name, kind, version,
		       description, format, content,
		       created_at, updated_at
		FROM schemas WHERE organization_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, q, orgID, id)
	var sch Schema
	if err := row.Scan(
		&sch.ID, &sch.OrganizationID, &sch.Name, &sch.Kind, &sch.Version,
		&sch.Description, &sch.Format, &sch.Content,
		&sch.CreatedAt, &sch.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Schema{}, ErrNotFound
		}
		return Schema{}, err
	}
	return sch, nil
}

// Create inserts a new schema. Name must be unique within the org.
func (s *Store) Create(ctx context.Context, orgID uuid.UUID, in Input) (Schema, error) {
	if err := normalise(&in); err != nil {
		return Schema{}, err
	}
	const q = `
		INSERT INTO schemas (organization_id, name, kind, version,
		                     description, format, content)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, organization_id, name, kind, version,
		          description, format, content,
		          created_at, updated_at`
	row := s.pool.QueryRow(ctx, q,
		orgID, in.Name, in.Kind, in.Version,
		in.Description, in.Format, in.Content)
	var sch Schema
	if err := row.Scan(
		&sch.ID, &sch.OrganizationID, &sch.Name, &sch.Kind, &sch.Version,
		&sch.Description, &sch.Format, &sch.Content,
		&sch.CreatedAt, &sch.UpdatedAt,
	); err != nil {
		if isNameClash(err) {
			return Schema{}, ErrNameExists
		}
		return Schema{}, fmt.Errorf("create schema: %w", err)
	}
	return sch, nil
}

// Update changes the editable fields. Name is editable; if the new
// name collides with another schema, ErrNameExists is returned.
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, in Input) (Schema, error) {
	if err := normalise(&in); err != nil {
		return Schema{}, err
	}
	const q = `
		UPDATE schemas
		SET name = $3, kind = $4, version = $5,
		    description = $6, format = $7, content = $8,
		    updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING id, organization_id, name, kind, version,
		          description, format, content,
		          created_at, updated_at`
	row := s.pool.QueryRow(ctx, q,
		orgID, id, in.Name, in.Kind, in.Version,
		in.Description, in.Format, in.Content)
	var sch Schema
	if err := row.Scan(
		&sch.ID, &sch.OrganizationID, &sch.Name, &sch.Kind, &sch.Version,
		&sch.Description, &sch.Format, &sch.Content,
		&sch.CreatedAt, &sch.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Schema{}, ErrNotFound
		}
		if isNameClash(err) {
			return Schema{}, ErrNameExists
		}
		return Schema{}, fmt.Errorf("update schema: %w", err)
	}
	return sch, nil
}

// normalise trims + defaults the input and validates the closed-set
// columns.
func normalise(in *Input) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Version = strings.TrimSpace(in.Version)
	if in.Name == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	if in.Format == "" {
		in.Format = "json"
	}
	if in.Kind == "" {
		in.Kind = KindSchema
	}
	if !validKinds[in.Kind] {
		return ErrBadKind
	}
	return nil
}

// isNameClash returns true for both the legacy and the new
// (name, version) uniqueness violation; the error string contains
// the constraint name.
func isNameClash(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "schemas_organization_id_name_key") ||
		strings.Contains(msg, "schemas_organization_id_name_version_key")
}

// Delete removes a schema (and CASCADEs all service_schemas links).
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM schemas WHERE organization_id = $1 AND id = $2`,
		orgID, id)
	if err != nil {
		return fmt.Errorf("delete schema: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UsageFor returns every (service, direction) row pointing at the
// given schema. Used to populate the schema detail "used by" panel.
func (s *Store) UsageFor(ctx context.Context, orgID, schemaID uuid.UUID) ([]Usage, error) {
	const q = `
		SELECT service_name, direction
		FROM service_schemas
		WHERE organization_id = $1 AND schema_id = $2
		ORDER BY service_name, direction`
	rows, err := s.pool.Query(ctx, q, orgID, schemaID)
	if err != nil {
		return nil, fmt.Errorf("usage for: %w", err)
	}
	defer rows.Close()
	out := make([]Usage, 0)
	for rows.Next() {
		var u Usage
		if err := rows.Scan(&u.ServiceName, &u.Direction); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ── per-service links ───────────────────────────────────────────────────

// ServiceSchemas is the pair of schema ids attached to one service,
// keyed by direction. Either may be nil.
type ServiceSchemas struct {
	In  *Schema `json:"in,omitempty"`
	Out *Schema `json:"out,omitempty"`
}

// ForService returns the in / out schemas of one service, hydrated with
// the full schema rows. Either side may be nil if no link exists.
func (s *Store) ForService(ctx context.Context, orgID uuid.UUID, service string) (ServiceSchemas, error) {
	const q = `
		SELECT ss.direction,
		       s.id, s.organization_id, s.name, s.kind, s.version,
		       s.description, s.format, s.content,
		       s.created_at, s.updated_at
		FROM service_schemas ss
		JOIN schemas s ON s.id = ss.schema_id
		WHERE ss.organization_id = $1 AND ss.service_name = $2`
	rows, err := s.pool.Query(ctx, q, orgID, service)
	if err != nil {
		return ServiceSchemas{}, fmt.Errorf("for service: %w", err)
	}
	defer rows.Close()
	var out ServiceSchemas
	for rows.Next() {
		var dir Direction
		var sch Schema
		if err := rows.Scan(
			&dir,
			&sch.ID, &sch.OrganizationID, &sch.Name, &sch.Kind, &sch.Version,
			&sch.Description, &sch.Format, &sch.Content,
			&sch.CreatedAt, &sch.UpdatedAt,
		); err != nil {
			return ServiceSchemas{}, err
		}
		switch dir {
		case DirectionIn:
			c := sch
			out.In = &c
		case DirectionOut:
			c := sch
			out.Out = &c
		}
	}
	return out, rows.Err()
}

// SetServiceSchemas writes the in / out links for one service in one
// transaction. A nil id clears the link in that direction. The service
// must exist in the catalog (services table); the FK enforces that.
func (s *Store) SetServiceSchemas(ctx context.Context, orgID uuid.UUID, service string, in, out *uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := writeOne(ctx, tx, orgID, service, DirectionIn, in); err != nil {
		return err
	}
	if err := writeOne(ctx, tx, orgID, service, DirectionOut, out); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func writeOne(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, service string, dir Direction, schemaID *uuid.UUID) error {
	if schemaID == nil || *schemaID == uuid.Nil {
		_, err := tx.Exec(ctx,
			`DELETE FROM service_schemas
			   WHERE organization_id = $1 AND service_name = $2 AND direction = $3`,
			orgID, service, dir)
		if err != nil {
			return fmt.Errorf("clear %s: %w", dir, err)
		}
		return nil
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO service_schemas (organization_id, service_name, direction, schema_id)
		   VALUES ($1, $2, $3, $4)
		   ON CONFLICT (organization_id, service_name, direction)
		   DO UPDATE SET schema_id = EXCLUDED.schema_id, updated_at = now()`,
		orgID, service, dir, *schemaID)
	if err != nil {
		return fmt.Errorf("set %s: %w", dir, err)
	}
	return nil
}
