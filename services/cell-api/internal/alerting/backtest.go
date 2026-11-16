// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/envctx"
)

// A dry run answers "how often would this check have fired?" against
// the telemetry already stored, so a proposed threshold arrives with
// evidence rather than a guess. It asks the same questions the engine
// asks, through the same evaluators and the same comparison, for
// windows ending at evenly spaced moments in the past; it never opens
// or touches an instance.
//
// It is a sample, not a replay: the engine looks every tick, a dry run
// looks at most MaxBacktestPoints times per environment. Consecutive
// breached looks are one episode, as consecutive breached ticks are one
// firing.

// MaxBacktestPoints bounds the queries one dry run may make per
// environment.
const MaxBacktestPoints = 400

// ErrBacktestUnsupported marks a rule a dry run cannot rehearse.
var ErrBacktestUnsupported = errors.New("this kind of check cannot be dry-run")

// BacktestEpisode is one stretch of consecutive breached looks.
type BacktestEpisode struct {
	Environment string    `json:"environment,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	// Peak is the value furthest past the threshold during the episode.
	Peak float64 `json:"peak"`
}

// BacktestResult is what a dry run found.
type BacktestResult struct {
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	WindowSeconds  int       `json:"window_seconds"`
	StepSeconds    int       `json:"step_seconds"`
	Environments   []string  `json:"environments,omitempty"`
	Points         int       `json:"points"`
	PointsWithData int       `json:"points_with_data"`
	BreachedPoints int       `json:"breached_points"`
	// Firings is how many separate times the check would have fired.
	Firings  int               `json:"firings"`
	Episodes []BacktestEpisode `json:"episodes"`
	// Min and Max are the values seen at looks that had data.
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	Note string   `json:"note"`
}

// measurement is one look: the value the check compares, whether there
// was anything to compare, and whether it breached.
type measurement struct {
	value    float64
	hasData  bool
	breached bool
}

// Backtest dry-runs rule over [from, to].
func (e *Engine) Backtest(ctx context.Context, rule AlertRule, from, to time.Time) (BacktestResult, error) {
	window, err := backtestWindow(rule)
	if err != nil {
		return BacktestResult{}, err
	}
	if !to.After(from.Add(window)) {
		return BacktestResult{}, fmt.Errorf("the period must be longer than the check's window (%s)", window)
	}
	// Looks fall at from+window, then every step up to to: span/step+1
	// of them. Rounding the step up keeps that at MaxBacktestPoints.
	step := window
	if span := to.Sub(from.Add(window)); span/step+1 > MaxBacktestPoints {
		step = (span + MaxBacktestPoints - 2) / (MaxBacktestPoints - 1)
	}
	res := BacktestResult{
		From: from, To: to,
		WindowSeconds: int(window.Seconds()), StepSeconds: int(step.Seconds()),
		Episodes: []BacktestEpisode{},
		Note: "Sampled: one look every step, each over the check's window, using the evaluator's own queries. " +
			"A breach shorter than a step can be missed, and renotification and maintenance windows are not modelled.",
	}

	envs := []string{""}
	if rule.IntegrationID != nil && e.envLister != nil {
		list, perEnv, eErr := e.environmentsFor(ctx, rule)
		if eErr != nil {
			return BacktestResult{}, fmt.Errorf("environments: %w", eErr)
		}
		if perEnv {
			envs = list
			res.Environments = list
		}
	}

	for _, env := range envs {
		ectx := ctx
		if env != "" {
			ectx = envctx.With(ctx, env)
		}
		var open *BacktestEpisode
		for end := from.Add(window); !end.After(to); end = end.Add(step) {
			m, mErr := e.measure(ectx, rule, end.Add(-window), end)
			if mErr != nil {
				return BacktestResult{}, mErr
			}
			res.Points++
			if m.hasData {
				res.PointsWithData++
				if res.Min == nil || m.value < *res.Min {
					v := m.value
					res.Min = &v
				}
				if res.Max == nil || m.value > *res.Max {
					v := m.value
					res.Max = &v
				}
			}
			if !m.breached {
				open = nil
				continue
			}
			res.BreachedPoints++
			if open == nil {
				res.Episodes = append(res.Episodes, BacktestEpisode{Environment: env, Start: end, End: end, Peak: m.value})
				open = &res.Episodes[len(res.Episodes)-1]
				continue
			}
			open.End = end
			if furtherPast(rule, m.value, open.Peak) {
				open.Peak = m.value
			}
		}
	}
	res.Firings = len(res.Episodes)
	sort.SliceStable(res.Episodes, func(i, j int) bool { return res.Episodes[i].Start.Before(res.Episodes[j].Start) })
	return res, nil
}

// backtestWindow is the window a rule's check reads, or why it cannot
// be dry-run.
func backtestWindow(rule AlertRule) (time.Duration, error) {
	switch {
	case rule.Signal == SignalMetric:
		if rule.Source == "pushed" {
			return 0, fmt.Errorf("%w: a pushed value has no history to replay", ErrBacktestUnsupported)
		}
		if rule.Spec.Aggregation == AggAge {
			return 0, fmt.Errorf("%w: age is measured from now, so it has no past values", ErrBacktestUnsupported)
		}
		return rule.Spec.ForWindowDuration(), nil
	case rule.LogSpec != nil:
		return rule.LogSpec.WindowDuration(), nil
	case rule.TraceErrorSpec != nil:
		return rule.TraceErrorSpec.WindowDuration(), nil
	case rule.TraceAttributeSpec != nil:
		return rule.TraceAttributeSpec.WindowDuration(), nil
	case rule.TraceLatencySpec != nil:
		return rule.TraceLatencySpec.WindowDuration(), nil
	case rule.TraceVolumeSpec != nil:
		return rule.TraceVolumeSpec.WindowDuration(), nil
	}
	return 0, ErrBacktestUnsupported
}

// furtherPast reports whether v is further past the rule's threshold
// than peak: lower for a check that fires below its threshold, higher
// otherwise.
func furtherPast(rule AlertRule, v, peak float64) bool {
	below := (rule.Signal == SignalMetric && (rule.Spec.Operator == OpLT || rule.Spec.Operator == OpLTE)) ||
		(rule.LogSpec != nil && rule.LogSpec.FiresBelow()) ||
		rule.TraceVolumeSpec != nil
	if below {
		return v < peak
	}
	return v > peak
}

// measure is one look at [from, to], decided the way the engine decides
// it for the rule's signal.
func (e *Engine) measure(ctx context.Context, rule AlertRule, from, to time.Time) (measurement, error) {
	switch {
	case rule.Signal == SignalMetric && rule.Spec.SplitBy != "":
		groups, err := e.eval.MetricAggregateGrouped(ctx, rule.Spec.MetricName, rule.Spec.Attrs, string(rule.Spec.Aggregation), rule.Spec.SplitBy, rule.ServiceName, rule.IntegrationID, rule.SystemID, from, to)
		if err != nil {
			return measurement{}, err
		}
		// A split check fires per value; the look breaches when any
		// value does, and reports the furthest one.
		var m measurement
		for _, g := range groups {
			if g.Samples == 0 {
				continue
			}
			b := EvaluateBreach(rule.Spec.Operator, g.Value, rule.Spec.Threshold)
			if !m.hasData || (b && !m.breached) || (b == m.breached && furtherPast(rule, g.Value, m.value)) {
				m = measurement{value: g.Value, hasData: true, breached: b || m.breached}
			}
		}
		return m, nil
	case rule.Signal == SignalMetric:
		value, samples, err := e.eval.MetricAggregate(ctx, rule.Spec.MetricName, rule.Spec.Attrs, string(rule.Spec.Aggregation), rule.ServiceName, rule.IntegrationID, rule.SystemID, from, to)
		if err != nil {
			return measurement{}, err
		}
		// No-data firing depends on whether the check ever reported, which
		// a look into the past cannot know; it counts as no data here.
		return measurement{value: value, hasData: samples > 0, breached: DecideMetric(rule.Spec, samples, value, false) == OutcomeBreach}, nil
	case rule.LogSpec != nil:
		spec := *rule.LogSpec
		count, err := e.logEval.CountLogs(ctx, LogCountQuery{
			MinSeverity: spec.MinSeverity, BodyContains: spec.BodyContains, Attrs: spec.Attrs,
			ServiceName: rule.ServiceName, IntegrationID: rule.IntegrationID, SystemID: rule.SystemID,
			From: from, To: to,
		})
		if err != nil {
			return measurement{}, err
		}
		threshold := uint64(max(spec.Threshold, 1))
		breached := count >= threshold
		if spec.FiresBelow() {
			breached = count < threshold
		}
		return measurement{value: float64(count), hasData: true, breached: breached}, nil
	case rule.TraceErrorSpec != nil:
		if !ruleHasScope(rule) {
			return measurement{}, nil
		}
		spec := *rule.TraceErrorSpec
		count, err := e.traceEval.CountErrorTraces(ctx, TraceErrorQuery{
			IntegrationID: rule.IntegrationID, SystemID: rule.SystemID, ServiceName: rule.ServiceName,
			From: from, To: to, Attrs: spec.Attrs,
		})
		if err != nil {
			return measurement{}, err
		}
		return measurement{value: float64(count), hasData: true, breached: count >= uint64(max(spec.Threshold, 1))}, nil
	case rule.TraceAttributeSpec != nil:
		spec := *rule.TraceAttributeSpec
		if !ruleHasScope(rule) || len(spec.Attrs) == 0 || e.attrEval == nil {
			return measurement{}, nil
		}
		count, err := e.attrEval.CountMatchingTraces(ctx, TraceAttributeQuery{
			IntegrationID: rule.IntegrationID, SystemID: rule.SystemID, ServiceName: rule.ServiceName,
			From: from, To: to, Attrs: spec.Attrs,
		})
		if err != nil {
			return measurement{}, err
		}
		return measurement{value: float64(count), hasData: true, breached: count >= uint64(max(spec.Threshold, 1))}, nil
	case rule.TraceLatencySpec != nil:
		spec := *rule.TraceLatencySpec
		if !ruleHasScope(rule) || e.latencyEval == nil {
			return measurement{}, nil
		}
		ms, samples, err := e.latencyEval.TraceLatencyMs(ctx, TraceLatencyQuery{
			IntegrationID: rule.IntegrationID, SystemID: rule.SystemID, ServiceName: rule.ServiceName,
			Quantile: spec.Quantile(), From: from, To: to,
		})
		if err != nil {
			return measurement{}, err
		}
		return measurement{value: ms, hasData: samples > 0, breached: samples > 0 && spec.ThresholdMs > 0 && ms >= float64(spec.ThresholdMs)}, nil
	case rule.TraceVolumeSpec != nil:
		spec := *rule.TraceVolumeSpec
		if !ruleHasScope(rule) || e.volumeEval == nil {
			return measurement{}, nil
		}
		total, scoped, err := e.volumeEval.TotalTraces(ctx, TraceVolumeQuery{
			IntegrationID: rule.IntegrationID, SystemID: rule.SystemID, ServiceName: rule.ServiceName,
			From: from, To: to,
		})
		if err != nil {
			return measurement{}, err
		}
		if !scoped {
			return measurement{}, nil
		}
		return measurement{value: float64(total), hasData: true, breached: spec.Threshold > 0 && total < uint64(spec.Threshold)}, nil
	}
	return measurement{}, ErrBacktestUnsupported
}
