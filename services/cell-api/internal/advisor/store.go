// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Persistence for advisor suggestions (issue #1, design §3.3).
//
// The findings themselves are recomputed from counted facts every run,
// so almost nothing here needs to be durable. What does is the human
// decision attached to a finding — and the two rules that make repeated
// evaluation tolerable rather than nagging:
//
//   - A dismissal STICKS. Upsert never revives a dismissed suggestion,
//     so an operator who says "no, that attribute is load-bearing" is
//     not asked again tomorrow, and the night after, forever.
//   - A dismissal is not permanent silence. When the facts move
//     materially — the volume roughly doubles — the finding is no
//     longer the one that was dismissed, and it re-opens. Otherwise a
//     single "no" would hide a metric that later grows into the largest
//     line on the bill.
//
// Both live in Upsert, because splitting them across the evaluator and
// the store is how they drift apart.
package advisor

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

// ErrNotFound is returned when a suggestion lookup misses.
var ErrNotFound = errors.New("advisor: suggestion not found")

// reopenFactor is how much the weight must move before a dismissed
// suggestion is considered a different finding and resurfaces.
//
// Doubling is deliberately coarse. The number exists to catch "this got
// materially worse", not to track drift: a tighter trigger would
// resurface the same dismissed item on ordinary week-to-week variation,
// which is precisely the nagging that makes people stop reading an
// advisor at all.
const reopenFactor = 2.0

// Suggestion is one finding plus whatever a human decided about it.
type Suggestion struct {
	ID          uuid.UUID `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	Class       string    `json:"class"`
	Advisor     string    `json:"advisor"`
	ScopeKind   string    `json:"scope_kind,omitempty"`
	ScopeID     string    `json:"scope_id,omitempty"`
	Title       string    `json:"title"`
	Loss        string    `json:"loss,omitempty"`
	Snippet     string    `json:"snippet,omitempty"`
	// SnippetTarget is the collector version the snippet was written
	// for (issue #16). Stored rather than derived: on an ACCEPTED
	// suggestion the snippet is the audit trail of what was advised, and
	// re-deriving the target later would make that record describe a
	// decision nobody made.
	SnippetTarget string `json:"snippet_target,omitempty"`
	// SnippetUnavailable is why there is no snippet, when the change
	// cannot be expressed for the target collector. The finding still
	// stands; only the YAML is withheld.
	SnippetUnavailable string         `json:"snippet_unavailable,omitempty"`
	Evidence           map[string]any `json:"evidence"`
	Weight             int64          `json:"weight"`
	State              string         `json:"state"`

	DecidedBy    *uuid.UUID `json:"decided_by,omitempty"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	DecisionNote string     `json:"decision_note,omitempty"`

	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// Store is the Postgres-backed suggestion store.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const suggestionCols = `id, fingerprint, class, advisor, scope_kind, scope_id, title, loss,
	snippet, snippet_target, snippet_unavailable, evidence, weight, state,
	decided_by, decided_at, decision_note, first_seen_at, last_seen_at`

func scanSuggestion(row pgx.Row) (Suggestion, error) {
	var s Suggestion
	var evidence []byte
	if err := row.Scan(&s.ID, &s.Fingerprint, &s.Class, &s.Advisor, &s.ScopeKind, &s.ScopeID,
		&s.Title, &s.Loss, &s.Snippet, &s.SnippetTarget, &s.SnippetUnavailable,
		&evidence, &s.Weight, &s.State,
		&s.DecidedBy, &s.DecidedAt, &s.DecisionNote, &s.FirstSeenAt, &s.LastSeenAt); err != nil {
		return Suggestion{}, err
	}
	if len(evidence) > 0 {
		_ = json.Unmarshal(evidence, &s.Evidence)
	}
	if s.Evidence == nil {
		s.Evidence = map[string]any{}
	}
	return s, nil
}

// Upsert records a finding the evaluator currently stands behind.
//
// An existing OPEN row is refreshed in place, keeping first_seen_at so
// the UI can say how long this has been true. An ACCEPTED or VERIFIED
// row is left alone: the human already acted, and the evaluator seeing
// the finding again usually means the collector has not been redeployed
// yet — which is information for the verification pass, not a reason to
// re-ask. A DISMISSED row only revives if the facts moved materially.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, f Suggestion) error {
	evidence, err := json.Marshal(f.Evidence)
	if err != nil {
		return fmt.Errorf("advisor: encode evidence: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO advisor_suggestions
			(org_id, fingerprint, class, advisor, scope_kind, scope_id, title, loss,
			 snippet, snippet_target, snippet_unavailable, evidence, weight, state)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$13,$14,$10,$11,'open')
		ON CONFLICT (org_id, fingerprint) DO UPDATE SET
			-- Refresh the display and the facts…
			title      = EXCLUDED.title,
			loss       = EXCLUDED.loss,
			snippet    = EXCLUDED.snippet,
			snippet_target      = EXCLUDED.snippet_target,
			snippet_unavailable = EXCLUDED.snippet_unavailable,
			evidence   = EXCLUDED.evidence,
			-- Weight is the re-open baseline, so a DISMISSED row keeps
			-- the value the human saw rather than tracking the finding.
			-- Refreshing it every night would drift the trigger upward
			-- with the metric itself: dismissed at 1000, refreshed to
			-- 1900, and now nothing under 3800 counts as a doubling —
			-- so a finding that genuinely doubled over a fortnight
			-- would never resurface. Frozen until it re-opens.
			weight = CASE
				WHEN advisor_suggestions.state = 'dismissed'
				     AND EXCLUDED.weight < GREATEST(advisor_suggestions.weight, 1) * $12
				THEN advisor_suggestions.weight
				ELSE EXCLUDED.weight
			END,
			last_seen_at = now(),
			updated_at   = now(),
			-- …but only reopen a dismissal the facts have outgrown. An
			-- accepted or verified row is never reopened here; the
			-- verification pass owns those transitions.
			state = CASE
				WHEN advisor_suggestions.state = 'dismissed'
				     AND EXCLUDED.weight >= GREATEST(advisor_suggestions.weight, 1) * $12 THEN 'open'
				ELSE advisor_suggestions.state
			END,
			dismissed_facts = CASE
				WHEN advisor_suggestions.state = 'dismissed'
				     AND EXCLUDED.weight >= GREATEST(advisor_suggestions.weight, 1) * $12 THEN NULL
				ELSE advisor_suggestions.dismissed_facts
			END`,
		orgID, f.Fingerprint, f.Class, f.Advisor, f.ScopeKind, f.ScopeID, f.Title, f.Loss,
		f.Snippet, evidence, f.Weight, reopenFactor, f.SnippetTarget, f.SnippetUnavailable)
	return err
}

// CloseMissing retires open suggestions of the given classes that this
// run did NOT produce — the finding stopped being true, usually because
// somebody acted on it or demand appeared.
//
// Deleting rather than marking resolved is deliberate: an OPEN finding
// that is no longer true has no history worth keeping, and a list of
// "things that briefly looked wasteful" is noise in every later query.
// Accepted and dismissed rows are never touched — those record a human
// decision and are the reason this table is durable at all.
func (s *Store) CloseMissing(ctx context.Context, orgID uuid.UUID, classes []string, keep []string) error {
	if len(classes) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		DELETE FROM advisor_suggestions
		WHERE org_id = $1 AND state = 'open'
		  AND class = ANY($2) AND NOT (fingerprint = ANY($3))`,
		orgID, classes, keep)
	return err
}

// List returns an org's suggestions, most valuable first. An empty
// advisor lists both; an empty state lists open + accepted + verified
// (the board), since dismissed items are deliberately out of sight.
func (s *Store) List(ctx context.Context, orgID uuid.UUID, advisorName, state string, limit int) ([]Suggestion, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+suggestionCols+` FROM advisor_suggestions
		WHERE org_id = $1
		  AND ($2 = '' OR advisor = $2)
		  AND (($3 = '' AND state <> 'dismissed') OR state = $3)
		ORDER BY weight DESC, first_seen_at ASC
		LIMIT $4`, orgID, advisorName, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Suggestion{}
	for rows.Next() {
		sg, err := scanSuggestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

// Get returns one suggestion within the org.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Suggestion, error) {
	sg, err := scanSuggestion(s.pool.QueryRow(ctx,
		`SELECT `+suggestionCols+` FROM advisor_suggestions WHERE org_id=$1 AND id=$2`, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Suggestion{}, ErrNotFound
	}
	return sg, err
}

// Decide records accept or dismiss. Dismissal snapshots the facts as
// they stood, which is what a later run compares against to decide
// whether the finding has changed enough to be worth raising again.
func (s *Store) Decide(ctx context.Context, orgID, id, userID uuid.UUID, state, note string) (Suggestion, error) {
	if state != "accepted" && state != "dismissed" {
		return Suggestion{}, fmt.Errorf("advisor: invalid decision %q", state)
	}
	var by any = userID
	if userID == uuid.Nil {
		by = nil
	}
	sg, err := scanSuggestion(s.pool.QueryRow(ctx, `
		UPDATE advisor_suggestions
		SET state = $3,
		    decided_by = $4,
		    decided_at = now(),
		    decision_note = $5,
		    dismissed_facts = CASE WHEN $3 = 'dismissed' THEN evidence ELSE dismissed_facts END,
		    updated_at = now()
		WHERE org_id = $1 AND id = $2
		RETURNING `+suggestionCols, orgID, id, state, by, note))
	if errors.Is(err, pgx.ErrNoRows) {
		return Suggestion{}, ErrNotFound
	}
	return sg, err
}

// MarkVerified flips accepted suggestions whose supply actually dropped.
//
// Accepting a suggestion is not evidence the collector changed — v1
// emits a snippet and trusts nobody. This is the one honest signal that
// it took effect, and it costs nothing: the next evaluation simply does
// not produce the finding any more.
func (s *Store) MarkVerified(ctx context.Context, orgID uuid.UUID, stillFound []string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE advisor_suggestions
		SET state = 'verified', updated_at = now()
		WHERE org_id = $1 AND state = 'accepted' AND NOT (fingerprint = ANY($2))`,
		orgID, stillFound)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CountNewSince powers the digest line: how many suggestions appeared
// since the user last looked.
func (s *Store) CountNewSince(ctx context.Context, orgID uuid.UUID, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM advisor_suggestions
		WHERE org_id = $1 AND state = 'open' AND first_seen_at > $2`, orgID, since).Scan(&n)
	return n, err
}
