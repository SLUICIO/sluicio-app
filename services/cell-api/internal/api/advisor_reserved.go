// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	imclickhouse "github.com/sluicio/sluicio-app/pkg/clickhouse"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// flowAttrWindow is how far back the Advisor looks for attributes that
// split a service into flows: the integration candidates' default view.
const flowAttrWindow = 24 * time.Hour

// spanNameFilters are the span.name conditions of the org's integration
// matchers and trace alert rules. An equals on a matcher already names
// the span, but a pattern or a negation selects names no demand key can
// list, so the Advisor asks the filters themselves.
func (h *Handlers) spanNameFilters(ctx context.Context, orgID uuid.UUID) []store.LogAttrFilter {
	var out []store.LogAttrFilter
	if h.Integrations != nil {
		if all, err := h.Integrations.AllMatchersWithIntegration(ctx, orgID); err == nil {
			for _, mi := range all {
				if mi.Matcher.Attribute == store.SpanNameAttrKey {
					out = append(out, attrFilterFromMatcher(mi.Matcher))
				}
			}
		} else {
			h.Logger.Warn("advisor: matchers for span names", "err", err)
		}
	}
	if h.Alerts != nil {
		if rules, err := h.Alerts.ListRules(ctx, orgID); err == nil {
			for _, r := range rules {
				if r.Signal != "trace" {
					continue
				}
				if r.TraceErrorSpec != nil {
					for _, a := range r.TraceErrorSpec.Attrs {
						if a.Key == store.SpanNameAttrKey {
							out = append(out, store.LogAttrFilter{Key: a.Key, Op: a.Op, Value: a.Value})
						}
					}
				}
				if r.TraceAttributeSpec != nil {
					for _, a := range r.TraceAttributeSpec.Attrs {
						if a.Key == store.SpanNameAttrKey {
							out = append(out, store.LogAttrFilter{Key: a.Key, Op: a.Op, Value: a.Value})
						}
					}
				}
			}
		} else {
			h.Logger.Warn("advisor: alert rules for span names", "err", err)
		}
	}
	return out
}

// spanNameSelected reports whether dropping spans of this name could
// change what one of the filters selects. It errs towards yes: a
// negation, a numeric comparison or a pattern that does not compile reads
// every name.
func spanNameSelected(filters []store.LogAttrFilter, name string) bool {
	for _, f := range filters {
		switch f.Op {
		case "", store.AttrOpEq:
			if name == f.Value {
				return true
			}
		case store.AttrOpStartsWith:
			if strings.HasPrefix(name, f.Value) {
				return true
			}
		case store.AttrOpEndsWith:
			if strings.HasSuffix(name, f.Value) {
				return true
			}
		case store.AttrOpContains:
			if strings.Contains(name, f.Value) {
				return true
			}
		case store.AttrOpMatches:
			re, err := regexp.Compile(f.Value)
			if err != nil || re.MatchString(name) {
				return true
			}
		case store.AttrOpNotExists:
			// Every span has a name: selects none.
		default:
			return true
		}
	}
	return false
}

// flowAttrs are, per service, the attributes the integration candidates
// would propose splitting it by (issue #18), over the last day.
func (h *Handlers) flowAttrs(ctx context.Context, orgID uuid.UUID) map[string][]string {
	if h.Store == nil {
		return nil
	}
	ctx = imclickhouse.WithOrgFilter(ctx, orgID.String())
	to := time.Now()
	from := to.Add(-flowAttrWindow)
	popt := proposals.DefaultPartitionOptions()
	all, err := h.Store.ListServices(ctx, from, to)
	if err != nil {
		h.Logger.Warn("advisor: services for flow attributes", "err", err)
		return nil
	}
	names := make([]string, 0, len(all))
	for _, s := range all {
		if s.TraceCount >= popt.MinTraces {
			names = append(names, s.ServiceName)
		}
	}
	rows, err := h.Store.AttrPartitionStats(ctx, names, from, to, 20000, popt.MaxValues)
	if err != nil {
		h.Logger.Warn("advisor: flow attributes", "err", err)
		return nil
	}
	stats := make([]proposals.AttrPartitionStats, 0, len(rows))
	for _, r := range rows {
		stats = append(stats, proposals.AttrPartitionStats{
			Service: r.Service, Key: r.Key, TracesWithKey: r.TracesWithKey, TracesTotal: r.TracesTotal,
			DistinctValues: r.DistinctValues, TraceValuePairs: r.TraceValuePairs, NumericValues: r.NumericValues, BooleanValues: r.BooleanValues,
		})
	}
	out := map[string][]string{}
	for _, c := range proposals.FindPartitions(stats, popt, 2, 0) {
		out[c.Service] = append(out[c.Service], c.Key)
	}
	return out
}
