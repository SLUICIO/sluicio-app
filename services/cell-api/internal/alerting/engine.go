// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"fmt"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/cellhealth"
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
	MetricAggregate(ctx context.Context, metricName string, attrs []AttrFilter, aggregation, serviceName string, integrationID, systemID *uuid.UUID, from, to time.Time) (float64, uint64, error)
	// MetricAggregateGrouped reduces the metric per distinct value of
	// splitKey, for split-by rules. One MetricGroup per value.
	MetricAggregateGrouped(ctx context.Context, metricName string, attrs []AttrFilter, aggregation, splitKey, serviceName string, integrationID, systemID *uuid.UUID, from, to time.Time) ([]MetricGroup, error)
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
	SystemID      *uuid.UUID // bound system (nil = none)
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
	SystemID      *uuid.UUID
	ServiceName   string
	From, To      time.Time
	// Attrs narrow which error spans make a trace count as failed
	// (AND-ed span/resource attribute predicates; empty = any error span).
	Attrs []AttrFilter
}

// TraceErrorCounter counts the distinct failed traces (traces with an
// error span) in a query's scope. Implemented by the ClickHouse store
// (adapted at wiring time), like LogCounter.
type TraceErrorCounter interface {
	CountErrorTraces(ctx context.Context, q TraceErrorQuery) (uint64, error)
}

// TraceAttributeQuery is the scope a trace_attribute rule counts matching
// traces for, over [From, To]. Scope resolution mirrors TraceErrorQuery.
type TraceAttributeQuery struct {
	IntegrationID *uuid.UUID
	SystemID      *uuid.UUID
	ServiceName   string
	From, To      time.Time
	// Attrs are the AND-ed span/resource predicates a span must satisfy
	// for its trace to count. Never empty — validated at the API edge.
	Attrs []AttrFilter
}

// TraceAttributeCounter counts the distinct traces in scope carrying a
// span that matches the query's predicates, whatever that span's status.
// Implemented by the ClickHouse store (adapted at wiring time).
type TraceAttributeCounter interface {
	CountMatchingTraces(ctx context.Context, q TraceAttributeQuery) (uint64, error)
}

// TraceLatencyQuery scopes a response-time check to an integration or a
// single service (exactly one set), aggregating span duration over
// [From,To] at Quantile (1.0 = max).
type TraceLatencyQuery struct {
	IntegrationID *uuid.UUID
	SystemID      *uuid.UUID
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
	SystemID      *uuid.UUID
	ServiceName   string
	From, To      time.Time
}

// TraceVolumeEvaluator returns the total distinct trace count for a query's
// scope. Implemented by the ClickHouse store (adapted at wiring time).
//
// Unlike the latency/error evaluators there is no "zero → skip": zero
// traces from a real scope is exactly the condition a low-traffic check
// exists to catch (dead-man's-switch). A named service that emitted
// nothing MUST fire.
//
// scoped=false is a different statement: the scope resolved to no
// services at all — a system or integration with no members yet. Nothing
// there COULD have traffic, so firing "traffic dropped" would page
// someone about a group that does not exist. The caller skips instead.
type TraceVolumeEvaluator interface {
	TotalTraces(ctx context.Context, q TraceVolumeQuery) (total uint64, scoped bool, err error)
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
	attrEval    TraceAttributeCounter
	resolver    ChannelResolver
	log         *slog.Logger
	org         uuid.UUID

	evalInterval time.Duration
	deliveryPoll time.Duration
	maxAttempts  int
	// A claimed job still 'running' after stuckAfter is assumed to belong
	// to a worker that is gone. Comfortably above any single delivery
	// (the HTTP client times out at 10s, SMTP well inside a minute), so
	// the sweep cannot race a delivery that is merely slow.
	stuckAfter      time.Duration
	reclaimInterval time.Duration
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

// SetTraceAttributeCounter wires the span-attribute evaluator. Left nil
// the loop never starts, which is how a cell without the ClickHouse
// adapter degrades: no rules evaluated rather than every rule erroring.
func (e *Engine) SetTraceAttributeCounter(c TraceAttributeCounter) { e.attrEval = c }

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
	if err := e.store.EnqueueJobs(ctx, instanceID, e.channelsFor(ctx, rule)); err != nil {
		return err
	}
	emitDomainEvent(ctx, e.org, alertDomainEvent("com.sluicio.alert.resolved", instanceID, rule))
	return nil
}

// alertDomainEvent builds the outbound event for one instance transition.
func alertDomainEvent(ceType string, instanceID uuid.UUID, rule AlertRule) DomainEvent {
	return DomainEvent{
		Type:          ceType,
		Subject:       rule.ID.String(),
		ServiceName:   rule.ServiceName,
		IntegrationID: rule.IntegrationID,
		Data: map[string]any{
			"rule_id":     rule.ID.String(),
			"rule_name":   rule.Name,
			"severity":    string(rule.Severity),
			"signal":      string(rule.Signal),
			"service":     rule.ServiceName,
			"instance_id": instanceID.String(),
		},
	}
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
	// The firing became a real notification (not suppressed, not folded)
	// — that's the moment it is also a domain event.
	emitDomainEvent(ctx, e.org, alertDomainEvent("com.sluicio.alert.fired", instanceID, rule))
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

		stuckAfter:      5 * time.Minute,
		reclaimInterval: time.Minute,
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
	if e.attrEval != nil {
		go e.loop(ctx, e.evalInterval, e.evaluateTraceAttributeOnce)
	}
	// Re-notification + per-integration coalescing run on the eval cadence,
	// off the set of firing unacked instances (independent of which signal
	// opened them).
	go e.loop(ctx, e.evalInterval, e.renotifyOnce)
	// Immediately, not on the first tick: the jobs worth recovering are
	// the ones the PREVIOUS process left behind, and an upgrade is
	// exactly when they appear.
	e.reclaimOnce(ctx)
	go e.loop(ctx, e.reclaimInterval, e.reclaimOnce)
	e.loop(ctx, e.deliveryPoll, e.deliverOnce)
}

func (e *Engine) loop(ctx context.Context, every time.Duration, tick func(context.Context)) {
	t := time.NewTicker(every)
	cellhealth.Register("alerting", every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick(ctx)
			// End of cycle, not start: a loop wedged inside its
			// own body is exactly what this catches.
			cellhealth.Beat("alerting")
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
	value, samples, err := e.eval.MetricAggregate(ctx, rule.Spec.MetricName, rule.Spec.Attrs, string(rule.Spec.Aggregation), rule.ServiceName, rule.IntegrationID, rule.SystemID, from, to)
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
	// everReported arms the no-data condition, and costs a query — so
	// only ask when the answer can change the outcome.
	everReported := false
	if samples == 0 && rule.Spec.FireOnNoData {
		reading, err := e.store.LatestReading(ctx, rule.ID)
		if err != nil {
			// Failing closed here would fire a no-data alert BECAUSE the
			// database hiccupped, which is the one false page this
			// feature must not produce.
			e.log.Warn("alert eval: latest reading failed; skipping no-data check", "rule", rule.ID, "err", err)
			return
		}
		everReported = reading != nil
	}
	outcome := DecideMetric(rule.Spec, samples, value, everReported)

	active, err := e.store.ActiveInstance(ctx, rule.ID)
	if err != nil {
		e.log.Error("alert eval: active instance failed", "rule", rule.ID, "err", err)
		return
	}

	summaryFor := func(state string) string {
		if outcome == OutcomeNoData {
			return noDataSummary(rule)
		}
		return ruleSummary(rule, value, state)
	}

	switch {
	case outcome.Firing() && active == nil:
		labels := ruleLabels(rule, value)
		if outcome == OutcomeNoData {
			markNoData(labels)
		}
		inst, err := e.store.OpenInstance(ctx, rule.ID, "all", labels, summaryFor("firing"))
		if err != nil {
			e.log.Error("alert eval: open instance failed", "rule", rule.ID, "err", err)
			return
		}
		if err := e.enqueueFiring(ctx, inst.ID, rule); err != nil {
			e.log.Error("alert eval: enqueue failed", "rule", rule.ID, "err", err)
		}
		e.log.Info("alert firing", "rule", rule.Name, "outcome", outcome, "value", value, "threshold", rule.Spec.Threshold, "channels", len(rule.ChannelIDs))
	case outcome.Firing() && active != nil:
		// A rule can cross between "breaching" and "stopped reporting"
		// while its alert stays open. Refreshing keeps the summary
		// describing what is actually wrong now, rather than whichever
		// condition happened to open it.
		labels := ruleLabels(rule, value)
		if outcome == OutcomeNoData {
			markNoData(labels)
		}
		if err := e.store.RefreshInstance(ctx, active.ID, labels, summaryFor("firing")); err != nil {
			e.log.Error("alert eval: refresh failed", "rule", rule.ID, "err", err)
		}
	case !outcome.Firing() && active != nil:
		// An unknown outcome resolves, as it always has — but it must not
		// claim to have measured a recovery it never saw.
		resolution := ruleSummary(rule, value, "resolved")
		if outcome == OutcomeUnknown {
			resolution = fmt.Sprintf("%s — %s stopped reporting; no longer evaluated",
				rule.Name, rule.Spec.MetricName)
		}
		e.resolveOrHold(ctx, active, rule, resolution, "metric")
	}
}

// markNoData tags an alert's labels as a no-data firing.
//
// It also blanks the numeric value, because ruleLabels fills it from the
// aggregate — which is 0 when nothing was measured. A notification
// rendering "value: 0" would state a reading that was never taken, and
// 0 is a plausible-looking number for most metrics, so the reader has no
// way to tell it apart from a real one.
func markNoData(labels map[string]string) {
	labels["condition"] = string(OutcomeNoData)
	// Kept as a key rather than deleted: templates may reference it, and
	// notification variable paths are additive-only.
	labels["value"] = "n/a"
}

// noDataSummary words a no-data firing. It deliberately quotes no value
// and no threshold: nothing was measured, so a comparison would be a
// fabrication, and the reader needs to know the difference between "the
// number is bad" and "there is no number".
func noDataSummary(rule AlertRule) string {
	return fmt.Sprintf("%s — %s stopped reporting (no data for %s)",
		rule.Name, rule.Spec.MetricName, rule.Spec.ForWindowDuration())
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
	groups, err := e.eval.MetricAggregateGrouped(ctx, rule.Spec.MetricName, rule.Spec.Attrs, string(rule.Spec.Aggregation), rule.Spec.SplitBy, rule.ServiceName, rule.IntegrationID, rule.SystemID, from, to)
	if err != nil {
		e.log.Error("alert eval: grouped aggregate failed", "rule", rule.ID, "err", err)
		return
	}
	var breaching []MetricGroup
	var totalSamples uint64
	for _, g := range groups {
		totalSamples += g.Samples
		if g.Samples > 0 && EvaluateBreach(rule.Spec.Operator, g.Value, rule.Spec.Threshold) {
			breaching = append(breaching, g)
		}
	}
	breached := len(breaching) > 0

	// No-data on a split rule means the metric went silent ENTIRELY.
	// A single split value disappearing cannot be detected — there is no
	// record of which values are meant to exist, only which ones reported
	// — so this catches the collector dying, not one queue going away.
	noData := false
	if totalSamples == 0 && rule.Spec.FireOnNoData {
		reading, err := e.store.LatestReading(ctx, rule.ID)
		if err != nil {
			e.log.Warn("alert eval: latest reading failed; skipping no-data check", "rule", rule.ID, "err", err)
			return
		}
		noData = reading != nil
	}
	if noData {
		breached = true
	}

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
		if noData {
			markNoData(labels)
			summary = noDataSummary(rule)
		}
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
		if noData {
			markNoData(labels)
			summary = noDataSummary(rule)
		}
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
		SystemID:      rule.SystemID,
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

// reclaimOnce re-queues deliveries left 'running' by a worker that died
// mid-send. Logged at info when it finds any: a restart during delivery
// is normal, silently losing the notification is not.
func (e *Engine) reclaimOnce(ctx context.Context) {
	n, err := e.store.ReclaimStuckJobs(ctx, e.stuckAfter)
	if err != nil {
		e.log.Error("alert delivery: reclaim stuck jobs failed", "err", err)
		return
	}
	if n > 0 {
		e.log.Info("alert delivery: re-queued interrupted deliveries", "jobs", n, "stuck_after", e.stuckAfter)
	}
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

// ruleHasScope reports whether a trace rule names something to count
// over. A failed-trace / latency / low-traffic rule is meaningless
// unbounded — "how many traces failed, anywhere?" is not a health check —
// so the evaluators skip a rule with no scope rather than scanning the
// whole cell.
//
// A system counts, since 0077: it resolves to its member services just
// as an integration resolves to its own.
func ruleHasScope(rule AlertRule) bool {
	return rule.IntegrationID != nil || rule.SystemID != nil || rule.ServiceName != ""
}

func (e *Engine) evaluateTraceErrorRule(ctx context.Context, rule AlertRule) {
	// A failed-trace rule must be scoped to an integration or a single
	// service; without either there's nothing to count.
	if rule.TraceErrorSpec == nil || !ruleHasScope(rule) {
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
		SystemID:      rule.SystemID,
		ServiceName:   rule.ServiceName,
		From:          from,
		To:            to,
		Attrs:         spec.Attrs,
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
	criteria := ""
	if rule.TraceErrorSpec != nil && len(rule.TraceErrorSpec.Attrs) > 0 {
		parts := make([]string, 0, len(rule.TraceErrorSpec.Attrs))
		for _, a := range rule.TraceErrorSpec.Attrs {
			parts = append(parts, fmt.Sprintf("%s %s %s", a.Key, a.Op, a.Value))
		}
		criteria = " [" + strings.Join(parts, ", ") + "]"
	}
	if state == "resolved" {
		return fmt.Sprintf("%s — recovered: %d failed traces in %s (threshold ≥%d)%s",
			rule.Name, count, win, threshold, criteria)
	}
	return fmt.Sprintf("%s — %d failed traces in %s (threshold ≥%d)%s",
		rule.Name, count, win, threshold, criteria)
}

// ── trace_attribute rules (span attribute value) ──────────────────────
//
// A trace_attribute rule fires when >= threshold distinct traces in scope
// carry a span matching the rule's attribute predicates over the trailing
// window — with no requirement that the span be an error. The path is the
// trace_error path with the status condition dropped; everything after
// the count (breach, open/touch/resolve, delivery) is shared.

func (e *Engine) evaluateTraceAttributeOnce(ctx context.Context) {
	rules, err := e.store.EnabledTraceAttributeRules(ctx, e.org)
	if err != nil {
		e.log.Error("trace attribute alert eval: list rules failed", "err", err)
		return
	}
	for _, rule := range rules {
		e.evaluateTraceAttributeRule(ctx, rule)
	}
}

func (e *Engine) evaluateTraceAttributeRule(ctx context.Context, rule AlertRule) {
	// No scope, or no predicates, means nothing to count. The empty-Attrs
	// guard is defence in depth: the API refuses to store such a rule,
	// but a rule that counted EVERY trace would fire immediately and look
	// like a broken product rather than a rejected input.
	if rule.TraceAttributeSpec == nil || !ruleHasScope(rule) {
		return
	}
	spec := *rule.TraceAttributeSpec
	if len(spec.Attrs) == 0 {
		return
	}
	threshold := spec.Threshold
	if threshold < 1 {
		threshold = 1
	}
	to := time.Now().UTC()
	from := to.Add(-spec.WindowDuration())
	count, err := e.attrEval.CountMatchingTraces(ctx, TraceAttributeQuery{
		IntegrationID: rule.IntegrationID,
		SystemID:      rule.SystemID,
		ServiceName:   rule.ServiceName,
		From:          from,
		To:            to,
		Attrs:         spec.Attrs,
	})
	if err != nil {
		e.log.Error("trace attribute alert eval: count failed", "rule", rule.ID, "err", err)
		return
	}
	breached := count >= uint64(threshold)

	active, err := e.store.ActiveInstance(ctx, rule.ID)
	if err != nil {
		e.log.Error("trace attribute alert eval: active instance failed", "rule", rule.ID, "err", err)
		return
	}

	switch {
	case breached && active == nil:
		labels := traceAttributeRuleLabels(rule, count)
		summary := traceAttributeRuleSummary(rule, count, "firing")
		inst, err := e.store.OpenInstance(ctx, rule.ID, "all", labels, summary)
		if err != nil {
			e.log.Error("trace attribute alert eval: open instance failed", "rule", rule.ID, "err", err)
			return
		}
		if err := e.enqueueFiring(ctx, inst.ID, rule); err != nil {
			e.log.Error("trace attribute alert eval: enqueue failed", "rule", rule.ID, "err", err)
		}
		e.log.Info("trace attribute alert firing", "rule", rule.Name, "count", count, "threshold", threshold, "channels", len(rule.ChannelIDs))
	case breached && active != nil:
		if err := e.store.TouchInstance(ctx, active.ID); err != nil {
			e.log.Error("trace attribute alert eval: touch failed", "rule", rule.ID, "err", err)
		}
	case !breached && active != nil:
		e.resolveOrHold(ctx, active, rule, traceAttributeRuleSummary(rule, count, "resolved"), "trace")
	}
}

func traceAttributeRuleLabels(rule AlertRule, count uint64) map[string]string {
	return map[string]string{
		"rule_id":   rule.ID.String(),
		"rule_name": rule.Name,
		"signal":    "trace_attribute",
		"severity":  string(rule.Severity),
		"count":     strconv.FormatUint(count, 10),
	}
}

// traceAttributeRuleSummary says "matching traces", never "failed
// traces". The traces it counts have usually succeeded — that is the
// whole point of the kind — and borrowing the failure wording would
// misreport what happened in the one place a reader is most likely to
// act on it.
func traceAttributeRuleSummary(rule AlertRule, count uint64, state string) string {
	threshold := 1
	win := "5m"
	criteria := ""
	if rule.TraceAttributeSpec != nil {
		if rule.TraceAttributeSpec.Threshold > threshold {
			threshold = rule.TraceAttributeSpec.Threshold
		}
		win = rule.TraceAttributeSpec.WindowDuration().String()
		parts := make([]string, 0, len(rule.TraceAttributeSpec.Attrs))
		for _, a := range rule.TraceAttributeSpec.Attrs {
			parts = append(parts, fmt.Sprintf("%s %s %s", a.Key, a.Op, a.Value))
		}
		if len(parts) > 0 {
			criteria = " [" + strings.Join(parts, ", ") + "]"
		}
	}
	if state == "resolved" {
		return fmt.Sprintf("%s — recovered: %d matching traces in %s (threshold ≥%d)%s",
			rule.Name, count, win, threshold, criteria)
	}
	return fmt.Sprintf("%s — %d matching traces in %s (threshold ≥%d)%s",
		rule.Name, count, win, threshold, criteria)
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
	if rule.TraceLatencySpec == nil || !ruleHasScope(rule) {
		return
	}
	spec := *rule.TraceLatencySpec
	to := time.Now().UTC()
	from := to.Add(-spec.WindowDuration())
	latencyMs, samples, err := e.latencyEval.TraceLatencyMs(ctx, TraceLatencyQuery{
		IntegrationID: rule.IntegrationID,
		SystemID:      rule.SystemID,
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
	if rule.TraceVolumeSpec == nil || !ruleHasScope(rule) {
		return
	}
	spec := *rule.TraceVolumeSpec
	to := time.Now().UTC()
	from := to.Add(-spec.WindowDuration())
	total, scoped, err := e.volumeEval.TotalTraces(ctx, TraceVolumeQuery{
		IntegrationID: rule.IntegrationID,
		SystemID:      rule.SystemID,
		ServiceName:   rule.ServiceName,
		From:          from,
		To:            to,
	})
	if err != nil {
		e.log.Error("volume alert eval: query failed", "rule", rule.ID, "err", err)
		return
	}
	if !scoped {
		// The system/integration has no member services. "Traffic dropped
		// below N" is not a claim we can make about a group with nothing
		// in it, and firing here would page someone at 3am about an empty
		// set. A named service with no traffic still fires — see the
		// interface doc.
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
