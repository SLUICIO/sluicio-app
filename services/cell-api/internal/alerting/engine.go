// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MetricEvaluator computes the aggregate a rule compares to its
// threshold. Implemented by the ClickHouse store (adapted at wiring time
// so this package stays independent of the store package). serviceName
// scopes a service-bound rule to that one service's emissions of the
// metric (empty = no scope, for integration/global rules) — without it a
// check on a metric many services emit would pool them all.
type MetricEvaluator interface {
	MetricAggregate(ctx context.Context, metricName string, attrs []AttrFilter, aggregation, serviceName string, integrationID *uuid.UUID, from, to time.Time) (float64, uint64, error)
	// MetricAggregateGrouped reduces the metric per distinct value of
	// splitKey, for split-by rules. One MetricGroup per value.
	MetricAggregateGrouped(ctx context.Context, metricName string, attrs []AttrFilter, aggregation, splitKey, serviceName string, integrationID *uuid.UUID, from, to time.Time) ([]MetricGroup, error)
}

// LogCountQuery is the criteria a log rule counts matches for, over
// [From, To]. Defined here (not store.LogQueryParams) so this package
// stays store-independent; the wiring layer adapts it (and resolves
// IntegrationID → service names) when calling ClickHouse.
type LogCountQuery struct {
	MinSeverity   int32
	BodyContains  string
	Attrs         []AttrFilter
	ServiceName   string     // bound service ("" = none)
	IntegrationID *uuid.UUID // bound integration (nil = none)
	From, To      time.Time
}

// LogCounter counts logs matching a query. Implemented by the
// ClickHouse store (adapted at wiring time), like MetricEvaluator.
type LogCounter interface {
	CountLogs(ctx context.Context, q LogCountQuery) (uint64, error)
}

// TraceErrorQuery is the scope a trace_error rule counts failed traces
// for, over [From, To]. The rule is bound to EITHER an integration (the
// wiring layer resolves IntegrationID → the integration's service set) or
// a single service (ServiceName) — exactly one is set. IntegrationID
// takes precedence when, defensively, both are present.
type TraceErrorQuery struct {
	IntegrationID *uuid.UUID
	ServiceName   string
	From, To      time.Time
}

// TraceErrorCounter counts the distinct failed traces (traces with an
// error span) in a query's scope. Implemented by the ClickHouse store
// (adapted at wiring time), like LogCounter.
type TraceErrorCounter interface {
	CountErrorTraces(ctx context.Context, q TraceErrorQuery) (uint64, error)
}

// TraceLatencyQuery scopes a response-time check to an integration or a
// single service (exactly one set), aggregating span duration over
// [From,To] at Quantile (1.0 = max).
type TraceLatencyQuery struct {
	IntegrationID *uuid.UUID
	ServiceName   string
	Quantile      float64
	From, To      time.Time
}

// TraceLatencyEvaluator returns the aggregate span latency (ms) + the
// sample count for a query's scope. samples==0 means no data (skip).
// Implemented by the ClickHouse store (adapted at wiring time).
type TraceLatencyEvaluator interface {
	TraceLatencyMs(ctx context.Context, q TraceLatencyQuery) (latencyMs float64, samples uint64, err error)
}

// TraceVolumeQuery scopes a low-traffic check to an integration or a single
// service (exactly one set), counting total distinct traces over [From,To].
type TraceVolumeQuery struct {
	IntegrationID *uuid.UUID
	ServiceName   string
	From, To      time.Time
}

// TraceVolumeEvaluator returns the total distinct trace count for a query's
// scope. Unlike the latency/error evaluators there is no "no data → skip":
// zero traces is exactly the condition a low-traffic check exists to catch
// (dead-man's-switch). Implemented by the ClickHouse store (adapted at
// wiring time).
type TraceVolumeEvaluator interface {
	TotalTraces(ctx context.Context, q TraceVolumeQuery) (uint64, error)
}

// Engine runs the two background loops behind alerting: an evaluator
// that turns rule breaches into alert_instances (+ enqueued jobs), and a
// delivery worker that drains notification_jobs to channels.
//
// v1 assumes a single cell-api instance: the evaluator has no leader
// election, so running multiple replicas could double-fire. Delivery is
// safe across replicas (FOR UPDATE SKIP LOCKED).
type Engine struct {
	store       *Store
	eval        MetricEvaluator
	logEval     LogCounter
	traceEval   TraceErrorCounter
	latencyEval TraceLatencyEvaluator
	volumeEval  TraceVolumeEvaluator
	resolver    ChannelResolver
	log         *slog.Logger
	org         uuid.UUID

	evalInterval time.Duration
	deliveryPoll time.Duration
	maxAttempts  int
	client       *http.Client

	// Short-TTL cache of the org's active maintenance windows, consulted
	// on every notification decision (windows number in the units; the
	// checks run per firing rule per tick).
	mwMu      sync.Mutex
	mwCache   []MaintenanceWindow
	mwFetched time.Time
}

// mwCacheTTL is how stale the active-window cache may go. Well under the
// eval interval so a freshly-created window takes effect within a tick.
const mwCacheTTL = 15 * time.Second

// suppressedBy returns the id of an active maintenance window covering
// the rule, or nil. On cache-refresh errors it fails toward delivery
// (alerts page rather than silently vanish).
func (e *Engine) suppressedBy(ctx context.Context, rule AlertRule) *uuid.UUID {
	e.mwMu.Lock()
	if time.Since(e.mwFetched) > mwCacheTTL {
		wins, err := e.store.ActiveMaintenanceWindows(ctx, e.org)
		if err != nil {
			e.log.Warn("maintenance windows load failed; not suppressing", "err", err)
			wins = nil
		}
		e.mwCache = wins
		e.mwFetched = time.Now()
	}
	wins := e.mwCache
	e.mwMu.Unlock()
	for _, w := range wins {
		if w.Covers(rule) {
			id := w.ID
			return &id
		}
	}
	return nil
}

// ChannelResolver picks the delivery channels for a firing rule, applying
// the global/integration/team routing fallback when the rule has none of
// its own. Implemented by notifyroutes.Store; nil = deliver only to the
// rule's explicit channels (legacy behaviour).
type ChannelResolver interface {
	Resolve(ctx context.Context, orgID uuid.UUID, explicit []uuid.UUID, integrationID, groupID *uuid.UUID) ([]uuid.UUID, error)
}

// Grouping modes a notification profile can impose on delivery. Mirror of
// notifyprofiles.Grouping* — kept as bare strings so this package stays
// independent of the profiles store (which implements BehaviorResolver
// structurally).
const (
	groupingPerCheck       = "per_check"
	groupingPerIntegration = "per_integration"
)

// BehaviorResolver surfaces the resolved notification profile's delivery
// behaviour (grouping mode + re-notify interval in minutes) for an alert's
// scope. Implemented by notifyprofiles.Store alongside ChannelResolver; the
// engine type-asserts its resolver to this and falls back to per-check /
// no-recurrence when it isn't available.
type BehaviorResolver interface {
	ResolveBehavior(ctx context.Context, orgID uuid.UUID, integrationID, groupID *uuid.UUID) (grouping string, renotifyMinutes int, err error)
}

// SetChannelResolver wires the routing resolver after construction.
func (e *Engine) SetChannelResolver(r ChannelResolver) { e.resolver = r }

// SetLatencyEvaluator wires the trace-latency evaluator after construction
// (kept off NewEngine's signature so adding it didn't churn callers).
func (e *Engine) SetLatencyEvaluator(l TraceLatencyEvaluator) { e.latencyEval = l }

// SetVolumeEvaluator wires the trace-volume (low-traffic) evaluator.
func (e *Engine) SetVolumeEvaluator(v TraceVolumeEvaluator) { e.volumeEval = v }

// behavior returns the rule's resolved grouping mode + re-notify interval.
// Defaults to per-check / no-recurrence when no profile resolver is wired
// or resolution fails — so a routing hiccup never changes delivery shape.
func (e *Engine) behavior(ctx context.Context, rule AlertRule) (grouping string, renotifyMinutes int) {
	br, ok := e.resolver.(BehaviorResolver)
	if !ok {
		return groupingPerCheck, 0
	}
	g, rn, err := br.ResolveBehavior(ctx, e.org, rule.IntegrationID, rule.GroupID)
	if err != nil {
		e.log.Warn("resolve behavior failed; using per-check", "rule", rule.ID, "err", err)
		return groupingPerCheck, 0
	}
	if g == "" {
		g = groupingPerCheck
	}
	if rn < 0 {
		rn = 0
	}
	return g, rn
}

// channelsFor resolves a rule's delivery channels through routing. Falls
// back to the rule's explicit channels if the resolver is unset or errors,
// so a routing hiccup never silently drops an alert.
func (e *Engine) channelsFor(ctx context.Context, rule AlertRule) []uuid.UUID {
	if e.resolver == nil {
		return rule.ChannelIDs
	}
	ch, err := e.resolver.Resolve(ctx, e.org, rule.ChannelIDs, rule.IntegrationID, rule.GroupID)
	if err != nil {
		e.log.Warn("channel resolve failed; using rule channels", "rule", rule.ID, "err", err)
		return rule.ChannelIDs
	}
	return ch
}

// enqueue resolves the rule's routed channels and enqueues a delivery job
// per channel for the instance. Used for resolve notifications, which are
// always per-instance.
func (e *Engine) enqueue(ctx context.Context, instanceID uuid.UUID, rule AlertRule) error {
	return e.store.EnqueueJobs(ctx, instanceID, e.channelsFor(ctx, rule))
}

// resolveOrHold drives the "condition cleared while an instance is open"
// transition by the rule's resolve mode (per-check, replacing the old
// sticky-by-signal hardcoding):
//   - ResolveManual + not yet acknowledged → hold: keep the instance firing
//     (just bump last_evaluated_at), so a check the operator hasn't seen
//     doesn't silently self-clear.
//   - otherwise → resolve, and notify the recovery only when the instance
//     wasn't already acknowledged (an acked alert is silenced).
func (e *Engine) resolveOrHold(ctx context.Context, active *AlertInstance, rule AlertRule, summary, kind string) {
	if rule.ResolveMode == ResolveManual && active.HandledAt == nil {
		if err := e.store.TouchInstance(ctx, active.ID); err != nil {
			e.log.Error(kind+" eval: hold (sticky) failed", "rule", rule.ID, "err", err)
		}
		return
	}
	if err := e.store.ResolveInstance(ctx, active.ID, summary); err != nil {
		e.log.Error(kind+" eval: resolve failed", "rule", rule.ID, "err", err)
		return
	}
	// Resolve notifications go out unless the operator already acked, or
	// the instance was muted at birth by a maintenance window — nobody was
	// told it fired, so "resolved" would be noise. An instance that fired
	// BEFORE the window (SuppressedBy nil) resolves loudly even mid-window:
	// resolves are good news and close the loop.
	if active.HandledAt == nil && active.SuppressedBy == nil {
		if err := e.enqueue(ctx, active.ID, rule); err != nil {
			e.log.Error(kind+" eval: enqueue resolve failed", "rule", rule.ID, "err", err)
		}
	}
	e.log.Info(kind+" alert resolved", "rule", rule.Name, "resolve_mode", rule.ResolveMode)
}

// enqueueFiring sends a firing notification, honouring the resolved
// profile's grouping mode. For per-check delivery (the default) it enqueues
// the per-instance jobs immediately and stamps the notify watermark, so the
// re-notify loop knows when this instance last paged. For per-integration
// delivery it does nothing here: the renotify loop owns that integration's
// notification stream (one alert per integration), coalescing every firing
// check into a single representative so a recipient isn't paged once per
// failing check. Without an integration to group on, per-integration falls
// back to per-check.
func (e *Engine) enqueueFiring(ctx context.Context, instanceID uuid.UUID, rule AlertRule) error {
	// Maintenance window covering this rule: record that the instance was
	// muted at birth and send nothing. The notify watermark stays unset,
	// so if the alert is still firing when the window ends, the renotify
	// loop sends the overdue first page.
	if w := e.suppressedBy(ctx, rule); w != nil {
		if err := e.store.MarkInstanceSuppressed(ctx, instanceID, *w); err != nil {
			e.log.Warn("mark suppressed failed", "instance", instanceID, "err", err)
		}
		e.log.Info("alert firing suppressed (maintenance)", "rule", rule.Name, "window", *w)
		return nil
	}
	if grouping, _ := e.behavior(ctx, rule); grouping == groupingPerIntegration && rule.IntegrationID != nil {
		return nil
	}
	if err := e.store.EnqueueJobs(ctx, instanceID, e.channelsFor(ctx, rule)); err != nil {
		return err
	}
	return e.store.MarkInstanceNotified(ctx, instanceID)
}

// NewEngine builds an Engine with sensible defaults. ALERT_EVAL_INTERVAL
// and ALERT_DELIVERY_POLL (Go durations) override the loop cadences.
func NewEngine(store *Store, eval MetricEvaluator, logEval LogCounter, traceEval TraceErrorCounter, org uuid.UUID, log *slog.Logger) *Engine {
	e := &Engine{
		store:        store,
		eval:         eval,
		logEval:      logEval,
		traceEval:    traceEval,
		log:          log,
		org:          org,
		evalInterval: 30 * time.Second,
		deliveryPoll: 5 * time.Second,
		maxAttempts:  5,
		client:       &http.Client{Timeout: 10 * time.Second},
	}
	if d := envDuration("ALERT_EVAL_INTERVAL"); d > 0 {
		e.evalInterval = d
	}
	if d := envDuration("ALERT_DELIVERY_POLL"); d > 0 {
		e.deliveryPoll = d
	}
	return e
}

func envDuration(key string) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 0
}

// Run starts the loops and blocks until ctx is cancelled: a metric
// evaluator, a pushed-value evaluator, a log evaluator (when wired), and
// the delivery worker.
func (e *Engine) Run(ctx context.Context) {
	go e.loop(ctx, e.evalInterval, e.evaluateOnce)
	go e.loop(ctx, e.evalInterval, e.evaluatePushedOnce)
	if e.logEval != nil {
		go e.loop(ctx, e.evalInterval, e.evaluateLogOnce)
	}
	if e.traceEval != nil {
		go e.loop(ctx, e.evalInterval, e.evaluateTraceErrorOnce)
	}
	if e.latencyEval != nil {
		go e.loop(ctx, e.evalInterval, e.evaluateTraceLatencyOnce)
	}
	if e.volumeEval != nil {
		go e.loop(ctx, e.evalInterval, e.evaluateTraceVolumeOnce)
	}
	// Re-notification + per-integration coalescing run on the eval cadence,
	// off the set of firing unacked instances (independent of which signal
	// opened them).
	go e.loop(ctx, e.evalInterval, e.renotifyOnce)
	e.loop(ctx, e.deliveryPoll, e.deliverOnce)
}

func (e *Engine) loop(ctx context.Context, every time.Duration, tick func(context.Context)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick(ctx)
		}
	}
}

// evaluateOnce evaluates every enabled metric rule and drives its
// instance state machine.
func (e *Engine) evaluateOnce(ctx context.Context) {
	rules, err := e.store.EnabledMetricRules(ctx, e.org)
	if err != nil {
		e.log.Error("alert eval: list rules failed", "err", err)
		return
	}
	for _, rule := range rules {
		e.evaluateRule(ctx, rule)
	}
}

func (e *Engine) evaluateRule(ctx context.Context, rule AlertRule) {
	if rule.Spec.SplitBy != "" {
		e.evaluateSplitRule(ctx, rule)
		return
	}
	to := time.Now().UTC()
	from := to.Add(-rule.Spec.ForWindowDuration())
	value, samples, err := e.eval.MetricAggregate(ctx, rule.Spec.MetricName, rule.Spec.Attrs, string(rule.Spec.Aggregation), rule.ServiceName, rule.IntegrationID, from, to)
	if err != nil {
		e.log.Error("alert eval: aggregate failed", "rule", rule.ID, "err", err)
		return
	}
	// Persist the computed value so a "show on service page" check can
	// render its latest reading without re-querying ClickHouse. Only when
	// the series had samples — a 0-from-no-data reading would mislead.
	if samples > 0 {
		if err := e.store.RecordReading(ctx, rule.ID, value); err != nil {
			e.log.Warn("alert eval: record reading failed", "rule", rule.ID, "err", err)
		}
	}
	breached := samples > 0 && EvaluateBreach(rule.Spec.Operator, value, rule.Spec.Threshold)

	active, err := e.store.ActiveInstance(ctx, rule.ID)
	if err != nil {
		e.log.Error("alert eval: active instance failed", "rule", rule.ID, "err", err)
		return
	}

	switch {
	case breached && active == nil:
		labels := ruleLabels(rule, value)
		summary := ruleSummary(rule, value, "firing")
		inst, err := e.store.OpenInstance(ctx, rule.ID, "all", labels, summary)
		if err != nil {
			e.log.Error("alert eval: open instance failed", "rule", rule.ID, "err", err)
			return
		}
		if err := e.enqueueFiring(ctx, inst.ID, rule); err != nil {
			e.log.Error("alert eval: enqueue failed", "rule", rule.ID, "err", err)
		}
		e.log.Info("alert firing", "rule", rule.Name, "value", value, "threshold", rule.Spec.Threshold, "channels", len(rule.ChannelIDs))
	case breached && active != nil:
		if err := e.store.TouchInstance(ctx, active.ID); err != nil {
			e.log.Error("alert eval: touch failed", "rule", rule.ID, "err", err)
		}
	case !breached && active != nil:
		e.resolveOrHold(ctx, active, rule, ruleSummary(rule, value, "resolved"), "metric")
	}
}

// evaluatePushedOnce evaluates every enabled pushed-value rule against
// its latest externally pushed reading — the analogue of evaluateOnce
// for source='pushed' health checks. A pushed value drives health +
// notifications through the same instance state machine.
func (e *Engine) evaluatePushedOnce(ctx context.Context) {
	rules, err := e.store.EnabledPushedRules(ctx, e.org)
	if err != nil {
		e.log.Error("alert eval: list pushed rules failed", "err", err)
		return
	}
	for _, rule := range rules {
		e.evaluatePushedRule(ctx, rule)
	}
}

// evaluatePushedRule compares a pushed rule's latest reading to its
// threshold and drives its instance state machine. With no reading yet
// (nothing pushed), the rule is treated as no-data: it neither fires nor
// resolves, so a never-fed check stays quiet rather than false-firing.
func (e *Engine) evaluatePushedRule(ctx context.Context, rule AlertRule) {
	reading, err := e.store.LatestReading(ctx, rule.ID)
	if err != nil {
		e.log.Error("alert eval: latest reading failed", "rule", rule.ID, "err", err)
		return
	}
	if reading == nil {
		return // no value pushed yet — nothing to evaluate
	}
	value := reading.Value
	breached := EvaluateBreach(rule.Spec.Operator, value, rule.Spec.Threshold)

	active, err := e.store.ActiveInstance(ctx, rule.ID)
	if err != nil {
		e.log.Error("alert eval: active instance failed", "rule", rule.ID, "err", err)
		return
	}

	switch {
	case breached && active == nil:
		labels := ruleLabels(rule, value)
		summary := pushedRuleSummary(rule, value, "firing")
		inst, err := e.store.OpenInstance(ctx, rule.ID, "all", labels, summary)
		if err != nil {
			e.log.Error("alert eval: open instance failed", "rule", rule.ID, "err", err)
			return
		}
		if err := e.enqueueFiring(ctx, inst.ID, rule); err != nil {
			e.log.Error("alert eval: enqueue failed", "rule", rule.ID, "err", err)
		}
		e.log.Info("pushed alert firing", "rule", rule.Name, "value", value, "threshold", rule.Spec.Threshold, "channels", len(rule.ChannelIDs))
	case breached && active != nil:
		if err := e.store.TouchInstance(ctx, active.ID); err != nil {
			e.log.Error("alert eval: touch failed", "rule", rule.ID, "err", err)
		}
	case !breached && active != nil:
		e.resolveOrHold(ctx, active, rule, pushedRuleSummary(rule, value, "resolved"), "pushed")
	}
}

// pushedRuleSummary renders a pushed-value firing/resolution. A pushed
// rule has no metric/aggregation, so the summary reads off the rule name
// and the value vs threshold (with unit, when set).
func pushedRuleSummary(rule AlertRule, value float64, state string) string {
	v := strconv.FormatFloat(value, 'f', -1, 64)
	th := strconv.FormatFloat(rule.Spec.Threshold, 'f', -1, 64)
	unit := ""
	if rule.Unit != "" {
		unit = " " + rule.Unit
	}
	if state == "resolved" {
		return fmt.Sprintf("%s — recovered to %s%s (threshold %s %s%s)",
			rule.Name, v, unit, opGlyph[rule.Spec.Operator], th, unit)
	}
	return fmt.Sprintf("%s — %s%s %s %s%s",
		rule.Name, v, unit, opGlyph[rule.Spec.Operator], th, unit)
}

// evaluateSplitRule evaluates a metric rule with SplitBy set: the metric
// is reduced per distinct value of the split attribute and each value is
// compared to the threshold independently. The rule fires as ONE instance
// whose summary enumerates every breaching value (e.g. each DLQ queue
// that's actually backed up) with a count; it resolves only when no value
// breaches. The breakdown is refreshed on every evaluation while the
// instance stays open, since which values breach drifts over time.
func (e *Engine) evaluateSplitRule(ctx context.Context, rule AlertRule) {
	to := time.Now().UTC()
	from := to.Add(-rule.Spec.ForWindowDuration())
	groups, err := e.eval.MetricAggregateGrouped(ctx, rule.Spec.MetricName, rule.Spec.Attrs, string(rule.Spec.Aggregation), rule.Spec.SplitBy, rule.ServiceName, rule.IntegrationID, from, to)
	if err != nil {
		e.log.Error("alert eval: grouped aggregate failed", "rule", rule.ID, "err", err)
		return
	}
	var breaching []MetricGroup
	for _, g := range groups {
		if g.Samples > 0 && EvaluateBreach(rule.Spec.Operator, g.Value, rule.Spec.Threshold) {
			breaching = append(breaching, g)
		}
	}
	breached := len(breaching) > 0

	// Persist a reading for "show on service page" — the worst (highest)
	// group value — so a split-by check's value tile isn't perpetually empty
	// (the scalar path records its single value; the split path didn't).
	if rule.DisplayOnService {
		var worst float64
		var have bool
		for _, g := range groups {
			if g.Samples > 0 && (!have || g.Value > worst) {
				worst, have = g.Value, true
			}
		}
		if have {
			if err := e.store.RecordReading(ctx, rule.ID, worst); err != nil {
				e.log.Warn("alert eval: record reading failed", "rule", rule.ID, "err", err)
			}
		}
	}

	active, err := e.store.ActiveInstance(ctx, rule.ID)
	if err != nil {
		e.log.Error("alert eval: active instance failed", "rule", rule.ID, "err", err)
		return
	}

	switch {
	case breached && active == nil:
		labels := splitRuleLabels(rule, breaching)
		summary := splitRuleSummary(rule, breaching, "firing")
		inst, err := e.store.OpenInstance(ctx, rule.ID, "all", labels, summary)
		if err != nil {
			e.log.Error("alert eval: open instance failed", "rule", rule.ID, "err", err)
			return
		}
		if err := e.enqueueFiring(ctx, inst.ID, rule); err != nil {
			e.log.Error("alert eval: enqueue failed", "rule", rule.ID, "err", err)
		}
		e.log.Info("alert firing (split)", "rule", rule.Name, "breaching", len(breaching), "split_by", rule.Spec.SplitBy, "channels", len(rule.ChannelIDs))
	case breached && active != nil:
		// Which values breach (and their readings) drifts while the
		// instance stays open — refresh the stored breakdown each tick.
		labels := splitRuleLabels(rule, breaching)
		summary := splitRuleSummary(rule, breaching, "firing")
		if err := e.store.RefreshInstance(ctx, active.ID, labels, summary); err != nil {
			e.log.Error("alert eval: refresh failed", "rule", rule.ID, "err", err)
		}
	case !breached && active != nil:
		e.resolveOrHold(ctx, active, rule, splitRuleSummary(rule, nil, "resolved"), "metric (split)")
	}
}

// splitEnumCap bounds how many breaching values the summary lists
// verbatim; any beyond it are summarised as "+N more".
const splitEnumCap = 12

func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// splitRuleSummary renders a split-by firing/resolution: the count of
// breaching split values plus the values and their readings (highest
// first, since the store orders groups by value desc).
func splitRuleSummary(rule AlertRule, breaching []MetricGroup, state string) string {
	op := opGlyph[rule.Spec.Operator]
	th := fmtFloat(rule.Spec.Threshold)
	cond := fmt.Sprintf("%s %s %s %s", rule.Spec.Aggregation, rule.Spec.MetricName, op, th)
	if state == "resolved" || len(breaching) == 0 {
		return fmt.Sprintf("%s — no %s breaching %s", rule.Name, rule.Spec.SplitBy, cond)
	}
	shown := len(breaching)
	if shown > splitEnumCap {
		shown = splitEnumCap
	}
	parts := make([]string, 0, shown)
	for _, g := range breaching[:shown] {
		label := g.Label
		if label == "" {
			label = "(unset)"
		}
		parts = append(parts, fmt.Sprintf("%s=%s", label, fmtFloat(g.Value)))
	}
	out := fmt.Sprintf("%s — %d %s breaching %s: %s",
		rule.Name, len(breaching), rule.Spec.SplitBy, cond, strings.Join(parts, ", "))
	if len(breaching) > shown {
		out += fmt.Sprintf(" +%d more", len(breaching)-shown)
	}
	return out
}

// splitRuleLabels denormalises a split-by firing onto the instance for
// delivery rendering: rule context plus the split key and breach count.
func splitRuleLabels(rule AlertRule, breaching []MetricGroup) map[string]string {
	return map[string]string{
		"rule_id":      rule.ID.String(),
		"rule_name":    rule.Name,
		"metric":       rule.Spec.MetricName,
		"aggregation":  string(rule.Spec.Aggregation),
		"operator":     string(rule.Spec.Operator),
		"threshold":    fmtFloat(rule.Spec.Threshold),
		"split_by":     rule.Spec.SplitBy,
		"breach_count": strconv.Itoa(len(breaching)),
		"severity":     string(rule.Severity),
	}
}

// evaluateLogOnce evaluates every enabled log rule and drives its
// instance state machine — the log analogue of evaluateOnce.
func (e *Engine) evaluateLogOnce(ctx context.Context) {
	rules, err := e.store.EnabledLogRules(ctx, e.org)
	if err != nil {
		e.log.Error("log alert eval: list rules failed", "err", err)
		return
	}
	for _, rule := range rules {
		e.evaluateLogRule(ctx, rule)
	}
}

// evaluateLogRule counts the logs matching the rule over its trailing
// window and fires/resolves an instance just like a metric rule:
// breached when count >= threshold; auto-resolves when it drops back
// under. One instance per rule (fingerprint "all"). A firing rule bound
// to a service marks that service unhealthy for free (FiringHealthServices
// keys on service_name regardless of signal).
func (e *Engine) evaluateLogRule(ctx context.Context, rule AlertRule) {
	if rule.LogSpec == nil {
		return
	}
	spec := *rule.LogSpec
	threshold := spec.Threshold
	if threshold < 1 {
		threshold = 1
	}
	to := time.Now().UTC()
	from := to.Add(-spec.WindowDuration())
	count, err := e.logEval.CountLogs(ctx, LogCountQuery{
		MinSeverity:   spec.MinSeverity,
		BodyContains:  spec.BodyContains,
		Attrs:         spec.Attrs,
		ServiceName:   rule.ServiceName,
		IntegrationID: rule.IntegrationID,
		From:          from,
		To:            to,
	})
	if err != nil {
		e.log.Error("log alert eval: count failed", "rule", rule.ID, "err", err)
		return
	}
	// Direction depends on the spec's comparison: the default "at_least"
	// fires on a flood (count ≥ threshold); "fewer_than" fires on a drought
	// (count < threshold), where zero matching logs is the canonical breach
	// and so deliberately has no no-data skip.
	breached := count >= uint64(threshold)
	if spec.FiresBelow() {
		breached = count < uint64(threshold)
	}

	active, err := e.store.ActiveInstance(ctx, rule.ID)
	if err != nil {
		e.log.Error("log alert eval: active instance failed", "rule", rule.ID, "err", err)
		return
	}

	switch {
	case breached && active == nil:
		labels := logRuleLabels(rule, count)
		summary := logRuleSummary(rule, count, "firing")
		inst, err := e.store.OpenInstance(ctx, rule.ID, "all", labels, summary)
		if err != nil {
			e.log.Error("log alert eval: open instance failed", "rule", rule.ID, "err", err)
			return
		}
		if err := e.enqueueFiring(ctx, inst.ID, rule); err != nil {
			e.log.Error("log alert eval: enqueue failed", "rule", rule.ID, "err", err)
		}
		e.log.Info("log alert firing", "rule", rule.Name, "count", count, "threshold", threshold, "channels", len(rule.ChannelIDs))
	case breached && active != nil:
		if err := e.store.TouchInstance(ctx, active.ID); err != nil {
			e.log.Error("log alert eval: touch failed", "rule", rule.ID, "err", err)
		}
	case !breached && active != nil:
		e.resolveOrHold(ctx, active, rule, logRuleSummary(rule, count, "resolved"), "log")
	}
}

// renotifyOnce drives a profile's delivery behaviour across every firing,
// unacknowledged alert: re-paging on the profile's re-notify interval, and
// coalescing per-integration profiles into a single notification per
// integration. Acknowledged alerts are skipped — an operator is already on
// them. Runs on the eval cadence.
func (e *Engine) renotifyOnce(ctx context.Context) {
	insts, err := e.store.FiringUnackedInstances(ctx, e.org)
	if err != nil {
		e.log.Error("renotify: list firing instances failed", "err", err)
		return
	}
	if len(insts) == 0 {
		return
	}

	type item struct {
		inst     FiringUnackedInstance
		rule     AlertRule
		renotify int
	}
	perCheck := make([]item, 0, len(insts))
	perInteg := map[uuid.UUID][]item{}
	ruleCache := map[uuid.UUID]AlertRule{}
	for _, fi := range insts {
		rule, ok := ruleCache[fi.RuleID]
		if !ok {
			r, gerr := e.store.GetRule(ctx, e.org, fi.RuleID)
			if gerr != nil {
				e.log.Warn("renotify: load rule failed", "rule", fi.RuleID, "err", gerr)
				continue
			}
			rule = r
			ruleCache[fi.RuleID] = r
		}
		grouping, renotify := e.behavior(ctx, rule)
		it := item{inst: fi, rule: rule, renotify: renotify}
		if grouping == groupingPerIntegration && rule.IntegrationID != nil {
			perInteg[*rule.IntegrationID] = append(perInteg[*rule.IntegrationID], it)
		} else {
			perCheck = append(perCheck, it)
		}
	}

	now := time.Now().UTC()
	// due reports whether a notification stream should page now: never-paged
	// streams (last == nil) always send; otherwise only when a positive
	// re-notify interval has elapsed.
	due := func(last *time.Time, renotify int) bool {
		if last == nil {
			return true
		}
		if renotify <= 0 {
			return false
		}
		return now.Sub(*last) >= time.Duration(renotify)*time.Minute
	}

	// Per-check: each firing alert re-pages independently once its profile's
	// interval elapses. The first page normally went out inline; a nil
	// watermark here means the instance was muted at birth by a maintenance
	// window — once no window covers the rule anymore, send that overdue
	// first page.
	for _, it := range perCheck {
		if w := e.suppressedBy(ctx, it.rule); w != nil {
			// Covered right now — stay quiet. Stamp instances that started
			// inside the window but haven't been marked yet (per-integration
			// births reach here without passing enqueueFiring).
			if it.inst.LastNotifiedAt == nil && it.inst.SuppressedBy == nil {
				if err := e.store.MarkInstanceSuppressed(ctx, it.inst.InstanceID, *w); err != nil {
					e.log.Warn("mark suppressed failed", "instance", it.inst.InstanceID, "err", err)
				}
			}
			continue
		}
		if it.inst.LastNotifiedAt == nil {
			if it.inst.SuppressedBy != nil {
				e.renotifyInstance(ctx, it.inst.InstanceID, it.rule, "notified (maintenance ended)")
			}
			continue
		}
		if it.renotify <= 0 {
			continue
		}
		if due(it.inst.LastNotifiedAt, it.renotify) {
			e.renotifyInstance(ctx, it.inst.InstanceID, it.rule, "re-notified")
		}
	}

	// Per-integration: one notification stream per integration. The
	// representative (earliest-firing alert) carries it; every other firing
	// check on the integration is folded in silently (still visible on the
	// integration's Errors page). Send on first sight and on the interval.
	for integ, items := range perInteg {
		rep := items[0]
		for _, it := range items[1:] {
			if it.inst.StartedAt.Before(rep.inst.StartedAt) {
				rep = it
			}
		}
		// Maintenance window covering the integration's rules: mute the
		// whole stream, stamping unmarked instances so their eventual
		// resolve stays silent too.
		if w := e.suppressedBy(ctx, rep.rule); w != nil {
			for _, it := range items {
				if it.inst.LastNotifiedAt == nil && it.inst.SuppressedBy == nil {
					if err := e.store.MarkInstanceSuppressed(ctx, it.inst.InstanceID, *w); err != nil {
						e.log.Warn("mark suppressed failed", "instance", it.inst.InstanceID, "err", err)
					}
				}
			}
			continue
		}
		if due(rep.inst.LastNotifiedAt, rep.renotify) {
			e.renotifyInstance(ctx, rep.inst.InstanceID, rep.rule, "integration-notified")
			e.log.Info("integration alert coalesced", "integration", integ, "firing_checks", len(items))
		}
	}
}

// renotifyInstance re-enqueues a firing instance's delivery jobs and
// re-stamps its notify watermark.
func (e *Engine) renotifyInstance(ctx context.Context, instanceID uuid.UUID, rule AlertRule, reason string) {
	if err := e.store.EnqueueJobs(ctx, instanceID, e.channelsFor(ctx, rule)); err != nil {
		e.log.Error("renotify: enqueue failed", "instance", instanceID, "err", err)
		return
	}
	if err := e.store.MarkInstanceNotified(ctx, instanceID); err != nil {
		e.log.Error("renotify: mark notified failed", "instance", instanceID, "err", err)
	}
	e.log.Info("alert "+reason, "rule", rule.Name, "instance", instanceID)
}

// deliverOnce claims due jobs and delivers them, recording success or a
// retryable failure.
func (e *Engine) deliverOnce(ctx context.Context) {
	jobs, err := e.store.ClaimDueJobs(ctx, 20)
	if err != nil {
		e.log.Error("alert delivery: claim failed", "err", err)
		return
	}
	for _, job := range jobs {
		msg, err := deliver(ctx, e.client, job)
		if err != nil {
			backoff := time.Duration(1<<job.Attempts) * 30 * time.Second
			if ferr := e.store.MarkJobFailed(ctx, job.JobID, job.Attempts, e.maxAttempts, err.Error(), backoff); ferr != nil {
				e.log.Error("alert delivery: mark failed errored", "job", job.JobID, "err", ferr)
			}
			e.log.Warn("alert delivery failed", "job", job.JobID, "channel", job.Channel.Name, "kind", job.Channel.Kind, "err", err)
			continue
		}
		// Persist the rendered subject/body so the delivery-history
		// view shows exactly what was sent, even if the rule's
		// template changes later.
		if err := e.store.MarkJobSucceeded(ctx, job.JobID, msg.Subject, msg.Body); err != nil {
			e.log.Error("alert delivery: mark succeeded errored", "job", job.JobID, "err", err)
		}
		e.log.Info("alert delivered", "channel", job.Channel.Name, "kind", job.Channel.Kind, "state", job.State)
	}
}

// ruleLabels denormalises a firing rule's context onto the instance, so
// delivery can render a payload without re-reading the rule.
func ruleLabels(rule AlertRule, value float64) map[string]string {
	return map[string]string{
		"rule_id":     rule.ID.String(),
		"rule_name":   rule.Name,
		"metric":      rule.Spec.MetricName,
		"aggregation": string(rule.Spec.Aggregation),
		"operator":    string(rule.Spec.Operator),
		"threshold":   strconv.FormatFloat(rule.Spec.Threshold, 'f', -1, 64),
		"value":       strconv.FormatFloat(value, 'f', -1, 64),
		"severity":    string(rule.Severity),
	}
}

var opGlyph = map[Operator]string{OpGT: ">", OpGTE: "≥", OpLT: "<", OpLTE: "≤", OpEQ: "=", OpNEQ: "≠"}

func ruleSummary(rule AlertRule, value float64, state string) string {
	v := strconv.FormatFloat(value, 'f', 2, 64)
	th := strconv.FormatFloat(rule.Spec.Threshold, 'f', -1, 64)
	if state == "resolved" {
		return fmt.Sprintf("%s — %s %s recovered to %s (threshold %s %s)",
			rule.Name, rule.Spec.Aggregation, rule.Spec.MetricName, v, opGlyph[rule.Spec.Operator], th)
	}
	return fmt.Sprintf("%s — %s %s = %s %s %s",
		rule.Name, rule.Spec.Aggregation, rule.Spec.MetricName, v, opGlyph[rule.Spec.Operator], th)
}

// logRuleLabels denormalises a firing log rule's context onto the
// instance for delivery rendering (same role as ruleLabels for metrics).
func logRuleLabels(rule AlertRule, count uint64) map[string]string {
	return map[string]string{
		"rule_id":   rule.ID.String(),
		"rule_name": rule.Name,
		"signal":    "log",
		"severity":  string(rule.Severity),
		"count":     strconv.FormatUint(count, 10),
	}
}

// logCriteria renders a log rule's match criteria compactly for summaries.
func logCriteria(spec LogRuleSpec) string {
	parts := []string{}
	if spec.MinSeverity > 0 {
		parts = append(parts, fmt.Sprintf("severity≥%d", spec.MinSeverity))
	}
	if spec.BodyContains != "" {
		parts = append(parts, fmt.Sprintf("contains %q", spec.BodyContains))
	}
	for _, a := range spec.Attrs {
		parts = append(parts, fmt.Sprintf("%s %s %s", a.Key, a.Op, a.Value))
	}
	if len(parts) == 0 {
		return "any log"
	}
	return strings.Join(parts, ", ")
}

func logRuleSummary(rule AlertRule, count uint64, state string) string {
	spec := LogRuleSpec{}
	if rule.LogSpec != nil {
		spec = *rule.LogSpec
	}
	threshold := spec.Threshold
	if threshold < 1 {
		threshold = 1
	}
	win := spec.WindowDuration().String()
	op := "≥"
	if spec.FiresBelow() {
		op = "<"
	}
	if state == "resolved" {
		return fmt.Sprintf("%s — recovered: %d matching logs in %s (threshold %s%d)",
			rule.Name, count, win, op, threshold)
	}
	return fmt.Sprintf("%s — %d matching logs in %s (threshold %s%d) [%s]",
		rule.Name, count, win, op, threshold, logCriteria(spec))
}

// ── trace_error rules ─────────────────────────────────────────────────
//
// A trace_error rule fires when its bound integration accumulates
// >= threshold failed traces (a trace with at least one error span) over
// the trailing window. Mirrors the log-rule path: count → breach →
// open/touch/resolve one instance per rule, reusing the same delivery
// pipeline. A firing rule on an integration shows up on the Errors tab's
// "failing health checks" section for free (it's an alert_instance).

func (e *Engine) evaluateTraceErrorOnce(ctx context.Context) {
	rules, err := e.store.EnabledTraceErrorRules(ctx, e.org)
	if err != nil {
		e.log.Error("trace alert eval: list rules failed", "err", err)
		return
	}
	for _, rule := range rules {
		e.evaluateTraceErrorRule(ctx, rule)
	}
}

func (e *Engine) evaluateTraceErrorRule(ctx context.Context, rule AlertRule) {
	// A failed-trace rule must be scoped to an integration or a single
	// service; without either there's nothing to count.
	if rule.TraceErrorSpec == nil || (rule.IntegrationID == nil && rule.ServiceName == "") {
		return
	}
	spec := *rule.TraceErrorSpec
	threshold := spec.Threshold
	if threshold < 1 {
		threshold = 1
	}
	to := time.Now().UTC()
	from := to.Add(-spec.WindowDuration())
	count, err := e.traceEval.CountErrorTraces(ctx, TraceErrorQuery{
		IntegrationID: rule.IntegrationID,
		ServiceName:   rule.ServiceName,
		From:          from,
		To:            to,
	})
	if err != nil {
		e.log.Error("trace alert eval: count failed", "rule", rule.ID, "err", err)
		return
	}
	breached := count >= uint64(threshold)

	active, err := e.store.ActiveInstance(ctx, rule.ID)
	if err != nil {
		e.log.Error("trace alert eval: active instance failed", "rule", rule.ID, "err", err)
		return
	}

	switch {
	case breached && active == nil:
		labels := traceErrorRuleLabels(rule, count)
		summary := traceErrorRuleSummary(rule, count, "firing")
		inst, err := e.store.OpenInstance(ctx, rule.ID, "all", labels, summary)
		if err != nil {
			e.log.Error("trace alert eval: open instance failed", "rule", rule.ID, "err", err)
			return
		}
		if err := e.enqueueFiring(ctx, inst.ID, rule); err != nil {
			e.log.Error("trace alert eval: enqueue failed", "rule", rule.ID, "err", err)
		}
		e.log.Info("trace alert firing", "rule", rule.Name, "count", count, "threshold", threshold, "channels", len(rule.ChannelIDs))
	case breached && active != nil:
		if err := e.store.TouchInstance(ctx, active.ID); err != nil {
			e.log.Error("trace alert eval: touch failed", "rule", rule.ID, "err", err)
		}
	case !breached && active != nil:
		e.resolveOrHold(ctx, active, rule, traceErrorRuleSummary(rule, count, "resolved"), "trace")
	}
}

func traceErrorRuleLabels(rule AlertRule, count uint64) map[string]string {
	return map[string]string{
		"rule_id":   rule.ID.String(),
		"rule_name": rule.Name,
		"signal":    "trace_error",
		"severity":  string(rule.Severity),
		"count":     strconv.FormatUint(count, 10),
	}
}

func traceErrorRuleSummary(rule AlertRule, count uint64, state string) string {
	threshold := 1
	win := "5m"
	if rule.TraceErrorSpec != nil {
		if rule.TraceErrorSpec.Threshold > threshold {
			threshold = rule.TraceErrorSpec.Threshold
		}
		win = rule.TraceErrorSpec.WindowDuration().String()
	}
	if state == "resolved" {
		return fmt.Sprintf("%s — recovered: %d failed traces in %s (threshold ≥%d)",
			rule.Name, count, win, threshold)
	}
	return fmt.Sprintf("%s — %d failed traces in %s (threshold ≥%d)",
		rule.Name, count, win, threshold)
}

// ── trace_latency rules (response time) ───────────────────────────────
//
// A trace_latency rule fires when the aggregate span latency (p95 or max)
// over the trailing window exceeds ThresholdMs, for the bound service or
// integration. Mirrors the trace_error path: aggregate → breach →
// open/touch/resolve one instance per rule, through the same delivery +
// resolve-mode pipeline.

func (e *Engine) evaluateTraceLatencyOnce(ctx context.Context) {
	rules, err := e.store.EnabledTraceLatencyRules(ctx, e.org)
	if err != nil {
		e.log.Error("latency alert eval: list rules failed", "err", err)
		return
	}
	for _, rule := range rules {
		e.evaluateTraceLatencyRule(ctx, rule)
	}
}

func (e *Engine) evaluateTraceLatencyRule(ctx context.Context, rule AlertRule) {
	if rule.TraceLatencySpec == nil || (rule.IntegrationID == nil && rule.ServiceName == "") {
		return
	}
	spec := *rule.TraceLatencySpec
	to := time.Now().UTC()
	from := to.Add(-spec.WindowDuration())
	latencyMs, samples, err := e.latencyEval.TraceLatencyMs(ctx, TraceLatencyQuery{
		IntegrationID: rule.IntegrationID,
		ServiceName:   rule.ServiceName,
		Quantile:      spec.Quantile(),
		From:          from,
		To:            to,
	})
	if err != nil {
		e.log.Error("latency alert eval: query failed", "rule", rule.ID, "err", err)
		return
	}
	// No spans in the window → no signal; don't fire or resolve on no-data.
	breached := samples > 0 && spec.ThresholdMs > 0 && latencyMs >= float64(spec.ThresholdMs)

	active, err := e.store.ActiveInstance(ctx, rule.ID)
	if err != nil {
		e.log.Error("latency alert eval: active instance failed", "rule", rule.ID, "err", err)
		return
	}

	switch {
	case breached && active == nil:
		labels := traceLatencyRuleLabels(rule, latencyMs)
		summary := traceLatencyRuleSummary(rule, latencyMs, "firing")
		inst, err := e.store.OpenInstance(ctx, rule.ID, "all", labels, summary)
		if err != nil {
			e.log.Error("latency alert eval: open instance failed", "rule", rule.ID, "err", err)
			return
		}
		if err := e.enqueueFiring(ctx, inst.ID, rule); err != nil {
			e.log.Error("latency alert eval: enqueue failed", "rule", rule.ID, "err", err)
		}
		e.log.Info("latency alert firing", "rule", rule.Name, "latency_ms", latencyMs, "threshold_ms", spec.ThresholdMs)
	case breached && active != nil:
		if err := e.store.TouchInstance(ctx, active.ID); err != nil {
			e.log.Error("latency alert eval: touch failed", "rule", rule.ID, "err", err)
		}
	case !breached && active != nil:
		e.resolveOrHold(ctx, active, rule, traceLatencyRuleSummary(rule, latencyMs, "resolved"), "latency")
	}
}

func traceLatencyRuleLabels(rule AlertRule, latencyMs float64) map[string]string {
	agg := "p95"
	if rule.TraceLatencySpec != nil && rule.TraceLatencySpec.Aggregation == "max" {
		agg = "max"
	}
	return map[string]string{
		"rule_id":     rule.ID.String(),
		"rule_name":   rule.Name,
		"signal":      "trace_latency",
		"aggregation": agg,
		"severity":    string(rule.Severity),
		"latency_ms":  strconv.FormatInt(int64(latencyMs), 10),
	}
}

func traceLatencyRuleSummary(rule AlertRule, latencyMs float64, state string) string {
	agg := "p95"
	thresholdMs := 0
	win := "5m"
	if rule.TraceLatencySpec != nil {
		if rule.TraceLatencySpec.Aggregation == "max" {
			agg = "max"
		}
		thresholdMs = rule.TraceLatencySpec.ThresholdMs
		win = rule.TraceLatencySpec.WindowDuration().String()
	}
	if state == "resolved" {
		return fmt.Sprintf("%s — recovered: %s response time %dms in %s (threshold ≥%dms)",
			rule.Name, agg, int64(latencyMs), win, thresholdMs)
	}
	return fmt.Sprintf("%s — %s response time %dms in %s (threshold ≥%dms)",
		rule.Name, agg, int64(latencyMs), win, thresholdMs)
}

// --- low-traffic (trace volume) checks ---------------------------------
//
// A trace_volume rule fires when a service/integration produces *fewer*
// than Threshold distinct traces over the window — a dead-man's-switch for
// a pipeline that has gone quiet. Zero traces is the canonical breach, so
// (unlike latency/error checks) there is no no-data skip.

func (e *Engine) evaluateTraceVolumeOnce(ctx context.Context) {
	rules, err := e.store.EnabledTraceVolumeRules(ctx, e.org)
	if err != nil {
		e.log.Error("volume alert eval: list rules failed", "err", err)
		return
	}
	for _, rule := range rules {
		e.evaluateTraceVolumeRule(ctx, rule)
	}
}

func (e *Engine) evaluateTraceVolumeRule(ctx context.Context, rule AlertRule) {
	if rule.TraceVolumeSpec == nil || (rule.IntegrationID == nil && rule.ServiceName == "") {
		return
	}
	spec := *rule.TraceVolumeSpec
	to := time.Now().UTC()
	from := to.Add(-spec.WindowDuration())
	total, err := e.volumeEval.TotalTraces(ctx, TraceVolumeQuery{
		IntegrationID: rule.IntegrationID,
		ServiceName:   rule.ServiceName,
		From:          from,
		To:            to,
	})
	if err != nil {
		e.log.Error("volume alert eval: query failed", "rule", rule.ID, "err", err)
		return
	}
	// Below the floor → unhealthy. Zero counts (the silent-service case the
	// user explicitly asked to catch) fire just like any other shortfall.
	breached := spec.Threshold > 0 && total < uint64(spec.Threshold)

	active, err := e.store.ActiveInstance(ctx, rule.ID)
	if err != nil {
		e.log.Error("volume alert eval: active instance failed", "rule", rule.ID, "err", err)
		return
	}

	switch {
	case breached && active == nil:
		labels := traceVolumeRuleLabels(rule, total)
		summary := traceVolumeRuleSummary(rule, total, "firing")
		inst, err := e.store.OpenInstance(ctx, rule.ID, "all", labels, summary)
		if err != nil {
			e.log.Error("volume alert eval: open instance failed", "rule", rule.ID, "err", err)
			return
		}
		if err := e.enqueueFiring(ctx, inst.ID, rule); err != nil {
			e.log.Error("volume alert eval: enqueue failed", "rule", rule.ID, "err", err)
		}
		e.log.Info("volume alert firing", "rule", rule.Name, "traces", total, "threshold", spec.Threshold)
	case breached && active != nil:
		if err := e.store.TouchInstance(ctx, active.ID); err != nil {
			e.log.Error("volume alert eval: touch failed", "rule", rule.ID, "err", err)
		}
	case !breached && active != nil:
		e.resolveOrHold(ctx, active, rule, traceVolumeRuleSummary(rule, total, "resolved"), "volume")
	}
}

func traceVolumeRuleLabels(rule AlertRule, total uint64) map[string]string {
	return map[string]string{
		"rule_id":   rule.ID.String(),
		"rule_name": rule.Name,
		"signal":    "trace_volume",
		"severity":  string(rule.Severity),
		"traces":    strconv.FormatUint(total, 10),
	}
}

func traceVolumeRuleSummary(rule AlertRule, total uint64, state string) string {
	threshold := 0
	win := "5m"
	if rule.TraceVolumeSpec != nil {
		threshold = rule.TraceVolumeSpec.Threshold
		win = rule.TraceVolumeSpec.WindowDuration().String()
	}
	if state == "resolved" {
		return fmt.Sprintf("%s — recovered: %d traces in %s (floor <%d)",
			rule.Name, total, win, threshold)
	}
	return fmt.Sprintf("%s — only %d traces in %s (floor <%d)",
		rule.Name, total, win, threshold)
}
