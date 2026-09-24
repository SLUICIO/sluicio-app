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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/monitoringtemplates"
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
	// DetectSpanAttrs recognises the type from SPAN ATTRIBUTE keys
	// instead of metric names, for a runtime that has no metrics of its
	// own to be recognised by (see migration 0097).
	DetectSpanAttrs []string `json:"detect_span_attrs"`
	Checks          []Check  `json:"checks"`
	// Runbook is the type-level default guidance — "Kafka consumer lag:
	// check consumer group health first". Rides along wherever the type
	// is reported so a responder gets it without a second lookup.
	Runbook   string    `json:"runbook,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, org_id, key, label, is_system, detect_prefixes, COALESCE(detect_span_attrs, '[]'::jsonb), checks, COALESCE(runbook, ''), created_at, updated_at`

func scan(row pgx.Row) (SystemType, error) {
	var t SystemType
	var prefixesJSON, spanAttrsJSON, checksJSON []byte
	if err := row.Scan(&t.ID, &t.OrganizationID, &t.Key, &t.Label, &t.IsSystem, &prefixesJSON, &spanAttrsJSON, &checksJSON, &t.Runbook, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return SystemType{}, err
	}
	if len(prefixesJSON) > 0 {
		_ = json.Unmarshal(prefixesJSON, &t.DetectPrefixes)
	}
	if t.DetectPrefixes == nil {
		t.DetectPrefixes = []string{}
	}
	if len(spanAttrsJSON) > 0 {
		_ = json.Unmarshal(spanAttrsJSON, &t.DetectSpanAttrs)
	}
	if t.DetectSpanAttrs == nil {
		t.DetectSpanAttrs = []string{}
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

// marshalStrings renders a string slice as a JSON array, with nil
// becoming [] rather than null so the NOT NULL column is satisfied.
func marshalStrings(v []string) (string, error) {
	if v == nil {
		v = []string{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("systemtypes: marshal strings: %w", err)
	}
	return string(b), nil
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

func (s *Store) Create(ctx context.Context, orgID uuid.UUID, key, label string, isSystem bool, prefixes, spanAttrs []string, checks []Check, runbook string) (SystemType, error) {
	p, c, err := marshalJSONArrays(prefixes, checks)
	if err != nil {
		return SystemType{}, err
	}
	a, err := marshalStrings(spanAttrs)
	if err != nil {
		return SystemType{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO system_types (org_id, key, label, is_system, detect_prefixes, detect_span_attrs, checks, runbook)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, $8)
		RETURNING `+cols, orgID, key, label, isSystem, p, a, c, runbook)
	return scan(row)
}

func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, label string, isSystem bool, prefixes, spanAttrs []string, checks []Check, runbook string) (SystemType, bool, error) {
	p, c, err := marshalJSONArrays(prefixes, checks)
	if err != nil {
		return SystemType{}, false, err
	}
	a, err := marshalStrings(spanAttrs)
	if err != nil {
		return SystemType{}, false, err
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE system_types
		SET label = $3, is_system = $4, detect_prefixes = $5::jsonb, detect_span_attrs = $6::jsonb,
		    checks = $7::jsonb, runbook = $8, updated_at = now()
		WHERE org_id = $1 AND id = $2
		RETURNING `+cols, orgID, id, label, isSystem, p, a, c, runbook)
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
