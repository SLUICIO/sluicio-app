// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package monitoringtemplates stores user-defined monitoring templates: an
// org-owned, named bundle of health-check specs that can be applied to a
// service like the code-defined built-ins. Checks are stored as JSON and
// mirror the built-in check spec (signal + metric/log fields); the API layer
// converts them to alert rules at apply time. Strings (not the alerting
// enums) are used here so this package stays independent of the alerting
// package and the JSON shape is stable on disk.
package monitoringtemplates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a template lookup misses.
var ErrNotFound = errors.New("monitoring template not found")

// AttrFilter is one metric/log attribute predicate (key·op·value).
type AttrFilter struct {
	Key   string `json:"key"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// Check is one health check in a template. Signal "" / "metric" uses the
// metric fields; "log" uses the log fields. Mirrors the built-in systemCheck.
type Check struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Signal      string `json:"signal,omitempty"`
	// metric
	Metric    string       `json:"metric,omitempty"`
	Agg       string       `json:"agg,omitempty"`
	Op        string       `json:"op,omitempty"`
	Threshold float64      `json:"threshold,omitempty"`
	Attrs     []AttrFilter `json:"attrs,omitempty"`
	SplitBy   string       `json:"split_by,omitempty"`
	// log
	MinSeverity  int32  `json:"min_severity,omitempty"`
	BodyContains string `json:"body_contains,omitempty"`
	LogThreshold int    `json:"log_threshold,omitempty"`
	// trace (signal "trace_error" | "trace_latency" | "trace_volume")
	TraceThreshold int `json:"trace_threshold,omitempty"` // trace_error / trace_volume
	ThresholdMs    int `json:"threshold_ms,omitempty"`    // trace_latency (p95)
	WindowSeconds  int `json:"window_seconds,omitempty"`  // trace checks; default 300
	// shared
	Severity string `json:"severity,omitempty"`
	Unit     string `json:"unit,omitempty"`
	Display  bool   `json:"display,omitempty"`
}

// Template is one stored custom template.
type Template struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"-"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Source         string    `json:"source"`
	Checks         []Check   `json:"checks"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, org_id, name, description, source, checks, created_at, updated_at`

func scan(row pgx.Row) (Template, error) {
	var t Template
	var checksJSON []byte
	if err := row.Scan(&t.ID, &t.OrganizationID, &t.Name, &t.Description, &t.Source, &checksJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return Template{}, err
	}
	if len(checksJSON) > 0 {
		_ = json.Unmarshal(checksJSON, &t.Checks)
	}
	if t.Checks == nil {
		t.Checks = []Check{}
	}
	return t, nil
}

// List returns every custom template in the org, newest first.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Template, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM monitoring_templates WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("monitoringtemplates: list: %w", err)
	}
	defer rows.Close()
	out := make([]Template, 0)
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get returns one template by id within the org.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Template, bool, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+cols+` FROM monitoring_templates WHERE org_id = $1 AND id = $2`, orgID, id)
	t, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, false, nil
	}
	if err != nil {
		return Template{}, false, err
	}
	return t, true, nil
}

func (s *Store) Create(ctx context.Context, orgID uuid.UUID, name, description, source string, checks []Check) (Template, error) {
	if checks == nil {
		checks = []Check{}
	}
	checksJSON, err := json.Marshal(checks)
	if err != nil {
		return Template{}, fmt.Errorf("monitoringtemplates: marshal checks: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO monitoring_templates (org_id, name, description, source, checks)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		RETURNING `+cols, orgID, name, description, source, string(checksJSON))
	return scan(row)
}

func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, name, description string, checks []Check) (Template, bool, error) {
	if checks == nil {
		checks = []Check{}
	}
	checksJSON, err := json.Marshal(checks)
	if err != nil {
		return Template{}, false, fmt.Errorf("monitoringtemplates: marshal checks: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE monitoring_templates
		SET name = $3, description = $4, checks = $5::jsonb, updated_at = now()
		WHERE org_id = $1 AND id = $2
		RETURNING `+cols, orgID, id, name, description, string(checksJSON))
	t, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, false, nil
	}
	if err != nil {
		return Template{}, false, err
	}
	return t, true, nil
}

func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM monitoring_templates WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("monitoringtemplates: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
