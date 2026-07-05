// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package alerting models metric alert rules and the notification
// channels they route to, on top of the alert_rules /
// notification_channels / alert_rule_routes / alert_instances /
// notification_jobs schema defined in 0001_initial. v1 supports the
// "metric" signal only: a rule watches one OTLP metric (optionally
// scoped by attribute filters), aggregates it over a window, and fires
// when the aggregate crosses a threshold.
package alerting

import (
	"time"

	"github.com/google/uuid"
)

// Severity mirrors the alert_severity enum.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// ValidSeverity reports whether s is a known severity.
func ValidSeverity(s Severity) bool {
	switch s {
	case SeverityInfo, SeverityWarning, SeverityCritical:
		return true
	}
	return false
}

// severityRank orders severities for "tightest rule wins" selection.
func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// Operator is the comparison between the aggregated value and the
// threshold.
type Operator string

const (
	OpGT  Operator = "gt"
	OpGTE Operator = "gte"
	OpLT  Operator = "lt"
	OpLTE Operator = "lte"
	OpEQ  Operator = "eq"
	OpNEQ Operator = "neq"
)

// ValidOperator reports whether op is a known comparison operator.
func ValidOperator(op Operator) bool {
	switch op {
	case OpGT, OpGTE, OpLT, OpLTE, OpEQ, OpNEQ:
		return true
	}
	return false
}

// EvaluateBreach applies the operator between value and threshold.
func EvaluateBreach(op Operator, value, threshold float64) bool {
	switch op {
	case OpGT:
		return value > threshold
	case OpGTE:
		return value >= threshold
	case OpLT:
		return value < threshold
	case OpLTE:
		return value <= threshold
	case OpEQ:
		return value == threshold
	case OpNEQ:
		return value != threshold
	}
	return false
}

// Aggregation is how a metric's points are reduced to one number over
// the rule's window.
type Aggregation string

const (
	AggMax Aggregation = "max"
	AggAvg Aggregation = "avg"
	AggMin Aggregation = "min"
	AggSum Aggregation = "sum"
	AggP95 Aggregation = "p95"
	// AggLast is the metric's most recent value within the window — the
	// reading at the latest timestamp, not a reduction over the window.
	// Lets a rule watch a point-in-time gauge (e.g. queue depth: "last
	// value of queue.depth where name=x is > 0").
	AggLast Aggregation = "last"
	// AggIncrease is the rise of a monotonic counter over the window
	// (delta = how many events happened), computed per series (max−min) and
	// summed. Use for "N exceptions in 5m", "dropped spans in 5m", etc. —
	// counters where the raw cumulative value is meaningless but its change
	// is the signal. Negative per-series deltas are clamped to 0; note a
	// counter reset *within* the window (process restart) undercounts, since
	// max−min misses the increments before the reset.
	AggIncrease Aggregation = "increase"
	// AggRate is AggIncrease divided by the window length (per-second rate).
	AggRate Aggregation = "rate"
	// AggAge treats the metric's latest VALUE as a Unix-epoch-seconds
	// timestamp and reduces to "seconds since that time" — now − value. It
	// powers staleness health checks on timestamp metrics like file.mtime:
	// the value is an absolute last-modified time, and age = now − mtime is
	// what you actually threshold ("fire if the file hasn't changed in > N
	// seconds"). Distinct from data freshness (now − ingest Timestamp).
	// Meaningless for non-timestamp metrics; pair with gt/gte and a
	// threshold in SECONDS.
	AggAge Aggregation = "age"
)

// ValidAggregation reports whether a is a known aggregation.
func ValidAggregation(a Aggregation) bool {
	switch a {
	case AggMax, AggAvg, AggMin, AggSum, AggP95, AggLast, AggIncrease, AggRate, AggAge:
		return true
	}
	return false
}

// AttrFilter is one metric attribute predicate carried into a rule's
// scope: key·op·value (mirrors the explorer's filter chips).
type AttrFilter struct {
	Key   string `json:"key"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// MetricRuleSpec is the JSONB rule_spec for a metric-signal rule.
type MetricRuleSpec struct {
	MetricName  string       `json:"metric_name"`
	Aggregation Aggregation  `json:"aggregation"`
	Operator    Operator     `json:"operator"`
	Threshold   float64      `json:"threshold"`
	ForWindow   string       `json:"for_window"` // Go duration, e.g. "5m"
	Attrs       []AttrFilter `json:"attrs,omitempty"`
	// SplitBy, when set, breaks the evaluation down by the distinct
	// values of this metric attribute instead of aggregating all matching
	// points into one number. Each value is compared to the threshold
	// independently and the firing enumerates every breaching value (e.g.
	// "DLQ depth > 0 split by queue_name" → the alert lists each queue
	// that's actually backed up). "" = no split (single aggregate).
	SplitBy string `json:"split_by,omitempty"`
}

// MetricGroup is one (attribute value → aggregate) row when a rule is
// evaluated with SplitBy. Label is the split attribute's value; Value the
// aggregate for that value's points; Samples the point count.
type MetricGroup struct {
	Label   string
	Value   float64
	Samples uint64
}

// ForWindowDuration parses ForWindow, clamped to [1m, 24h] with a 5m
// fallback so a bad value can't make evaluation degenerate.
func (s MetricRuleSpec) ForWindowDuration() time.Duration {
	d, err := time.ParseDuration(s.ForWindow)
	if err != nil || d < time.Minute {
		return 5 * time.Minute
	}
	if d > 24*time.Hour {
		return 24 * time.Hour
	}
	return d
}

// LogRuleSpec is the JSONB rule_spec for a log-signal rule. It fires
// when the number of logs matching {MinSeverity, BodyContains, Attrs}
// within the trailing WindowSeconds reaches Threshold. Threshold=1
// expresses "alert on any matching log" (e.g. severity=error, or body
// contains "blabla"); higher thresholds express rate conditions ("5
// errors in 10 minutes"). The service / integration scope lives on the
// rule row (service_name / integration_id), not in the spec.
type LogRuleSpec struct {
	// MinSeverity is the OTLP SeverityNumber floor; 0 = any severity.
	// (info≈9, warn≈13, error≈17, fatal≈21.)
	MinSeverity int32 `json:"min_severity"`
	// BodyContains is a case-insensitive substring of the log body; "" = no text filter.
	BodyContains string `json:"body_contains"`
	// Attrs are attribute predicates (key·op·value), AND-ed.
	Attrs []AttrFilter `json:"attrs,omitempty"`
	// Threshold: the match count the comparison is made against. Min 1.
	Threshold int `json:"threshold"`
	// WindowSeconds is the trailing window the matches are counted over.
	WindowSeconds int `json:"window_seconds"`
	// Comparison decides which direction breaches:
	//   "at_least"  (default) — fire when count >= Threshold (a spike of
	//                matching logs, e.g. errors).
	//   "fewer_than"          — fire when count <  Threshold (absence of
	//                expected logs, e.g. a heartbeat line; 0 counts as below).
	Comparison string `json:"comparison,omitempty"`
}

// Log comparison directions.
const (
	LogComparisonAtLeast   = "at_least"
	LogComparisonFewerThan = "fewer_than"
)

// FiresBelow reports whether this log rule breaches when the count is BELOW
// the threshold (absence detection) rather than at/above it.
func (s LogRuleSpec) FiresBelow() bool { return s.Comparison == LogComparisonFewerThan }

// maxCheckWindow caps a count/volume health check's trailing window.
// Raised from 24h to 30d so checks can legitimately span days (e.g.
// "fewer than N logs in the last 2 days").
const maxCheckWindow = 30 * 24 * time.Hour

// MaxCheckWindowSeconds is maxCheckWindow in seconds, exported for the API
// layer's request validation (same ceiling as WindowDuration's clamp).
const MaxCheckWindowSeconds = 30 * 24 * 60 * 60

// WindowDuration parses WindowSeconds, clamped to [1m, maxCheckWindow] with
// a 5m fallback so a bad value can't make evaluation degenerate.
func (s LogRuleSpec) WindowDuration() time.Duration {
	d := time.Duration(s.WindowSeconds) * time.Second
	if d < time.Minute {
		return 5 * time.Minute
	}
	if d > maxCheckWindow {
		return maxCheckWindow
	}
	return d
}

// Signal names — the kind of telemetry a rule watches. Stored in the
// alert_rules.signal column. "metric" is the default/legacy value.
const (
	SignalMetric = "metric"
	SignalLog    = "log"
	// SignalTraceError is the failed-trace signal. Its stored value is
	// "trace" — the (previously unused) member of the alert_signal enum
	// defined in 0001_initial — so no schema migration is needed.
	SignalTraceError = "trace"
)

// TraceErrorSpecKind is the rule_spec.kind marker for a failed-trace
// rule. It is what disambiguates failed-trace rules from trace-COMPLETION
// rules — both share signal='trace' on the alert_rules row, so the only
// thing that tells them apart is this kind tag (trace-completion specs
// carry kind="trace_completion", or no kind for legacy rows). Every
// signal='trace' query in the alerting AND tracecompletion packages keys
// off this to avoid cross-contaminating each other's lists/evaluators.
const TraceErrorSpecKind = "trace_error"

// TraceErrorRuleSpec is the JSONB rule_spec for a trace_error-signal
// rule: fire when the integration accumulates >= Threshold failed traces
// (a trace with at least one error span) over the trailing WindowSeconds.
// The integration scope lives on the rule row (integration_id), not in
// the spec — mirroring how LogRuleSpec leaves scope on the row.
type TraceErrorRuleSpec struct {
	// Kind tags this spec as a failed-trace rule (TraceErrorSpecKind).
	// Required so readers can tell it apart from a trace-completion spec
	// on the same signal='trace' row.
	Kind string `json:"kind"`
	// Threshold: fire when the failed-trace count over the window is >=
	// this. Min 1 ("alert on any failed trace").
	Threshold int `json:"threshold"`
	// WindowSeconds is the trailing window the failed traces are counted over.
	WindowSeconds int `json:"window_seconds"`
}

// WindowDuration parses WindowSeconds, clamped to [1m, maxCheckWindow]
// with a 5m fallback (mirrors LogRuleSpec.WindowDuration).
func (s TraceErrorRuleSpec) WindowDuration() time.Duration {
	d := time.Duration(s.WindowSeconds) * time.Second
	if d < time.Minute {
		return 5 * time.Minute
	}
	if d > maxCheckWindow {
		return maxCheckWindow
	}
	return d
}

// TraceLatencySpecKind marks a response-time (latency) trace rule, sharing
// signal='trace' with failed-trace + trace-completion rows — disambiguated
// by this kind tag everywhere signal='trace' is queried.
const TraceLatencySpecKind = "trace_latency"

// TraceLatencyRuleSpec is the JSONB rule_spec for a trace-latency rule:
// fire when the aggregate span/trace duration over the window exceeds
// ThresholdMs. Aggregation is "p95" (default) or "max". Scope lives on the
// rule row (service_name / integration_id).
type TraceLatencyRuleSpec struct {
	Kind          string `json:"kind"`         // TraceLatencySpecKind
	ThresholdMs   int    `json:"threshold_ms"` // fire when latency_ms >= this
	WindowSeconds int    `json:"window_seconds"`
	Aggregation   string `json:"aggregation"` // "p95" | "max"
}

// WindowDuration mirrors TraceErrorRuleSpec.WindowDuration (clamp [1m,30d]).
func (s TraceLatencyRuleSpec) WindowDuration() time.Duration {
	d := time.Duration(s.WindowSeconds) * time.Second
	if d < time.Minute {
		return 5 * time.Minute
	}
	if d > maxCheckWindow {
		return maxCheckWindow
	}
	return d
}

// Quantile maps the aggregation to a ClickHouse quantile (max == 1.0).
func (s TraceLatencyRuleSpec) Quantile() float64 {
	if s.Aggregation == "max" {
		return 1.0
	}
	return 0.95
}

// TraceVolumeSpecKind marks a low-traffic (throughput) trace rule: fire when
// the TOTAL distinct trace count over the window is BELOW Threshold — a
// dead-man's-switch for a service that should be receiving traffic. Shares
// signal='trace' with the other trace kinds, disambiguated by this tag.
const TraceVolumeSpecKind = "trace_volume"

// TraceVolumeRuleSpec is the JSONB rule_spec for a trace-volume rule: fire
// when the total distinct trace count over WindowSeconds is < Threshold
// (0 counts as below — a fully silent service trips it). Scope lives on the
// rule row (service_name / integration_id).
type TraceVolumeRuleSpec struct {
	Kind          string `json:"kind"` // TraceVolumeSpecKind
	Threshold     int    `json:"threshold"`
	WindowSeconds int    `json:"window_seconds"`
}

// WindowDuration mirrors the other trace specs (clamp [1m, 30d]).
func (s TraceVolumeRuleSpec) WindowDuration() time.Duration {
	d := time.Duration(s.WindowSeconds) * time.Second
	if d < time.Minute {
		return 5 * time.Minute
	}
	if d > maxCheckWindow {
		return maxCheckWindow
	}
	return d
}

// Source is where a health check's value comes from.
type Source string

const (
	// SourceTelemetry: Sluicio computes the value by aggregating an OTLP
	// metric series over the rule's window (the original metric-rule
	// behaviour). The evaluator records each computed value as a reading.
	SourceTelemetry Source = "telemetry"
	// SourcePushed: the value is POSTed in by an external scraper (e.g. a
	// queue-depth poller). The pushed evaluator compares the latest
	// pushed reading to the threshold; metric_name/aggregation/window are
	// ignored. Only valid for metric-signal rules.
	SourcePushed Source = "pushed"
)

// ValidSource reports whether s is a known value source.
func ValidSource(s Source) bool {
	switch s {
	case SourceTelemetry, SourcePushed:
		return true
	}
	return false
}

// Resolve modes for a health check / alert rule.
const (
	// ResolveAuto: the alert resolves itself when the condition clears
	// (self-recovering). The default.
	ResolveAuto = "auto"
	// ResolveManual: the alert stays firing after the condition clears,
	// until a human acknowledges it.
	ResolveManual = "manual"
)

// AlertRule is a stored rule plus its routed channel IDs. Signal is
// "metric", "log", or "trace" (failed-trace); Spec holds the metric
// rule_spec, LogSpec the log rule_spec, and TraceErrorSpec the
// failed-trace rule_spec — exactly one is populated, per Signal.
type AlertRule struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	IntegrationID  *uuid.UUID `json:"integration_id,omitempty"`
	ServiceName    string     `json:"service_name,omitempty"` // bound service (health check); "" = none
	// GroupID is the owning team. nil = org-wide (visible to everyone);
	// set = visible/editable only by team members + org admins.
	GroupID     *uuid.UUID     `json:"group_id,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Signal      string         `json:"signal"`
	Spec        MetricRuleSpec `json:"spec"`
	LogSpec     *LogRuleSpec   `json:"log_spec,omitempty"`
	// TraceErrorSpec holds the rule_spec for a failed-trace rule;
	// TraceLatencySpec for a response-time rule — both signal='trace',
	// disambiguated by rule_spec.kind. Exactly one is populated per kind.
	TraceErrorSpec   *TraceErrorRuleSpec   `json:"trace_error_spec,omitempty"`
	TraceLatencySpec *TraceLatencyRuleSpec `json:"trace_latency_spec,omitempty"`
	// TraceVolumeSpec: a low-traffic rule (total traces below threshold),
	// also signal='trace', disambiguated by rule_spec.kind=trace_volume.
	TraceVolumeSpec *TraceVolumeRuleSpec `json:"trace_volume_spec,omitempty"`
	Severity        Severity             `json:"severity"`
	EvalSeconds     int                  `json:"evaluation_seconds"`
	Enabled         bool                 `json:"enabled"`
	// Source is where a metric rule's value comes from: "telemetry"
	// (aggregate OTLP, default) or "pushed" (external value). Log rules
	// are always telemetry.
	Source Source `json:"source"`
	// DisplayOnService surfaces the check's latest reading as a value
	// tile on its bound service page (only meaningful when ServiceName
	// is set). Unit is the tile's display unit ("", "msgs", "ms", …).
	DisplayOnService bool   `json:"display_on_service"`
	Unit             string `json:"unit,omitempty"`
	// ResolveMode decides what happens when the check's condition clears:
	// ResolveAuto (self-recovering) or ResolveManual (stays firing until a
	// human acknowledges it). Empty defaults to auto.
	ResolveMode string      `json:"resolve_mode"`
	ChannelIDs  []uuid.UUID `json:"channel_ids"`
	// TitleTemplate / BodyTemplate are optional Go text/template
	// strings rendered at delivery against the firing's context
	// ({{.rule_name}}, {{.value}}, {{.threshold}}, {{.severity}},
	// {{.state}}, {{.summary}}, …). Empty = built-in summary.
	TitleTemplate string `json:"title_template,omitempty"`
	BodyTemplate  string `json:"body_template,omitempty"`
	// NotificationContent controls which enrichment blocks the rule's
	// email/webhook include + an optional inline Liquid email override. Nil =
	// no enrichment + org-default email template (back-compat).
	NotificationContent *NotificationContent `json:"notification_config,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

// Channel kinds Conduit can deliver to. Stored as free text in the
// notification_channels.kind column; validated here.
const (
	ChannelWebhook   = "webhook"
	ChannelSlack     = "slack"
	ChannelPagerDuty = "pagerduty"
	ChannelEmail     = "email"
)

// ValidChannelKind reports whether kind is a deliverable channel kind.
func ValidChannelKind(kind string) bool {
	switch kind {
	case ChannelWebhook, ChannelSlack, ChannelPagerDuty, ChannelEmail:
		return true
	}
	return false
}

// NotificationChannel is a delivery target (Slack webhook, PagerDuty
// routing key, generic webhook, or email over SMTP). Config is
// kind-specific: webhook/slack carry {"url": …}; pagerduty carries
// {"routing_key": …}; email carries {"smtp_host","smtp_port","from",
// "to", and optional "username"/"password"}.
type NotificationChannel struct {
	ID             uuid.UUID         `json:"id"`
	OrganizationID uuid.UUID         `json:"organization_id"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	Config         map[string]string `json:"config"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// MetricRuleSummary aggregates the enabled metric rules watching one
// metric name: the count, the tightest threshold to draw, and the most
// severe severity. Backs the explorer's rule badge + sparkline line.
type MetricRuleSummary struct {
	Count     int
	Threshold float64
	Severity  Severity
}

// Reading is the latest numeric value recorded for a rule (the value the
// evaluator computed, or an externally pushed value).
type Reading struct {
	Value      float64   `json:"value"`
	ObservedAt time.Time `json:"observed_at"`
}

// ServiceReading is the read model for a "show on service page" health
// check: its definition plus the latest reading (if any) and whether
// that reading breaches the threshold. Powers the service-page tiles.
type ServiceReading struct {
	RuleID     uuid.UUID  `json:"rule_id"`
	Name       string     `json:"name"`
	Unit       string     `json:"unit"`
	Source     Source     `json:"source"`
	Operator   Operator   `json:"operator"`
	Threshold  float64    `json:"threshold"`
	Value      *float64   `json:"value,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	HasValue   bool       `json:"has_value"`
	Breached   bool       `json:"breached"`
}
