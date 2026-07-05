// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package audit defines the audit-log contract the core API depends on: the
// record/view types and a Recorder interface. The core holds only this
// interface, so it builds and runs with NO Enterprise code present — a
// community build wires the no-op Recorder, while the Enterprise build
// (ee/audit) supplies the persistent Postgres implementation. Reading and
// writing are additionally gated by the `audit_log` license entitlement at
// the call sites.
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Entry is one audit record to write. ActorUserID is nil for system or
// token-initiated actions. Metadata is optional free-form context.
type Entry struct {
	OrgID       uuid.UUID
	ActorUserID *uuid.UUID
	ActorName   string
	ActorEmail  string
	Action      string
	TargetType  string
	TargetID    string
	Metadata    map[string]any
	IP          string
}

// View is one audit record as returned to the API.
type View struct {
	ID          int64          `json:"id"`
	ActorUserID *uuid.UUID     `json:"actor_user_id,omitempty"`
	ActorName   string         `json:"actor_name"`
	ActorEmail  string         `json:"actor_email,omitempty"`
	Action      string         `json:"action"`
	TargetType  string         `json:"target_type,omitempty"`
	TargetID    string         `json:"target_id,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	IP          string         `json:"ip,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Filter narrows a List query. Zero values mean "no constraint", so the
// zero Filter is the unfiltered listing.
type Filter struct {
	// ActorQ matches actor_name OR actor_email, case-insensitive substring —
	// "who did this" without needing the UUID.
	ActorQ string
	// ActorUserID pins to one user exactly (e.g. from a member picker).
	ActorUserID *uuid.UUID
	// Action is a prefix match: "member." finds member.added,
	// member.role_changed, …; an exact action string finds just that one.
	Action string
	// TargetType / TargetID match the audited resource exactly.
	TargetType string
	TargetID   string
	// From / To bound occurred_at (inclusive from, exclusive to).
	From time.Time
	To   time.Time
}

// VerifyResult is the outcome of a chain-integrity walk over one org's
// entries. Entries written before hash chaining shipped carry no hash;
// they're counted in LegacyUnhashed and can't be verified retroactively.
type VerifyResult struct {
	OK bool `json:"ok"`
	// Checked is how many hashed entries were re-hashed and link-checked.
	Checked int64 `json:"entries_checked"`
	// LegacyUnhashed counts pre-chain entries (informational, not a failure).
	LegacyUnhashed int64 `json:"legacy_unhashed"`
	// FirstBrokenID is the oldest entry whose hash or linkage failed;
	// 0 when OK.
	FirstBrokenID int64 `json:"first_broken_id,omitempty"`
	// Detail says what broke at FirstBrokenID ("content hash mismatch" /
	// "chain link mismatch"); empty when OK.
	Detail string `json:"detail,omitempty"`
}

// Recorder persists and queries audit entries. The core holds this
// interface; the Enterprise edition supplies the real implementation.
type Recorder interface {
	Record(ctx context.Context, e Entry) error
	List(ctx context.Context, orgID uuid.UUID, f Filter, limit int, beforeID int64) ([]View, error)
	// Verify walks orgID's hash chain oldest→newest and reports whether
	// every entry's content hash and prev-link still hold.
	Verify(ctx context.Context, orgID uuid.UUID) (VerifyResult, error)
	// Prune deletes entries older than cutoff (all orgs), preserving each
	// org's chain verifiability via the pruning anchor. Returns rows removed.
	Prune(ctx context.Context, cutoff time.Time) (int64, error)
}

// Noop is the community/default Recorder: it drops writes and returns no
// entries, so the audit endpoints degrade cleanly when no Enterprise audit
// store is wired (the call sites are entitlement-gated too).
type Noop struct{}

// Record discards the entry.
func (Noop) Record(context.Context, Entry) error { return nil }

// List returns no entries.
func (Noop) List(context.Context, uuid.UUID, Filter, int, int64) ([]View, error) {
	return []View{}, nil
}

// Verify reports an empty, intact chain.
func (Noop) Verify(context.Context, uuid.UUID) (VerifyResult, error) {
	return VerifyResult{OK: true}, nil
}

// Prune removes nothing.
func (Noop) Prune(context.Context, time.Time) (int64, error) { return 0, nil }
