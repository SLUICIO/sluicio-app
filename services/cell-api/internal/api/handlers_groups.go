// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/store"
)

// Group-by rollups for the Metrics catalog and the Logs search: one
// summary row per group (Service / Integration / Type-or-Severity / an
// attribute value). Integration grouping resolves services → integration
// in Go over the per-service rollup. Expanding a group re-runs the list
// endpoint scoped to the group (service=, type=, attr=, or integration=).

const noIntegrationLabel = "(no integration)"

// parseGroupBy validates the `by` param against an allow-list and, for
// "attribute", the required `key` against the attr-key charset.
func parseGroupBy(r *http.Request, allowed map[string]bool) (by, key string, ok bool, msg string) {
	by = strings.TrimSpace(r.URL.Query().Get("by"))
	if by == "" || !allowed[by] {
		return "", "", false, "invalid or missing `by`"
	}
	if by == "attribute" {
		key = strings.TrimSpace(r.URL.Query().Get("key"))
		if !attrKeyRe.MatchString(key) {
			return "", "", false, "invalid attribute key"
		}
	}
	return by, key, true, ""
}

// parseMinSeverity reads the OTLP severity floor from `min_severity`.
func parseMinSeverity(r *http.Request) int32 {
	if v := r.URL.Query().Get("min_severity"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24 {
			return int32(n)
		}
	}
	return 0
}

// integrationFilter resolves the `integration` query param to a
// ServiceName IN (...) filter using the given candidate services.
// Returns (services, true) when the param is present; a no-match yields
// a sentinel ([""]) that excludes everything so the list comes back
// empty rather than unfiltered.
func (h *Handlers) integrationFilter(ctx context.Context, r *http.Request, candidates func() ([]string, error)) ([]string, [][]store.LogAttrFilter, bool) {
	name := strings.TrimSpace(r.URL.Query().Get("integration"))
	if name == "" {
		return nil, nil, false
	}
	cand, err := candidates()
	if err != nil {
		return []string{""}, nil, true
	}
	// The integration-grouped rollups bin services that map to no
	// integration into a "(no integration)" group. Expanding it sends
	// that label back as the `integration` value — resolve it to the
	// candidate services that belong to no integration (rather than
	// treating it as a real integration name, which matches nothing and
	// returns an empty list despite the group's non-zero count).
	var svcs []string
	var attrGroups [][]store.LogAttrFilter
	if name == noIntegrationLabel {
		svcs = h.servicesWithoutIntegration(ctx, cand)
	} else {
		svcs = h.servicesForIntegration(ctx, name, cand)
		// Attribute matchers (producer, consumer, …) become a DNF row
		// predicate on the integration's telemetry. service.name matchers
		// already shaped svcs above.
		if id, ok := h.integrationIDByName(ctx, name); ok {
			attrGroups = h.integrationGroups(ctx, id)
		}
	}
	if len(svcs) == 0 {
		return []string{""}, attrGroups, true
	}
	return svcs, attrGroups, true
}

// integrationIDByName resolves an integration's display name to its ID
// within the request's org. Returns false if no integration matches.
func (h *Handlers) integrationIDByName(ctx context.Context, name string) (uuid.UUID, bool) {
	ints, err := h.Integrations.List(ctx, middleware.OrgIDFromContext(ctx))
	if err != nil {
		return uuid.Nil, false
	}
	for _, in := range ints {
		if in.Name == name {
			return in.ID, true
		}
	}
	return uuid.Nil, false
}

// catalogServiceNames returns the org's known service names from the
// Postgres catalog snapshot — the reconciler keeps it current. Used as
// the `candidates` source for integrationFilter on the hot logs paths
// (list / volume / groups) where the original CH-based DistinctLogServices
// query was scanning every part in the window per request. See
// docs/performance-audit.md → P0-5.
func (h *Handlers) catalogServiceNames(ctx context.Context) ([]string, error) {
	rows, err := h.Catalog.AllServices(ctx, middleware.OrgIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ServiceName)
	}
	return out, nil
}

// servicesForIntegration filters candidate service names to those that
// resolve to the given integration name.
func (h *Handlers) servicesForIntegration(ctx context.Context, name string, candidates []string) []string {
	var out []string
	for _, svc := range candidates {
		ints, err := h.Resolver.IntegrationsFor(ctx, middleware.OrgIDFromContext(ctx), svc)
		if err != nil {
			continue
		}
		for _, in := range ints {
			if in.Name == name {
				out = append(out, svc)
				break
			}
		}
	}
	return out
}

// servicesWithoutIntegration filters candidate service names to those
// that resolve to no integration — the members of the "(no integration)"
// group in integration-grouped rollups. Mirrors rollupLogsByIntegration's
// fallback bucket so expanding that group lists exactly its logs.
func (h *Handlers) servicesWithoutIntegration(ctx context.Context, candidates []string) []string {
	var out []string
	for _, svc := range candidates {
		ints, err := h.Resolver.IntegrationsFor(ctx, middleware.OrgIDFromContext(ctx), svc)
		if err != nil {
			continue
		}
		if len(ints) == 0 {
			out = append(out, svc)
		}
	}
	return out
}

// metricGroups: GET /api/v1/metric-groups?by=service|integration|type|attribute[&key=]
func (h *Handlers) metricGroups(w http.ResponseWriter, r *http.Request) {
	by, key, ok, msg := parseGroupBy(r, map[string]bool{"service": true, "integration": true, "type": true, "attribute": true})
	if !ok {
		httpserver.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	tr := ParseRange(r, time.Hour)
	attrs, err := parseAttrFilters(r)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// G5: enforce policy-based service visibility for metric group counts.
	pf := h.resolveServiceFilter(r, strings.TrimSpace(r.URL.Query().Get("service")), nil)
	if pf.Blocked {
		httpserver.WriteError(w, http.StatusNotFound, "service not found")
		return
	}
	if pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, MetricGroupsResponse{Window: tr.Window(), By: by, Groups: []MetricGroup{}})
		return
	}
	params := store.MetricCatalogParams{
		Service:    pf.Service,
		ServiceIn:  pf.ServiceIn,
		NameQuery:  strings.TrimSpace(r.URL.Query().Get("q")),
		MetricType: normalizeMetricType(r.URL.Query().Get("type")),
		From:       tr.From,
		To:         tr.To,
		Attrs:      attrs,
	}

	// Integration grouping rolls up the per-service rollup in Go.
	storeBy := by
	if by == "integration" {
		storeBy = "service"
	}
	rows, err := h.Store.MetricGroups(r.Context(), params, storeBy, key)
	if err != nil {
		h.Logger.Error("metric groups failed", "err", err, "by", by)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	var groups []MetricGroup
	if by == "integration" {
		groups = rollupMetricsByIntegration(r.Context(), h, rows)
	} else {
		groups = make([]MetricGroup, 0, len(rows))
		for _, g := range rows {
			groups = append(groups, MetricGroup{Key: g.Key, MetricCount: g.MetricCount, SeriesCount: g.SeriesCount, PointCount: g.PointCount})
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, MetricGroupsResponse{Window: tr.Window(), By: by, Groups: groups})
}

func rollupMetricsByIntegration(ctx context.Context, h *Handlers, rows []store.MetricGroupRow) []MetricGroup {
	agg := map[string]*MetricGroup{}
	add := func(name string, g store.MetricGroupRow) {
		cur := agg[name]
		if cur == nil {
			cur = &MetricGroup{Key: name}
			agg[name] = cur
		}
		cur.MetricCount += g.MetricCount
		cur.SeriesCount += g.SeriesCount
		cur.PointCount += g.PointCount
	}
	for _, g := range rows {
		ints, err := h.Resolver.IntegrationsFor(ctx, middleware.OrgIDFromContext(ctx), g.Key)
		if err != nil || len(ints) == 0 {
			add(noIntegrationLabel, g)
			continue
		}
		for _, in := range ints {
			add(in.Name, g)
		}
	}
	return sortedMetricGroups(agg)
}

func sortedMetricGroups(agg map[string]*MetricGroup) []MetricGroup {
	out := make([]MetricGroup, 0, len(agg))
	for _, g := range agg {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MetricCount != out[j].MetricCount {
			return out[i].MetricCount > out[j].MetricCount
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// logGroups: GET /api/v1/logs/groups?by=service|integration|severity|attribute[&key=]
func (h *Handlers) logGroups(w http.ResponseWriter, r *http.Request) {
	by, key, ok, msg := parseGroupBy(r, map[string]bool{"service": true, "integration": true, "severity": true, "attribute": true})
	if !ok {
		httpserver.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	tr := ParseRange(r, time.Hour)
	attrs, err := parseAttrFilters(r)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	rawService := strings.TrimSpace(r.URL.Query().Get("service"))
	// `integration=<name>` narrows group counts to one integration's
	// services. Catalog-sourced candidates (P0-5).
	serviceIn, integGroups, _ := h.integrationFilter(r.Context(), r, func() ([]string, error) {
		return h.catalogServiceNames(r.Context())
	})
	// G5: enforce policy-based service visibility for log group counts.
	pf := h.resolveServiceFilter(r, rawService, serviceIn)
	if pf.Blocked {
		httpserver.WriteError(w, http.StatusNotFound, "service not found")
		return
	}
	if pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, LogGroupsResponse{By: by, Groups: []LogGroup{}})
		return
	}
	params := store.LogQueryParams{
		Service:      pf.Service,
		ServiceIn:    pf.ServiceIn,
		From:         tr.From,
		To:           tr.To,
		MinSeverity:  parseMinSeverity(r),
		BodyContains: strings.TrimSpace(r.URL.Query().Get("q")),
		Attrs:        attrs,
		AttrGroups:   integGroups,
	}

	storeBy := by
	if by == "integration" {
		storeBy = "service"
	}
	rows, err := h.Store.LogGroups(r.Context(), params, storeBy, key)
	if err != nil {
		h.Logger.Error("log groups failed", "err", err, "by", by)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	var groups []LogGroup
	if by == "integration" {
		groups = rollupLogsByIntegration(r.Context(), h, rows)
	} else {
		groups = make([]LogGroup, 0, len(rows))
		for _, g := range rows {
			groups = append(groups, LogGroup{Key: g.Key, Count: g.Count, ErrorCount: g.ErrorCount})
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, LogGroupsResponse{Window: tr.Window(), By: by, Groups: groups})
}

func rollupLogsByIntegration(ctx context.Context, h *Handlers, rows []store.LogGroupRow) []LogGroup {
	agg := map[string]*LogGroup{}
	add := func(name string, g store.LogGroupRow) {
		cur := agg[name]
		if cur == nil {
			cur = &LogGroup{Key: name}
			agg[name] = cur
		}
		cur.Count += g.Count
		cur.ErrorCount += g.ErrorCount
	}
	for _, g := range rows {
		ints, err := h.Resolver.IntegrationsFor(ctx, middleware.OrgIDFromContext(ctx), g.Key)
		if err != nil || len(ints) == 0 {
			add(noIntegrationLabel, g)
			continue
		}
		for _, in := range ints {
			add(in.Name, g)
		}
	}
	out := make([]LogGroup, 0, len(agg))
	for _, g := range agg {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}
