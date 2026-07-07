// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package maintenance holds the two halves of the operator-communication
// feature: announcements (persistent user-facing banners with per-user,
// server-side dismissal) and maintenance windows (bounded alert-delivery
// suppression). Design: docs/maintenance-and-announcements-design.md.
package maintenance

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

// MaxWindowDuration bounds a maintenance window. Forgotten silences are
// the classic self-inflicted outage; extending is a fresh audited write.
const MaxWindowDuration = 7 * 24 * time.Hour

var (
	ErrNotFound       = errors.New("maintenance: not found")
	ErrNotDismissible = errors.New("maintenance: announcement is not dismissible")
)

// Announcement is one persistent banner. OrgID nil = cell-wide
// (operator-created), shown to every org's users.
type Announcement struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       *uuid.UUID `json:"org_id,omitempty"`
	Message     string     `json:"message"`
	Severity    string     `json:"severity"` // info | warning | critical
	StartsAt    time.Time  `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	Dismissible bool       `json:"dismissible"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// WindowScope says which alert rules a window silences.
//
//   - all_org:  everything in the org.
//   - entities: explicit integration/system/service lists. System scope is
//     snapshotted to member service names at write time (ServiceNamesExpanded)
//     — windows live ≤7 days, and a snapshot fails toward less silence, the
//     same posture as scoped manage.
//   - group:    alert rules owned by the team (alert_rules.group_id).
type WindowScope struct {
	Kind           string      `json:"kind"` // all_org | entities | group
	IntegrationIDs []uuid.UUID `json:"integration_ids,omitempty"`
	SystemIDs      []uuid.UUID `json:"system_ids,omitempty"`
	ServiceNames   []string    `json:"service_names,omitempty"`
	// ServiceNamesExpanded is the write-time snapshot of the SystemIDs'
	// member services (kept separate so the UI can keep showing the
	// chosen systems).
	ServiceNamesExpanded []string   `json:"service_names_expanded,omitempty"`
	GroupID              *uuid.UUID `json:"group_id,omitempty"`
}

// Window is one maintenance window. Active is computed at read time.
type Window struct {
	ID             uuid.UUID   `json:"id"`
	OrgID          uuid.UUID   `json:"org_id"`
	Name           string      `json:"name"`
	Reason         string      `json:"reason,omitempty"`
	StartsAt       time.Time   `json:"starts_at"`
	EndsAt         time.Time   `json:"ends_at"`
	Scope          WindowScope `json:"scope"`
	AnnouncementID *uuid.UUID  `json:"announcement_id,omitempty"`
	CreatedBy      *uuid.UUID  `json:"created_by,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
	Active         bool        `json:"active"`
}

// Store is the Postgres CRUD layer for both tables.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ── announcements ────────────────────────────────────────────────────

const annCols = `id, org_id, message, severity, starts_at, ends_at, dismissible, created_by, created_at`

func scanAnnouncement(row pgx.Row) (Announcement, error) {
	var a Announcement
	err := row.Scan(&a.ID, &a.OrgID, &a.Message, &a.Severity, &a.StartsAt,
		&a.EndsAt, &a.Dismissible, &a.CreatedBy, &a.CreatedAt)
	return a, err
}

// ActiveForUser returns the announcements the given user should see right
// now: the org's + cell-wide ones, in their active period, minus the ones
// this user dismissed. Deliberately NOT filtered by group visibility —
// announcements are organizational communication, broadcast is the point.
func (s *Store) ActiveForUser(ctx context.Context, orgID, userID uuid.UUID) ([]Announcement, error) {
	q := `
		SELECT ` + annCols + `
		FROM announcements a
		WHERE (a.org_id = $1 OR a.org_id IS NULL)
		  AND a.starts_at <= now()
		  AND (a.ends_at IS NULL OR a.ends_at > now())
		  AND NOT EXISTS (
		      SELECT 1 FROM announcement_dismissals d
		      WHERE d.announcement_id = a.id AND d.user_id = $2)
		ORDER BY a.severity = 'critical' DESC, a.created_at DESC`
	rows, err := s.pool.Query(ctx, q, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("maintenance: active announcements: %w", err)
	}
	defer rows.Close()
	out := []Announcement{}
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Dismiss records the user's dismissal. Non-dismissible announcements
// refuse (they're forced sticky for critical maintenance).
func (s *Store) Dismiss(ctx context.Context, id, userID uuid.UUID) error {
	var dismissible bool
	err := s.pool.QueryRow(ctx,
		`SELECT dismissible FROM announcements WHERE id = $1`, id).Scan(&dismissible)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("maintenance: dismiss lookup: %w", err)
	}
	if !dismissible {
		return ErrNotDismissible
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO announcement_dismissals (announcement_id, user_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, userID)
	if err != nil {
		return fmt.Errorf("maintenance: dismiss: %w", err)
	}
	return nil
}

// List returns the management view: the org's announcements when orgID is
// set, the cell-wide ones when nil (operator surface).
func (s *Store) List(ctx context.Context, orgID *uuid.UUID) ([]Announcement, error) {
	where := `a.org_id IS NULL`
	args := []any{}
	if orgID != nil {
		where = `a.org_id = $1`
		args = append(args, *orgID)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+annCols+` FROM announcements a WHERE `+where+` ORDER BY a.created_at DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("maintenance: list announcements: %w", err)
	}
	defer rows.Close()
	out := []Announcement{}
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) CreateAnnouncement(ctx context.Context, a Announcement) (Announcement, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO announcements (org_id, message, severity, starts_at, ends_at, dismissible, created_by)
		VALUES ($1, $2, $3, COALESCE($4, now()), $5, $6, $7)
		RETURNING `+annCols,
		a.OrgID, a.Message, a.Severity, nilIfZero(a.StartsAt), a.EndsAt, a.Dismissible, a.CreatedBy)
	created, err := scanAnnouncement(row)
	if err != nil {
		return Announcement{}, fmt.Errorf("maintenance: create announcement: %w", err)
	}
	return created, nil
}

// DeleteAnnouncement removes one row, scoped so an org admin can only
// delete their org's announcements and the operator only cell-wide ones.
func (s *Store) DeleteAnnouncement(ctx context.Context, id uuid.UUID, orgID *uuid.UUID) error {
	where := `id = $1 AND org_id IS NULL`
	args := []any{id}
	if orgID != nil {
		where = `id = $1 AND org_id = $2`
		args = append(args, *orgID)
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM announcements WHERE `+where, args...)
	if err != nil {
		return fmt.Errorf("maintenance: delete announcement: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ── maintenance windows ──────────────────────────────────────────────

const winCols = `id, org_id, name, reason, starts_at, ends_at, scope, announcement_id, created_by, created_at`

func scanWindow(row pgx.Row) (Window, error) {
	var w Window
	var scopeJSON []byte
	err := row.Scan(&w.ID, &w.OrgID, &w.Name, &w.Reason, &w.StartsAt, &w.EndsAt,
		&scopeJSON, &w.AnnouncementID, &w.CreatedBy, &w.CreatedAt)
	if err != nil {
		return Window{}, err
	}
	if err := json.Unmarshal(scopeJSON, &w.Scope); err != nil {
		return Window{}, fmt.Errorf("maintenance: bad scope json on %s: %w", w.ID, err)
	}
	now := time.Now()
	w.Active = !now.Before(w.StartsAt) && now.Before(w.EndsAt)
	return w, nil
}

// ListWindows returns the org's windows, newest first. Ended windows are
// kept for 30 days of history, then fall off the list (rows stay for the
// suppressed_by linkage until manually cleaned).
func (s *Store) ListWindows(ctx context.Context, orgID uuid.UUID) ([]Window, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+winCols+` FROM maintenance_windows
		WHERE org_id = $1 AND ends_at > now() - interval '30 days'
		ORDER BY starts_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("maintenance: list windows: %w", err)
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

func (s *Store) GetWindow(ctx context.Context, orgID, id uuid.UUID) (Window, error) {
	w, err := scanWindow(s.pool.QueryRow(ctx,
		`SELECT `+winCols+` FROM maintenance_windows WHERE org_id = $1 AND id = $2`, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Window{}, ErrNotFound
	}
	return w, err
}

func (s *Store) CreateWindow(ctx context.Context, w Window) (Window, error) {
	scopeJSON, err := json.Marshal(w.Scope)
	if err != nil {
		return Window{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO maintenance_windows (org_id, name, reason, starts_at, ends_at, scope, created_by)
		VALUES ($1, $2, $3, COALESCE($4, now()), $5, $6, $7)
		RETURNING `+winCols,
		w.OrgID, w.Name, w.Reason, nilIfZero(w.StartsAt), w.EndsAt, scopeJSON, w.CreatedBy)
	created, err := scanWindow(row)
	if err != nil {
		return Window{}, fmt.Errorf("maintenance: create window: %w", err)
	}
	return created, nil
}

// UpdateWindow replaces the mutable fields (name, reason, bounds, scope).
func (s *Store) UpdateWindow(ctx context.Context, orgID uuid.UUID, w Window) (Window, error) {
	scopeJSON, err := json.Marshal(w.Scope)
	if err != nil {
		return Window{}, err
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE maintenance_windows
		SET name = $3, reason = $4, starts_at = $5, ends_at = $6, scope = $7
		WHERE org_id = $1 AND id = $2
		RETURNING `+winCols,
		orgID, w.ID, w.Name, w.Reason, w.StartsAt, w.EndsAt, scopeJSON)
	updated, err := scanWindow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Window{}, ErrNotFound
	}
	if err != nil {
		return Window{}, fmt.Errorf("maintenance: update window: %w", err)
	}
	return updated, nil
}

// EndWindow ends an active window now, or deletes it outright if it never
// started (a scheduled window being cancelled). Ended-early windows stay
// as rows so suppressed instances keep their linkage.
func (s *Store) EndWindow(ctx context.Context, orgID, id uuid.UUID) (Window, error) {
	w, err := s.GetWindow(ctx, orgID, id)
	if err != nil {
		return Window{}, err
	}
	if time.Now().Before(w.StartsAt) {
		_, err := s.pool.Exec(ctx,
			`DELETE FROM maintenance_windows WHERE org_id = $1 AND id = $2`, orgID, id)
		if err != nil {
			return Window{}, fmt.Errorf("maintenance: cancel window: %w", err)
		}
		w.Active = false
		return w, nil
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE maintenance_windows SET ends_at = now()
		WHERE org_id = $1 AND id = $2
		RETURNING `+winCols, orgID, id)
	ended, err := scanWindow(row)
	if err != nil {
		return Window{}, fmt.Errorf("maintenance: end window: %w", err)
	}
	return ended, nil
}

// SetWindowAnnouncement links (or unlinks) the auto-created announcement.
func (s *Store) SetWindowAnnouncement(ctx context.Context, orgID, id uuid.UUID, announcementID *uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE maintenance_windows SET announcement_id = $3
		WHERE org_id = $1 AND id = $2`, orgID, id, announcementID)
	return err
}
