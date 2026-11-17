// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tracecompletion

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// twoStageSpec is a gated pipeline Start → Klart(30s) → ToBeDone(60s).
func twoStageSpec() RuleSpec {
	s := RuleSpec{
		Kind:          "trace_completion",
		StartSpanName: "Start",
		Stages: []Stage{
			{SpanNames: []string{"Klart"}, TimeoutSeconds: 30},
			{SpanNames: []string{"ToBeDone"}, TimeoutSeconds: 60},
		},
		LookbackSeconds: 240,
	}
	s.Normalize()
	return s
}

func TestNormalizeLegacyFoldsToSingleStage(t *testing.T) {
	s := RuleSpec{
		Kind:             "trace_completion",
		ClosingSpanNames: []string{" Klart ", "OrderComplete"},
		TimeoutSeconds:   30,
	}
	s.Normalize()
	if len(s.Stages) != 1 {
		t.Fatalf("want 1 synthesized stage, got %d", len(s.Stages))
	}
	if got := s.Stages[0].SpanNames; len(got) != 2 || got[0] != "Klart" || got[1] != "OrderComplete" {
		t.Fatalf("stage span names not trimmed/preserved: %v", got)
	}
	if s.Gated() {
		t.Fatalf("legacy rule must be ungated (empty start span)")
	}
	if s.LookbackSeconds != 4*30 {
		t.Fatalf("default lookback want 120, got %d", s.LookbackSeconds)
	}
	es := s.EffectiveStages()
	if len(es) != 1 || es[0].TimeoutSeconds != 30 {
		t.Fatalf("effective stage timeout want 30, got %+v", es)
	}
}

func TestNormalizeDefaultTimeoutInheritance(t *testing.T) {
	s := RuleSpec{
		Kind:                  "trace_completion",
		StartSpanName:         "Start",
		DefaultTimeoutSeconds: 45,
		Stages: []Stage{
			{SpanNames: []string{"A"}},                     // inherits 45
			{SpanNames: []string{"B"}, TimeoutSeconds: 90}, // explicit 90
		},
		LookbackSeconds: 240,
	}
	s.Normalize()
	es := s.EffectiveStages()
	if es[0].TimeoutSeconds != 45 || es[1].TimeoutSeconds != 90 {
		t.Fatalf("timeout inheritance wrong: %+v", es)
	}
	if s.maxStageTimeout() != 90 {
		t.Fatalf("maxStageTimeout want 90, got %d", s.maxStageTimeout())
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		spec    RuleSpec
		wantErr bool
	}{
		{"valid two-stage", twoStageSpec(), false},
		{
			"no stages",
			func() RuleSpec { s := RuleSpec{Kind: "trace_completion", LookbackSeconds: 60}; return s }(),
			true,
		},
		{
			"lookback too small",
			RuleSpec{Kind: "trace_completion", StartSpanName: "Start",
				Stages: []Stage{{SpanNames: []string{"Klart"}, TimeoutSeconds: 60}}, LookbackSeconds: 60},
			true,
		},
		{
			"timeout out of range",
			RuleSpec{Kind: "trace_completion", StartSpanName: "Start",
				Stages: []Stage{{SpanNames: []string{"Klart"}, TimeoutSeconds: 99999}}, LookbackSeconds: 999999},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	spec := twoStageSpec()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	mk := func(mut func(*traceAgg)) traceAgg {
		a := traceAgg{TraceID: "t", HasStart: true, StartAt: now.Add(-10 * time.Second),
			StageAt: make([]time.Time, 2), HasStage: make([]bool, 2), TraceMin: now.Add(-10 * time.Second)}
		mut(&a)
		return a
	}

	t.Run("completed when every stage is reached", func(t *testing.T) {
		a := mk(func(a *traceAgg) {
			a.HasStage[0], a.StageAt[0] = true, now.Add(-5*time.Second)
			a.HasStage[1], a.StageAt[1] = true, now
		})
		if c := classify(spec, a, now); c.State != StateCompleted {
			t.Fatalf("want completed, got %s", c.State)
		}
	})

	// Stages are AND-ed: the last one arriving does not excuse an earlier
	// one that never did ("A, then B1 or B2" with no A is not done).
	t.Run("final stage alone stalls at the missing earlier stage", func(t *testing.T) {
		a := mk(func(a *traceAgg) {
			a.StartAt, a.TraceMin = now.Add(-time.Hour), now.Add(-time.Hour)
			a.HasStage[1], a.StageAt[1] = true, now
		})
		c := classify(spec, a, now)
		if c.State != StateDelayed || c.DelayedStage != 1 {
			t.Fatalf("want delayed at stage 1, got %s at %d", c.State, c.DelayedStage)
		}
	})

	t.Run("pending within first-stage timeout", func(t *testing.T) {
		a := mk(func(a *traceAgg) { a.StartAt = now.Add(-10 * time.Second) }) // 10s < 30s
		if c := classify(spec, a, now); c.State != StatePending {
			t.Fatalf("want pending, got %s (stage %d)", c.State, c.DelayedStage)
		}
	})

	t.Run("delayed at stage 1", func(t *testing.T) {
		a := mk(func(a *traceAgg) { a.StartAt = now.Add(-31 * time.Second) }) // >30s, no Klart
		c := classify(spec, a, now)
		if c.State != StateDelayed || c.DelayedStage != 1 {
			t.Fatalf("want delayed@1, got %s@%d", c.State, c.DelayedStage)
		}
		if !c.StartedAt.Equal(a.StartAt) {
			t.Fatalf("delayed baseline should be the start span ts")
		}
	})

	t.Run("delayed at stage 2", func(t *testing.T) {
		a := mk(func(a *traceAgg) {
			a.HasStage[0] = true
			a.StageAt[0] = now.Add(-90 * time.Second) // Klart 90s ago, ToBeDone never (>60s)
		})
		c := classify(spec, a, now)
		if c.State != StateDelayed || c.DelayedStage != 2 {
			t.Fatalf("want delayed@2, got %s@%d", c.State, c.DelayedStage)
		}
		if !c.StartedAt.Equal(a.StageAt[0]) {
			t.Fatalf("stage-2 baseline should be stage-1 span ts")
		}
	})
}

func TestFingerprintRoundTrip(t *testing.T) {
	fp := stageFingerprint("abcdef123456", 2)
	tid, stage := parseFingerprint(fp)
	if tid != "abcdef123456" || stage != 2 {
		t.Fatalf("round trip failed: %q → (%q,%d)", fp, tid, stage)
	}
	// Legacy bare fingerprint → stage 0.
	if tid, stage := parseFingerprint("abcdef123456"); tid != "abcdef123456" || stage != 0 {
		t.Fatalf("legacy fingerprint parse: (%q,%d)", tid, stage)
	}
}

// fanOutSpec is a message fanned out to two subscribers that all emit
// the same standardised span name: start → the extractor's Step.Send
// (30s) → every loader's Step.Send (60s).
func fanOutSpec() RuleSpec {
	s := RuleSpec{
		Kind:          "trace_completion",
		StartSpanName: "process orders",
		Stages: []Stage{
			{Spans: []SpanMatch{{Service: "order-publisher", SpanName: "Step.Send"}}, TimeoutSeconds: 30},
			{Spans: []SpanMatch{
				{Service: "erp-loader", SpanName: "Step.Send"},
				{Service: "wms-loader", SpanName: "Step.Send"},
			}, Require: RequireAll, TimeoutSeconds: 60},
		},
		LookbackSeconds: 240,
	}
	s.Normalize()
	return s
}

// A stage stored before spans existed still reads as its names from
// any service, and a stage with spans mirrors their names into
// span_names for readers that only know those.
func TestNormalizeSpans(t *testing.T) {
	s := twoStageSpec()
	if got := s.Stages[0].Spans; len(got) != 1 || got[0] != (SpanMatch{SpanName: "Klart"}) || s.Stages[0].Require != RequireAny {
		t.Fatalf("legacy names fold into spans: %+v %q", got, s.Stages[0].Require)
	}
	f := fanOutSpec()
	if got := f.Stages[1].SpanNames; len(got) != 1 || got[0] != "Step.Send" {
		t.Fatalf("span_names mirrors the distinct names: %v", got)
	}
	if got := f.ClosingSpanNames; len(got) != 1 || got[0] != "Step.Send" {
		t.Fatalf("closing names: %v", got)
	}
	es := f.EffectiveStages()
	if es[0].All || !es[1].All {
		t.Fatalf("require: %+v", es)
	}
	if got := es[1].Label(); got != "erp-loader: Step.Send + wms-loader: Step.Send" {
		t.Fatalf("label %q", got)
	}

	// Spans win over stale span_names; "all" of one span is just that span.
	one := RuleSpec{Stages: []Stage{{
		Spans:     []SpanMatch{{Service: " a ", SpanName: " Step.Send "}, {Service: "a", SpanName: "Step.Send"}},
		SpanNames: []string{"Old"}, Require: RequireAll, TimeoutSeconds: 10,
	}}}
	one.Normalize()
	if got := one.Stages[0].Spans; len(got) != 1 || got[0] != (SpanMatch{Service: "a", SpanName: "Step.Send"}) {
		t.Fatalf("trimmed and de-duplicated: %+v", got)
	}
	if one.EffectiveStages()[0].All {
		t.Fatal("all of a single span is any")
	}

	bad := fanOutSpec()
	bad.Stages[1].Require = "both"
	bad.Normalize()
	if err := bad.Validate(); err == nil {
		t.Fatal("an unknown require is refused")
	}
}

// The fan-out from the reported trace: a stage that requires every
// subscriber is reached when the last of them delivers, and a trace
// stuck part-way names the subscriber that has not.
func TestClassifyFanOut(t *testing.T) {
	spec := fanOutSpec()
	stages := spec.EffectiveStages()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	mk := func(extractor, erp, wms time.Duration) traceAgg {
		a := traceAgg{TraceID: "t", HasStart: true, StartAt: now.Add(-200 * time.Second)}
		sp := newSightings(stages)
		set := func(i, j int, ago time.Duration) {
			if ago >= 0 {
				sp.at[i][j], sp.has[i][j] = now.Add(-ago), true
			}
		}
		set(0, 0, extractor)
		set(1, 0, erp)
		set(1, 1, wms)
		a.foldStages(stages, sp)
		return a
	}
	const never = -1

	if c := classify(spec, mk(150*time.Second, 120*time.Second, 100*time.Second), now); c.State != StateCompleted {
		t.Fatalf("both loaders delivered: %s", c.State)
	}
	a := mk(150*time.Second, 120*time.Second, 100*time.Second)
	if !a.StageAt[1].Equal(now.Add(-100 * time.Second)) {
		t.Fatalf("an all stage is reached at its last span: %v", a.StageAt[1])
	}

	c := classify(spec, mk(150*time.Second, 120*time.Second, never), now)
	if c.State != StateDelayed || c.DelayedStage != 2 || c.DelayedStageName != "wms-loader: Step.Send" {
		t.Fatalf("wms never delivered: %s@%d %q", c.State, c.DelayedStage, c.DelayedStageName)
	}
	if c := classify(spec, mk(150*time.Second, never, never), now); c.DelayedStageName != "erp-loader: Step.Send + wms-loader: Step.Send" {
		t.Fatalf("neither delivered names the whole stage: %q", c.DelayedStageName)
	}
	if c := classify(spec, mk(20*time.Second, never, never), now); c.State != StatePending {
		t.Fatalf("within the loaders' 60s: %s", c.State)
	}
}

// Only a span that names its service compares ServiceName.
func TestSpanColumns(t *testing.T) {
	var sb strings.Builder
	args := appendSpanColumns(&sb, nil, fanOutSpec().EffectiveStages())
	if got := strings.Count(sb.String(), "ServiceName = ?"); got != 6 {
		t.Fatalf("three scoped spans, each in minIf and countIf: %d in %s", got, sb.String())
	}
	if len(args) != 12 {
		t.Fatalf("args %v", args)
	}
	sb.Reset()
	appendSpanColumns(&sb, nil, twoStageSpec().EffectiveStages())
	if strings.Contains(sb.String(), "ServiceName") {
		t.Fatalf("an unscoped span leaves the service alone: %s", sb.String())
	}
}

// Configuration transfer copies rule_spec as stored JSON, so the new
// fields must survive a round trip through it.
func TestSpecJSONRoundTrip(t *testing.T) {
	raw, err := json.Marshal(fanOutSpec())
	if err != nil {
		t.Fatal(err)
	}
	var back RuleSpec
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	back.Normalize()
	if got := back.EffectiveStages()[1]; !got.All || len(got.Spans) != 2 || got.Spans[1].Service != "wms-loader" {
		t.Fatalf("round trip: %+v from %s", got, raw)
	}
}
