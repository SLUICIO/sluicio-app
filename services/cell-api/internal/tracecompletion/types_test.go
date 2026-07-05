// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tracecompletion

import (
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

	t.Run("completed when final stage present", func(t *testing.T) {
		a := mk(func(a *traceAgg) { a.HasStage[1] = true; a.StageAt[1] = now })
		if c := classify(spec, a, now); c.State != StateCompleted {
			t.Fatalf("want completed, got %s", c.State)
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
