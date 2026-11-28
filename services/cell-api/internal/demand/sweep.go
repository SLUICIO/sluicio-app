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
	"github.com/sluicio/sluicio-app/services/cell-api/internal/cellhealth"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/dashboards"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetmappings"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/messageviews"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/monitoringtemplates"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/payloadlinks"
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
	// FacetSource lists the org's facet mappings. An attribute that
	// classifies a service into a facet is consumed by CODE rather than
	// by any query, so without this sweep the Telemetry Advisor would
	// see no demand for it and propose deleting the attribute that makes
	// the service's own detail page work.
	FacetSource interface {
		ListAll(ctx context.Context, orgID uuid.UUID) ([]facetmappings.Mapping, error)
	}
	// DashboardSource lists the org's dashboards. A pinned integration
	// is a standing claim on its services' telemetry: somebody looks at
	// that card every morning, and whether they filtered anything is not
	// the point.
	DashboardSource interface {
		List(ctx context.Context, orgID uuid.UUID, callerID *uuid.UUID) ([]dashboards.Dashboard, error)
	}
	// IntegrationSource lists the org's integrations and their metric and
	// log sources. An integration's message columns and filters read span
	// attributes, and its sources read metrics and logs, in code rather
	// than through any query the human ledger sees.
	IntegrationSource interface {
		List(ctx context.Context, orgID uuid.UUID) ([]integrations.Integration, error)
		SourcesForIntegration(ctx context.Context, integrationID uuid.UUID) ([]integrations.Source, error)
	}
	// SystemServices resolves a system to its member service names, as
	// IntegrationServices does for an integration.
	SystemServices interface {
		SystemMemberNames(ctx context.Context, orgID, systemID uuid.UUID) ([]string, error)
	}
	// PayloadLinkSource lists the attributes that hold payload links,
	// read on every message list.
	PayloadLinkSource interface {
		Get(ctx context.Context, orgID uuid.UUID) ([]payloadlinks.Attribute, error)
	}
)

// Sweeper walks org config once a day and records mechanical demand.
type Sweeper struct {
	Writer       *Writer
	OrgID        uuid.UUID
	Rules        AlertRuleSource
	Matchers     MatcherSource
	Completions  CompletionSource
	Templates    TemplateSource
	Views        ViewSource
	Facets       FacetSource
	Dashboards   DashboardSource
	Integrations IntegrationSource
	PayloadLinks PayloadLinkSource
	Catalog      IntegrationServices
	Systems      SystemServices
	Log          *slog.Logger
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
	cellhealth.Register("demand-sweep", s.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunOnce(ctx)
			// End of cycle, not start: a loop wedged inside its
			// own body is exactly what this catches.
			cellhealth.Beat("demand-sweep")
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
		{"facet mappings", s.sweepFacets},
		{"dashboards", s.sweepDashboards},
		{"integrations", s.sweepIntegrations},
		{"payload links", s.sweepPayloadLinks},
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
// belongs to. A service-bound rule is its own scope; an integration- or
// system-bound one spreads over its member services; an org-wide rule
// records against "" (whole-org demand for that key). A scope that
// cannot be resolved also records against "", erring towards keeping
// telemetry; one that resolves to no services records nothing, since it
// reads nothing: recorded against "", a log rule on an empty integration
// would have kept every service's logs from the advisor.
func (s *Sweeper) scopeServices(ctx context.Context, serviceName string, integrationID, systemID *uuid.UUID) []string {
	if serviceName != "" {
		return []string{serviceName}
	}
	var (
		svcs []string
		err  error
	)
	switch {
	case integrationID != nil && s.Catalog != nil:
		svcs, err = s.Catalog.IntegrationServices(ctx, *integrationID)
	case systemID != nil && s.Systems != nil:
		svcs, err = s.Systems.SystemMemberNames(ctx, s.OrgID, *systemID)
	case integrationID == nil && systemID == nil:
		return []string{""}
	default:
		return []string{""} // bound, but no resolver: keep it all
	}
	if err != nil {
		return []string{""}
	}
	return svcs
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
		scopes := s.scopeServices(ctx, r.ServiceName, r.IntegrationID, r.SystemID)
		for _, svc := range scopes {
			switch r.Signal {
			case "metric":
				// RecordKeys skips empty keys: an empty metric name or
				// split_by recorded as such would be whole-signal demand,
				// which keeps every metric from T1.
				s.Writer.RecordKeys(s.OrgID, SignalMetric, svc, []string{r.Spec.MetricName, r.Spec.SplitBy}, KindRule)
				s.Writer.RecordKeys(s.OrgID, SignalMetric, svc, attrKeys(r.Spec.Attrs), KindRule)
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
				if r.TraceAttributeSpec != nil {
					s.Writer.RecordKeys(s.OrgID, SignalTrace, svc, attrKeys(r.TraceAttributeSpec.Attrs), KindRule)
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
		scopes := s.scopeServices(ctx, "", &integrationID, nil)
		for _, svc := range scopes {
			// Span names are their own namespace in the Key column;
			// ConsumerKind "completion" is what tells a reader that.
			s.Writer.Record(s.OrgID, SignalTrace, svc, spec.StartSpanName, KindCompletion)
			// EffectiveStages folds the legacy ClosingSpanNames into the
			// stage list — reading both fields would double-count.
			for _, st := range spec.EffectiveStages() {
				for _, m := range st.Spans {
					// A span that names its service is demand on that
					// service alone.
					if m.Service == "" || m.Service == svc {
						s.Writer.Record(s.OrgID, SignalTrace, svc, m.SpanName, KindCompletion)
					}
				}
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
			// A template is a blueprint, bound to no service until it is
			// applied, and an applied check is an alert rule, swept with
			// the rules. What it names is still org-wide demand for a
			// metric: a metric only a template reads should not be called
			// unused. Its log and trace checks name no key, and recorded
			// against "" they would keep every service's logs from T2.
			if c.Signal != "" && c.Signal != "metric" {
				continue
			}
			s.Writer.RecordKeys(s.OrgID, SignalMetric, "", []string{c.Metric, c.SplitBy}, KindTemplate)
			s.Writer.RecordKeys(s.OrgID, SignalMetric, "", templateAttrKeys(c.Attrs), KindTemplate)
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

// sweepFacets records the attribute keys that drive facet
// classification. These are the most dangerous keys to lose: nothing
// QUERIES them, so every other demand source is silent about them,
// while deleting one would quietly change how a service is classified
// across the whole product.
func (s *Sweeper) sweepFacets(ctx context.Context) error {
	if s.Facets == nil {
		return nil
	}
	mappings, err := s.Facets.ListAll(ctx, s.OrgID)
	if err != nil {
		return err
	}
	for _, m := range mappings {
		if m.AttributeKey == "" {
			continue
		}
		s.Writer.Record(s.OrgID, SignalTrace, m.ServiceName, m.AttributeKey, KindFacet)
	}
	return nil
}

// sweepDashboards records whole-signal demand for the services behind
// every pinned integration.
//
// Whole-signal, not per-key: a dashboard card does not name attributes,
// it says "this integration matters enough to look at daily". That is a
// claim on the service's traces in general, and the honest way to
// record it is the same way a human opening the service page is
// recorded.
func (s *Sweeper) sweepDashboards(ctx context.Context) error {
	if s.Dashboards == nil || s.Catalog == nil {
		return nil
	}
	// callerID nil = every dashboard in the org. The ledger records THAT
	// an integration is pinned, never on whose dashboard.
	boards, err := s.Dashboards.List(ctx, s.OrgID, nil)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, b := range boards {
		for _, item := range b.Items {
			if item.IntegrationID == uuid.Nil {
				continue
			}
			svcs, err := s.Catalog.IntegrationServices(ctx, item.IntegrationID)
			if err != nil {
				continue
			}
			for _, svc := range svcs {
				if seen[svc] {
					continue
				}
				seen[svc] = true
				s.Writer.Record(s.OrgID, SignalTrace, svc, "", KindDashboard)
			}
		}
	}
	return nil
}

// sweepIntegrations records what integrations read in code: the span
// attributes promoted to message-list columns and filter fields, on the
// integration's services, and the metrics and logs of its sources, on
// the sources' origin services (a whole stream when a source names no
// metric).
func (s *Sweeper) sweepIntegrations(ctx context.Context) error {
	if s.Integrations == nil {
		return nil
	}
	list, err := s.Integrations.List(ctx, s.OrgID)
	if err != nil {
		return err
	}
	for _, in := range list {
		var keys []string
		for _, c := range in.MessageColumns {
			if c.Kind == integrations.ColumnKindAttribute || (c.Kind == "" && c.Key != "") {
				keys = append(keys, c.Key)
			}
		}
		for _, f := range integrations.AttributeFilters(in.MessageFilters) {
			keys = append(keys, f.Key)
		}
		if len(keys) > 0 {
			id := in.ID
			for _, svc := range s.scopeServices(ctx, "", &id, nil) {
				s.Writer.RecordKeys(s.OrgID, SignalTrace, svc, keys, KindIntegration)
			}
		}
		sources, err := s.Integrations.SourcesForIntegration(ctx, in.ID)
		if err != nil {
			s.Log.Warn("demand sweep: integration sources", "integration", in.ID, "err", err)
			continue
		}
		for _, src := range sources {
			var svcs []string
			switch src.OriginKind {
			case integrations.OriginService:
				svcs = []string{src.Origin}
			case integrations.OriginSystem:
				if id, err := uuid.Parse(src.Origin); err == nil {
					svcs = s.scopeServices(ctx, "", nil, &id)
				}
			}
			var conds []string
			for _, c := range src.Conditions {
				conds = append(conds, c.Attribute)
			}
			for _, svc := range svcs {
				switch src.Signal {
				case integrations.SourceMetrics:
					if len(src.MetricNames) == 0 {
						s.Writer.Record(s.OrgID, SignalMetric, svc, "", KindIntegration)
					}
					s.Writer.RecordKeys(s.OrgID, SignalMetric, svc, src.MetricNames, KindIntegration)
					s.Writer.RecordKeys(s.OrgID, SignalMetric, svc, conds, KindIntegration)
				case integrations.SourceLogs:
					s.Writer.Record(s.OrgID, SignalLog, svc, "", KindIntegration)
					s.Writer.RecordKeys(s.OrgID, SignalLog, svc, conds, KindIntegration)
				}
			}
		}
	}
	return nil
}

// sweepPayloadLinks records the attributes that hold payload links. They
// are org-wide: every message list reads them, whatever the service.
func (s *Sweeper) sweepPayloadLinks(ctx context.Context) error {
	if s.PayloadLinks == nil {
		return nil
	}
	attrs, err := s.PayloadLinks.Get(ctx, s.OrgID)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(attrs))
	for _, a := range attrs {
		keys = append(keys, a.Attribute)
	}
	s.Writer.RecordKeys(s.OrgID, SignalTrace, "", keys, KindPayloadLink)
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
