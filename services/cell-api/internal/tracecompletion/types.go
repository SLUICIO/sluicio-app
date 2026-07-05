// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package tracecompletion implements per-integration trace-completion
// SLA rules. A rule describes an ordered pipeline a trace is expected
// to walk:
//
//	Start ─(within N s)─▶ Klart ─(within M s)─▶ "To be done"
//
// Only traces that contain the START span are evaluated and counted as
// the integration's messages — the start span gates the whole rule
// ("this trace is one of ours"). After the start span, each STAGE is a
// hop the trace must reach within that stage's timeout. The clock for
// stage i runs from the timestamp of stage i-1's span (the start span
// for the first stage). If a hop hasn't arrived within its timeout the
// trace flips to 'delayed' at that stage and fires the integration's
// alert channels.
//
// Backward compatibility: a legacy rule had a flat OR-list of closing
// span names + a single timeout. That is exactly a one-stage pipeline
// with no start gate, so Normalize() folds the legacy shape into a
// single Stage with an empty StartSpanName (ungated). Everything below
// is expressed in terms of EffectiveStages() so both shapes share one
// code path.
//
// The data model deliberately piggybacks on the existing alerting
// schema. A trace-completion rule is just an `alert_rules` row with
// signal='trace' and a JSON rule_spec of the shape declared in this
// file. The metric alert engine filters to signal='metric' so the two
// evaluators don't collide. Firings go through alert_instances +
// notification_jobs just like metric firings, which means the existing
// delivery loop (Slack/PagerDuty/webhook) handles them with zero
// change.
//
// "Sticky delayed": once a (trace_id, stage) is firing, it stays in
// alert_instances as state='firing' for that fingerprint. The evaluator
// resolves it only on positive evidence that the stage's span finally
// arrived ("delivered with delay") — never just because the trace aged
// out of the lookback window.

package tracecompletion

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Stage is one hop in the pipeline: the trace must emit a span whose
// name is in SpanNames (OR-list) within TimeoutSeconds of the previous
// stage's span. TimeoutSeconds == 0 means "inherit" — see
// EffectiveStages.
type Stage struct {
	// SpanNames is the OR-list of span names that satisfy this hop.
	// Case-sensitive exact match against what the upstream emits.
	SpanNames []string `json:"span_names"`

	// TimeoutSeconds is this hop's SLA. 0 → fall back to the rule's
	// DefaultTimeoutSeconds, then to the legacy TimeoutSeconds.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// RuleSpec is the JSON shape stored in alert_rules.rule_spec when
// signal='trace'. The evaluator decodes this from the rule_spec column,
// runs the completion check against ClickHouse, and writes any new
// delays through the alerting store.
type RuleSpec struct {
	// Kind discriminates trace-completion rules from any future
	// trace-signal rule shapes. Always "trace_completion" today.
	Kind string `json:"kind"`

	// StartSpanName gates the rule: only traces containing a span with
	// this name are evaluated and counted as integration messages. The
	// first stage's clock runs from this span's timestamp. Empty means
	// ungated — the legacy behaviour where every trace on the
	// integration's services was evaluated.
	StartSpanName string `json:"start_span_name,omitempty"`

	// Stages is the ordered list of hops after the start span. A trace
	// is "completed" once the final stage's span appears; it is
	// "delayed" if any hop's timeout elapses before that hop's span
	// arrives.
	Stages []Stage `json:"stages,omitempty"`

	// DefaultTimeoutSeconds is the per-stage timeout used when a stage
	// leaves TimeoutSeconds at 0. Falls back to TimeoutSeconds.
	DefaultTimeoutSeconds int `json:"default_timeout_seconds,omitempty"`

	// ── legacy fields (kept for back-compat / migration) ──────────────

	// ClosingSpanNames is the legacy flat OR-list of "done" span names.
	// Normalize() mirrors the final stage's names here so older readers
	// stay coherent; new rules should use Stages.
	ClosingSpanNames []string `json:"closing_span_names,omitempty"`

	// TimeoutSeconds is the legacy single timeout. Now also the
	// last-resort per-stage default (see DefaultTimeoutSeconds).
	TimeoutSeconds int `json:"timeout_seconds"`

	// LookbackSeconds is how far back the evaluator scans for traces.
	// Must be at least 2× the longest effective stage timeout so a hop
	// has time to breach before its trace drops out of the window.
	LookbackSeconds int `json:"lookback_seconds"`
}

// EffectiveStage is a stage with its timeout resolved and its span
// names trimmed. This is what the evaluator and validator work against
// so legacy + new rules share one shape.
type EffectiveStage struct {
	SpanNames      []string
	TimeoutSeconds int
}

// Normalize canonicalises a spec in place: sets Kind, trims whitespace,
// folds a legacy closing-span list into a single Stage, mirrors the
// final stage names back into ClosingSpanNames, and fills a default
// lookback. Called when decoding rules from the DB (store.scanRule) and
// when parsing create/update input, so every code path downstream sees
// the same canonical form.
func (s *RuleSpec) Normalize() {
	if s.Kind == "" {
		s.Kind = "trace_completion"
	}
	s.StartSpanName = strings.TrimSpace(s.StartSpanName)

	// Legacy → single stage. Carry the legacy timeout onto the stage so
	// the meaning is preserved exactly.
	if len(s.Stages) == 0 && len(s.ClosingSpanNames) > 0 {
		s.Stages = []Stage{{
			SpanNames:      append([]string(nil), s.ClosingSpanNames...),
			TimeoutSeconds: s.TimeoutSeconds,
		}}
	}

	// Trim every stage's span names (fresh slice, no aliasing).
	for i := range s.Stages {
		s.Stages[i].SpanNames = trimNonEmpty(s.Stages[i].SpanNames)
	}

	// Mirror final stage names into the legacy field so the "done" set
	// stays meaningful for any reader that still looks at it.
	if len(s.Stages) > 0 {
		s.ClosingSpanNames = append([]string(nil), s.Stages[len(s.Stages)-1].SpanNames...)
	}

	// Default lookback: 4× the longest hop so a sticky-delayed trace has
	// headroom to age out without falling out of the window mid-flight.
	if s.LookbackSeconds == 0 {
		if maxT := s.maxStageTimeout(); maxT > 0 {
			s.LookbackSeconds = 4 * maxT
		}
	}
}

// EffectiveStages resolves each stage's timeout (stage → default →
// legacy timeout) and trims span names. Pure: it tolerates a
// not-yet-normalized legacy spec by treating ClosingSpanNames as a
// single stage. Stages with no usable span names are dropped.
func (s RuleSpec) EffectiveStages() []EffectiveStage {
	def := s.defaultTimeout()
	stages := s.Stages
	if len(stages) == 0 && len(s.ClosingSpanNames) > 0 {
		stages = []Stage{{SpanNames: s.ClosingSpanNames}}
	}
	out := make([]EffectiveStage, 0, len(stages))
	for _, st := range stages {
		names := trimNonEmpty(st.SpanNames)
		if len(names) == 0 {
			continue
		}
		t := st.TimeoutSeconds
		if t <= 0 {
			t = def
		}
		out = append(out, EffectiveStage{SpanNames: names, TimeoutSeconds: t})
	}
	return out
}

// FinalStageNames returns the span names that mark the whole pipeline
// "done" — the last stage's names. Used by the late-completion sweep.
func (s RuleSpec) FinalStageNames() []string {
	es := s.EffectiveStages()
	if len(es) == 0 {
		return nil
	}
	return es[len(es)-1].SpanNames
}

// Gated reports whether the rule has a start-span gate. Ungated
// (legacy) rules evaluate every trace on the integration's services.
func (s RuleSpec) Gated() bool {
	return strings.TrimSpace(s.StartSpanName) != ""
}

func (s RuleSpec) defaultTimeout() int {
	if s.DefaultTimeoutSeconds > 0 {
		return s.DefaultTimeoutSeconds
	}
	return s.TimeoutSeconds
}

func (s RuleSpec) maxStageTimeout() int {
	max := 0
	for _, es := range s.EffectiveStages() {
		if es.TimeoutSeconds > max {
			max = es.TimeoutSeconds
		}
	}
	return max
}

// Validate enforces the bounds. Called by the HTTP layer before
// persisting and by the evaluator before issuing a CH query (a
// malformed rule hand-edited in the DB shouldn't crash the loop).
func (s RuleSpec) Validate() error {
	if s.Kind != "" && s.Kind != "trace_completion" {
		return errors.New("tracecompletion: spec kind must be 'trace_completion'")
	}
	stages := s.EffectiveStages()
	if len(stages) == 0 {
		return errors.New("tracecompletion: at least one stage (or closing_span_name) is required")
	}
	for i, es := range stages {
		if len(es.SpanNames) == 0 {
			return fmt.Errorf("tracecompletion: stage %d must have at least one span name", i+1)
		}
		if es.TimeoutSeconds < 1 || es.TimeoutSeconds > 86400 {
			return fmt.Errorf("tracecompletion: stage %d timeout must be between 1 and 86400 seconds", i+1)
		}
	}
	maxT := s.maxStageTimeout()
	if s.LookbackSeconds < 2*maxT {
		return errors.New("tracecompletion: lookback_seconds must be at least 2× the longest stage timeout")
	}
	if s.LookbackSeconds > 7*86400 {
		return errors.New("tracecompletion: lookback_seconds capped at 7 days")
	}
	return nil
}

// Timeout returns the legacy single timeout as a Duration. Retained for
// callers that still need a representative window; per-stage logic uses
// EffectiveStages instead.
func (s RuleSpec) Timeout() time.Duration {
	return time.Duration(s.TimeoutSeconds) * time.Second
}

// Lookback returns the lookback as a time.Duration.
func (s RuleSpec) Lookback() time.Duration {
	return time.Duration(s.LookbackSeconds) * time.Second
}

// trimNonEmpty trims each string and drops the empties, returning a
// fresh slice.
func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, n := range in {
		if t := strings.TrimSpace(n); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// TraceState is the per-trace classification the evaluator produces.
type TraceState string

const (
	// StateCompleted means the final stage's span has been seen.
	StateCompleted TraceState = "completed"
	// StatePending means the trace is gated-in and in flight: it hasn't
	// reached its current stage's span yet, but is still within that
	// stage's timeout. Normal.
	StatePending TraceState = "pending"
	// StateDelayed means a stage's timeout elapsed before its span
	// arrived. This is what fires alert channels.
	StateDelayed TraceState = "delayed"
)

// TraceClassification is one trace's evaluation result.
type TraceClassification struct {
	TraceID    string
	StartedAt  time.Time // baseline the breaching stage's clock ran from
	LastSpanAt time.Time
	State      TraceState

	// DelayedStage is the 1-based index of the stage the trace stalled
	// at (0 when not delayed). DelayedStageName is a human label of that
	// hop's expected span(s), for the firing summary.
	DelayedStage     int
	DelayedStageName string
}

// Counts is the per-integration aggregate the integration-detail page
// renders as chips. Pending + completed + delayed sum to the total
// gated traces in the lookback window.
type Counts struct {
	Completed int `json:"completed"`
	Pending   int `json:"pending"`
	Delayed   int `json:"delayed"`
}
