// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tracecompletion

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

// Store wraps Postgres operations specific to trace-completion rules.
// Rules themselves live in the existing `alert_rules` table with
// signal='trace' — see the package comment for the rationale. This
// store deliberately does NOT touch alert_instances or
// notification_jobs; the evaluator goes through the alerting store
// for those so the existing delivery loop covers us for free.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Rule is the projected shape of an alert_rules row of signal='trace'.
// Distinct from alerting.AlertRule because that one's Spec is typed
// as MetricRuleSpec.
type Rule struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	IntegrationID  uuid.UUID
	Name           string
	Description    string
	Severity       string
	Enabled        bool
	Spec           RuleSpec
	ChannelIDs     []uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// RuleInput is what the create / update HTTP handlers accept.
type RuleInput struct {
	IntegrationID uuid.UUID
	Name          string
	Description   string
	Severity      string
	Enabled       bool
	Spec          RuleSpec
	ChannelIDs    []uuid.UUID
}

// validateInput is the input gate. Bad data fails fast at the HTTP
// boundary rather than at evaluation time.
func (in RuleInput) validate() error {
	if in.IntegrationID == uuid.Nil {
		return errors.New("integration_id is required")
	}
	if in.Name == "" {
		return errors.New("name is required")
	}
	switch in.Severity {
	case "info", "warning", "critical":
	default:
		return fmt.Errorf("severity must be info|warning|critical (got %q)", in.Severity)
	}
	return in.Spec.Validate()
}

// ── reads ────────────────────────────────────────────────────────────

const ruleCols = `id, organization_id, integration_id,
    name, COALESCE(description, ''), severity::text, enabled,
    rule_spec, created_at, updated_at`

// EnabledForEval returns all enabled trace-completion rules in the
// org. Used by the periodic evaluator.
func (s *Store) EnabledForEval(ctx context.Context, orgID uuid.UUID) ([]Rule, error) {
	const q = `SELECT ` + ruleCols + `
		FROM alert_rules
		WHERE organization_id = $1 AND signal = 'trace' AND enabled
		  AND COALESCE(rule_spec->>'kind', '') <> 'trace_error'
		ORDER BY integration_id, created_at`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("tracecompletion: list enabled: %w", err)
	}
	defer rows.Close()
	return s.scanRules(ctx, rows)
}

// ListForIntegration returns all rules (enabled or not) attached to
// one integration. Used by the integration-settings UI.
func (s *Store) ListForIntegration(ctx context.Context, orgID, integrationID uuid.UUID) ([]Rule, error) {
	const q = `SELECT ` + ruleCols + `
		FROM alert_rules
		WHERE organization_id = $1 AND integration_id = $2 AND signal = 'trace'
		  AND COALESCE(rule_spec->>'kind', '') <> 'trace_error'
		ORDER BY created_at`
	rows, err := s.pool.Query(ctx, q, orgID, integrationID)
	if err != nil {
		return nil, fmt.Errorf("tracecompletion: list for integration: %w", err)
	}
	defer rows.Close()
	return s.scanRules(ctx, rows)
}

// Get fetches one rule by id, scoped to the org.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Rule, error) {
	const q = `SELECT ` + ruleCols + `
		FROM alert_rules
		WHERE id = $1 AND organization_id = $2 AND signal = 'trace'
		  AND COALESCE(rule_spec->>'kind', '') <> 'trace_error'`
	row := s.pool.QueryRow(ctx, q, id, orgID)
	r, err := scanRule(row)
	if err != nil {
		return Rule{}, err
	}
	if err := s.loadChannels(ctx, &r); err != nil {
		return Rule{}, err
	}
	return r, nil
}

// scanRules iterates a query result + loads each row's channel
// associations (alert_rule_routes). N+1 in row count, but rule counts
// per org are small (≤ tens).
func (s *Store) scanRules(ctx context.Context, rows pgx.Rows) ([]Rule, error) {
	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadChannels(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func scanRule(row pgx.Row) (Rule, error) {
	var r Rule
	var specRaw []byte
	if err := row.Scan(
		&r.ID, &r.OrganizationID, &r.IntegrationID,
		&r.Name, &r.Description, &r.Severity, &r.Enabled,
		&specRaw, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Rule{}, ErrNotFound
		}
		return Rule{}, err
	}
	if err := json.Unmarshal(specRaw, &r.Spec); err != nil {
		return Rule{}, fmt.Errorf("tracecompletion: decode rule_spec for %s: %w", r.ID, err)
	}
	// Canonicalise legacy (closing-span-only) specs into the stage shape
	// so every downstream reader sees one form.
	r.Spec.Normalize()
	return r, nil
}

func (s *Store) loadChannels(ctx context.Context, r *Rule) error {
	rows, err := s.pool.Query(ctx,
		`SELECT channel_id FROM alert_rule_routes WHERE alert_rule_id = $1`, r.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	// Start with an empty (non-nil) slice so JSON marshalling emits
	// [] rather than null when the rule has no routed channels — the
	// frontend treats channel_ids as an array unconditionally.
	r.ChannelIDs = []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		r.ChannelIDs = append(r.ChannelIDs, id)
	}
	return rows.Err()
}

// ── writes ───────────────────────────────────────────────────────────

// Create inserts a new trace-completion rule. The rule_spec column
// gets the serialized RuleSpec; signal is hardcoded to 'trace' so
// the existing metric evaluator skips it.
func (s *Store) Create(ctx context.Context, orgID uuid.UUID, in RuleInput) (Rule, error) {
	if err := in.validate(); err != nil {
		return Rule{}, err
	}
	specJSON, err := json.Marshal(in.Spec)
	if err != nil {
		return Rule{}, fmt.Errorf("tracecompletion: encode spec: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Rule{}, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO alert_rules
		    (organization_id, integration_id, name, description, signal,
		     rule_spec, severity, enabled)
		VALUES ($1, $2, $3, $4, 'trace', $5::jsonb, $6::alert_severity, $7)
		RETURNING id`,
		orgID, in.IntegrationID, in.Name, in.Description,
		specJSON, in.Severity, in.Enabled).Scan(&id); err != nil {
		return Rule{}, fmt.Errorf("tracecompletion: insert: %w", err)
	}
	if err := s.replaceChannelsTx(ctx, tx, id, in.ChannelIDs); err != nil {
		return Rule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Rule{}, err
	}
	return s.Get(ctx, orgID, id)
}

// Update replaces the mutable fields of an existing rule. Doesn't
// touch instances/jobs — those are sticky by design.
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, in RuleInput) (Rule, error) {
	if err := in.validate(); err != nil {
		return Rule{}, err
	}
	specJSON, err := json.Marshal(in.Spec)
	if err != nil {
		return Rule{}, fmt.Errorf("tracecompletion: encode spec: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Rule{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE alert_rules
		SET integration_id = $3, name = $4, description = $5,
		    rule_spec = $6::jsonb, severity = $7::alert_severity,
		    enabled = $8, updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND signal = 'trace'
		  AND COALESCE(rule_spec->>'kind', '') <> 'trace_error'`,
		id, orgID, in.IntegrationID, in.Name, in.Description,
		specJSON, in.Severity, in.Enabled)
	if err != nil {
		return Rule{}, fmt.Errorf("tracecompletion: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Rule{}, ErrNotFound
	}
	if err := s.replaceChannelsTx(ctx, tx, id, in.ChannelIDs); err != nil {
		return Rule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Rule{}, err
	}
	return s.Get(ctx, orgID, id)
}

// Delete removes a rule. Per the FK on alert_instances + cascade,
// any historical firings stay (the FK is ON DELETE CASCADE — see
// 0001_initial.up.sql — so they'd actually go too; OK for v1).
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM alert_rules WHERE id = $1 AND organization_id = $2 AND signal = 'trace'
		  AND COALESCE(rule_spec->>'kind', '') <> 'trace_error'`,
		id, orgID)
	if err != nil {
		return fmt.Errorf("tracecompletion: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// replaceChannelsTx rewrites alert_rule_routes for one rule.
func (s *Store) replaceChannelsTx(ctx context.Context, tx pgx.Tx, ruleID uuid.UUID, channelIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM alert_rule_routes WHERE alert_rule_id = $1`, ruleID); err != nil {
		return fmt.Errorf("tracecompletion: clear routes: %w", err)
	}
	for _, cid := range channelIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO alert_rule_routes (alert_rule_id, channel_id) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`,
			ruleID, cid); err != nil {
			return fmt.Errorf("tracecompletion: insert route: %w", err)
		}
	}
	return nil
}

// Firing is one alert_instance row joined with the rule that owns it.
// Returned by ListFirings to drive the "delayed traces" panel on the
// integration settings page.
type Firing struct {
	InstanceID uuid.UUID
	RuleID     uuid.UUID
	RuleName   string
	// IntegrationID identifies the integration the rule belongs to.
	// A single trace can participate in multiple integrations; the
	// trace's "delayed" status is per-integration, so the UI needs
	// this to filter firings to the integration the user is looking
	// at right now.
	IntegrationID   uuid.UUID
	Severity        string // 'info' | 'warning' | 'critical' — inherited from the rule
	TraceID         string
	State           string // 'firing' (sticky) or 'resolved'
	StartedAt       time.Time
	LastEvaluatedAt time.Time
	EndedAt         *time.Time
	Summary         string
	// TraceStartedAt is when the trace itself began (parsed from the
	// labels map written by the evaluator). Distinct from StartedAt
	// which is when the FIRING opened — those usually differ by the
	// rule's timeout.
	TraceStartedAt *time.Time
	// HandledAt is set when an operator marked this delayed trace as
	// handled (e.g. the message was manually resent). A handled firing
	// stays state='firing' (so it's never re-fired) but no longer counts
	// as delayed and is rendered benign.
	HandledAt *time.Time
}

// ListFirings returns alert_instances for any trace-completion rule on
// the given integration whose firing opened within [from, to], newest
// first. Includes both 'firing' and 'resolved' so the UI can show a
// historical record of when SLAs were breached, not just the currently-
// open ones. The window matches the page's time selector so the list
// doesn't show all-time delays. Limit caps the result.
func (s *Store) ListFirings(ctx context.Context, orgID, integrationID uuid.UUID, from, to time.Time, limit int) ([]Firing, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT
		    i.id, r.id, r.name, r.integration_id, r.severity::text,
		    i.fingerprint, i.state::text,
		    i.started_at, i.last_evaluated_at, i.ended_at,
		    COALESCE(i.summary, ''),
		    i.labels->>'started_at' AS trace_started_at,
		    i.handled_at
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1
		  AND r.integration_id = $2
		  AND r.signal = 'trace'
		  AND COALESCE(r.rule_spec->>'kind', '') <> 'trace_error'
		  AND i.started_at >= $3 AND i.started_at <= $4
		ORDER BY i.started_at DESC
		LIMIT $5`
	rows, err := s.pool.Query(ctx, q, orgID, integrationID, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("tracecompletion: list firings: %w", err)
	}
	defer rows.Close()
	out := make([]Firing, 0)
	for rows.Next() {
		var f Firing
		var traceStarted *string
		if err := rows.Scan(
			&f.InstanceID, &f.RuleID, &f.RuleName, &f.IntegrationID, &f.Severity,
			&f.TraceID, &f.State,
			&f.StartedAt, &f.LastEvaluatedAt, &f.EndedAt,
			&f.Summary, &traceStarted, &f.HandledAt,
		); err != nil {
			return nil, err
		}
		if traceStarted != nil && *traceStarted != "" {
			if t, err := time.Parse(time.RFC3339Nano, *traceStarted); err == nil {
				f.TraceStartedAt = &t
			}
		}
		// fingerprint is the bare trace_id for legacy firings and
		// trace_id#stage for multi-stage ones; the wire/UI TraceID is
		// always the bare id.
		f.TraceID, _ = parseFingerprint(f.TraceID)
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListFiringsForTrace returns alert_instances opened on a specific
// trace (matched on the fingerprint column, which the evaluator
// populates with the trace_id). Used by the TraceDetail page to
// drive the trace-level StatusPip — a single firing in 'firing'
// state from a warning rule flips the trace to warn-styled.
//
// Scoped by org so a leaked trace_id can't surface firings from
// another tenant.
func (s *Store) ListFiringsForTrace(ctx context.Context, orgID uuid.UUID, traceID string) ([]Firing, error) {
	const q = `
		SELECT
		    i.id, r.id, r.name, r.integration_id, r.severity::text,
		    i.fingerprint, i.state::text,
		    i.started_at, i.last_evaluated_at, i.ended_at,
		    COALESCE(i.summary, ''),
		    i.labels->>'started_at' AS trace_started_at,
		    i.handled_at
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1
		  AND r.signal = 'trace'
		  AND COALESCE(r.rule_spec->>'kind', '') <> 'trace_error'
		  AND (i.fingerprint = $2 OR i.fingerprint LIKE $2 || '#%')
		ORDER BY i.started_at DESC`
	rows, err := s.pool.Query(ctx, q, orgID, traceID)
	if err != nil {
		return nil, fmt.Errorf("tracecompletion: list firings for trace: %w", err)
	}
	defer rows.Close()
	out := make([]Firing, 0)
	for rows.Next() {
		var f Firing
		var traceStarted *string
		if err := rows.Scan(
			&f.InstanceID, &f.RuleID, &f.RuleName, &f.IntegrationID, &f.Severity,
			&f.TraceID, &f.State,
			&f.StartedAt, &f.LastEvaluatedAt, &f.EndedAt,
			&f.Summary, &traceStarted, &f.HandledAt,
		); err != nil {
			return nil, err
		}
		if traceStarted != nil && *traceStarted != "" {
			if t, err := time.Parse(time.RFC3339Nano, *traceStarted); err == nil {
				f.TraceStartedAt = &t
			}
		}
		// fingerprint is the bare trace_id for legacy firings and
		// trace_id#stage for multi-stage ones; the wire/UI TraceID is
		// always the bare id.
		f.TraceID, _ = parseFingerprint(f.TraceID)
		out = append(out, f)
	}
	return out, rows.Err()
}

// OpenFiring identifies one still-open (state='firing') alert_instance
// by its trace and the pipeline stage it stalled at. Legacy firings
// (bare trace_id fingerprint) come back with Stage == 0, which the
// sweep treats as "the rule's final stage".
type OpenFiring struct {
	Fingerprint string // the raw alert_instances.fingerprint, used to resolve
	TraceID     string
	Stage       int
}

// OpenFirings returns every alert_instance in state='firing' for one
// rule, decomposed into (trace_id, stage). Used by the evaluator's
// sweep step to look up which traces+stages it should re-check against
// ClickHouse for late-arriving spans.
func (s *Store) OpenFirings(ctx context.Context, orgID, ruleID uuid.UUID) ([]OpenFiring, error) {
	const q = `
		SELECT i.fingerprint
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1
		  AND r.id = $2
		  AND r.signal = 'trace'
		  AND COALESCE(r.rule_spec->>'kind', '') <> 'trace_error'
		  AND i.state = 'firing'`
	rows, err := s.pool.Query(ctx, q, orgID, ruleID)
	if err != nil {
		return nil, fmt.Errorf("tracecompletion: open firings: %w", err)
	}
	defer rows.Close()
	out := make([]OpenFiring, 0)
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return nil, err
		}
		tid, stage := parseFingerprint(fp)
		out = append(out, OpenFiring{Fingerprint: fp, TraceID: tid, Stage: stage})
	}
	return out, rows.Err()
}

// OpenDelayedTraceCounts returns, per integration, the number of
// distinct traces currently delayed (an open 'firing' alert_instance on
// a trace-completion rule). This is the same notion the integration
// detail page's "delayed (open)" tile shows, so the dashboard stays
// consistent with it. Counts DISTINCT traces (not per-stage firings):
// fingerprints are "<trace_id>" (legacy) or "<trace_id>#<stage>", and
// split_part(...,'#',1) collapses a trace delayed at multiple stages to
// one. One query for the whole org.
func (s *Store) OpenDelayedTraceCounts(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]int, error) {
	const q = `
		SELECT r.integration_id,
		       COUNT(DISTINCT split_part(i.fingerprint, '#', 1)) AS delayed
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1
		  AND r.signal = 'trace'
		  AND COALESCE(r.rule_spec->>'kind', '') <> 'trace_error'
		  AND i.state = 'firing'
		  AND i.handled_at IS NULL
		GROUP BY r.integration_id`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("tracecompletion: open delayed counts: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]int)
	for rows.Next() {
		var integID uuid.UUID
		var n int
		if err := rows.Scan(&integID, &n); err != nil {
			return nil, err
		}
		out[integID] = n
	}
	return out, rows.Err()
}

// OpenDelayedTraceCount is the single-integration form of
// OpenDelayedTraceCounts.
func (s *Store) OpenDelayedTraceCount(ctx context.Context, orgID, integrationID uuid.UUID) (int, error) {
	const q = `
		SELECT COUNT(DISTINCT split_part(i.fingerprint, '#', 1))
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1
		  AND r.integration_id = $2
		  AND r.signal = 'trace'
		  AND COALESCE(r.rule_spec->>'kind', '') <> 'trace_error'
		  AND i.state = 'firing'
		  AND i.handled_at IS NULL`
	var n int
	if err := s.pool.QueryRow(ctx, q, orgID, integrationID).Scan(&n); err != nil {
		return 0, fmt.Errorf("tracecompletion: open delayed count: %w", err)
	}
	return n, nil
}

// OpenDelayedTraceIDs returns the distinct trace_ids currently delayed
// (open, unhandled firing) for one integration. Used to intersect with a
// time window so the success-rate "delayed" count is window-consistent
// (a sticky firing for a trace outside the window must not be counted
// against that window's traffic).
func (s *Store) OpenDelayedTraceIDs(ctx context.Context, orgID, integrationID uuid.UUID) ([]string, error) {
	const q = `
		SELECT DISTINCT split_part(i.fingerprint, '#', 1)
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1
		  AND r.integration_id = $2
		  AND r.signal = 'trace'
		  AND COALESCE(r.rule_spec->>'kind', '') <> 'trace_error'
		  AND i.state = 'firing'
		  AND i.handled_at IS NULL`
	rows, err := s.pool.Query(ctx, q, orgID, integrationID)
	if err != nil {
		return nil, fmt.Errorf("tracecompletion: open delayed trace ids: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			return nil, err
		}
		out = append(out, tid)
	}
	return out, rows.Err()
}

// OpenDelayedTraceIDsByIntegration is the org-wide form: integration_id →
// its open-delayed (unhandled) distinct trace_ids. One query for the
// dashboard's per-card success math.
func (s *Store) OpenDelayedTraceIDsByIntegration(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID][]string, error) {
	const q = `
		SELECT r.integration_id, split_part(i.fingerprint, '#', 1) AS trace_id
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1
		  AND r.signal = 'trace'
		  AND COALESCE(r.rule_spec->>'kind', '') <> 'trace_error'
		  AND i.state = 'firing'
		  AND i.handled_at IS NULL`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("tracecompletion: open delayed trace ids by integration: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID][]string{}
	seen := map[uuid.UUID]map[string]struct{}{}
	for rows.Next() {
		var integID uuid.UUID
		var tid string
		if err := rows.Scan(&integID, &tid); err != nil {
			return nil, err
		}
		if seen[integID] == nil {
			seen[integID] = map[string]struct{}{}
		}
		if _, dup := seen[integID][tid]; dup {
			continue
		}
		seen[integID][tid] = struct{}{}
		out[integID] = append(out[integID], tid)
	}
	return out, rows.Err()
}

// HandleFiring marks a delayed trace's firing as operator-handled (e.g.
// the message was manually resent). The instance stays state='firing'
// so the evaluator never re-fires the same delay, but handled_at is set
// so it no longer counts as delayed anywhere. Scoped to the org +
// integration + a trace rule. Returns ErrNotFound if there is no
// matching, still-open, not-yet-handled firing.
func (s *Store) HandleFiring(ctx context.Context, orgID, integrationID, instanceID uuid.UUID) error {
	const q = `
		UPDATE alert_instances AS i
		SET handled_at = now()
		FROM alert_rules r
		WHERE i.alert_rule_id = r.id
		  AND i.id = $3
		  AND r.organization_id = $1
		  AND r.integration_id = $2
		  AND r.signal = 'trace'
		  AND COALESCE(r.rule_spec->>'kind', '') <> 'trace_error'
		  AND i.state = 'firing'
		  AND i.handled_at IS NULL`
	tag, err := s.pool.Exec(ctx, q, orgID, integrationID, instanceID)
	if err != nil {
		return fmt.Errorf("tracecompletion: handle firing: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── error sentinels ──────────────────────────────────────────────────

var ErrNotFound = errors.New("tracecompletion: rule not found")
