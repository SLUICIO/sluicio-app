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

	"github.com/sluicio/sluicio-app/services/cell-api/internal/cellhealth"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/envctx"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
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
	resolver ChannelResolver
}

// ChannelResolver turns a rule's own channels, or the notification
// profile that governs its integration when it has none, into the
// channels a firing is sent to. notifyprofiles.Store implements it, as
// it does for the alerting engine, so a delayed trace is routed the way
// every other check is.
type ChannelResolver interface {
	Resolve(ctx context.Context, orgID uuid.UUID, explicit []uuid.UUID, integrationID, groupID *uuid.UUID) ([]uuid.UUID, error)
}

// SetChannelResolver wires the routing resolver after construction.
func (e *Evaluator) SetChannelResolver(r ChannelResolver) { e.resolver = r }

// channelsFor is where a delayed trace is sent: the rule's own channels
// when it has them, else its integration's profile, then the org
// default. Before this a rule with no channels of its own sent its first
// page nowhere, while the engine's re-notify loop, which already routed
// through profiles, paged the same firing later.
func (e *Evaluator) channelsFor(ctx context.Context, rule Rule) []uuid.UUID {
	if e.resolver == nil {
		return rule.ChannelIDs
	}
	integ := rule.IntegrationID
	ch, err := e.resolver.Resolve(ctx, e.orgID, rule.ChannelIDs, &integ, nil)
	if err != nil {
		e.logger.Warn("tracecompletion: channel resolve failed; using rule channels", "rule_id", rule.ID, "err", err)
		return rule.ChannelIDs
	}
	return ch
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
	// SuppressingWindowForIntegration returns the id of an active
	// maintenance window covering rules on the given integration, or nil.
	SuppressingWindowForIntegration(ctx context.Context, orgID, integrationID uuid.UUID) (*uuid.UUID, error)
	// MarkInstanceSuppressed stamps the window that muted an instance's
	// firing notification, so no page goes out and its eventual resolve
	// stays silent too. The engine's renotify loop sends the overdue
	// first page if the instance is still firing when the window ends.
	MarkInstanceSuppressed(ctx context.Context, instanceID, windowID uuid.UUID) error
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
	cellhealth.Register("trace-completion", e.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.EvaluateOnce(ctx); err != nil {
				e.logger.Warn("tracecompletion evaluate failed", "err", err)
			}
			// End of cycle, not start: a loop wedged inside its
			// own body is exactly what this catches.
			cellhealth.Beat("trace-completion")
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

	// Resolve any covering maintenance window once per rule per tick.
	// Lookup errors fail toward delivery (page rather than silently
	// vanish), mirroring the engine's suppressedBy.
	var suppress *uuid.UUID
	if len(delayed) > 0 {
		w, err := e.alerts.SuppressingWindowForIntegration(ctx, e.orgID, rule.IntegrationID)
		if err != nil {
			e.logger.Warn("tracecompletion: maintenance windows load failed; not suppressing",
				"rule_id", rule.ID, "err", err)
		} else {
			suppress = w
		}
	}

	for _, c := range delayed {
		if err := e.fireDelayed(ctx, rule, c, suppress); err != nil {
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
		// A late span counts only from the integration's own services,
		// as it does when the trace is first evaluated.
		services, err := e.catalog.IntegrationServices(ctx, rule.IntegrationID)
		if err != nil {
			e.logger.Warn("tracecompletion: integration services for the late sweep failed",
				"rule_id", rule.ID, "err", err)
			continue
		}
		if len(services) == 0 {
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
			closed, err := e.findClosedTraces(ctx, stages[stage-1], services, traceIDs)
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

// findClosedTraces returns, for the given trace_ids, the ones that
// have since reached stage, and when: one of its spans for an "any"
// stage, every one of them for an "all" stage, from the integration's
// services and regardless of when the trace started.
func (e *Evaluator) findClosedTraces(ctx context.Context, stage EffectiveStage, services, traceIDs []string) (map[string]time.Time, error) {
	if len(traceIDs) == 0 || len(stage.Spans) == 0 || len(services) == 0 {
		return nil, nil
	}
	stages := []EffectiveStage{stage}
	var sb strings.Builder
	sb.WriteString("SELECT TraceId")
	args := appendSpanColumns(&sb, nil, stages)
	fmt.Fprintf(&sb, " FROM traces WHERE TraceId IN (%s) AND ServiceName IN (%s) GROUP BY TraceId",
		qPlaceholders(len(traceIDs)), qPlaceholders(len(services)))
	for _, t := range traceIDs {
		args = append(args, t)
	}
	for _, s := range services {
		args = append(args, s)
	}
	rows, err := e.ch.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse query: %w", err)
	}
	defer rows.Close()
	out := make(map[string]time.Time, len(traceIDs))
	for rows.Next() {
		var tid string
		sp := newSightings(stages)
		if err := rows.Scan(append([]any{&tid}, sp.dest()...)...); err != nil {
			return nil, err
		}
		if at, ok, _ := reach(stage, sp.at[0], sp.has[0]); ok {
			out[tid] = at
		}
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
		stageName = stages[stage-1].Label()
		timeout = stages[stage-1].TimeoutSeconds
	}
	summary := fmt.Sprintf(
		"Trace %s reached stage %d of %q (%s) at %s: delivered with delay (SLA %ds)",
		short(traceID), stage, rule.Name, stageName, closedAt.UTC().Format(time.RFC3339), timeout,
	)
	e.logger.Info("tracecompletion: stage delivered with delay",
		"rule_id", rule.ID, "trace_id", traceID, "stage", stage, "closed_at", closedAt)
	if err := e.alerts.ResolveInstance(ctx, active.ID, summary); err != nil {
		return fmt.Errorf("resolve instance: %w", err)
	}
	return nil
}

// EnvironmentLabel is the instance label naming the environment a
// firing is about; the same key as alerting.EnvironmentLabel, which the
// Errors feed, the environment picker and delivery read.
const EnvironmentLabel = "environment"

// fireDelayed handles one trace delayed at a specific stage. If we've
// already opened an instance for this (trace, stage) we just touch it
// (sticky). If it's new, we open the instance + enqueue notification
// jobs for every configured channel — unless an active maintenance
// window covers the integration (suppress non-nil), in which case the
// instance is stamped suppressed at birth and no jobs are enqueued.
func (e *Evaluator) fireDelayed(ctx context.Context, rule Rule, c TraceClassification, suppress *uuid.UUID) error {
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
		// What every other check's firing carries: without them the
		// webhook went out with an empty severity and no rule name.
		"severity":      rule.Severity,
		"rule_name":     rule.Name,
		"stage_index":   strconv.Itoa(c.DelayedStage),
		"stage_name":    c.DelayedStageName,
		"start_span":    rule.Spec.StartSpanName,
		"closing_spans": c.DelayedStageName,
		// started_at is the moment the breached stage's clock started
		// (the prior stage's / start span's timestamp).
		"started_at": c.StartedAt.UTC().Format(time.RFC3339Nano),
	}
	// The rule reads every environment at once, so each firing names the
	// environment of its own trace, as a per-environment check's firing
	// does: the Errors pages and the environment picker read it, and the
	// trace's environment picks the notification profile.
	if c.Environment != "" {
		labels[EnvironmentLabel] = c.Environment
		ctx = envctx.With(ctx, c.Environment)
	}
	since := time.Since(c.StartedAt).Round(time.Second)
	summary := fmt.Sprintf(
		"Trace %s is delayed at stage %d of %q: waited %s with no '%s' span",
		short(c.TraceID), c.DelayedStage, rule.Name, since, c.DelayedStageName,
	)
	if c.Environment != "" {
		summary += " [" + envctx.Display(c.Environment) + "]"
	}

	inst, err := e.alerts.OpenInstance(ctx, rule.ID, fp, labels, summary)
	if err != nil {
		return fmt.Errorf("open instance: %w", err)
	}
	if suppress != nil {
		// Muted at birth by a maintenance window: record why and send
		// nothing. The firing still shows on the integration's delayed
		// surfaces; if it's still open when the window ends, the engine's
		// renotify loop sends the overdue first page.
		if err := e.alerts.MarkInstanceSuppressed(ctx, inst.ID, *suppress); err != nil {
			return fmt.Errorf("mark suppressed: %w", err)
		}
		e.logger.Info("tracecompletion: trace delayed (suppressed by maintenance)",
			"rule_id", rule.ID, "trace_id", c.TraceID, "stage", c.DelayedStage,
			"window", *suppress)
		return nil
	}
	channels := e.channelsFor(ctx, rule)
	if len(channels) > 0 {
		if err := e.alerts.EnqueueJobs(ctx, inst.ID, channels); err != nil {
			return fmt.Errorf("enqueue jobs: %w", err)
		}
	}
	e.logger.Info("tracecompletion: trace delayed",
		"rule_id", rule.ID, "trace_id", c.TraceID, "stage", c.DelayedStage,
		"stage_started_at", c.StartedAt, "channels", len(channels))
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
	StageAt  []time.Time // when each stage was reached
	HasStage []bool
	// Missing is, per stage of an "all" stage reached only in part,
	// the spans that have not arrived yet.
	Missing  [][]SpanMatch
	TraceMin time.Time
	LastSpan time.Time
	// Environment is the trace's telemetry environment, "" when its
	// spans named none.
	Environment string
}

// sightings is one trace's first-seen timestamp and presence of every
// span of every stage, as scanned from ClickHouse.
type sightings struct {
	at  [][]time.Time
	has [][]bool
}

func newSightings(stages []EffectiveStage) sightings {
	sp := sightings{at: make([][]time.Time, len(stages)), has: make([][]bool, len(stages))}
	for i, st := range stages {
		sp.at[i] = make([]time.Time, len(st.Spans))
		sp.has[i] = make([]bool, len(st.Spans))
	}
	return sp
}

// dest lists scan targets in the order appendSpanColumns selects them.
func (sp sightings) dest() []any {
	var out []any
	for i := range sp.at {
		for j := range sp.at[i] {
			out = append(out, &sp.at[i][j], &sp.has[i][j])
		}
	}
	return out
}

// foldStages turns span sightings into stage arrivals: an "any" stage
// is reached at the first of its spans, an "all" stage at the last of
// them, and only once every one has arrived.
func (a *traceAgg) foldStages(stages []EffectiveStage, sp sightings) {
	a.StageAt = make([]time.Time, len(stages))
	a.HasStage = make([]bool, len(stages))
	a.Missing = make([][]SpanMatch, len(stages))
	for i, st := range stages {
		a.StageAt[i], a.HasStage[i], a.Missing[i] = reach(st, sp.at[i], sp.has[i])
	}
}

func reach(st EffectiveStage, at []time.Time, has []bool) (time.Time, bool, []SpanMatch) {
	var first, last time.Time
	var missing []SpanMatch
	seen := 0
	for j, m := range st.Spans {
		if !has[j] {
			missing = append(missing, m)
			continue
		}
		seen++
		if first.IsZero() || at[j].Before(first) {
			first = at[j]
		}
		if at[j].After(last) {
			last = at[j]
		}
	}
	if !st.All {
		return first, seen > 0, nil
	}
	if seen == len(st.Spans) {
		return last, true, nil
	}
	if seen == 0 {
		missing = nil // nothing arrived: the whole stage is what is missing
	}
	return time.Time{}, false, missing
}

// spanCond is the predicate for one span of a stage. ServiceName is
// only compared when the span names its service.
func spanCond(m SpanMatch) (string, []any) {
	if m.Service == "" {
		return "SpanName = ?", []any{m.SpanName}
	}
	return "(SpanName = ? AND ServiceName = ?)", []any{m.SpanName, m.Service}
}

// appendSpanColumns selects, per span of each stage, when it was first
// seen in the trace and whether it was seen at all.
func appendSpanColumns(sb *strings.Builder, args []any, stages []EffectiveStage) []any {
	for i, st := range stages {
		for j, m := range st.Spans {
			cond, a := spanCond(m)
			fmt.Fprintf(sb, ", minIf(Timestamp, %s) AS s%d_%d_at, countIf(%s) > 0 AS s%d_%d_has", cond, i, j, cond, i, j)
			args = append(args, a...)
			args = append(args, a...)
		}
	}
	return args
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
	args = appendSpanColumns(&sb, args, stages)
	sb.WriteString(", min(Timestamp) AS trace_min")
	sb.WriteString(", max(Timestamp) AS last_span")
	// One trace runs in one environment; max() prefers a named one over a
	// span that carried none. Read through the cell's environment setting
	// (its key and aliases), as every other environment read is, so the
	// label matches what the environment picker offers.
	sb.WriteString(", max(" + store.EnvironmentExpr() + ") AS env")
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
		var a traceAgg
		dest := []any{&a.TraceID}
		if gated {
			dest = append(dest, &a.StartAt, &a.HasStart)
		}
		sp := newSightings(stages)
		dest = append(dest, sp.dest()...)
		dest = append(dest, &a.TraceMin, &a.LastSpan, &a.Environment)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		a.foldStages(stages, sp)
		out = append(out, a)
	}
	return out, rows.Err()
}

// classify buckets one trace as of `now`. A trace is completed once
// every stage has been reached; otherwise it stalls at the first stage
// that has not, delayed if that stage's clock (starting from the prior
// stage / start span) has run past its timeout, else pending. The final
// stage alone is not enough: stages are AND-ed, so "A, then B1 or B2"
// must not count a trace where B1 arrived but A never did.
func classify(spec RuleSpec, a traceAgg, now time.Time) TraceClassification {
	stages := spec.EffectiveStages()
	c := TraceClassification{TraceID: a.TraceID, LastSpanAt: a.LastSpan, Environment: a.Environment}
	n := len(stages)
	if n == 0 {
		c.State = StatePending
		return c
	}
	base := stageBaseline(spec, a)
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
		c.DelayedStageName = stages[i].Label()
		if i < len(a.Missing) && len(a.Missing[i]) > 0 {
			// An "all" stage that is part-way there names only the
			// spans still missing: which subscriber has not delivered.
			c.DelayedStageName = labelOf(a.Missing[i], stages[i].All)
		}
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

// ── dry run ──────────────────────────────────────────────────────────

// MaxDryRunWindow bounds a dry run: it reads every trace of the
// integration's services in the window, like one evaluation does over
// its lookback.
const MaxDryRunWindow = 72 * time.Hour

// DryRunTrace is one delayed trace a dry run found.
type DryRunTrace struct {
	TraceID    string    `json:"trace_id"`
	Stage      int       `json:"stage"`
	WaitingFor string    `json:"waiting_for"`
	StartedAt  time.Time `json:"stage_started_at"`
}

// DryRunResult is what a rule would have made of the traces in a window,
// judged as of now: the counts the integration's chip would show, how
// far the traces got, and some of the delayed ones.
type DryRunResult struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Traces is how many traces the rule follows: those with the start
	// span, or every trace of the services when it has none.
	Traces int `json:"traces"`
	// TracesOnServices is every trace of the integration's services in
	// the window, so a start span no trace carries shows as 0 of N.
	TracesOnServices int           `json:"traces_on_services"`
	Completed        int           `json:"completed"`
	Pending          int           `json:"pending"`
	Delayed          int           `json:"delayed"`
	ReachedStage     []int         `json:"reached_stage"`
	DelayedAtStage   []int         `json:"delayed_at_stage"`
	Samples          []DryRunTrace `json:"delayed_samples"`
}

// DryRun evaluates a draft rule over [from, to] on the given services
// without firing anything.
func (e *Evaluator) DryRun(ctx context.Context, spec RuleSpec, services []string, from, to time.Time) (DryRunResult, error) {
	stages := spec.EffectiveStages()
	res := DryRunResult{From: from, To: to, ReachedStage: make([]int, len(stages)), DelayedAtStage: make([]int, len(stages)), Samples: []DryRunTrace{}}
	if len(services) == 0 {
		return res, nil
	}
	var total uint64
	if err := e.ch.QueryRow(ctx,
		fmt.Sprintf("SELECT toUInt64(uniq(TraceId)) FROM traces WHERE Timestamp >= ? AND Timestamp <= ? AND ServiceName IN (%s)", qPlaceholders(len(services))),
		append([]any{from, to}, stringsAsAny(services)...)...,
	).Scan(&total); err != nil {
		return res, fmt.Errorf("clickhouse query: %w", err)
	}
	res.TracesOnServices = int(total)
	aggs, err := e.scanTraces(ctx, spec, services, from, to)
	if err != nil {
		return res, err
	}
	now := time.Now().UTC()
	res.Traces = len(aggs)
	for _, a := range aggs {
		for i := range stages {
			if i < len(a.HasStage) && a.HasStage[i] {
				res.ReachedStage[i]++
			}
		}
		c := classify(spec, a, now)
		switch c.State {
		case StateCompleted:
			res.Completed++
		case StatePending:
			res.Pending++
		case StateDelayed:
			res.Delayed++
			if c.DelayedStage >= 1 && c.DelayedStage <= len(stages) {
				res.DelayedAtStage[c.DelayedStage-1]++
			}
			if len(res.Samples) < 10 {
				res.Samples = append(res.Samples, DryRunTrace{TraceID: c.TraceID, Stage: c.DelayedStage, WaitingFor: c.DelayedStageName, StartedAt: c.StartedAt})
			}
		}
	}
	return res, nil
}

func stringsAsAny(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
