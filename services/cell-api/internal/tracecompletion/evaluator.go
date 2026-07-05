// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tracecompletion

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// Evaluator periodically classifies recent traces per integration rule
// and fires new alert instances for any newly-delayed ones, per
// pipeline stage. Self-healing: once a delayed stage's span eventually
// arrives, the firing for that (trace, stage) is resolved on the next
// tick by the sweep, which the UI renders as "delivered with delay".
type Evaluator struct {
	rules    *Store
	alerts   AlertingStore // narrow interface (see below)
	catalog  CatalogStore  // integration → service names lookup
	ch       driver.Conn
	orgID    uuid.UUID
	logger   *slog.Logger
	interval time.Duration
}

// AlertingStore is the slice of the alerting.Store the evaluator needs.
// Defined here as an interface so tracecompletion doesn't import the
// alerting package directly (smaller blast radius if the alerting types
// churn).
type AlertingStore interface {
	OpenInstance(ctx context.Context, ruleID uuid.UUID, fingerprint string,
		labels map[string]string, summary string) (AlertInstance, error)
	TouchInstance(ctx context.Context, id uuid.UUID) error
	EnqueueJobs(ctx context.Context, instanceID uuid.UUID, channelIDs []uuid.UUID) error
	ActiveInstanceByFingerprint(ctx context.Context, ruleID uuid.UUID, fingerprint string) (*AlertInstance, error)
	// ResolveInstance closes a firing — used when a delayed stage's span
	// finally arrives, so the historical record captures "delivered with
	// delay" rather than staying open.
	ResolveInstance(ctx context.Context, id uuid.UUID, summary string) error
}

// CatalogStore is the slice of the catalog.Store we need.
type CatalogStore interface {
	IntegrationServices(ctx context.Context, integrationID uuid.UUID) ([]string, error)
}

// AlertInstance is the local shadow of alerting.AlertInstance so the
// interface above doesn't drag the whole alerting package in.
type AlertInstance struct {
	ID uuid.UUID
}

// New wires the dependencies. interval=0 picks a sane default (30s).
// Faster than the metric evaluator's 1m because trace-completion is
// time-sensitive — a 30s SLA means we'd otherwise be up to 60s late
// reporting a breach.
func New(rules *Store, alerts AlertingStore, catalog CatalogStore, ch driver.Conn,
	orgID uuid.UUID, logger *slog.Logger, interval time.Duration) *Evaluator {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Evaluator{
		rules: rules, alerts: alerts, catalog: catalog, ch: ch,
		orgID: orgID, logger: logger, interval: interval,
	}
}

// Run loops until ctx is cancelled. Evaluates once on start so the first
// tick doesn't wait the full interval.
func (e *Evaluator) Run(ctx context.Context) {
	if err := e.EvaluateOnce(ctx); err != nil {
		e.logger.Warn("tracecompletion initial evaluate failed", "err", err)
	}
	t := time.NewTicker(e.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.EvaluateOnce(ctx); err != nil {
				e.logger.Warn("tracecompletion evaluate failed", "err", err)
			}
		}
	}
}

// EvaluateOnce processes every enabled rule, firing newly-delayed
// (trace, stage) pairs, then sweeps still-open firings against
// ClickHouse to resolve any whose stage span has since arrived (in or
// out of the lookback window). Per-rule failures are logged but don't
// stop the loop.
func (e *Evaluator) EvaluateOnce(ctx context.Context) error {
	rules, err := e.rules.EnabledForEval(ctx, e.orgID)
	if err != nil {
		return fmt.Errorf("tracecompletion: load rules: %w", err)
	}
	for _, rule := range rules {
		if err := e.evaluateRule(ctx, rule); err != nil {
			e.logger.Warn("tracecompletion: rule eval failed",
				"rule_id", rule.ID, "integration_id", rule.IntegrationID, "err", err)
		}
	}
	if err := e.sweepStaleFirings(ctx, rules); err != nil {
		e.logger.Warn("tracecompletion: stale-firings sweep failed", "err", err)
	}
	return nil
}

// evaluateRule runs one rule's ClickHouse query and opens (or touches)
// alert instances for any trace currently delayed at one of its stages.
// Resolution is handled centrally by sweepStaleFirings.
func (e *Evaluator) evaluateRule(ctx context.Context, rule Rule) error {
	if err := rule.Spec.Validate(); err != nil {
		return err
	}
	services, err := e.catalog.IntegrationServices(ctx, rule.IntegrationID)
	if err != nil {
		return fmt.Errorf("tracecompletion: integration services: %w", err)
	}
	if len(services) == 0 {
		// Integration has no resolved services in the catalog yet. Nothing to do.
		return nil
	}

	now := time.Now().UTC()
	delayed, counts, err := e.scanAndBucket(ctx, rule.Spec, services, now, now.Add(-rule.Spec.Lookback()), now)
	if err != nil {
		return err
	}
	e.logger.Debug("tracecompletion: classified",
		"rule_id", rule.ID,
		"completed", counts.Completed, "pending", counts.Pending, "delayed", counts.Delayed)

	for _, c := range delayed {
		if err := e.fireDelayed(ctx, rule, c); err != nil {
			e.logger.Warn("tracecompletion: fire failed",
				"rule_id", rule.ID, "trace_id", c.TraceID, "stage", c.DelayedStage, "err", err)
		}
	}
	return nil
}

// sweepStaleFirings resolves firings whose delayed stage's span has
// since arrived. For each rule we group its open firings by stage, ask
// ClickHouse "of these trace_ids, which have one of stage k's span
// names?" (one query per stage, bounded by the small number of open
// firings), and resolve the matching (trace, stage) instances. This
// catches both in-window late arrivals and ones that completed after
// aging out of the lookback window. The CH bloom filter on TraceId
// keeps it cheap even unbounded by Timestamp.
func (e *Evaluator) sweepStaleFirings(ctx context.Context, rules []Rule) error {
	for _, rule := range rules {
		stages := rule.Spec.EffectiveStages()
		if len(stages) == 0 {
			continue
		}
		open, err := e.rules.OpenFirings(ctx, e.orgID, rule.ID)
		if err != nil {
			return fmt.Errorf("list open firings for rule %s: %w", rule.ID, err)
		}
		if len(open) == 0 {
			continue
		}
		// Group open firings by the stage they stalled at. Legacy bare
		// fingerprints (Stage == 0) map to the final stage.
		byStage := map[int][]OpenFiring{}
		for _, of := range open {
			stage := of.Stage
			if stage == 0 {
				stage = len(stages)
			}
			if stage < 1 || stage > len(stages) {
				continue
			}
			byStage[stage] = append(byStage[stage], of)
		}
		for stage, firings := range byStage {
			traceIDs := make([]string, 0, len(firings))
			for _, of := range firings {
				traceIDs = append(traceIDs, of.TraceID)
			}
			closed, err := e.findClosedTraces(ctx, stages[stage-1].SpanNames, traceIDs)
			if err != nil {
				e.logger.Warn("tracecompletion: closed-traces query failed",
					"rule_id", rule.ID, "stage", stage, "err", err)
				continue
			}
			for _, of := range firings {
				closedAt, ok := closed[of.TraceID]
				if !ok {
					continue
				}
				if err := e.resolveStageFiring(ctx, rule, of.Fingerprint, of.TraceID, stage, closedAt); err != nil {
					e.logger.Warn("tracecompletion: late resolve failed",
						"rule_id", rule.ID, "trace_id", of.TraceID, "stage", stage, "err", err)
				}
			}
		}
	}
	return nil
}

// findClosedTraces returns, for the given trace_ids, the ones that have
// at least one span whose name is in spanNames anywhere in the traces
// table — regardless of when the trace started.
func (e *Evaluator) findClosedTraces(ctx context.Context, spanNames, traceIDs []string) (map[string]time.Time, error) {
	if len(traceIDs) == 0 || len(spanNames) == 0 {
		return nil, nil
	}
	q := fmt.Sprintf(`
		SELECT TraceId, max(Timestamp) AS closed_at
		FROM traces
		WHERE TraceId IN (%s)
		  AND SpanName IN (%s)
		GROUP BY TraceId
	`, qPlaceholders(len(traceIDs)), qPlaceholders(len(spanNames)))
	args := make([]any, 0, len(traceIDs)+len(spanNames))
	for _, t := range traceIDs {
		args = append(args, t)
	}
	for _, n := range spanNames {
		args = append(args, n)
	}
	rows, err := e.ch.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse query: %w", err)
	}
	defer rows.Close()
	out := make(map[string]time.Time, len(traceIDs))
	for rows.Next() {
		var tid string
		var closedAt time.Time
		if err := rows.Scan(&tid, &closedAt); err != nil {
			return nil, err
		}
		out[tid] = closedAt
	}
	return out, rows.Err()
}

// resolveStageFiring closes one open (trace, stage) firing once that
// stage's span arrives. No-op if there's no active firing for the
// fingerprint.
func (e *Evaluator) resolveStageFiring(ctx context.Context, rule Rule, fingerprint, traceID string, stage int, closedAt time.Time) error {
	active, err := e.alerts.ActiveInstanceByFingerprint(ctx, rule.ID, fingerprint)
	if err != nil {
		return fmt.Errorf("active instance lookup: %w", err)
	}
	if active == nil {
		return nil
	}
	stages := rule.Spec.EffectiveStages()
	stageName := ""
	timeout := 0
	if stage >= 1 && stage <= len(stages) {
		stageName = strings.Join(stages[stage-1].SpanNames, " / ")
		timeout = stages[stage-1].TimeoutSeconds
	}
	summary := fmt.Sprintf(
		"Trace %s on integration %s eventually reached stage %d (%s) at %s — delivered with delay (SLA %ds)",
		short(traceID), rule.Name, stage, stageName, closedAt.UTC().Format(time.RFC3339), timeout,
	)
	e.logger.Info("tracecompletion: stage delivered with delay",
		"rule_id", rule.ID, "trace_id", traceID, "stage", stage, "closed_at", closedAt)
	if err := e.alerts.ResolveInstance(ctx, active.ID, summary); err != nil {
		return fmt.Errorf("resolve instance: %w", err)
	}
	return nil
}

// fireDelayed handles one trace delayed at a specific stage. If we've
// already opened an instance for this (trace, stage) we just touch it
// (sticky). If it's new, we open the instance + enqueue notification
// jobs for every configured channel.
func (e *Evaluator) fireDelayed(ctx context.Context, rule Rule, c TraceClassification) error {
	// One instance per (trace, stage): a trace that breaches stage 1,
	// advances, then stalls at stage 2 produces two distinct firings.
	fp := stageFingerprint(c.TraceID, c.DelayedStage)

	active, err := e.alerts.ActiveInstanceByFingerprint(ctx, rule.ID, fp)
	if err != nil {
		return fmt.Errorf("active instance lookup: %w", err)
	}
	if active != nil {
		// Already firing for this stage. Sticky: keep the heartbeat alive.
		return e.alerts.TouchInstance(ctx, active.ID)
	}

	labels := map[string]string{
		"integration_id": rule.IntegrationID.String(),
		"trace_id":       c.TraceID,
		"rule_kind":      "trace_completion",
		"stage_index":    strconv.Itoa(c.DelayedStage),
		"stage_name":     c.DelayedStageName,
		"start_span":     rule.Spec.StartSpanName,
		"closing_spans":  c.DelayedStageName,
		// started_at is the moment the breached stage's clock started
		// (the prior stage's / start span's timestamp).
		"started_at": c.StartedAt.UTC().Format(time.RFC3339Nano),
	}
	since := time.Since(c.StartedAt).Round(time.Second)
	summary := fmt.Sprintf(
		"Trace %s on integration %s is delayed at stage %d: waited %s with no '%s' span",
		short(c.TraceID), rule.Name, c.DelayedStage, since, c.DelayedStageName,
	)

	inst, err := e.alerts.OpenInstance(ctx, rule.ID, fp, labels, summary)
	if err != nil {
		return fmt.Errorf("open instance: %w", err)
	}
	if len(rule.ChannelIDs) > 0 {
		if err := e.alerts.EnqueueJobs(ctx, inst.ID, rule.ChannelIDs); err != nil {
			return fmt.Errorf("enqueue jobs: %w", err)
		}
	}
	e.logger.Info("tracecompletion: trace delayed",
		"rule_id", rule.ID, "trace_id", c.TraceID, "stage", c.DelayedStage,
		"stage_started_at", c.StartedAt, "channels", len(rule.ChannelIDs))
	return nil
}

// CountsFor returns the per-integration completed/pending/delayed counts
// without firing anything. Used by the integration-detail chip.
func (e *Evaluator) CountsFor(ctx context.Context, rule Rule) (Counts, error) {
	if err := rule.Spec.Validate(); err != nil {
		return Counts{}, err
	}
	services, err := e.catalog.IntegrationServices(ctx, rule.IntegrationID)
	if err != nil {
		return Counts{}, err
	}
	if len(services) == 0 {
		return Counts{}, nil
	}
	now := time.Now().UTC()
	_, counts, err := e.scanAndBucket(ctx, rule.Spec, services, now, now.Add(-rule.Spec.Lookback()), now)
	return counts, err
}

// ── classification internals ─────────────────────────────────────────

// traceAgg is one trace's per-stage aggregate scanned from ClickHouse.
type traceAgg struct {
	TraceID  string
	StartAt  time.Time // start span first-seen; zero when ungated/absent
	HasStart bool
	StageAt  []time.Time // per-stage first-seen timestamp
	HasStage []bool
	TraceMin time.Time
	LastSpan time.Time
}

// scanAndBucket scans the gated traces in [from, to] and buckets them as
// of `now`, returning the delayed classifications plus the full counts.
func (e *Evaluator) scanAndBucket(ctx context.Context, spec RuleSpec, services []string, now, from, to time.Time) ([]TraceClassification, Counts, error) {
	aggs, err := e.scanTraces(ctx, spec, services, from, to)
	if err != nil {
		return nil, Counts{}, err
	}
	var delayed []TraceClassification
	var counts Counts
	for _, a := range aggs {
		c := classify(spec, a, now)
		switch c.State {
		case StateCompleted:
			counts.Completed++
		case StatePending:
			counts.Pending++
		case StateDelayed:
			counts.Delayed++
			delayed = append(delayed, c)
		}
	}
	return delayed, counts, nil
}

// scanTraces runs the per-stage GROUP BY TraceId query. For a gated rule
// the HAVING clause drops every trace that doesn't carry the start span,
// so only the integration's own traces are returned.
func (e *Evaluator) scanTraces(ctx context.Context, spec RuleSpec, services []string, from, to time.Time) ([]traceAgg, error) {
	stages := spec.EffectiveStages()
	gated := spec.Gated()

	var sb strings.Builder
	args := make([]any, 0)
	sb.WriteString("SELECT TraceId")
	if gated {
		sb.WriteString(", minIf(Timestamp, SpanName = ?) AS start_at")
		args = append(args, spec.StartSpanName)
		sb.WriteString(", countIf(SpanName = ?) > 0 AS has_start")
		args = append(args, spec.StartSpanName)
	}
	for i, st := range stages {
		ph := qPlaceholders(len(st.SpanNames))
		fmt.Fprintf(&sb, ", minIf(Timestamp, SpanName IN (%s)) AS stage%d_at", ph, i)
		for _, n := range st.SpanNames {
			args = append(args, n)
		}
		fmt.Fprintf(&sb, ", countIf(SpanName IN (%s)) > 0 AS has_stage%d", ph, i)
		for _, n := range st.SpanNames {
			args = append(args, n)
		}
	}
	sb.WriteString(", min(Timestamp) AS trace_min")
	sb.WriteString(", max(Timestamp) AS last_span")
	fmt.Fprintf(&sb, " FROM traces WHERE Timestamp >= ? AND Timestamp <= ? AND ServiceName IN (%s)", qPlaceholders(len(services)))
	args = append(args, from, to)
	for _, s := range services {
		args = append(args, s)
	}
	sb.WriteString(" GROUP BY TraceId")
	if gated {
		// The start-span gate: only traces carrying the start span are
		// evaluated/counted as this integration's messages.
		sb.WriteString(" HAVING has_start")
	}

	rows, err := e.ch.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse query: %w", err)
	}
	defer rows.Close()

	out := make([]traceAgg, 0)
	for rows.Next() {
		a := traceAgg{
			StageAt:  make([]time.Time, len(stages)),
			HasStage: make([]bool, len(stages)),
		}
		dest := make([]any, 0, 2*len(stages)+5)
		dest = append(dest, &a.TraceID)
		if gated {
			dest = append(dest, &a.StartAt, &a.HasStart)
		}
		for i := range stages {
			dest = append(dest, &a.StageAt[i], &a.HasStage[i])
		}
		dest = append(dest, &a.TraceMin, &a.LastSpan)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// classify buckets one trace as of `now`. A trace is completed once the
// final stage's span is present; otherwise it stalls at the first stage
// whose span is absent — delayed if that stage's clock (starting from
// the prior stage / start span) has run past its timeout, else pending.
func classify(spec RuleSpec, a traceAgg, now time.Time) TraceClassification {
	stages := spec.EffectiveStages()
	c := TraceClassification{TraceID: a.TraceID, LastSpanAt: a.LastSpan}
	n := len(stages)
	if n == 0 {
		c.State = StatePending
		return c
	}
	base := stageBaseline(spec, a)
	if a.HasStage[n-1] {
		c.State = StateCompleted
		c.StartedAt = base
		return c
	}
	prevTs := base
	for i := 0; i < n; i++ {
		if a.HasStage[i] {
			prevTs = a.StageAt[i]
			continue
		}
		timeout := time.Duration(stages[i].TimeoutSeconds) * time.Second
		threshold := now.Add(-timeout)
		c.StartedAt = prevTs
		c.DelayedStage = i + 1
		c.DelayedStageName = strings.Join(stages[i].SpanNames, " / ")
		if !prevTs.IsZero() && !prevTs.After(threshold) {
			c.State = StateDelayed
		} else {
			c.State = StatePending
		}
		return c
	}
	c.State = StateCompleted
	c.StartedAt = base
	return c
}

// stageBaseline is the clock origin for the first stage: the start span
// timestamp for a gated rule, or the trace's first span otherwise.
func stageBaseline(spec RuleSpec, a traceAgg) time.Time {
	if spec.Gated() {
		return a.StartAt
	}
	return a.TraceMin
}

// ── fingerprint helpers ──────────────────────────────────────────────

// stageFingerprint composes the alert_instances.fingerprint for a
// (trace, stage) firing. Trace ids are hex, so '#' never collides.
func stageFingerprint(traceID string, stage int) string {
	return traceID + "#" + strconv.Itoa(stage)
}

// parseFingerprint splits a fingerprint back into (trace_id, stage). A
// legacy bare-trace-id fingerprint returns stage 0.
func parseFingerprint(fp string) (string, int) {
	if i := strings.LastIndexByte(fp, '#'); i >= 0 {
		if n, err := strconv.Atoi(fp[i+1:]); err == nil {
			return fp[:i], n
		}
	}
	return fp, 0
}

// qPlaceholders returns "?,?,…" with n placeholders for an IN-list.
func qPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// short truncates a trace id for log messages / summaries. We keep the
// first 12 chars (48 bits) which is plenty to disambiguate.
func short(traceID string) string {
	if len(traceID) > 12 {
		return traceID[:12] + "…"
	}
	return traceID
}
