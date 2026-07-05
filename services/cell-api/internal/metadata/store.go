// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package metadata stores user-defined metadata fields that decorate
// integrations and/or services. A Field is defined once for the org
// (key, label, type, applies-to scopes); Values are then attached to
// integrations or services keyed by field id.
//
// Values are stored as TEXT regardless of declared type; the API
// validates each value against its field's type on save and the
// frontend renders the right input variant.
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Field types supported by the metadata system.
const (
	TypeText    = "text"
	TypeBoolean = "boolean"
	TypeNumber  = "number"
	TypeSelect  = "select"
)

// Field is a user-defined metadata field definition.
type Field struct {
	ID                   uuid.UUID `json:"id"`
	Key                  string    `json:"key"`
	Label                string    `json:"label"`
	Type                 string    `json:"type"`
	Options              []string  `json:"options,omitempty"`
	Description          string    `json:"description"`
	AppliesToIntegration bool      `json:"applies_to_integration"`
	AppliesToService     bool      `json:"applies_to_service"`
	AppliesToSystem      bool      `json:"applies_to_system"`
	// SystemTypeKey narrows a system-scoped field to one system type
	// ("" = all systems). Ignored unless AppliesToSystem.
	SystemTypeKey string    `json:"system_type_key"`
	Required      bool      `json:"required"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// FieldInput is the create / update payload.
type FieldInput struct {
	Key                  string   `json:"key"`
	Label                string   `json:"label"`
	Type                 string   `json:"type"`
	Options              []string `json:"options,omitempty"`
	Description          string   `json:"description"`
	AppliesToIntegration bool     `json:"applies_to_integration"`
	AppliesToService     bool     `json:"applies_to_service"`
	AppliesToSystem      bool     `json:"applies_to_system"`
	SystemTypeKey        string   `json:"system_type_key"`
	Required             bool     `json:"required"`
}

// Errors surfaced to callers.
var (
	ErrNotFound       = errors.New("metadata: field not found")
	ErrFieldExists    = errors.New("metadata: field key already exists")
	ErrValidation     = errors.New("metadata: validation error")
	ErrUnknownType    = errors.New("metadata: unknown type")
	ErrNoScope        = errors.New("metadata: field must apply to integration and/or service")
	ErrInvalidValue   = errors.New("metadata: value does not satisfy the field's type")
	ErrFieldRequired  = errors.New("metadata: required field is missing a value")
	ErrFieldWrongType = errors.New("metadata: field type does not apply to this scope")
)

// Store is the Postgres-backed CRUD layer.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ── Fields ──────────────────────────────────────────────────────────────

// ListFields returns every field defined for the org, ordered by label.
func (s *Store) ListFields(ctx context.Context, orgID uuid.UUID) ([]Field, error) {
	const q = `
		SELECT id, key, label, type, options, description,
		       applies_to_integration, applies_to_service, applies_to_system, system_type_key, required,
		       created_at, updated_at
		FROM metadata_fields
		WHERE organization_id = $1
		ORDER BY lower(label) ASC`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("list fields: %w", err)
	}
	defer rows.Close()

	out := make([]Field, 0)
	for rows.Next() {
		f, err := scanField(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetField returns one field by id.
func (s *Store) GetField(ctx context.Context, orgID, id uuid.UUID) (Field, error) {
	const q = `
		SELECT id, key, label, type, options, description,
		       applies_to_integration, applies_to_service, applies_to_system, system_type_key, required,
		       created_at, updated_at
		FROM metadata_fields WHERE organization_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, q, orgID, id)
	f, err := scanField(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Field{}, ErrNotFound
	}
	return f, err
}

// CreateField validates and inserts a new field.
func (s *Store) CreateField(ctx context.Context, orgID uuid.UUID, in FieldInput) (Field, error) {
	if err := validateFieldInput(in); err != nil {
		return Field{}, err
	}
	optsJSON, err := encodeOptions(in.Options)
	if err != nil {
		return Field{}, err
	}
	const q = `
		INSERT INTO metadata_fields
		    (organization_id, key, label, type, options, description,
		     applies_to_integration, applies_to_service, applies_to_system, system_type_key, required)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, key, label, type, options, description,
		          applies_to_integration, applies_to_service, applies_to_system, system_type_key, required,
		          created_at, updated_at`
	row := s.pool.QueryRow(ctx, q,
		orgID, in.Key, in.Label, in.Type, optsJSON, in.Description,
		in.AppliesToIntegration, in.AppliesToService, in.AppliesToSystem, in.SystemTypeKey, in.Required)
	f, err := scanField(row)
	if err != nil && strings.Contains(err.Error(), "metadata_fields_organization_id_key_key") {
		return Field{}, ErrFieldExists
	}
	return f, err
}

// UpdateField updates the editable parts of a field. The key + type are
// immutable once created — changing them would silently invalidate any
// stored values keyed by id. Re-create the field if you need that.
func (s *Store) UpdateField(ctx context.Context, orgID, id uuid.UUID, in FieldInput) (Field, error) {
	existing, err := s.GetField(ctx, orgID, id)
	if err != nil {
		return Field{}, err
	}
	// Preserve key + type from the existing row.
	in.Key = existing.Key
	in.Type = existing.Type
	if err := validateFieldInput(in); err != nil {
		return Field{}, err
	}
	optsJSON, err := encodeOptions(in.Options)
	if err != nil {
		return Field{}, err
	}
	const q = `
		UPDATE metadata_fields
		SET label = $3, options = $4, description = $5,
		    applies_to_integration = $6, applies_to_service = $7,
		    applies_to_system = $8, system_type_key = $9,
		    required = $10, updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING id, key, label, type, options, description,
		          applies_to_integration, applies_to_service, applies_to_system, system_type_key, required,
		          created_at, updated_at`
	row := s.pool.QueryRow(ctx, q,
		orgID, id, in.Label, optsJSON, in.Description,
		in.AppliesToIntegration, in.AppliesToService, in.AppliesToSystem, in.SystemTypeKey, in.Required)
	return scanField(row)
}

// DeleteField removes a field. Values cascade.
func (s *Store) DeleteField(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM metadata_fields WHERE organization_id = $1 AND id = $2`,
		orgID, id)
	if err != nil {
		return fmt.Errorf("delete field: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Values ──────────────────────────────────────────────────────────────

// IntegrationValues returns the field-id → value map for one integration.
func (s *Store) IntegrationValues(ctx context.Context, integrationID uuid.UUID) (map[uuid.UUID]string, error) {
	const q = `SELECT field_id, value FROM integration_metadata WHERE integration_id = $1`
	rows, err := s.pool.Query(ctx, q, integrationID)
	if err != nil {
		return nil, fmt.Errorf("integration values: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]string)
	for rows.Next() {
		var fid uuid.UUID
		var v string
		if err := rows.Scan(&fid, &v); err != nil {
			return nil, err
		}
		out[fid] = v
	}
	return out, rows.Err()
}

// IntegrationValuesBulk returns the value map for every integration in
// one go: integration_id -> field_id -> value. Used by list endpoints
// to avoid an N+1 round-trip per row. Integrations with no saved values
// simply aren't keys in the result.
func (s *Store) IntegrationValuesBulk(ctx context.Context, integrationIDs []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]map[uuid.UUID]string)
	if len(integrationIDs) == 0 {
		return out, nil
	}
	const q = `SELECT integration_id, field_id, value FROM integration_metadata WHERE integration_id = ANY($1)`
	rows, err := s.pool.Query(ctx, q, integrationIDs)
	if err != nil {
		return nil, fmt.Errorf("integration values bulk: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var iid, fid uuid.UUID
		var v string
		if err := rows.Scan(&iid, &fid, &v); err != nil {
			return nil, err
		}
		m, ok := out[iid]
		if !ok {
			m = make(map[uuid.UUID]string)
			out[iid] = m
		}
		m[fid] = v
	}
	return out, rows.Err()
}

// SetIntegrationValues replaces the values for one integration in a single
// transaction. Keys with empty strings are deleted (so unsetting a field
// works the same way as never setting it).
func (s *Store) SetIntegrationValues(ctx context.Context, orgID, integrationID uuid.UUID, values map[string]string) error {
	fields, err := s.ListFields(ctx, orgID)
	if err != nil {
		return err
	}
	resolved, err := resolveAndValidate(fields, values, scopeIntegration, "")
	if err != nil {
		return err
	}
	return s.replaceValues(ctx, "integration_metadata",
		[]string{"integration_id"}, []any{integrationID}, resolved)
}

// ServiceValues returns the field-id → value map for one service.
func (s *Store) ServiceValues(ctx context.Context, orgID uuid.UUID, service string) (map[uuid.UUID]string, error) {
	const q = `SELECT field_id, value FROM service_metadata_extras WHERE organization_id = $1 AND service_name = $2`
	rows, err := s.pool.Query(ctx, q, orgID, service)
	if err != nil {
		return nil, fmt.Errorf("service values: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]string)
	for rows.Next() {
		var fid uuid.UUID
		var v string
		if err := rows.Scan(&fid, &v); err != nil {
			return nil, err
		}
		out[fid] = v
	}
	return out, rows.Err()
}

// ServiceValuesBulk returns service_name → (field-id → value) for many
// services in one query — the services-list analogue of
// IntegrationValuesBulk, so the list can show/filter on metadata without an
// N+1 of ServiceValues.
func (s *Store) ServiceValuesBulk(ctx context.Context, orgID uuid.UUID, services []string) (map[string]map[uuid.UUID]string, error) {
	out := make(map[string]map[uuid.UUID]string)
	if len(services) == 0 {
		return out, nil
	}
	const q = `SELECT service_name, field_id, value FROM service_metadata_extras WHERE organization_id = $1 AND service_name = ANY($2)`
	rows, err := s.pool.Query(ctx, q, orgID, services)
	if err != nil {
		return nil, fmt.Errorf("service values bulk: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var fid uuid.UUID
		var v string
		if err := rows.Scan(&name, &fid, &v); err != nil {
			return nil, err
		}
		m, ok := out[name]
		if !ok {
			m = make(map[uuid.UUID]string)
			out[name] = m
		}
		m[fid] = v
	}
	return out, rows.Err()
}

// SetServiceValues replaces a service's metadata in one transaction.
func (s *Store) SetServiceValues(ctx context.Context, orgID uuid.UUID, service string, values map[string]string) error {
	fields, err := s.ListFields(ctx, orgID)
	if err != nil {
		return err
	}
	resolved, err := resolveAndValidate(fields, values, scopeService, "")
	if err != nil {
		return err
	}
	return s.replaceValues(ctx, "service_metadata_extras",
		[]string{"organization_id", "service_name"}, []any{orgID, service}, resolved)
}

// SystemValues returns the field-id → value map for one system.
func (s *Store) SystemValues(ctx context.Context, systemID uuid.UUID) (map[uuid.UUID]string, error) {
	const q = `SELECT field_id, value FROM system_metadata WHERE system_id = $1`
	rows, err := s.pool.Query(ctx, q, systemID)
	if err != nil {
		return nil, fmt.Errorf("system values: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]string)
	for rows.Next() {
		var fid uuid.UUID
		var v string
		if err := rows.Scan(&fid, &v); err != nil {
			return nil, err
		}
		out[fid] = v
	}
	return out, rows.Err()
}

// SetSystemValues replaces a system's metadata in one transaction. Only fields
// that apply to the system (applies_to_system + matching type, "" = all) are
// accepted.
func (s *Store) SetSystemValues(ctx context.Context, orgID, systemID uuid.UUID, systemTypeKey string, values map[string]string) error {
	fields, err := s.ListFields(ctx, orgID)
	if err != nil {
		return err
	}
	resolved, err := resolveAndValidate(fields, values, scopeSystem, systemTypeKey)
	if err != nil {
		return err
	}
	return s.replaceValues(ctx, "system_metadata",
		[]string{"system_id"}, []any{systemID}, resolved)
}

// SystemFields returns the fields that apply to a system of the given type, in
// label order — the field set to render on a system's metadata tab.
func (s *Store) SystemFields(ctx context.Context, orgID uuid.UUID, systemTypeKey string) ([]Field, error) {
	all, err := s.ListFields(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]Field, 0)
	for _, f := range all {
		if systemFieldApplies(f, systemTypeKey) {
			out = append(out, f)
		}
	}
	return out, nil
}

// ── Internals ───────────────────────────────────────────────────────────

type scope int

const (
	scopeIntegration scope = iota
	scopeService
	scopeSystem
)

// systemFieldApplies reports whether a system-scoped field applies to a system
// of the given type ("" type_key = applies to all systems).
func systemFieldApplies(f Field, systemTypeKey string) bool {
	return f.AppliesToSystem && (f.SystemTypeKey == "" || f.SystemTypeKey == systemTypeKey)
}

// resolveAndValidate maps key→value to field-id→value, validating each
// value against its field type and rejecting unknown keys / wrong-scope
// fields / required-but-empty fields.
func resolveAndValidate(fields []Field, values map[string]string, sc scope, systemTypeKey string) (map[uuid.UUID]string, error) {
	applies := func(f Field) bool {
		switch sc {
		case scopeIntegration:
			return f.AppliesToIntegration
		case scopeService:
			return f.AppliesToService
		case scopeSystem:
			return systemFieldApplies(f, systemTypeKey)
		}
		return false
	}
	byKey := make(map[string]Field, len(fields))
	for _, f := range fields {
		byKey[f.Key] = f
	}
	out := make(map[uuid.UUID]string)
	for k, v := range values {
		f, ok := byKey[k]
		if !ok {
			return nil, fmt.Errorf("%w: unknown field %q", ErrValidation, k)
		}
		if !applies(f) {
			return nil, fmt.Errorf("%w: field %q", ErrFieldWrongType, k)
		}
		v = strings.TrimSpace(v)
		if v == "" {
			// empty == unset; skip insertion. Required check below covers it.
			continue
		}
		if err := validateValue(f, v); err != nil {
			return nil, fmt.Errorf("%w: field %q: %v", ErrInvalidValue, k, err)
		}
		out[f.ID] = v
	}
	// Required-field check: in this scope, every required field must
	// have a value either pre-existing or in the incoming payload.
	for _, f := range fields {
		if !f.Required || !applies(f) {
			continue
		}
		if _, ok := out[f.ID]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrFieldRequired, f.Key)
		}
	}
	return out, nil
}

// replaceValues writes the resolved field-id → value map for the given
// owner (integration or service) in one transaction: delete then insert.
func (s *Store) replaceValues(ctx context.Context, table string, ownerCols []string, ownerVals []any, values map[uuid.UUID]string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// DELETE … WHERE owner_cols match
	whereParts := make([]string, len(ownerCols))
	for i, c := range ownerCols {
		whereParts[i] = fmt.Sprintf("%s = $%d", c, i+1)
	}
	delSQL := fmt.Sprintf("DELETE FROM %s WHERE %s", table, strings.Join(whereParts, " AND "))
	if _, err := tx.Exec(ctx, delSQL, ownerVals...); err != nil {
		return fmt.Errorf("delete old values: %w", err)
	}

	// INSERT one row per remaining value.
	cols := append([]string{}, ownerCols...)
	cols = append(cols, "field_id", "value")
	for fid, v := range values {
		placeholders := make([]string, len(cols))
		for i := range cols {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
		args := append([]any{}, ownerVals...)
		args = append(args, fid, v)
		insSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
		if _, err := tx.Exec(ctx, insSQL, args...); err != nil {
			return fmt.Errorf("insert value: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func validateFieldInput(in FieldInput) error {
	in.Key = strings.TrimSpace(in.Key)
	in.Label = strings.TrimSpace(in.Label)
	if in.Key == "" {
		return fmt.Errorf("%w: key is required", ErrValidation)
	}
	if in.Label == "" {
		return fmt.Errorf("%w: label is required", ErrValidation)
	}
	switch in.Type {
	case TypeText, TypeBoolean, TypeNumber, TypeSelect:
	default:
		return fmt.Errorf("%w: %s", ErrUnknownType, in.Type)
	}
	if in.Type == TypeSelect && len(in.Options) == 0 {
		return fmt.Errorf("%w: select fields require at least one option", ErrValidation)
	}
	if !in.AppliesToIntegration && !in.AppliesToService && !in.AppliesToSystem {
		return ErrNoScope
	}
	return nil
}

func validateValue(f Field, v string) error {
	switch f.Type {
	case TypeText:
		return nil
	case TypeBoolean:
		if v != "true" && v != "false" {
			return fmt.Errorf("expected \"true\" or \"false\"")
		}
		return nil
	case TypeNumber:
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return err
		}
		return nil
	case TypeSelect:
		for _, o := range f.Options {
			if o == v {
				return nil
			}
		}
		return fmt.Errorf("value not in allowed options")
	}
	return ErrUnknownType
}

func encodeOptions(opts []string) ([]byte, error) {
	if len(opts) == 0 {
		return nil, nil
	}
	return json.Marshal(opts)
}

// scanField scans one row in the order the SELECT lists.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanField(r rowScanner) (Field, error) {
	var f Field
	var optionsRaw []byte
	if err := r.Scan(
		&f.ID, &f.Key, &f.Label, &f.Type, &optionsRaw, &f.Description,
		&f.AppliesToIntegration, &f.AppliesToService, &f.AppliesToSystem, &f.SystemTypeKey, &f.Required,
		&f.CreatedAt, &f.UpdatedAt,
	); err != nil {
		return Field{}, err
	}
	if len(optionsRaw) > 0 {
		if err := json.Unmarshal(optionsRaw, &f.Options); err != nil {
			return Field{}, fmt.Errorf("decode options: %w", err)
		}
	}
	return f, nil
}
