// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

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

// ErrNotFound is returned when a rule or channel reference is missing.
var ErrNotFound = errors.New("alerting: not found")

// Store is the Postgres-backed CRUD layer for alert rules + channels.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const ruleCols = `id, organization_id, integration_id, COALESCE(service_name, ''), group_id, name, COALESCE(description, ''),
	signal, rule_spec, severity,
	EXTRACT(EPOCH FROM evaluation_interval)::bigint, enabled,
	COALESCE(title_template, ''), COALESCE(body_template, ''), created_at, updated_at,
	COALESCE(source, 'telemetry'), display_on_service, COALESCE(unit, ''), COALESCE(resolve_mode, 'auto'),
	notification_config`

// notifConfigJSON marshals a rule's notification config for the jsonb column;
// nil → SQL NULL (no per-rule config).
func notifConfigJSON(nc *NotificationContent) any {
	if nc == nil {
		return nil
	}
	b, err := json.Marshal(nc)
	if err != nil {
		return nil
	}
	return b
}

func scanRule(row pgx.Row) (AlertRule, error) {
	var (
		r       AlertRule
		intg    uuid.NullUUID
		grp     uuid.NullUUID
		specRaw []byte
		ncRaw   []byte
	)
	if err := row.Scan(
		&r.ID, &r.OrganizationID, &intg, &r.ServiceName, &grp, &r.Name, &r.Description,
		&r.Signal, &specRaw, &r.Severity,
		&r.EvalSeconds, &r.Enabled,
		&r.TitleTemplate, &r.BodyTemplate, &r.CreatedAt, &r.UpdatedAt,
		&r.Source, &r.DisplayOnService, &r.Unit, &r.ResolveMode,
		&ncRaw,
	); err != nil {
		return AlertRule{}, err
	}
	if len(ncRaw) > 0 {
		var nc NotificationContent
		if json.Unmarshal(ncRaw, &nc) == nil {
			r.NotificationContent = &nc
		}
	}
	if intg.Valid {
		id := intg.UUID
		r.IntegrationID = &id
	}
	if grp.Valid {
		id := grp.UUID
		r.GroupID = &id
	}
	if len(specRaw) > 0 {
		// rule_spec shape depends on the signal: metric rules carry a
		// MetricRuleSpec, log rules a LogRuleSpec, failed-trace rules a
		// TraceErrorRuleSpec. signal='trace' is SHARED with trace-COMPLETION
		// rules (a separate feature) — so only treat the row as failed-trace
		// when its spec carries kind=TraceErrorSpecKind. A completion row
		// scanned here leaves all specs nil; alerting management lists
		// filter such rows out (see ListRules), so this is just defensive.
		switch r.Signal {
		case SignalLog:
			var ls LogRuleSpec
			_ = json.Unmarshal(specRaw, &ls)
			r.LogSpec = &ls
		case SignalTraceError:
			var probe struct {
				Kind string `json:"kind"`
			}
			_ = json.Unmarshal(specRaw, &probe)
			switch probe.Kind {
			case TraceLatencySpecKind:
				var ls TraceLatencyRuleSpec
				_ = json.Unmarshal(specRaw, &ls)
				r.TraceLatencySpec = &ls
			case TraceErrorSpecKind:
				var ts TraceErrorRuleSpec
				_ = json.Unmarshal(specRaw, &ts)
				r.TraceErrorSpec = &ts
			case TraceVolumeSpecKind:
				var vs TraceVolumeRuleSpec
				_ = json.Unmarshal(specRaw, &vs)
				r.TraceVolumeSpec = &vs
			}
		default:
			_ = json.Unmarshal(specRaw, &r.Spec)
		}
	}
	r.ChannelIDs = []uuid.UUID{}
	return r, nil
}

// ListRules returns every rule in the org, newest first, with routed
// channel IDs attached. Trace-COMPLETION rules (signal='trace' without a
// failed-trace kind tag) are excluded — they're a distinct rule type with
// their own per-integration management surface, and rendering them here
// would show a multi-stage SLA spec as a garbage metric/failed-trace row.
func (s *Store) ListRules(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+`
		FROM alert_rules
		WHERE organization_id = $1
		  AND (signal <> 'trace' OR rule_spec->>'kind' IN ('`+TraceErrorSpecKind+`', '`+TraceLatencySpecKind+`', '`+TraceVolumeSpecKind+`'))
		ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}
	defer rows.Close()
	var out []AlertRule
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
	if err := s.attachChannelIDs(ctx, orgID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachChannelIDs loads the routed channel IDs for a set of rules in
// one query and appends them onto each rule, scoped to the org.
func (s *Store) attachChannelIDs(ctx context.Context, orgID uuid.UUID, rules []AlertRule) error {
	if len(rules) == 0 {
		return nil
	}
	byID := make(map[uuid.UUID]int, len(rules))
	for i := range rules {
		byID[rules[i].ID] = i
	}
	rr, err := s.pool.Query(ctx, `
		SELECT rt.alert_rule_id, rt.channel_id
		FROM alert_rule_routes rt
		JOIN alert_rules a ON a.id = rt.alert_rule_id
		WHERE a.organization_id = $1`, orgID)
	if err != nil {
		return fmt.Errorf("list alert routes: %w", err)
	}
	defer rr.Close()
	for rr.Next() {
		var ruleID, chID uuid.UUID
		if err := rr.Scan(&ruleID, &chID); err != nil {
			return err
		}
		if i, ok := byID[ruleID]; ok {
			rules[i].ChannelIDs = append(rules[i].ChannelIDs, chID)
		}
	}
	return rr.Err()
}

// GetRule returns one rule with its routed channels.
func (s *Store) GetRule(ctx context.Context, orgID, id uuid.UUID) (AlertRule, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+ruleCols+`
		FROM alert_rules WHERE organization_id = $1 AND id = $2`, orgID, id)
	r, err := scanRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertRule{}, ErrNotFound
	}
	if err != nil {
		return AlertRule{}, fmt.Errorf("get alert rule: %w", err)
	}
	r.ChannelIDs, err = s.channelIDsFor(ctx, id)
	if err != nil {
		return AlertRule{}, err
	}
	return r, nil
}

func (s *Store) channelIDsFor(ctx context.Context, ruleID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT channel_id FROM alert_rule_routes WHERE alert_rule_id = $1`, ruleID)
	if err != nil {
		return nil, fmt.Errorf("channel ids: %w", err)
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ruleSignal returns the rule's signal, defaulting to "metric".
func ruleSignal(r AlertRule) string {
	switch r.Signal {
	case SignalLog, SignalTraceError:
		return r.Signal
	}
	return SignalMetric
}

// ruleSpecJSON marshals the rule_spec for the rule's signal: the
// LogRuleSpec for log rules, the TraceErrorRuleSpec for trace_error
// rules, the MetricRuleSpec otherwise.
func ruleSpecJSON(r AlertRule) ([]byte, error) {
	switch {
	case r.Signal == SignalLog && r.LogSpec != nil:
		return json.Marshal(*r.LogSpec)
	case r.Signal == SignalTraceError && r.TraceLatencySpec != nil:
		return json.Marshal(*r.TraceLatencySpec)
	case r.Signal == SignalTraceError && r.TraceVolumeSpec != nil:
		return json.Marshal(*r.TraceVolumeSpec)
	case r.Signal == SignalTraceError && r.TraceErrorSpec != nil:
		return json.Marshal(*r.TraceErrorSpec)
	}
	return json.Marshal(r.Spec)
}

// ruleSource returns the stored value source, defaulting to telemetry.
// Only metric rules may be pushed; log rules are always telemetry.
func ruleSource(r AlertRule) string {
	if r.Source == SourcePushed && ruleSignal(r) == "metric" {
		return string(SourcePushed)
	}
	return string(SourceTelemetry)
}

// resolveModeOf normalises a rule's resolve mode for storage: "manual"
// (firing until acknowledged) or, for anything else/empty, "auto"
// (self-recovering when the condition clears).
func resolveModeOf(r AlertRule) string {
	if r.ResolveMode == ResolveManual {
		return ResolveManual
	}
	return ResolveAuto
}

// CreateRule inserts a rule (metric or log) and its channel routes in
// one transaction.
func (s *Store) CreateRule(ctx context.Context, r AlertRule) (AlertRule, error) {
	spec, err := ruleSpecJSON(r)
	if err != nil {
		return AlertRule{}, fmt.Errorf("marshal rule_spec: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AlertRule{}, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		INSERT INTO alert_rules (organization_id, integration_id, service_name, group_id, name, description, signal, rule_spec, severity, enabled, title_template, body_template, source, display_on_service, unit, resolve_mode, notification_config)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING `+ruleCols,
		r.OrganizationID, uuidArg(r.IntegrationID), nilIfEmpty(r.ServiceName), uuidArg(r.GroupID), r.Name, nilIfEmpty(r.Description), ruleSignal(r), spec, string(r.Severity), r.Enabled, nilIfEmpty(r.TitleTemplate), nilIfEmpty(r.BodyTemplate), ruleSource(r), r.DisplayOnService, nilIfEmpty(r.Unit), resolveModeOf(r), notifConfigJSON(r.NotificationContent))
	created, err := scanRule(row)
	if err != nil {
		return AlertRule{}, fmt.Errorf("insert alert rule: %w", err)
	}
	if err := replaceRoutes(ctx, tx, created.ID, r.ChannelIDs); err != nil {
		return AlertRule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AlertRule{}, err
	}
	created.ChannelIDs = dedupe(r.ChannelIDs)
	return created, nil
}

// UpdateRule replaces the mutable fields + routes of a rule.
func (s *Store) UpdateRule(ctx context.Context, orgID uuid.UUID, r AlertRule) (AlertRule, error) {
	spec, err := ruleSpecJSON(r)
	if err != nil {
		return AlertRule{}, fmt.Errorf("marshal rule_spec: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AlertRule{}, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		UPDATE alert_rules
		SET name = $3, description = $4, rule_spec = $5, severity = $6, enabled = $7, integration_id = $8, service_name = $9, group_id = $10, title_template = $11, body_template = $12, source = $13, display_on_service = $14, unit = $15, resolve_mode = $16, notification_config = $17, updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING `+ruleCols,
		orgID, r.ID, r.Name, nilIfEmpty(r.Description), spec, string(r.Severity), r.Enabled, uuidArg(r.IntegrationID), nilIfEmpty(r.ServiceName), uuidArg(r.GroupID), nilIfEmpty(r.TitleTemplate), nilIfEmpty(r.BodyTemplate), ruleSource(r), r.DisplayOnService, nilIfEmpty(r.Unit), resolveModeOf(r), notifConfigJSON(r.NotificationContent))
	updated, err := scanRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertRule{}, ErrNotFound
	}
	if err != nil {
		return AlertRule{}, fmt.Errorf("update alert rule: %w", err)
	}
	if err := replaceRoutes(ctx, tx, updated.ID, r.ChannelIDs); err != nil {
		return AlertRule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AlertRule{}, err
	}
	updated.ChannelIDs = dedupe(r.ChannelIDs)
	return updated, nil
}

// DeleteRule removes a rule (cascade drops its routes/instances).
func (s *Store) DeleteRule(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM alert_rules WHERE organization_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("delete alert rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func replaceRoutes(ctx context.Context, tx pgx.Tx, ruleID uuid.UUID, channelIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM alert_rule_routes WHERE alert_rule_id = $1`, ruleID); err != nil {
		return fmt.Errorf("clear routes: %w", err)
	}
	for _, ch := range dedupe(channelIDs) {
		if _, err := tx.Exec(ctx,
			`INSERT INTO alert_rule_routes (alert_rule_id, channel_id) VALUES ($1, $2)`, ruleID, ch); err != nil {
			return fmt.Errorf("insert route: %w", err)
		}
	}
	return nil
}

// EnabledMetricRules returns the enabled metric-signal rules in the org,
// used by the evaluator and the catalog rule summaries.
func (s *Store) EnabledLogRules(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+`
		FROM alert_rules WHERE organization_id = $1 AND enabled AND signal = 'log'`, orgID)
	if err != nil {
		return nil, fmt.Errorf("enabled log rules: %w", err)
	}
	defer rows.Close()
	var out []AlertRule
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
	if err := s.attachChannelIDs(ctx, orgID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// EnabledTraceErrorRules returns the enabled failed-trace rules in the
// org — the ones the trace-error evaluator counts failed traces for. The
// kind filter is essential: signal='trace' is shared with trace-COMPLETION
// rules, and without it those would be evaluated here as threshold-0
// failed-trace rules and fire constantly.
func (s *Store) EnabledTraceErrorRules(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+`
		FROM alert_rules
		WHERE organization_id = $1 AND enabled
		  AND signal = 'trace' AND rule_spec->>'kind' = '`+TraceErrorSpecKind+`'`, orgID)
	if err != nil {
		return nil, fmt.Errorf("enabled trace_error rules: %w", err)
	}
	defer rows.Close()
	var out []AlertRule
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
	if err := s.attachChannelIDs(ctx, orgID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// EnabledTraceLatencyRules returns the enabled response-time (latency)
// trace rules — signal='trace' with the trace_latency kind tag.
func (s *Store) EnabledTraceLatencyRules(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+`
		FROM alert_rules
		WHERE organization_id = $1 AND enabled
		  AND signal = 'trace' AND rule_spec->>'kind' = '`+TraceLatencySpecKind+`'`, orgID)
	if err != nil {
		return nil, fmt.Errorf("enabled trace_latency rules: %w", err)
	}
	defer rows.Close()
	var out []AlertRule
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
	if err := s.attachChannelIDs(ctx, orgID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// EnabledTraceVolumeRules returns the enabled low-traffic (volume) trace
// rules — signal='trace' with the trace_volume kind tag.
func (s *Store) EnabledTraceVolumeRules(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+`
		FROM alert_rules
		WHERE organization_id = $1 AND enabled
		  AND signal = 'trace' AND rule_spec->>'kind' = '`+TraceVolumeSpecKind+`'`, orgID)
	if err != nil {
		return nil, fmt.Errorf("enabled trace_volume rules: %w", err)
	}
	defer rows.Close()
	var out []AlertRule
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
	if err := s.attachChannelIDs(ctx, orgID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// EnabledMetricRules returns the enabled telemetry-sourced metric rules:
// the ones the metric evaluator aggregates from ClickHouse (and the
// explorer summarises). Pushed rules are evaluated separately —
// see EnabledPushedRules.
func (s *Store) EnabledMetricRules(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error) {
	return s.enabledMetricRulesBySource(ctx, orgID, SourceTelemetry)
}

// EnabledPushedRules returns the enabled pushed-value metric rules: the
// ones the pushed evaluator compares against their latest pushed reading.
func (s *Store) EnabledPushedRules(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error) {
	return s.enabledMetricRulesBySource(ctx, orgID, SourcePushed)
}

func (s *Store) enabledMetricRulesBySource(ctx context.Context, orgID uuid.UUID, source Source) ([]AlertRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+`
		FROM alert_rules WHERE organization_id = $1 AND enabled AND signal = 'metric'
		  AND COALESCE(source, 'telemetry') = $2`, orgID, string(source))
	if err != nil {
		return nil, fmt.Errorf("enabled metric rules: %w", err)
	}
	defer rows.Close()
	var out []AlertRule
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
	// Routes too — the evaluator needs each rule's channels to enqueue.
	if err := s.attachChannelIDs(ctx, orgID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// RecordReading upserts the latest numeric reading for a rule: the value
// the evaluator just computed (telemetry) or a value pushed by a scraper.
func (s *Store) RecordReading(ctx context.Context, ruleID uuid.UUID, value float64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO alert_rule_readings (alert_rule_id, value, observed_at)
		VALUES ($1, $2, now())
		ON CONFLICT (alert_rule_id)
		DO UPDATE SET value = EXCLUDED.value, observed_at = EXCLUDED.observed_at`, ruleID, value)
	if err != nil {
		return fmt.Errorf("record reading: %w", err)
	}
	return nil
}

// LatestReading returns the most recent reading for a rule, or nil if
// none has been recorded yet.
func (s *Store) LatestReading(ctx context.Context, ruleID uuid.UUID) (*Reading, error) {
	var rd Reading
	err := s.pool.QueryRow(ctx,
		`SELECT value, observed_at FROM alert_rule_readings WHERE alert_rule_id = $1`, ruleID).
		Scan(&rd.Value, &rd.ObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest reading: %w", err)
	}
	return &rd, nil
}

// ServiceReadings returns the "show on service page" health checks bound
// to one service, each with its latest reading and breach state — the
// read model behind the service-page value tiles.
func (s *Store) ServiceReadings(ctx context.Context, orgID uuid.UUID, serviceName string) ([]ServiceReading, error) {
	const q = `
		SELECT r.id, r.name, COALESCE(r.unit, ''), COALESCE(r.source, 'telemetry'),
		       r.rule_spec, ar.value, ar.observed_at
		FROM alert_rules r
		LEFT JOIN alert_rule_readings ar ON ar.alert_rule_id = r.id
		WHERE r.organization_id = $1 AND r.service_name = $2
		  AND r.display_on_service AND r.signal = 'metric'
		ORDER BY r.name`
	rows, err := s.pool.Query(ctx, q, orgID, serviceName)
	if err != nil {
		return nil, fmt.Errorf("service readings: %w", err)
	}
	defer rows.Close()
	out := []ServiceReading{}
	for rows.Next() {
		var (
			sr      ServiceReading
			specRaw []byte
			val     *float64
			obs     *time.Time
		)
		if err := rows.Scan(&sr.RuleID, &sr.Name, &sr.Unit, &sr.Source, &specRaw, &val, &obs); err != nil {
			return nil, err
		}
		var spec MetricRuleSpec
		if len(specRaw) > 0 {
			_ = json.Unmarshal(specRaw, &spec)
		}
		sr.Operator = spec.Operator
		sr.Threshold = spec.Threshold
		if val != nil {
			sr.Value = val
			sr.ObservedAt = obs
			sr.HasValue = true
			sr.Breached = EvaluateBreach(spec.Operator, *val, spec.Threshold)
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// MetricRuleSummaries groups the enabled metric rules by metric name:
// count, tightest threshold to draw, and the most severe severity.
func (s *Store) MetricRuleSummaries(ctx context.Context, orgID uuid.UUID) (map[string]MetricRuleSummary, error) {
	rules, err := s.EnabledMetricRules(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := map[string]MetricRuleSummary{}
	for _, r := range rules {
		name := r.Spec.MetricName
		if name == "" {
			continue
		}
		cur, ok := out[name]
		if !ok {
			out[name] = MetricRuleSummary{Count: 1, Threshold: r.Spec.Threshold, Severity: r.Severity}
			continue
		}
		cur.Count++
		// Prefer the more severe rule's threshold; tie-break to the lower
		// threshold (the easier-to-breach line to draw).
		if severityRank(r.Severity) > severityRank(cur.Severity) ||
			(severityRank(r.Severity) == severityRank(cur.Severity) && r.Spec.Threshold < cur.Threshold) {
			cur.Threshold = r.Spec.Threshold
			cur.Severity = r.Severity
		}
		out[name] = cur
	}
	return out, nil
}

// FiringHealthServices returns the set of service names that currently
// have a firing alert rule bound to them — the service analogue of
// FiringHealthIntegrations, so a metric formula can define a single
// service's healthy state.
func (s *Store) FiringHealthServices(ctx context.Context, orgID uuid.UUID) (map[string]bool, error) {
	const q = `
		SELECT DISTINCT r.service_name
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1 AND i.state = 'firing'
		  AND r.service_name IS NOT NULL AND r.service_name <> ''`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("firing health services: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// FiringHealthIntegrations returns the set of integration IDs that
// currently have a firing health check bound directly to the integration
// (a metric/pushed/log/failed-trace rule with integration_id set). This is
// the integration analogue of FiringHealthServices: an integration-scoped
// firing check makes the integration unhealthy on its own, independent of
// its member services' statuses.
//
// Trace-COMPLETION (delayed-trace) firings are EXCLUDED — they share
// signal='trace' but represent "delayed", a separate dimension already
// surfaced via the delayed-trace count (statusWithDelays), not "unhealthy".
// The kind predicate mirrors ListRules so the two stay consistent.
func (s *Store) FiringHealthIntegrations(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]bool, error) {
	const q = `
		SELECT DISTINCT r.integration_id
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1 AND i.state = 'firing'
		  AND r.integration_id IS NOT NULL
		  AND (r.signal <> 'trace' OR r.rule_spec->>'kind' IN ('` + TraceErrorSpecKind + `', '` + TraceLatencySpecKind + `', '` + TraceVolumeSpecKind + `'))`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("firing health integrations: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// --- notification channels ---------------------------------------------

const channelCols = `id, organization_id, name, kind, config, created_at, updated_at`

func scanChannel(row pgx.Row) (NotificationChannel, error) {
	var (
		c   NotificationChannel
		cfg []byte
	)
	if err := row.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Kind, &cfg, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return NotificationChannel{}, err
	}
	c.Config = map[string]string{}
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &c.Config)
	}
	return c, nil
}

// ListChannels returns every channel in the org, by name.
func (s *Store) ListChannels(ctx context.Context, orgID uuid.UUID) ([]NotificationChannel, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+channelCols+`
		FROM notification_channels WHERE organization_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()
	var out []NotificationChannel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChannel returns one channel by ID.
func (s *Store) GetChannel(ctx context.Context, orgID, id uuid.UUID) (NotificationChannel, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+channelCols+`
		FROM notification_channels WHERE organization_id = $1 AND id = $2`, orgID, id)
	c, err := scanChannel(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationChannel{}, ErrNotFound
	}
	if err != nil {
		return NotificationChannel{}, fmt.Errorf("get channel: %w", err)
	}
	return c, nil
}

// CreateChannel inserts a notification channel.
func (s *Store) CreateChannel(ctx context.Context, c NotificationChannel) (NotificationChannel, error) {
	cfg, err := json.Marshal(c.Config)
	if err != nil {
		return NotificationChannel{}, fmt.Errorf("marshal config: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO notification_channels (organization_id, name, kind, config)
		VALUES ($1, $2, $3, $4)
		RETURNING `+channelCols, c.OrganizationID, c.Name, c.Kind, cfg)
	return scanChannel(row)
}

// UpdateChannel changes a channel's name/kind/config.
func (s *Store) UpdateChannel(ctx context.Context, orgID uuid.UUID, c NotificationChannel) (NotificationChannel, error) {
	cfg, err := json.Marshal(c.Config)
	if err != nil {
		return NotificationChannel{}, fmt.Errorf("marshal config: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE notification_channels SET name = $3, kind = $4, config = $5, updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING `+channelCols, orgID, c.ID, c.Name, c.Kind, cfg)
	updated, err := scanChannel(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationChannel{}, ErrNotFound
	}
	if err != nil {
		return NotificationChannel{}, fmt.Errorf("update channel: %w", err)
	}
	return updated, nil
}

// DeleteChannel removes a channel (cascade drops its routes + jobs).
func (s *Store) DeleteChannel(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notification_channels WHERE organization_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- helpers -----------------------------------------------------------

func uuidArg(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return *id
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]struct{}{}
	out := []uuid.UUID{}
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
