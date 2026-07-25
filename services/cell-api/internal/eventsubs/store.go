// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package eventsubs is outbound event subscriptions (issue #4 part 2):
// domain events (com.sluicio.<entity>.<verb>) matched against
// subscription filters and queued for delivery to webhook channels.
// Emission is fire-and-forget from the caller's perspective — an
// enqueue failure logs and drops (events are best-effort notifications;
// the audit log is the record). Delivery runs in a worker with the same
// claim/backoff shape as alert notification jobs.
package eventsubs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a subscription lookup misses.
var ErrNotFound = errors.New("eventsubs: not found")

// Subscription is one stored subscription.
type Subscription struct {
	ID           uuid.UUID  `json:"id"`
	OrgID        uuid.UUID  `json:"-"`
	GroupID      *uuid.UUID `json:"group_id,omitempty"` // nil = org-wide (admin-only)
	Name         string     `json:"name"`
	Enabled      bool       `json:"enabled"`
	EventFilters []string   `json:"event_filters"`
	ChannelID    uuid.UUID  `json:"channel_id"`
	CreatedBy    *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Job is one queued outbound event delivery.
type Job struct {
	ID             uuid.UUID
	SubscriptionID uuid.UUID
	EventID        string
	EventType      string
	Subject        string
	OccurredAt     time.Time
	Payload        map[string]any
	Attempts       int
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// MatchesFilter reports whether an event type matches one filter glob.
// Three forms: exact ("com.sluicio.integration.created"), trailing-*
// prefix ("com.sluicio.integration.*"), and bare "*" (everything —
// allowed but must be explicit).
func MatchesFilter(eventType, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "*" {
		return true
	}
	if strings.HasSuffix(filter, "*") {
		return strings.HasPrefix(eventType, strings.TrimSuffix(filter, "*"))
	}
	return eventType == filter
}

// MatchesAny reports whether the event type matches any of the filters.
func MatchesAny(eventType string, filters []string) bool {
	for _, f := range filters {
		if MatchesFilter(eventType, f) {
			return true
		}
	}
	return false
}

const cols = `id, org_id, group_id, name, enabled, event_filters, channel_id, created_by, created_at, updated_at`

func scan(row pgx.Row) (Subscription, error) {
	var s Subscription
	var filters []byte
	if err := row.Scan(&s.ID, &s.OrgID, &s.GroupID, &s.Name, &s.Enabled, &filters, &s.ChannelID, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return Subscription{}, err
	}
	_ = json.Unmarshal(filters, &s.EventFilters)
	if s.EventFilters == nil {
		s.EventFilters = []string{}
	}
	return s, nil
}

// List returns the org's subscriptions, newest first.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Subscription, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM event_subscriptions WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("eventsubs: list: %w", err)
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		sub, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// Get returns one subscription in the org.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Subscription, error) {
	sub, err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM event_subscriptions WHERE org_id = $1 AND id = $2`, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, ErrNotFound
	}
	if err != nil {
		return Subscription{}, fmt.Errorf("eventsubs: get: %w", err)
	}
	return sub, nil
}

// Create stores a subscription.
func (s *Store) Create(ctx context.Context, sub Subscription) (Subscription, error) {
	filters, _ := json.Marshal(sub.EventFilters)
	row := s.pool.QueryRow(ctx, `
		INSERT INTO event_subscriptions (org_id, group_id, name, enabled, event_filters, channel_id, created_by)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
		RETURNING `+cols,
		sub.OrgID, sub.GroupID, sub.Name, sub.Enabled, string(filters), sub.ChannelID, sub.CreatedBy)
	created, err := scan(row)
	if err != nil {
		return Subscription{}, fmt.Errorf("eventsubs: create: %w", err)
	}
	return created, nil
}

// Update rewrites the mutable fields (name, enabled, filters, channel).
// The scope (group_id) is immutable after create — like dashboards.
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, name string, enabled bool, filters []string, channelID uuid.UUID) (Subscription, error) {
	fj, _ := json.Marshal(filters)
	row := s.pool.QueryRow(ctx, `
		UPDATE event_subscriptions
		SET name = $3, enabled = $4, event_filters = $5::jsonb, channel_id = $6, updated_at = now()
		WHERE org_id = $1 AND id = $2
		RETURNING `+cols, orgID, id, name, enabled, string(fj), channelID)
	sub, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, ErrNotFound
	}
	if err != nil {
		return Subscription{}, fmt.Errorf("eventsubs: update: %w", err)
	}
	return sub, nil
}

// Delete removes one subscription in the org.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM event_subscriptions WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("eventsubs: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// EnabledForOrg returns the org's enabled subscriptions — the emit
// path's working set (filter matching happens in Go; the set is tiny).
func (s *Store) EnabledForOrg(ctx context.Context, orgID uuid.UUID) ([]Subscription, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM event_subscriptions WHERE org_id = $1 AND enabled`, orgID)
	if err != nil {
		return nil, fmt.Errorf("eventsubs: enabled: %w", err)
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		sub, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// ChannelRef returns a subscription's org + destination channel ids —
// what the delivery worker needs to load the channel config.
func (s *Store) ChannelRef(ctx context.Context, subscriptionID uuid.UUID) (orgID, channelID uuid.UUID, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT org_id, channel_id FROM event_subscriptions WHERE id = $1`,
		subscriptionID).Scan(&orgID, &channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrNotFound
	}
	return orgID, channelID, err
}

// ── the outbound queue ───────────────────────────────────────────────

// Enqueue queues one event delivery for a subscription.
func (s *Store) Enqueue(ctx context.Context, subscriptionID uuid.UUID, eventID, eventType, subject string, occurredAt time.Time, payload map[string]any) error {
	pj, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("eventsubs: marshal payload: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO event_jobs (subscription_id, event_id, event_type, subject, occurred_at, payload)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
		subscriptionID, eventID, eventType, subject, occurredAt, string(pj))
	if err != nil {
		return fmt.Errorf("eventsubs: enqueue: %w", err)
	}
	return nil
}

// ClaimDue claims up to limit pending jobs whose next_attempt_at has
// passed (FOR UPDATE SKIP LOCKED — same pattern as notification jobs).
func (s *Store) ClaimDue(ctx context.Context, limit int) ([]Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, subscription_id, event_id, event_type, subject, occurred_at, payload, attempts
		FROM event_jobs
		WHERE state = 'pending' AND next_attempt_at <= now()
		ORDER BY next_attempt_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("eventsubs: claim: %w", err)
	}
	var jobs []Job
	var ids []uuid.UUID
	for rows.Next() {
		var j Job
		var payload []byte
		if err := rows.Scan(&j.ID, &j.SubscriptionID, &j.EventID, &j.EventType, &j.Subject, &j.OccurredAt, &payload, &j.Attempts); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(payload, &j.Payload)
		jobs = append(jobs, j)
		ids = append(ids, j.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE event_jobs SET state = 'running' WHERE id = ANY($1)`, ids); err != nil {
			return nil, err
		}
	}
	return jobs, tx.Commit(ctx)
}

// MaxAttempts caps delivery retries per job (with backoff below).
const MaxAttempts = 5

// MarkResult finalizes one attempt: done on success; otherwise pending
// with exponential backoff until MaxAttempts, then failed.
func (s *Store) MarkResult(ctx context.Context, jobID uuid.UUID, attempt int, sendErr error) error {
	if sendErr == nil {
		_, err := s.pool.Exec(ctx, `UPDATE event_jobs SET state = 'done', attempts = $2, last_error = '' WHERE id = $1`, jobID, attempt)
		return err
	}
	msg := sendErr.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if attempt >= MaxAttempts {
		_, err := s.pool.Exec(ctx, `UPDATE event_jobs SET state = 'failed', attempts = $2, last_error = $3 WHERE id = $1`, jobID, attempt, msg)
		return err
	}
	backoff := time.Duration(1<<attempt) * 15 * time.Second // 30s, 1m, 2m, 4m
	_, err := s.pool.Exec(ctx, `
		UPDATE event_jobs SET state = 'pending', attempts = $2, last_error = $3, next_attempt_at = now() + $4
		WHERE id = $1`, jobID, attempt, msg, backoff)
	return err
}

// PruneFinished deletes done/failed jobs older than the retention
// window — events are notifications, not records; the queue must not
// grow forever. Called opportunistically from the worker.
func (s *Store) PruneFinished(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM event_jobs
		WHERE state IN ('done', 'failed') AND occurred_at < now() - $1`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("eventsubs: prune: %w", err)
	}
	return tag.RowsAffected(), nil
}
