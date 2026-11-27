// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ending a temporary change. The collector is sent its base: a permanent
// change held while the temporary one ran, else the last permanent
// configuration it applied, else an empty remote configuration, which a
// supervisor (0.166.0, checked) answers by running only its local files.
// The base is read when the change ends, not when it started, so a
// permanent change made meanwhile is what the collector returns to.

// Reasons a temporary change ended.
const (
	EndedExpired = "expired"
	EndedEarly   = "ended"
)

// EndTemporary ends the temporary deployment id of a collector and queues
// the base to send. It returns the deployment that restores the base.
// ErrNotFound when id is not an active temporary deployment.
func (s *Store) EndTemporary(ctx context.Context, collectorID, id uuid.UUID, reason string) (Deployment, error) {
	if s.keys == nil {
		return Deployment{}, ErrNoKey
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE collector_deployments SET status = 'ended', ended_at = now(), ended_reason = $3,
		       finished_at = COALESCE(finished_at, now())
		WHERE id = $2 AND collector_id = $1 AND duration_seconds IS NOT NULL
		  AND status IN ('pending', 'sent', 'applying', 'applied')`, collectorID, id, reason)
	if err != nil {
		return Deployment{}, err
	}
	if tag.RowsAffected() == 0 {
		return Deployment{}, ErrNotFound
	}
	base, err := s.restoreBase(ctx, tx, collectorID, id)
	if err != nil {
		return Deployment{}, err
	}
	return base, tx.Commit(ctx)
}

// restoreBase queues the collector's base after a temporary change
// (ended) and returns it.
func (s *Store) restoreBase(ctx context.Context, tx pgx.Tx, collectorID, ended uuid.UUID) (Deployment, error) {
	// A permanent change held behind the temporary one is the base.
	held, err := scanDeployment(tx.QueryRow(ctx, `
		UPDATE collector_deployments SET status = 'pending'
		WHERE id = (SELECT id FROM collector_deployments WHERE collector_id = $1 AND status = 'held'
		            ORDER BY created_at DESC LIMIT 1)
		RETURNING `+deploymentCols, collectorID))
	if err == nil {
		return held, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, fmt.Errorf("collectors: release held: %w", err)
	}

	// Else the last permanent configuration the collector applied, sent
	// again; else its local configuration (an empty remote one).
	var (
		baseID *uuid.UUID
		body   string
		msg    = "Back to the collector's local configuration after a temporary change"
	)
	var sealed, hash string
	var bid uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, config_hash, config_enc FROM collector_deployments
		WHERE collector_id = $1 AND duration_seconds IS NULL AND status = 'applied'
		ORDER BY finished_at DESC NULLS LAST, created_at DESC LIMIT 1`, collectorID).Scan(&bid, &hash, &sealed)
	switch {
	case err == nil:
		files, err := s.keys.Open(collectorID, hash, sealed)
		if err != nil || len(files) != 1 {
			return Deployment{}, fmt.Errorf("collectors: open base: %w", err)
		}
		body, baseID, msg = files[0].Body, &bid, "Back to the configuration before a temporary change"
	case !errors.Is(err, pgx.ErrNoRows):
		return Deployment{}, fmt.Errorf("collectors: base: %w", err)
	}
	newHash, newSealed, err := s.keys.Seal(collectorID, deploymentFiles(body))
	if err != nil {
		return Deployment{}, err
	}
	if err := supersede(ctx, tx, collectorID); err != nil {
		return Deployment{}, err
	}
	return scanDeployment(tx.QueryRow(ctx, `
		INSERT INTO collector_deployments (collector_id, config_hash, config_enc, message, created_by_name, via, rolled_back_from, status)
		VALUES ($1, $2, $3, $4, 'Sluicio', 'revert', $5, 'pending')
		RETURNING `+deploymentCols,
		collectorID, newHash, newSealed, msg, baseID))
}

// ActiveTemporary is the collector's active temporary deployment, or nil.
func (s *Store) ActiveTemporary(ctx context.Context, collectorID uuid.UUID) (*Deployment, error) {
	d, err := scanDeployment(s.pool.QueryRow(ctx, `SELECT `+deploymentCols+` FROM collector_deployments
		WHERE collector_id = $1 AND duration_seconds IS NOT NULL AND status IN ('pending', 'sent', 'applying', 'applied')
		ORDER BY created_at DESC LIMIT 1`, collectorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// Expiry is what the sweep did for one collector: the temporary change
// that ended and the base queued in its place.
type Expiry struct {
	CollectorID uuid.UUID
	Ended       uuid.UUID
	Base        Deployment
}

// ExpireDue ends every temporary deployment whose time is up, and sends
// on any change held behind a temporary one that failed or was
// superseded (so nothing is holding it any more). It returns the
// collectors to deliver to.
func (s *Store) ExpireDue(ctx context.Context, now time.Time) ([]Expiry, []uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT collector_id, id FROM collector_deployments
		WHERE status = 'applied' AND expires_at IS NOT NULL AND expires_at <= $1`, now)
	if err != nil {
		return nil, nil, err
	}
	type due struct{ collector, id uuid.UUID }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.collector, &d.id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		list = append(list, d)
	}
	rows.Close()
	var ended []Expiry
	for _, d := range list {
		base, err := s.EndTemporary(ctx, d.collector, d.id, EndedExpired)
		if errors.Is(err, ErrNotFound) {
			continue // ended early in the meantime
		}
		if err != nil {
			return ended, nil, err
		}
		ended = append(ended, Expiry{CollectorID: d.collector, Ended: d.id, Base: base})
	}

	// Held with nothing holding it: the temporary change failed or was
	// superseded before it applied.
	released, err := s.pool.Query(ctx, `
		UPDATE collector_deployments h SET status = 'pending'
		WHERE h.status = 'held'
		  AND h.id = (SELECT id FROM collector_deployments WHERE collector_id = h.collector_id AND status = 'held'
		              ORDER BY created_at DESC LIMIT 1)
		  AND NOT EXISTS (SELECT 1 FROM collector_deployments t
		                  WHERE t.collector_id = h.collector_id AND t.duration_seconds IS NOT NULL
		                    AND t.status IN ('pending', 'sent', 'applying', 'applied'))
		RETURNING h.collector_id`)
	if err != nil {
		return ended, nil, err
	}
	defer released.Close()
	var free []uuid.UUID
	for released.Next() {
		var id uuid.UUID
		if err := released.Scan(&id); err != nil {
			return ended, nil, err
		}
		free = append(free, id)
	}
	return ended, free, released.Err()
}
