// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package systemtypes stores the org-customisable system-types catalog. A
// system type (rabbitmq, kafka, otel-collector, …) owns its detection prefixes
// and its starter health checks. Built-in types stay code-defined and
// read-only; these rows are an org's CUSTOM types plus OVERRIDES of a built-in
// (a row whose key matches a built-in replaces it for that org). Checks are
// stored as JSON mirroring the built-in check spec; the shape is shared with
// the monitoringtemplates package (same stored-check concept + converters).
package systemtypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/monitoringtemplates"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a system-type lookup misses.
var ErrNotFound = errors.New("system type not found")

// Check is one starter health check — shared with the monitoringtemplates
// stored-check shape (signal + metric/log fields), so the API's existing
// converters apply to both.
type Check = monitoringtemplates.Check

// SystemType is one stored, org-owned system type (custom or built-in override).
type SystemType struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"-"`
	Key            string    `json:"key"`
	Label          string    `json:"label"`
	IsSystem       bool      `json:"is_system"`
	DetectPrefixes []string  `json:"detect_prefixes"`
	Checks         []Check   `json:"checks"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, org_id, key, label, is_system, detect_prefixes, checks, created_at, updated_at`

func scan(row pgx.Row) (SystemType, error) {
	var t SystemType
	var prefixesJSON, checksJSON []byte
	if err := row.Scan(&t.ID, &t.OrganizationID, &t.Key, &t.Label, &t.IsSystem, &prefixesJSON, &checksJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return SystemType{}, err
	}
	if len(prefixesJSON) > 0 {
		_ = json.Unmarshal(prefixesJSON, &t.DetectPrefixes)
	}
	if t.DetectPrefixes == nil {
		t.DetectPrefixes = []string{}
	}
	if len(checksJSON) > 0 {
		_ = json.Unmarshal(checksJSON, &t.Checks)
	}
	if t.Checks == nil {
		t.Checks = []Check{}
	}
	return t, nil
}

// List returns every custom/override system type in the org, by label.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]SystemType, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM system_types WHERE org_id = $1 ORDER BY label`, orgID)
	if err != nil {
		return nil, fmt.Errorf("systemtypes: list: %w", err)
	}
	defer rows.Close()
	out := make([]SystemType, 0)
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get returns one system type by id within the org.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (SystemType, bool, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+cols+` FROM system_types WHERE org_id = $1 AND id = $2`, orgID, id)
	t, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return SystemType{}, false, nil
	}
	if err != nil {
		return SystemType{}, false, err
	}
	return t, true, nil
}

func marshalJSONArrays(prefixes []string, checks []Check) (string, string, error) {
	if prefixes == nil {
		prefixes = []string{}
	}
	if checks == nil {
		checks = []Check{}
	}
	p, err := json.Marshal(prefixes)
	if err != nil {
		return "", "", fmt.Errorf("systemtypes: marshal prefixes: %w", err)
	}
	c, err := json.Marshal(checks)
	if err != nil {
		return "", "", fmt.Errorf("systemtypes: marshal checks: %w", err)
	}
	return string(p), string(c), nil
}

func (s *Store) Create(ctx context.Context, orgID uuid.UUID, key, label string, isSystem bool, prefixes []string, checks []Check) (SystemType, error) {
	p, c, err := marshalJSONArrays(prefixes, checks)
	if err != nil {
		return SystemType{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO system_types (org_id, key, label, is_system, detect_prefixes, checks)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb)
		RETURNING `+cols, orgID, key, label, isSystem, p, c)
	return scan(row)
}

func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, label string, isSystem bool, prefixes []string, checks []Check) (SystemType, bool, error) {
	p, c, err := marshalJSONArrays(prefixes, checks)
	if err != nil {
		return SystemType{}, false, err
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE system_types
		SET label = $3, is_system = $4, detect_prefixes = $5::jsonb, checks = $6::jsonb, updated_at = now()
		WHERE org_id = $1 AND id = $2
		RETURNING `+cols, orgID, id, label, isSystem, p, c)
	t, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return SystemType{}, false, nil
	}
	if err != nil {
		return SystemType{}, false, err
	}
	return t, true, nil
}

func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM system_types WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("systemtypes: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
