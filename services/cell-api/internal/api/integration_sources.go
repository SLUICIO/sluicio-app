// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Metric and log sources of an integration (#43): telemetry that is
// evidence about an integration without its origin being a member. See
// integrations/sources.go for the model.
//
// A read of an integration's metrics or logs is its members' slice OR
// each source's slice. The members' groups are pinned to the member
// services once sources widen the service list, so a member's condition
// never reaches a source's rows; a source's group is its origin services
// plus its conditions (plus, for metrics, which metric names), marked as
// a source so the store ORs it beside the members' predicate and keeps
// it out of the one-trace gate.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// ResolvedSource is a source with its origin resolved to services.
type ResolvedSource struct {
	Services    []string
	Conditions  []integrations.SourceCondition
	MetricNames []string
}

// ResolveSources resolves the sources of one signal: a service origin is
// itself, a system origin its member services now. A system with no
// members contributes nothing.
func ResolveSources(ctx context.Context, sources []integrations.Source, signal string, systemMembers func(context.Context, uuid.UUID) ([]string, error)) ([]ResolvedSource, error) {
	var out []ResolvedSource
	for _, s := range sources {
		if s.Signal != signal {
			continue
		}
		var svcs []string
		switch s.OriginKind {
		case integrations.OriginService:
			svcs = []string{s.Origin}
		case integrations.OriginSystem:
			id, err := uuid.Parse(s.Origin)
			if err != nil {
				continue
			}
			if svcs, err = systemMembers(ctx, id); err != nil {
				return nil, err
			}
		}
		if len(svcs) == 0 {
			continue
		}
		out = append(out, ResolvedSource{Services: svcs, Conditions: s.Conditions, MetricNames: s.MetricNames})
	}
	return out, nil
}

// anyOf is a service.name condition naming a set of services exactly.
func anyOf(svcs []string) store.LogAttrFilter {
	q := make([]string, len(svcs))
	for i, s := range svcs {
		q[i] = regexp.QuoteMeta(s)
	}
	return store.LogAttrFilter{Key: "service.name", Op: store.AttrOpMatches, Value: "^(?:" + strings.Join(q, "|") + ")$"}
}

// globsToRegex turns metric name globs (rabbitmq.queue.*) into one
// anchored regular expression.
func globsToRegex(globs []string) string {
	parts := make([]string, len(globs))
	for i, g := range globs {
		parts[i] = strings.ReplaceAll(regexp.QuoteMeta(strings.TrimSpace(g)), `\*`, ".*")
	}
	return "^(?:" + strings.Join(parts, "|") + ")$"
}

// WithSources unions an integration's members (members, memberGroups)
// with its sources of one signal. It returns the services to read and
// the groups to apply. Without sources it returns its input unchanged.
func WithSources(members []string, memberGroups [][]store.LogAttrFilter, sources []ResolvedSource, signal string) ([]string, [][]store.LogAttrFilter) {
	if len(sources) == 0 {
		return members, memberGroups
	}
	svcs := append([]string(nil), members...)
	seen := map[string]bool{}
	for _, m := range members {
		seen[m] = true
	}
	var groups [][]store.LogAttrFilter
	if len(members) > 0 {
		pin := anyOf(members)
		if len(memberGroups) == 0 {
			groups = append(groups, []store.LogAttrFilter{pin})
		}
		for _, g := range memberGroups {
			ng := append(append([]store.LogAttrFilter(nil), g...), pin)
			if len(g) > 0 {
				ng[len(ng)-1].RequireAllGroups, ng[len(ng)-1].Member = g[0].RequireAllGroups, g[0].Member
			}
			groups = append(groups, ng)
		}
	}
	for _, s := range sources {
		for _, svc := range s.Services {
			if !seen[svc] {
				seen[svc] = true
				svcs = append(svcs, svc)
			}
		}
		g := []store.LogAttrFilter{anyOf(s.Services)}
		for _, c := range s.Conditions {
			g = append(g, sourceConditionFilter(c))
		}
		if signal == integrations.SourceMetrics && len(s.MetricNames) > 0 {
			g = append(g, store.LogAttrFilter{Key: store.MetricNameAttrKey, Op: store.AttrOpMatches, Value: globsToRegex(s.MetricNames)})
		}
		for i := range g {
			g[i].Source = true
		}
		groups = append(groups, g)
	}
	return svcs, groups
}

// sourceConditionFilter compiles one source condition: a matcher's
// operators as an integration rule compiles them, the numeric ones to
// the store's numeric comparisons.
func sourceConditionFilter(c integrations.SourceCondition) store.LogAttrFilter {
	switch c.Operator {
	case integrations.SourceOpGt:
		return store.LogAttrFilter{Key: c.Attribute, Op: store.AttrOpGt, Value: strings.TrimSpace(c.Value)}
	case integrations.SourceOpGte:
		return store.LogAttrFilter{Key: c.Attribute, Op: store.AttrOpGte, Value: strings.TrimSpace(c.Value)}
	case integrations.SourceOpLt:
		return store.LogAttrFilter{Key: c.Attribute, Op: store.AttrOpLt, Value: strings.TrimSpace(c.Value)}
	case integrations.SourceOpLte:
		return store.LogAttrFilter{Key: c.Attribute, Op: store.AttrOpLte, Value: strings.TrimSpace(c.Value)}
	}
	return attrFilterFromMatcher(integrations.Matcher{Attribute: c.Attribute, Operator: c.Operator, Value: c.Value})
}

// sourceSignal maps an access signal to a source signal, "" for one
// sources do not feed (traces, messages).
func sourceSignal(sig identity.Signal) string {
	switch sig {
	case identity.SignalMetrics:
		return integrations.SourceMetrics
	case identity.SignalLogs:
		return integrations.SourceLogs
	}
	return ""
}

// integrationSignalScope is what a read of one integration's metrics or
// logs covers: its members (as given, already narrowed to what the
// caller may list) and its conditions, plus its sources of that signal,
// in the environments the request reads. For any other signal it is the
// members and conditions alone.
func (h *Handlers) integrationSignalScope(ctx context.Context, id uuid.UUID, members []string, sig identity.Signal) ([]string, [][]store.LogAttrFilter, error) {
	matchers, err := h.Integrations.MatchersForIntegration(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	mode, err := h.Integrations.RuleMatchForIntegration(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	groups := AttrGroupsFromMatchers(matchers, mode)
	svcs := members
	if signal := sourceSignal(sig); signal != "" {
		sources, err := h.Integrations.SourcesForIntegration(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		resolved, err := ResolveSources(ctx, sources, signal, func(ctx context.Context, sys uuid.UUID) ([]string, error) {
			return h.Catalog.SystemMemberNames(ctx, middleware.OrgIDFromContext(ctx), sys)
		})
		if err != nil {
			return nil, nil, err
		}
		svcs, groups = WithSources(members, groups, resolved, signal)
	}
	return svcs, withRequestEnvironment(ctx, groups), nil
}

// SourceView is a source as the Services tab lists it: a system origin
// carries the system's name and its member services now.
type SourceView struct {
	integrations.Source
	OriginName     string   `json:"origin_name"`
	OriginServices []string `json:"origin_services"`
}

// listIntegrationSources: GET /api/v1/integrations/{id}/sources
func (h *Handlers) listIntegrationSources(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	if !h.canSeeIntegration(r, id) {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return
	}
	sources, err := h.Integrations.SourcesForIntegration(r.Context(), id)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"sources": h.sourceViews(r.Context(), sources)})
}

func (h *Handlers) sourceViews(ctx context.Context, sources []integrations.Source) []SourceView {
	orgID := middleware.OrgIDFromContext(ctx)
	out := make([]SourceView, 0, len(sources))
	for _, s := range sources {
		v := SourceView{Source: s, OriginName: s.Origin, OriginServices: []string{s.Origin}}
		if s.OriginKind == integrations.OriginSystem {
			v.OriginServices = []string{}
			if id, err := uuid.Parse(s.Origin); err == nil {
				if name, ok := h.systemName(ctx, orgID, id); ok {
					v.OriginName = name
				}
				if svcs, err := h.Catalog.SystemMemberNames(ctx, orgID, id); err == nil && svcs != nil {
					sort.Strings(svcs)
					v.OriginServices = svcs
				}
			}
		}
		out = append(out, v)
	}
	return out
}

// putIntegrationSources: PUT /api/v1/integrations/{id}/sources replaces
// the integration's sources.
func (h *Handlers) putIntegrationSources(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	var body struct {
		Sources []integrations.Source `json:"sources"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	orgID := middleware.OrgID(r)
	for _, s := range body.Sources {
		if s.OriginKind != integrations.OriginSystem {
			continue
		}
		sys, err := uuid.Parse(s.Origin)
		if err != nil {
			continue // Validate says so
		}
		if _, ok := h.systemName(r.Context(), orgID, sys); !ok {
			httpserver.WriteError(w, http.StatusBadRequest, "a source names a system that does not exist")
			return
		}
	}
	if err := h.Integrations.ReplaceSources(r.Context(), orgID, id, body.Sources); err != nil {
		switch {
		case errors.Is(err, integrations.ErrNotFound):
			httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		case integrations.IsValidationError(err):
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		default:
			h.Logger.Error("replace integration sources failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "could not save sources")
		}
		return
	}
	sources, err := h.Integrations.SourcesForIntegration(r.Context(), id)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	h.recordAudit(r, "integration.sources_updated", "integration", id.String(), map[string]any{"sources": len(sources)})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"sources": h.sourceViews(r.Context(), sources)})
}

// systemName returns a system's name, ok false when the org has no such
// system.
func (h *Handlers) systemName(ctx context.Context, orgID, id uuid.UUID) (string, bool) {
	sy, ok, err := h.Catalog.GetSystem(ctx, orgID, id)
	if err != nil || !ok {
		return "", false
	}
	return sy.Name, true
}
