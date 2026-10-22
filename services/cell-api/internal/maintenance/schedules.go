// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Schedule is a recurring maintenance window (#9): the series. Its
// occurrences are ordinary Windows with ScheduleID set, created by
// Materialize, so everything that acts on a window acts on an occurrence.
type Schedule struct {
	ID         uuid.UUID   `json:"id"`
	OrgID      uuid.UUID   `json:"org_id"`
	Name       string      `json:"name"`
	Reason     string      `json:"reason,omitempty"`
	Scope      WindowScope `json:"scope"`
	Recurrence Recurrence  `json:"recurrence"`
	Announce   bool        `json:"announce"`
	// SkippedStarts are occurrences someone cancelled before they began.
	SkippedStarts []time.Time  `json:"skipped_starts"`
	CreatedBy     *uuid.UUID   `json:"created_by,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
	Next          []Occurrence `json:"next"`
}

// NextOccurrences is how many upcoming occurrences a listing shows.
const NextOccurrences = 3

const schedCols = `id, org_id, name, reason, scope, recurrence, announce, skipped_starts, created_by, created_at, updated_at`

func scanSchedule(row pgx.Row) (Schedule, error) {
	var (
		sc              Schedule
		scopeJSON, rJSN []byte
	)
	if err := row.Scan(&sc.ID, &sc.OrgID, &sc.Name, &sc.Reason, &scopeJSON, &rJSN, &sc.Announce,
		&sc.SkippedStarts, &sc.CreatedBy, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
		return Schedule{}, err
	}
	if err := json.Unmarshal(scopeJSON, &sc.Scope); err != nil {
		return Schedule{}, fmt.Errorf("maintenance: bad scope json on schedule %s: %w", sc.ID, err)
	}
	if err := json.Unmarshal(rJSN, &sc.Recurrence); err != nil {
		return Schedule{}, fmt.Errorf("maintenance: bad recurrence json on schedule %s: %w", sc.ID, err)
	}
	if sc.SkippedStarts == nil {
		sc.SkippedStarts = []time.Time{}
	}
	sc.Next = sc.Recurrence.Next(time.Now(), NextOccurrences, sc.SkippedStarts)
	if sc.Next == nil {
		sc.Next = []Occurrence{}
	}
	return sc, nil
}

func (s *Store) querySchedules(ctx context.Context, q string, args ...any) ([]Schedule, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("maintenance: list schedules: %w", err)
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ListSchedules returns the org's recurring windows, oldest first.
func (s *Store) ListSchedules(ctx context.Context, orgID uuid.UUID) ([]Schedule, error) {
	return s.querySchedules(ctx,
		`SELECT `+schedCols+` FROM maintenance_schedules WHERE org_id = $1 ORDER BY created_at`, orgID)
}

// AllSchedules returns every org's recurring windows, for Materialize.
func (s *Store) AllSchedules(ctx context.Context) ([]Schedule, error) {
	return s.querySchedules(ctx, `SELECT `+schedCols+` FROM maintenance_schedules ORDER BY created_at`)
}

func (s *Store) GetSchedule(ctx context.Context, orgID, id uuid.UUID) (Schedule, error) {
	sc, err := scanSchedule(s.pool.QueryRow(ctx,
		`SELECT `+schedCols+` FROM maintenance_schedules WHERE org_id = $1 AND id = $2`, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, ErrNotFound
	}
	return sc, err
}

func (s *Store) CreateSchedule(ctx context.Context, sc Schedule) (Schedule, error) {
	scopeJSON, err := json.Marshal(sc.Scope)
	if err != nil {
		return Schedule{}, err
	}
	rJSON, err := json.Marshal(sc.Recurrence)
	if err != nil {
		return Schedule{}, err
	}
	created, err := scanSchedule(s.pool.QueryRow(ctx, `
		INSERT INTO maintenance_schedules (org_id, name, reason, scope, recurrence, announce, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+schedCols,
		sc.OrgID, sc.Name, sc.Reason, scopeJSON, rJSON, sc.Announce, sc.CreatedBy))
	if err != nil {
		return Schedule{}, fmt.Errorf("maintenance: create schedule: %w", err)
	}
	return created, nil
}

// UpdateSchedule replaces the series definition. Skips are kept, and a
// skip only matches an occurrence with exactly that start, so editing
// the time of day lets a skipped day happen again at its new time.
func (s *Store) UpdateSchedule(ctx context.Context, orgID uuid.UUID, sc Schedule) (Schedule, error) {
	scopeJSON, err := json.Marshal(sc.Scope)
	if err != nil {
		return Schedule{}, err
	}
	rJSON, err := json.Marshal(sc.Recurrence)
	if err != nil {
		return Schedule{}, err
	}
	updated, err := scanSchedule(s.pool.QueryRow(ctx, `
		UPDATE maintenance_schedules
		SET name = $3, reason = $4, scope = $5, recurrence = $6, announce = $7, updated_at = now()
		WHERE org_id = $1 AND id = $2
		RETURNING `+schedCols,
		orgID, sc.ID, sc.Name, sc.Reason, scopeJSON, rJSON, sc.Announce))
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, ErrNotFound
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("maintenance: update schedule: %w", err)
	}
	return updated, nil
}

// DeleteSchedule removes the series. Past and in-progress occurrences
// keep their rows (schedule_id becomes NULL) and with them the record of
// what they suppressed; the caller ends or removes them first.
func (s *Store) DeleteSchedule(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM maintenance_schedules WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("maintenance: delete schedule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ScheduleWindows returns a series' occurrence rows that have not ended:
// the one in progress and any created ahead of time.
func (s *Store) ScheduleWindows(ctx context.Context, orgID, scheduleID uuid.UUID) ([]Window, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+winCols+` FROM maintenance_windows
		WHERE org_id = $1 AND schedule_id = $2 AND ends_at > now()
		ORDER BY starts_at`, orgID, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("maintenance: schedule windows: %w", err)
	}
	defer rows.Close()
	out := []Window{}
	for rows.Next() {
		w, err := scanWindow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// OccurrenceWindow returns the row for one occurrence, if it was created.
func (s *Store) OccurrenceWindow(ctx context.Context, scheduleID uuid.UUID, start time.Time) (Window, bool, error) {
	w, err := scanWindow(s.pool.QueryRow(ctx,
		`SELECT `+winCols+` FROM maintenance_windows WHERE schedule_id = $1 AND starts_at = $2`, scheduleID, start))
	if errors.Is(err, pgx.ErrNoRows) {
		return Window{}, false, nil
	}
	if err != nil {
		return Window{}, false, err
	}
	return w, true, nil
}

// DeleteWindowRow removes a window row outright, without the skip that
// EndWindow records: for replacing an occurrence that has not started
// after its series was edited or deleted.
func (s *Store) DeleteWindowRow(ctx context.Context, orgID, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM maintenance_windows WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("maintenance: delete window: %w", err)
	}
	return nil
}
