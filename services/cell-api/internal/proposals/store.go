// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package proposals is the safe-write primitive for agents (issue #8,
// WS2). An agent reads freely within its token's RBAC but never mutates
// monitoring config; it files a Proposal — a diff plus a rationale —
// and a human with the rights to make that change approves or rejects
// it. Approval applies through the ordinary engine, so an
// agent-originated change is indistinguishable from a hand-made one
// afterwards and stays just as editable.
//
// The loop is Community. Governance layered on top (approval policies,
// the agent audit trail, per-token budgets) is Enterprise — gating
// approval itself would leave CE users holding proposals nobody can
// approve, hiding the feature from the people evaluating it.
//
// Drift is the interesting part. Each change records what the agent saw
// (Before) alongside what it wants (After). Between proposing and
// approving, a human may have edited the same field; applying blindly
// would silently revert them. Approval therefore re-reads the target and
// refuses when Before no longer matches — see CheckDrift.
package proposals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/cellhealth"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound is returned when a proposal lookup misses.
	ErrNotFound = errors.New("proposals: not found")
	// ErrNotPending is returned when a decision is attempted on a
	// proposal that was already decided, expired or superseded. It is a
	// conflict, not a validation failure: two reviewers racing on the
	// same proposal is normal, and the loser must be told plainly.
	ErrNotPending = errors.New("proposals: proposal is no longer pending")
	// ErrDrift is returned when the target changed since the proposal
	// was filed, so approving would clobber whoever changed it.
	ErrDrift = errors.New("proposals: the target changed since this was proposed")
)

// DefaultTTL is how long a proposal stays reviewable. Long enough to
// survive a holiday week, short enough that an ignored queue doesn't rot
// into background noise.
const DefaultTTL = 14 * 24 * time.Hour

// Change is one field-level edit. Before is what the proposer observed;
// it is the drift check's input, not decoration. Values are stored as
// raw JSON so a change can carry a scalar, a list or an object without
// this package knowing the target's schema.
type Change struct {
	Field  string          `json:"field"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

// Proposal is one stored change request.
type Proposal struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"-"`
	TargetKind  string     `json:"target_kind"`
	TargetID    *uuid.UUID `json:"target_id,omitempty"` // nil = create new
	TargetLabel string     `json:"target_label"`

	Changes   []Change `json:"changes"`
	Rationale string   `json:"rationale"`

	ProposedByKind  string     `json:"proposed_by_kind"`
	ProposedByID    *uuid.UUID `json:"proposed_by_id,omitempty"`
	ProposedByLabel string     `json:"proposed_by_label"`
	Via             string     `json:"via"`

	State        string     `json:"state"`
	DecidedBy    *uuid.UUID `json:"decided_by,omitempty"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	DecisionNote string     `json:"decision_note"`

	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Pending reports whether the proposal can still be decided.
func (p Proposal) Pending() bool { return p.State == "pending" }

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, org_id, target_kind, target_id, target_label, changes, rationale,
	proposed_by_kind, proposed_by_id, proposed_by_label, via,
	state, decided_by, decided_at, decision_note, expires_at, created_at, updated_at`

func scan(row pgx.Row) (Proposal, error) {
	var p Proposal
	var raw []byte
	err := row.Scan(&p.ID, &p.OrgID, &p.TargetKind, &p.TargetID, &p.TargetLabel, &raw, &p.Rationale,
		&p.ProposedByKind, &p.ProposedByID, &p.ProposedByLabel, &p.Via,
		&p.State, &p.DecidedBy, &p.DecidedAt, &p.DecisionNote, &p.ExpiresAt, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Proposal{}, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p.Changes); err != nil {
			return Proposal{}, fmt.Errorf("proposals: decode changes: %w", err)
		}
	}
	return p, nil
}

// Create files a new proposal. Any earlier pending proposal against the
// same target is marked superseded in the same transaction: a queue with
// three competing edits to one check is worse than useless, and the
// agent's latest read is the only one whose Before is still current.
func (s *Store) Create(ctx context.Context, p Proposal, ttl time.Duration) (Proposal, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	changes, err := json.Marshal(p.Changes)
	if err != nil {
		return Proposal{}, fmt.Errorf("proposals: encode changes: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Proposal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if p.TargetID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE proposals SET state='superseded', updated_at=now()
			 WHERE org_id=$1 AND target_kind=$2 AND target_id=$3 AND state='pending'`,
			p.OrgID, p.TargetKind, *p.TargetID); err != nil {
			return Proposal{}, err
		}
	}

	row := tx.QueryRow(ctx, `INSERT INTO proposals
		(org_id, target_kind, target_id, target_label, changes, rationale,
		 proposed_by_kind, proposed_by_id, proposed_by_label, via, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING `+cols,
		p.OrgID, p.TargetKind, p.TargetID, p.TargetLabel, changes, p.Rationale,
		p.ProposedByKind, p.ProposedByID, p.ProposedByLabel, p.Via, time.Now().Add(ttl))
	out, err := scan(row)
	if err != nil {
		return Proposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Proposal{}, err
	}
	return out, nil
}

// Get returns one proposal within the org.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Proposal, error) {
	p, err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM proposals WHERE org_id=$1 AND id=$2`, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Proposal{}, ErrNotFound
	}
	return p, err
}

// List returns the org's proposals, newest first. An empty state lists
// every state — the inbox asks for "pending", the history view doesn't.
func (s *Store) List(ctx context.Context, orgID uuid.UUID, state string, limit int) ([]Proposal, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + cols + ` FROM proposals WHERE org_id=$1`
	args := []any{orgID}
	if state != "" {
		q += ` AND state=$2`
		args = append(args, state)
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d`, limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Proposal, 0, limit)
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PendingCount drives the inbox badge.
func (s *Store) PendingCount(ctx context.Context, orgID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM proposals WHERE org_id=$1 AND state='pending'`, orgID).Scan(&n)
	return n, err
}

// CheckDrift compares each change's Before against the target's value
// now, keyed by field. A field missing from `current` is treated as
// drifted: the caller could not produce it, and approving on an unknown
// basis is exactly what this guards against.
//
// Returns the fields that no longer match, empty when the target is
// untouched.
func CheckDrift(changes []Change, current map[string]json.RawMessage) []string {
	var drifted []string
	for _, c := range changes {
		now, ok := current[c.Field]
		if !ok {
			drifted = append(drifted, c.Field)
			continue
		}
		if !jsonEqual(c.Before, now) {
			drifted = append(drifted, c.Field)
		}
	}
	return drifted
}

// jsonEqual compares two raw JSON values semantically, so key order and
// insignificant whitespace don't read as drift.
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	ab, err1 := json.Marshal(av)
	bb, err2 := json.Marshal(bv)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}

// Decide records a human's decision. state must be "approved" or
// "rejected". The UPDATE is guarded on state='pending' so two reviewers
// racing produce one winner and one ErrNotPending, rather than both
// believing they decided it.
func (s *Store) Decide(ctx context.Context, orgID, id, decidedBy uuid.UUID, state, note string) (Proposal, error) {
	if state != "approved" && state != "rejected" {
		return Proposal{}, fmt.Errorf("proposals: invalid decision %q", state)
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE proposals SET state=$4, decided_by=$3, decided_at=now(), decision_note=$5, updated_at=now()
		 WHERE org_id=$1 AND id=$2 AND state='pending' RETURNING `+cols,
		orgID, id, decidedBy, state, note)
	p, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		// Either it doesn't exist or it wasn't pending; distinguish so
		// the API can answer 404 vs 409.
		if _, gErr := s.Get(ctx, orgID, id); gErr != nil {
			return Proposal{}, ErrNotFound
		}
		return Proposal{}, ErrNotPending
	}
	return p, err
}

// ExpireDue moves pending proposals past their expiry to 'expired'.
// Called on a timer. Expiring rather than deleting keeps the record that
// a proposal went unreviewed, which is its own signal about the queue.
func (s *Store) ExpireDue(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE proposals SET state='expired', updated_at=now()
		 WHERE state='pending' AND expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// SweepInterval is how often the expiry sweep runs. Proposals live for
// days, so this only has to be small relative to the TTL — hourly keeps
// the inbox honest without a pointless query every minute.
const SweepInterval = time.Hour

// RunExpirySweep expires overdue proposals until ctx is cancelled.
//
// The sweep is what makes the TTL real: without it a proposal past its
// expiry would sit in the inbox looking actionable, and approving one
// filed three weeks ago would apply reasoning nobody can still check.
//
// It runs once at startup rather than waiting a full interval — a cell
// that was down over a weekend should not serve stale proposals for an
// hour after it comes back. Errors are logged and the loop continues:
// a failed sweep is a retry next tick, never a reason to take down the
// process.
func (s *Store) RunExpirySweep(ctx context.Context, logger *slog.Logger) {
	sweep := func() {
		n, err := s.ExpireDue(ctx)
		if err != nil {
			// A cancelled context during shutdown is not a failure.
			if ctx.Err() == nil {
				logger.Warn("proposal expiry sweep failed", "err", err)
			}
			return
		}
		if n > 0 {
			logger.Info("expired unreviewed proposals", "count", n)
		}
	}
	sweep()

	t := time.NewTicker(SweepInterval)
	cellhealth.Register("proposal-expiry", SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
			// End of cycle, not start: a loop wedged inside its
			// own body is exactly what this catches.
			cellhealth.Beat("proposal-expiry")
		}
	}
}
