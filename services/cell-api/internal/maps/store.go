// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package maps owns the data-transformation catalogue: XSLT, jq,
// JSONata, Liquid, Mustache, Handlebars, etc. A map describes how to
// turn one message shape into another and optionally pins both the
// input ("from") and output ("to") schemas it operates against, so the
// UI can surface the relationship directly rather than asking the user
// to remember.
//
// The model is deliberately thin: same identity rules as schemas
// (unique on org/name/version), no service link table (maps are
// transformations, not per-service attachments), and lazy hydration of
// the linked schemas only when callers actually need their bodies.
package maps

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

// SchemaRef is the lightweight handle we render alongside a map: just
// enough to identify and link to the underlying schema without paying
// for the whole content body. Populated by hydrating joins on List/Get.
type SchemaRef struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Version string    `json:"version"`
	Format  string    `json:"format"`
	Kind    string    `json:"kind"`
}

// Map is one row in the maps catalogue.
type Map struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id,omitempty"`
	Name           string    `json:"name"`
	Version        string    `json:"version"`
	Description    string    `json:"description"`
	// Format is the transformation language: "xslt", "jq", "jsonata",
	// "liquid", "mustache", "handlebars", "other". Free-text on the
	// wire — the UI curates the list.
	Format    string    `json:"format"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	// FromSchemaID / ToSchemaID are the optional links to the input
	// and output schemas this map operates against. Either may be nil.
	FromSchemaID *uuid.UUID `json:"from_schema_id,omitempty"`
	ToSchemaID   *uuid.UUID `json:"to_schema_id,omitempty"`
	// FromSchema / ToSchema are hydrated on list / get responses so
	// the UI can render names + chips without a second round-trip.
	// Nil when the corresponding *_id is nil.
	FromSchema *SchemaRef `json:"from_schema,omitempty"`
	ToSchema   *SchemaRef `json:"to_schema,omitempty"`
}

// Input is the write payload accepted by Create / Update. The *_id
// fields are passed as strings (rather than *uuid.UUID) so the JSON
// API can clear a link by sending an empty string.
type Input struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Description  string `json:"description"`
	Format       string `json:"format"`
	Content      string `json:"content"`
	FromSchemaID string `json:"from_schema_id"`
	ToSchemaID   string `json:"to_schema_id"`
}

// Errors surfaced to callers.
var (
	ErrNotFound     = errors.New("maps: not found")
	ErrNameExists   = errors.New("maps: name and version already in use")
	ErrValidation   = errors.New("maps: validation error")
	ErrBadSchemaRef = errors.New("maps: referenced schema does not exist")
)

// Store is the Postgres-backed CRUD layer.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// listSelect is the shared SELECT clause used by both List and Get. The
// LEFT JOINs on schemas hydrate from/to into SchemaRef structs in a
// single round-trip; rows with NULL ids come back with all-NULL ref
// columns, which we lift back to *SchemaRef = nil in scanRow.
const listSelect = `
	SELECT m.id, m.organization_id, m.name, m.version,
	       m.description, m.format, m.content,
	       m.created_at, m.updated_at,
	       m.from_schema_id, m.to_schema_id,
	       fs.id, fs.name, fs.version, fs.format, fs.kind,
	       ts.id, ts.name, ts.version, ts.format, ts.kind
	FROM maps m
	LEFT JOIN schemas fs ON fs.id = m.from_schema_id
	LEFT JOIN schemas ts ON ts.id = m.to_schema_id`

// List returns every map for the org, ordered by name then version.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Map, error) {
	rows, err := s.pool.Query(ctx,
		listSelect+`
		WHERE m.organization_id = $1
		ORDER BY lower(m.name) ASC, m.version ASC`,
		orgID)
	if err != nil {
		return nil, fmt.Errorf("list maps: %w", err)
	}
	defer rows.Close()
	out := make([]Map, 0)
	for rows.Next() {
		m, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Get returns one map by id.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Map, error) {
	row := s.pool.QueryRow(ctx,
		listSelect+` WHERE m.organization_id = $1 AND m.id = $2`,
		orgID, id)
	m, err := scanRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Map{}, ErrNotFound
		}
		return Map{}, err
	}
	return m, nil
}

// scanRow handles both List rows.Next() and Get QueryRow callers since
// the row interface is the same shape. The schema-ref columns come
// back as a tuple of NULLs when the LEFT JOIN didn't match — we lift
// the leading id NULL to *SchemaRef = nil and skip the rest.
type scanner interface {
	Scan(dest ...any) error
}

func scanRow(r scanner) (Map, error) {
	var m Map
	var fsID, tsID *uuid.UUID
	var fsName, fsVer, fsFmt, fsKind *string
	var tsName, tsVer, tsFmt, tsKind *string
	if err := r.Scan(
		&m.ID, &m.OrganizationID, &m.Name, &m.Version,
		&m.Description, &m.Format, &m.Content,
		&m.CreatedAt, &m.UpdatedAt,
		&m.FromSchemaID, &m.ToSchemaID,
		&fsID, &fsName, &fsVer, &fsFmt, &fsKind,
		&tsID, &tsName, &tsVer, &tsFmt, &tsKind,
	); err != nil {
		return Map{}, err
	}
	if fsID != nil {
		m.FromSchema = &SchemaRef{
			ID:      *fsID,
			Name:    deref(fsName),
			Version: deref(fsVer),
			Format:  deref(fsFmt),
			Kind:    deref(fsKind),
		}
	}
	if tsID != nil {
		m.ToSchema = &SchemaRef{
			ID:      *tsID,
			Name:    deref(tsName),
			Version: deref(tsVer),
			Format:  deref(tsFmt),
			Kind:    deref(tsKind),
		}
	}
	return m, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Create inserts a new map. Name+version must be unique within the org.
func (s *Store) Create(ctx context.Context, orgID uuid.UUID, in Input) (Map, error) {
	from, to, err := normalise(&in)
	if err != nil {
		return Map{}, err
	}
	const q = `
		INSERT INTO maps (organization_id, name, version, description, format, content,
		                  from_schema_id, to_schema_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, q,
		orgID, in.Name, in.Version, in.Description, in.Format, in.Content,
		from, to,
	).Scan(&id); err != nil {
		if isNameClash(err) {
			return Map{}, ErrNameExists
		}
		if isFKViolation(err) {
			return Map{}, ErrBadSchemaRef
		}
		return Map{}, fmt.Errorf("create map: %w", err)
	}
	// Re-read with hydration so the caller gets the populated
	// FromSchema / ToSchema refs.
	return s.Get(ctx, orgID, id)
}

// Update changes the editable fields. Name+version remain unique; if
// the new identity collides with another map, ErrNameExists is returned.
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, in Input) (Map, error) {
	from, to, err := normalise(&in)
	if err != nil {
		return Map{}, err
	}
	const q = `
		UPDATE maps
		SET name = $3, version = $4, description = $5, format = $6, content = $7,
		    from_schema_id = $8, to_schema_id = $9, updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING id`
	var got uuid.UUID
	if err := s.pool.QueryRow(ctx, q,
		orgID, id, in.Name, in.Version, in.Description, in.Format, in.Content,
		from, to,
	).Scan(&got); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Map{}, ErrNotFound
		}
		if isNameClash(err) {
			return Map{}, ErrNameExists
		}
		if isFKViolation(err) {
			return Map{}, ErrBadSchemaRef
		}
		return Map{}, fmt.Errorf("update map: %w", err)
	}
	return s.Get(ctx, orgID, got)
}

// Delete removes a map.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM maps WHERE organization_id = $1 AND id = $2`,
		orgID, id)
	if err != nil {
		return fmt.Errorf("delete map: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// normalise trims + validates the input. Returns the parsed *uuid.UUID
// values for the optional from/to schema links (nil when empty).
func normalise(in *Input) (*uuid.UUID, *uuid.UUID, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Version = strings.TrimSpace(in.Version)
	if in.Name == "" {
		return nil, nil, fmt.Errorf("%w: name is required", ErrValidation)
	}
	if in.Format == "" {
		in.Format = "xslt"
	}
	from, err := parseOptionalUUID(in.FromSchemaID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: from_schema_id: %v", ErrValidation, err)
	}
	to, err := parseOptionalUUID(in.ToSchemaID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: to_schema_id: %v", ErrValidation, err)
	}
	return from, to, nil
}

func parseOptionalUUID(s string) (*uuid.UUID, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil, nil
	}
	u, err := uuid.Parse(t)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// isNameClash returns true for the (org, name, version) uniqueness
// violation. Postgres includes the constraint name in the error.
func isNameClash(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "maps_organization_id_name_version_key")
}

// isFKViolation flags the case where from_schema_id / to_schema_id
// references a non-existent schema. Surfaces as ErrBadSchemaRef so the
// API can return 400 rather than a generic 500.
func isFKViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "maps_from_schema_id_fkey") ||
		strings.Contains(msg, "maps_to_schema_id_fkey")
}
