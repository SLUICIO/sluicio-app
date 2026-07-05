// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package erroracks stores per-service "clear errors" acknowledgements.
// When the maintenance team reviews a service's failures, they set a
// watermark (acknowledged_until) plus an optional comment; service
// health + error counts then ignore error traces at or before that
// watermark until newer failures arrive. One row per (org, service) —
// re-clearing just moves the watermark forward.
package erroracks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ack is a service's current error acknowledgement.
type Ack struct {
	ServiceName        string     `json:"service_name"`
	AcknowledgedUntil  time.Time  `json:"acknowledged_until"`
	Comment            string     `json:"comment,omitempty"`
	AcknowledgedBy     *uuid.UUID `json:"acknowledged_by,omitempty"`
	AcknowledgedByName string     `json:"acknowledged_by_name,omitempty"` // resolved at the handler
	AcknowledgedAt     time.Time  `json:"acknowledged_at"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Upsert sets (or moves forward) the watermark for a service.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, service string, until time.Time, comment string, by *uuid.UUID) (Ack, error) {
	const q = `
		INSERT INTO service_error_acks
		    (organization_id, service_name, acknowledged_until, comment, acknowledged_by, acknowledged_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (organization_id, service_name) DO UPDATE
		SET acknowledged_until = EXCLUDED.acknowledged_until,
		    comment            = EXCLUDED.comment,
		    acknowledged_by    = EXCLUDED.acknowledged_by,
		    acknowledged_at    = now()
		RETURNING service_name, acknowledged_until, COALESCE(comment, ''), acknowledged_by, acknowledged_at`
	return scanAck(s.pool.QueryRow(ctx, q, orgID, service, until, nilIfEmpty(comment), by))
}

// Get returns the current ack for a service, or (nil) if none.
func (s *Store) Get(ctx context.Context, orgID uuid.UUID, service string) (*Ack, error) {
	const q = `SELECT service_name, acknowledged_until, COALESCE(comment, ''), acknowledged_by, acknowledged_at
		FROM service_error_acks WHERE organization_id = $1 AND service_name = $2`
	a, err := scanAck(s.pool.QueryRow(ctx, q, orgID, service))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// GetAll returns every service's ack in the org, keyed by service name.
// Used by the services list + integration health rollups so they can
// apply the watermark in one Postgres round trip.
func (s *Store) GetAll(ctx context.Context, orgID uuid.UUID) (map[string]Ack, error) {
	const q = `SELECT service_name, acknowledged_until, COALESCE(comment, ''), acknowledged_by, acknowledged_at
		FROM service_error_acks WHERE organization_id = $1`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("erroracks: list: %w", err)
	}
	defer rows.Close()
	out := map[string]Ack{}
	for rows.Next() {
		a, err := scanAck(rows)
		if err != nil {
			return nil, err
		}
		out[a.ServiceName] = a
	}
	return out, rows.Err()
}

// Delete removes a service's ack (un-clears it).
func (s *Store) Delete(ctx context.Context, orgID uuid.UUID, service string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM service_error_acks WHERE organization_id = $1 AND service_name = $2`, orgID, service)
	if err != nil {
		return fmt.Errorf("erroracks: delete: %w", err)
	}
	return nil
}

func scanAck(row pgx.Row) (Ack, error) {
	var a Ack
	var by uuid.NullUUID
	if err := row.Scan(&a.ServiceName, &a.AcknowledgedUntil, &a.Comment, &by, &a.AcknowledgedAt); err != nil {
		return Ack{}, err
	}
	if by.Valid {
		id := by.UUID
		a.AcknowledgedBy = &id
	}
	return a, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
