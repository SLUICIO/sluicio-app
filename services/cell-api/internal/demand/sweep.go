// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The mechanical half of the demand ledger (design §2): a daily walk of
// the org's configuration, writing one row per telemetry key any config
// references. Config IS demand, permanently, for as long as the
// reference exists — so unlike human demand this needs no
// instrumentation, only a sweep.
//
// The rule that shapes everything here: DISABLED CONFIG STILL COUNTS.
// A metric referenced by a paused alert rule is in use; advising someone
// to drop it would break the rule the moment they re-enable it. Every
// list method below is therefore the "all", not the "enabled", variant —
// see tracecompletion.ListAll, which exists for exactly this reason.
//
// The sweep is deliberately tolerant: one failing source logs and the
// rest still run. A partial day of config demand is a much smaller
// problem than a sweep that aborts and records nothing.

package demand

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/messageviews"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/monitoringtemplates"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tracecompletion"
)

// Narrow source interfaces rather than concrete stores: the sweep reads
// six unrelated packages, and depending on their types wholesale would
// make this the most coupled file in cell-api.
type (
	AlertRuleSource interface {
		ListRules(ctx context.Context, orgID uuid.UUID) ([]alerting.AlertRule, error)
	}
	MatcherSource interface {
		AllMatchersWithIntegration(ctx context.Context, orgID uuid.UUID) ([]integrations.MatcherWithIntegration, error)
	}
	CompletionSource interface {
		ListAll(ctx context.Context, orgID uuid.UUID) ([]tracecompletion.Rule, error)
	}
	TemplateSource interface {
		List(ctx context.Context, orgID uuid.UUID) ([]monitoringtemplates.Template, error)
	}
	ViewSource interface {
		List(ctx context.Context, orgID uuid.UUID, ownerUserID *uuid.UUID) ([]messageviews.View, error)
	}
	// IntegrationServices resolves an integration to its member service
	// names, so integration-scoped config lands on the services whose
	// telemetry it actually consumes.
	IntegrationServices interface {
		IntegrationServices(ctx context.Context, integrationID uuid.UUID) ([]string, error)
	}
)

// Sweeper walks org config once a day and records mechanical demand.
type Sweeper struct {
	Writer      *Writer
	OrgID       uuid.UUID
	Rules       AlertRuleSource
	Matchers    MatcherSource
	Completions CompletionSource
	Templates   TemplateSource
	Views       ViewSource
	Catalog     IntegrationServices
	Log         *slog.Logger
	// Every defaults to 24h. The sweep is idempotent within a day —
	// SummingMergeTree would just add another Hits=1 — so the exact
	// cadence only affects how quickly new config shows up as demand.
	Every time.Duration
}

// Run sweeps once at startup (so a fresh cell has config demand
// immediately rather than tomorrow) and then on the ticker.
func (s *Sweeper) Run(ctx context.Context) {
	if s.Every <= 0 {
		s.Every = 24 * time.Hour
	}
	s.RunOnce(ctx)
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunOnce(ctx)
		}
	}
}

// RunOnce performs one sweep. Exported so a future admin endpoint (and
// the tests) can trigger it without waiting a day.
func (s *Sweeper) RunOnce(ctx context.Context) {
	started := time.Now()
	for _, step := range []struct {
		name string
		fn   func(context.Context) error
	}{
		{"alert rules", s.sweepAlertRules},
		{"matchers", s.sweepMatchers},
		{"completion rules", s.sweepCompletions},
		{"templates", s.sweepTemplates},
		{"message views", s.sweepViews},
	} {
		if err := step.fn(ctx); err != nil {
			// Tolerated: the other sources still contribute today's rows.
			s.Log.Warn("demand sweep step failed", "step", step.name, "err", err)
		}
	}
	// Flush rather than leaving a day's worth of config demand waiting on
	// the writer's 30s tick.
	if err := s.Writer.Flush(ctx); err != nil {
		s.Log.Warn("demand sweep flush failed", "err", err)
		return
	}
	s.Log.Info("demand sweep complete", "took", time.Since(started).Round(time.Millisecond))
}

// scopeServices resolves a rule's scope to the service names its demand
// belongs to. A service-bound rule is its own scope; an integration-bound
// one spreads over member services; an org-wide rule records against ""
// (whole-org demand for that key).
func (s *Sweeper) scopeServices(ctx context.Context, serviceName string, integrationID *uuid.UUID) []string {
	if serviceName != "" {
		return []string{serviceName}
	}
	if integrationID != nil && s.Catalog != nil {
		if svcs, err := s.Catalog.IntegrationServices(ctx, *integrationID); err == nil && len(svcs) > 0 {
			return svcs
		}
	}
	return []string{""}
}

func (s *Sweeper) sweepAlertRules(ctx context.Context) error {
	// ListRules returns enabled AND disabled rules, which is what the
	// sweep wants. Note it excludes trace-completion rules by design —
	// those come from sweepCompletions.
	rules, err := s.Rules.ListRules(ctx, s.OrgID)
	if err != nil {
		return err
	}
	for _, r := range rules {
		scopes := s.scopeServices(ctx, r.ServiceName, r.IntegrationID)
		for _, svc := range scopes {
			switch r.Signal {
			case "metric":
				s.Writer.Record(s.OrgID, SignalMetric, svc, r.Spec.MetricName, KindRule)
				s.Writer.RecordKeys(s.OrgID, SignalMetric, svc, attrKeys(r.Spec.Attrs), KindRule)
				s.Writer.Record(s.OrgID, SignalMetric, svc, r.Spec.SplitBy, KindRule)
			case "log":
				// Whole-signal demand: a severity floor or body filter
				// consumes the service's logs without naming a key.
				s.Writer.Record(s.OrgID, SignalLog, svc, "", KindRule)
				if r.LogSpec != nil {
					s.Writer.RecordKeys(s.OrgID, SignalLog, svc, attrKeys(r.LogSpec.Attrs), KindRule)
				}
			case "trace":
				s.Writer.Record(s.OrgID, SignalTrace, svc, "", KindRule)
				if r.TraceErrorSpec != nil {
					s.Writer.RecordKeys(s.OrgID, SignalTrace, svc, attrKeys(r.TraceErrorSpec.Attrs), KindRule)
				}
			}
		}
	}
	return nil
}

func (s *Sweeper) sweepMatchers(ctx context.Context) error {
	all, err := s.Matchers.AllMatchersWithIntegration(ctx, s.OrgID)
	if err != nil {
		return err
	}
	for _, m := range all {
		// Service matchers name a service, not a telemetry attribute —
		// they're routing, not consumption.
		if m.Matcher.IsServiceMatcher() || m.Matcher.Attribute == "" {
			continue
		}
		// Matchers run over span/resource attributes at classification
		// time, before any service scoping exists — org-wide demand.
		s.Writer.Record(s.OrgID, SignalTrace, "", m.Matcher.Attribute, KindMatcher)
	}
	return nil
}

func (s *Sweeper) sweepCompletions(ctx context.Context) error {
	rules, err := s.Completions.ListAll(ctx, s.OrgID)
	if err != nil {
		return err
	}
	for _, r := range rules {
		spec := r.Spec
		// Completion rules are always integration-bound (non-pointer id).
		integrationID := r.IntegrationID
		scopes := s.scopeServices(ctx, "", &integrationID)
		for _, svc := range scopes {
			// Span names are their own namespace in the Key column;
			// ConsumerKind "completion" is what tells a reader that.
			s.Writer.Record(s.OrgID, SignalTrace, svc, spec.StartSpanName, KindCompletion)
			// EffectiveStages folds the legacy ClosingSpanNames into the
			// stage list — reading both fields would double-count.
			for _, st := range spec.EffectiveStages() {
				s.Writer.RecordKeys(s.OrgID, SignalTrace, svc, st.SpanNames, KindCompletion)
			}
		}
	}
	return nil
}

// sweepTemplates covers monitoring templates. System types share the
// same Check type (systemtypes.Check is an alias), so a caller that
// wants both can wrap the second source in the same interface.
func (s *Sweeper) sweepTemplates(ctx context.Context) error {
	tpls, err := s.Templates.List(ctx, s.OrgID)
	if err != nil {
		return err
	}
	for _, t := range tpls {
		for _, c := range t.Checks {
			// A template is a blueprint — it isn't bound to a service
			// until applied, so its demand is org-wide.
			switch c.Signal {
			case "log":
				s.Writer.Record(s.OrgID, SignalLog, "", "", KindTemplate)
			case "trace":
				s.Writer.Record(s.OrgID, SignalTrace, "", "", KindTemplate)
			default:
				s.Writer.Record(s.OrgID, SignalMetric, "", c.Metric, KindTemplate)
				s.Writer.RecordKeys(s.OrgID, SignalMetric, "", templateAttrKeys(c.Attrs), KindTemplate)
				s.Writer.Record(s.OrgID, SignalMetric, "", c.SplitBy, KindTemplate)
			}
		}
	}
	return nil
}

func (s *Sweeper) sweepViews(ctx context.Context) error {
	// ownerUserID nil = every view in the org, not one user's. The ledger
	// records THAT a key is referenced by a saved view, never by whom.
	views, err := s.Views.List(ctx, s.OrgID, nil)
	if err != nil {
		return err
	}
	for _, v := range views {
		for _, f := range v.Filters {
			// Only payload filters carry an attribute key; the rest name
			// built-in columns (status, service, traceId…).
			if f.Field == messageviews.FieldPayload && f.FieldPath != "" {
				s.Writer.Record(s.OrgID, SignalTrace, "", f.FieldPath, KindView)
			}
		}
	}
	return nil
}

func attrKeys(attrs []alerting.AttrFilter) []string {
	out := make([]string, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, a.Key)
	}
	return out
}

func templateAttrKeys(attrs []monitoringtemplates.AttrFilter) []string {
	out := make([]string, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, a.Key)
	}
	return out
}
