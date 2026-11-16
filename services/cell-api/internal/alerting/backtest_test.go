// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// steppedMetric answers a metric aggregate from the time the window
// ends, so a test can lay out a history and see what a dry run makes of
// it.
type steppedMetric struct {
	at    func(to time.Time) (float64, uint64)
	calls int
}

func (s *steppedMetric) MetricAggregate(_ context.Context, _ string, _ []AttrFilter, _, _ string, _, _ *uuid.UUID, _, to time.Time) (float64, uint64, error) {
	s.calls++
	v, n := s.at(to)
	return v, n, nil
}

func (s *steppedMetric) MetricAggregateGrouped(context.Context, string, []AttrFilter, string, string, string, *uuid.UUID, *uuid.UUID, time.Time, time.Time) ([]MetricGroup, error) {
	return nil, nil
}

type countingTraces struct{ at func(to time.Time) uint64 }

func (c countingTraces) CountErrorTraces(_ context.Context, q TraceErrorQuery) (uint64, error) {
	return c.at(q.To), nil
}

func TestBacktestCountsEpisodes(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	// Above 10 from 00:30 to 00:45 and again from 01:30 to 01:35.
	metric := &steppedMetric{at: func(end time.Time) (float64, uint64) {
		m := end.Sub(from).Minutes()
		if (m > 30 && m <= 45) || (m > 90 && m <= 95) {
			return 20 + m/100, 3
		}
		if m > 100 && m <= 105 {
			return 0, 0 // no data: neither a breach nor a value
		}
		return 2, 3
	}}
	e := NewEngine(nil, metric, nil, nil, uuid.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	rule := AlertRule{Signal: SignalMetric, Spec: MetricRuleSpec{
		MetricName: "queue.depth", Aggregation: AggMax, Operator: OpGT, Threshold: 10, ForWindow: "5m",
	}}

	res, err := e.Backtest(context.Background(), rule, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if res.StepSeconds != 300 || res.Points != 24 || metric.calls != 24 {
		t.Fatalf("one look per 5m window over 2h: step %d, %d points, %d calls", res.StepSeconds, res.Points, metric.calls)
	}
	if res.Firings != 2 || res.BreachedPoints != 4 {
		t.Fatalf("two episodes of 3 and 1 looks: %+v", res)
	}
	if got := res.Episodes[0]; !got.Start.Equal(from.Add(35*time.Minute)) || !got.End.Equal(from.Add(45*time.Minute)) || got.Peak != 20.45 {
		t.Fatalf("first episode: %+v", got)
	}
	if res.PointsWithData != 23 || *res.Min != 2 || *res.Max != 20.95 {
		t.Fatalf("values: %d with data, min %v max %v", res.PointsWithData, *res.Min, *res.Max)
	}
}

// A long period is sampled, never more than MaxBacktestPoints looks.
func TestBacktestBoundsItsQueries(t *testing.T) {
	metric := &steppedMetric{at: func(time.Time) (float64, uint64) { return 1, 1 }}
	e := NewEngine(nil, metric, nil, nil, uuid.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	rule := AlertRule{Signal: SignalMetric, Spec: MetricRuleSpec{MetricName: "m", Aggregation: AggAvg, Operator: OpGT, Threshold: 5, ForWindow: "1m"}}
	res, err := e.Backtest(context.Background(), rule, to.Add(-30*24*time.Hour), to)
	if err != nil {
		t.Fatal(err)
	}
	if metric.calls > MaxBacktestPoints || res.Points != metric.calls || res.StepSeconds <= 60 {
		t.Fatalf("30 days of 1m windows: %d calls, step %ds", metric.calls, res.StepSeconds)
	}
}

func TestBacktestTraceErrorsAndRefusals(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	integ := uuid.New()
	traces := countingTraces{at: func(end time.Time) uint64 {
		if end.Sub(from) == 20*time.Minute {
			return 4
		}
		return 0
	}}
	e := NewEngine(nil, nil, nil, traces, uuid.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	rule := AlertRule{Signal: "trace", IntegrationID: &integ, TraceErrorSpec: &TraceErrorRuleSpec{Kind: TraceErrorSpecKind, Threshold: 3, WindowSeconds: 600}}
	res, err := e.Backtest(context.Background(), rule, from, from.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.Firings != 1 || res.Episodes[0].Peak != 4 {
		t.Fatalf("one look with 4 failed traces: %+v", res)
	}

	for name, r := range map[string]AlertRule{
		"pushed": {Signal: SignalMetric, Source: "pushed", Spec: MetricRuleSpec{ForWindow: "5m"}},
		"age":    {Signal: SignalMetric, Spec: MetricRuleSpec{Aggregation: AggAge, ForWindow: "5m"}},
	} {
		if _, err := e.Backtest(context.Background(), r, from, from.Add(time.Hour)); !errors.Is(err, ErrBacktestUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.Backtest(context.Background(), rule, from, from.Add(5*time.Minute)); err == nil {
		t.Error("a period shorter than the window is refused")
	}
}
