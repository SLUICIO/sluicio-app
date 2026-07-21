// SPDX-License-Identifier: LicenseRef-Sluicio-Enterprise
//
// Copyright (c) ROMA IT AB. All rights reserved.
// Part of Sluicio Enterprise Edition — see ee/LICENSE.md.

// Package audit is the Enterprise audit-log store: an append-only record of
// security-relevant admin actions. It implements the core audit.Recorder
// contract (pkg/audit), so the core depends on the interface, not on this
// proprietary package. Writing and reading are both gated by the `audit_log`
// license entitlement at the call sites in the core API; this package is just
// the persistence.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	coreaudit "github.com/sluicio/sluicio-app/pkg/audit"
)

// Store persists and queries audit entries.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store over the control-plane Postgres pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Store implements the core audit.Recorder contract.
var _ coreaudit.Recorder = (*Store)(nil)

// Record appends one entry, hash-chained to its predecessor. Best-effort by
// contract: callers log + swallow the error so a failed audit write never
// breaks the action being audited.
//
// Chain shape: entry_hash = sha256(prev_hash ∥ canonical(entry)), where prev
// is the org's newest entry (or the pruning anchor when the log was trimmed,
// or "" for a fresh org / the legacy unhashed prefix). A per-org advisory
// lock serializes writers so two concurrent inserts can't both claim the
// same predecessor. occurred_at is computed here — not by the DB default —
// because the hash must cover it.
func (s *Store) Record(ctx context.Context, e coreaudit.Entry) error {
	payload := canonicalMetadata(e.Metadata)
	// Postgres stores microseconds; truncate so the hashed value and the
	// stored value round-trip identically for Verify.
	occurredAt := time.Now().UTC().Truncate(time.Microsecond)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("audit: record: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('audit_chain:' || $1::text))`, e.OrgID); err != nil {
		return fmt.Errorf("audit: record: lock: %w", err)
	}
	// Predecessor: newest row's entry_hash (empty for a legacy row, which
	// deliberately restarts the chain after the unverifiable prefix), else
	// the pruning anchor, else "".
	var prev string
	err = tx.QueryRow(ctx, `
		SELECT entry_hash FROM audit_log
		WHERE organization_id = $1 ORDER BY id DESC LIMIT 1`, e.OrgID).Scan(&prev)
	if err != nil {
		prev = ""
		_ = tx.QueryRow(ctx, `
			SELECT last_hash FROM audit_chain_anchor
			WHERE organization_id = $1`, e.OrgID).Scan(&prev)
	}
	actorID := ""
	if e.ActorUserID != nil {
		actorID = e.ActorUserID.String()
	}
	hash := chainHash(prev, e.OrgID.String(), actorID, e.ActorName, e.ActorEmail,
		e.Action, e.TargetType, e.TargetID, string(payload), e.IP, occurredAt)

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log
		    (organization_id, actor_user_id, actor_name, actor_email, action, resource_type, resource_id, payload, ip, occurred_at, entry_hash, prev_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		e.OrgID, e.ActorUserID, e.ActorName, e.ActorEmail, e.Action, e.TargetType, e.TargetID, payload, e.IP,
		occurredAt, hash, prev); err != nil {
		return fmt.Errorf("audit: record: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("audit: record: commit: %w", err)
	}
	return nil
}

// canonicalMetadata marshals entry metadata into the canonical JSON that
// participates in the chain hash: the exact round-trip Verify performs
// after JSONB storage (unmarshal into map[string]any, re-marshal with
// sorted keys). Metadata values that aren't plain maps — structs, which
// marshal in field-declaration order — would otherwise hash differently
// at write time than at verify time, permanently flagging the entry as
// tampered (the shipped config-import report did exactly that).
func canonicalMetadata(metadata map[string]any) []byte {
	payload := []byte("{}")
	if len(metadata) > 0 {
		if b, err := json.Marshal(metadata); err == nil {
			payload = b
		}
	}
	var canon map[string]any
	if err := json.Unmarshal(payload, &canon); err != nil || len(canon) == 0 {
		return []byte("{}")
	}
	b, err := json.Marshal(canon)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// chainHash computes the tamper-evidence hash for one entry. Fields are
// joined with an unambiguous separator; metadata participates as its
// canonical Go-marshalled JSON (sorted keys), which is also how Verify
// re-derives it after a JSONB round-trip.
func chainHash(prev, orgID, actorID, actorName, actorEmail, action, targetType, targetID, payloadJSON, ip string, occurredAt time.Time) string {
	h := sha256.New()
	for _, part := range []string{
		prev, orgID, actorID, actorName, actorEmail,
		action, targetType, targetID, payloadJSON, ip,
		occurredAt.UTC().Format(time.RFC3339Nano),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0x1f}) // unit separator: "a"+"bc" ≠ "ab"+"c"
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Verify walks one org's entries oldest→newest, re-deriving every hash and
// prev-link. The seed is the pruning anchor when present. Legacy rows
// (empty entry_hash, written before chaining shipped) are counted and
// skipped; a hashed row that follows legacy rows links from "" by
// construction, so the walk stays consistent across the boundary.
func (s *Store) Verify(ctx context.Context, orgID uuid.UUID) (coreaudit.VerifyResult, error) {
	res := coreaudit.VerifyResult{OK: true}
	// Seed: the pruning anchor when the log has been trimmed, else "".
	prev := ""
	var anchorID int64
	_ = s.pool.QueryRow(ctx, `
		SELECT last_id, last_hash FROM audit_chain_anchor
		WHERE organization_id = $1`, orgID).Scan(&anchorID, &prev)
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor_user_id, actor_name, actor_email, action, resource_type, resource_id, payload, ip, occurred_at, entry_hash, prev_hash
		FROM audit_log WHERE organization_id = $1 ORDER BY id ASC`, orgID)
	if err != nil {
		return res, fmt.Errorf("audit: verify: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                                          int64
			actorUID                                    *uuid.UUID
			name, email, action, ttype, tid, ip, eh, ph string
			payload                                     []byte
			at                                          time.Time
		)
		if err := rows.Scan(&id, &actorUID, &name, &email, &action, &ttype, &tid, &payload, &ip, &at, &eh, &ph); err != nil {
			return res, err
		}
		if eh == "" {
			// Legacy prefix: unverifiable by design. A hashed row written
			// after a legacy row linked from "" at write time, so reset.
			res.LegacyUnhashed++
			prev = ""
			continue
		}
		// Re-marshal metadata through Go so key order matches write time
		// (JSONB does not preserve the original ordering).
		canonicalPayload := "{}"
		if len(payload) > 0 {
			var m map[string]any
			if err := json.Unmarshal(payload, &m); err == nil && len(m) > 0 {
				if b, err := json.Marshal(m); err == nil {
					canonicalPayload = string(b)
				}
			}
		}
		actorID := ""
		if actorUID != nil {
			actorID = actorUID.String()
		}
		if ph != prev {
			res.OK = false
			res.FirstBrokenID = id
			res.Detail = "chain link mismatch"
			return res, nil
		}
		want := chainHash(ph, orgID.String(), actorID, name, email, action, ttype, tid, canonicalPayload, ip, at)
		if want != eh {
			res.OK = false
			res.FirstBrokenID = id
			res.Detail = "content hash mismatch"
			return res, nil
		}
		res.Checked++
		prev = eh
	}
	return res, rows.Err()
}

// Prune deletes entries older than cutoff across all orgs while keeping
// each org's chain verifiable: the newest deleted row's (id, hash) is
// upserted into audit_chain_anchor so the surviving head still has a seed.
// Runs per org under the same advisory lock as Record so a concurrent
// insert can't interleave with the delete + anchor write.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT organization_id FROM audit_log WHERE occurred_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("audit: prune: orgs: %w", err)
	}
	var orgs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		orgs = append(orgs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var total int64
	for _, org := range orgs {
		n, err := s.pruneOrg(ctx, org, cutoff)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (s *Store) pruneOrg(ctx context.Context, org uuid.UUID, cutoff time.Time) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("audit: prune: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('audit_chain:' || $1::text))`, org); err != nil {
		return 0, fmt.Errorf("audit: prune: lock: %w", err)
	}
	var lastID int64
	var lastHash string
	err = tx.QueryRow(ctx, `
		SELECT id, entry_hash FROM audit_log
		WHERE organization_id = $1 AND occurred_at < $2
		ORDER BY id DESC LIMIT 1`, org, cutoff).Scan(&lastID, &lastHash)
	if err != nil {
		return 0, nil // raced away — nothing old left for this org
	}
	tag, err := tx.Exec(ctx, `
		DELETE FROM audit_log WHERE organization_id = $1 AND occurred_at < $2`, org, cutoff)
	if err != nil {
		return 0, fmt.Errorf("audit: prune: delete: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_chain_anchor (organization_id, last_id, last_hash, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (organization_id) DO UPDATE
		SET last_id = EXCLUDED.last_id, last_hash = EXCLUDED.last_hash, updated_at = now()`,
		org, lastID, lastHash); err != nil {
		return 0, fmt.Errorf("audit: prune: anchor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("audit: prune: commit: %w", err)
	}
	return tag.RowsAffected(), nil
}

// List returns up to `limit` of an org's most recent entries matching the
// filter, newest first. `beforeID` pages backwards (entries with id <
// beforeID); 0 starts at the newest. Keyset pagination composes with the
// filter, so "load more" under an active search keeps working.
func (s *Store) List(ctx context.Context, orgID uuid.UUID, f coreaudit.Filter, limit int, beforeID int64) ([]coreaudit.View, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `
		SELECT id, actor_user_id, actor_name, actor_email, action, resource_type, resource_id, payload, ip, occurred_at
		FROM audit_log
		WHERE organization_id = $1 AND ($2 = 0 OR id < $2)`
	args := []any{orgID, beforeID}
	add := func(clause string, v any) {
		args = append(args, v)
		q += fmt.Sprintf(clause, len(args))
	}
	if f.ActorQ != "" {
		// One bind, matched against both denormalised actor columns.
		args = append(args, "%"+f.ActorQ+"%")
		q += fmt.Sprintf(" AND (actor_name ILIKE $%d OR actor_email ILIKE $%d)", len(args), len(args))
	}
	if f.ActorUserID != nil {
		add(" AND actor_user_id = $%d", *f.ActorUserID)
	}
	if f.Action != "" {
		add(" AND action LIKE $%d", likePrefix(f.Action))
	}
	if f.TargetType != "" {
		add(" AND resource_type = $%d", f.TargetType)
	}
	if f.TargetID != "" {
		add(" AND resource_id = $%d", f.TargetID)
	}
	if !f.From.IsZero() {
		add(" AND occurred_at >= $%d", f.From)
	}
	if !f.To.IsZero() {
		add(" AND occurred_at < $%d", f.To)
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()
	out := []coreaudit.View{}
	for rows.Next() {
		var (
			v    coreaudit.View
			meta []byte
		)
		if err := rows.Scan(&v.ID, &v.ActorUserID, &v.ActorName, &v.ActorEmail, &v.Action, &v.TargetType, &v.TargetID, &meta, &v.IP, &v.CreatedAt); err != nil {
			return nil, err
		}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &v.Metadata)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// likePrefix turns a user-supplied action prefix into a safe LIKE pattern:
// escape LIKE metacharacters, then anchor as prefix.
func likePrefix(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s) + "%"
}
