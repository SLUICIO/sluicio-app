// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Suggested integrations, derived from the call graph (issue #10).
//
// Surfaced by the CELL itself rather than only through an agent, which
// answers the issue's first open question. The candidate generation is
// deterministic and needs no model, so a cell with no agent attached
// still gets suggestions; an agent adds the judgement on top (what the
// group is, which matcher expresses it durably, the rationale a
// reviewer reads).

package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// IntegrationCandidate is one proposed grouping.
type IntegrationCandidate struct {
	Services []string `json:"services"`
	// SharedServices are adjacent services left OUT because they take
	// part in other flows too, typically a gateway or a shared worker.
	// Named rather than dropped: including one fuses unrelated flows
	// into a useless suggestion, omitting it silently hides a real
	// participant, and the reviewer is the one who should choose.
	SharedServices []string `json:"shared_services,omitempty"`
	// Traces is the traffic observed on hops INSIDE the group, which is
	// the evidence for the grouping existing at all. A reviewer gets the
	// number rather than a bare list of names.
	Traces uint64 `json:"internal_traces"`
	// DedupKey lets a caller check whether this grouping is already
	// waiting in the inbox before filing it again.
	DedupKey string `json:"dedup_key"`
}

// IntegrationCandidatesResponse is what the cell can suggest right now.
type IntegrationCandidatesResponse struct {
	Window     WindowSummary          `json:"window"`
	Candidates []IntegrationCandidate `json:"candidates"`
	// Unassigned is how many services belong to no integration, so the
	// reader can tell "nothing to suggest" from "nothing left to assign".
	Unassigned int `json:"unassigned_services"`
	// Skipped names groupings that were too large to be useful, usually
	// a hub service chaining unrelated flows together. Reported rather
	// than silently dropped: a cap that says nothing reads as "there was
	// nothing else to find", which is the opposite of the truth.
	Skipped []IntegrationCandidate `json:"skipped_oversized,omitempty"`
	// Partitions are services that look like several flows (issue #18):
	// an attribute splits their traffic, one value per message, into a
	// handful of parts, each a candidate integration.
	Partitions []PartitionSuggestion `json:"partitions"`
}

// PartitionSuggestion is one attribute that splits one service into
// flows: one proposed integration per value, defined as
// service.name = Service AND Attribute = value.
type PartitionSuggestion struct {
	Service   string `json:"service"`
	Attribute string `json:"attribute"`
	// Coverage is the share of the service's messages carrying the
	// attribute; ValuesPerMessage is ~1 for a partition.
	Coverage         float64 `json:"coverage"`
	ValuesPerMessage float64 `json:"values_per_message"`
	// NamedAsFlow: the attribute's name says flow, route, integration,
	// pipeline or the like. Ranked first; the rest may be a property of
	// the message rather than a flow, which is the reviewer's call.
	NamedAsFlow bool `json:"named_as_flow"`
	// InIntegrations names the integrations the service already belongs
	// to. A shared runtime usually sits in one catch-all integration,
	// which is the case this exists for; nothing changes until someone
	// acts on the suggestion.
	InIntegrations []string                   `json:"in_integrations,omitempty"`
	Values         []PartitionValueSuggestion `json:"values"`
}

// PartitionValueSuggestion is one value: one proposed integration.
type PartitionValueSuggestion struct {
	Value     string    `json:"value"`
	Messages  uint64    `json:"messages"`
	Failed    uint64    `json:"failed"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// Integration is set when an integration already selects exactly
	// this value on this service.
	Integration string `json:"integration,omitempty"`
}

// integrationCandidates: GET /api/v1/integration-candidates?range=...
func (h *Handlers) integrationCandidates(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, 7*24*time.Hour)
	orgID := middleware.OrgID(r)

	all, err := h.Store.ListServices(r.Context(), tr.From, tr.To)
	if err != nil {
		h.Logger.Error("list services for candidates failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// Everything already claimed by an integration, so candidates are
	// drawn only from what nobody has grouped yet.
	assigned := map[string]bool{}
	integrationsOf := map[string][]string{}
	integs, err := h.Integrations.List(r.Context(), orgID)
	if err == nil {
		for _, i := range integs {
			members, mErr := h.Catalog.IntegrationServices(r.Context(), i.ID)
			if mErr != nil {
				continue
			}
			for _, m := range members {
				assigned[m] = true
				integrationsOf[m] = append(integrationsOf[m], i.Name)
			}
		}
	} else {
		h.Logger.Warn("list integrations for candidates failed", "err", err)
	}

	unassigned := []string{}
	// How many traces each service takes part in. This is the
	// DENOMINATOR the overlap test needs: an edge's count is already the
	// intersection, so comparing it against a service's own total says
	// whether the two do their work together or one of them simply calls
	// everybody.
	serviceTraces := map[string]uint64{}
	for _, s := range all {
		serviceTraces[s.ServiceName] = s.TraceCount
		if !assigned[s.ServiceName] {
			unassigned = append(unassigned, s.ServiceName)
		}
	}
	// Visibility: a caller must not learn about services their policy
	// hides, even as a suggestion.
	if visible, any := h.filterVisibleMembers(r, unassigned); any {
		unassigned = visible
	} else {
		unassigned = nil
	}

	edgeRows, err := h.Store.ServiceEdges(r.Context(), unassigned, tr.From, tr.To, nil)
	if err != nil {
		h.Logger.Error("service edges for candidates failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Hand-off edges count towards a grouping too (issue #25). Two
	// services joined only by a queue are as much one integration as
	// two joined by a call — arguably more so, since an asynchronous
	// hand-off is usually a deliberate boundary in a business flow
	// rather than an implementation detail.
	if linkRows, e := h.Store.ServiceLinkEdges(r.Context(), unassigned, tr.From, tr.To); e != nil {
		h.Logger.Warn("service link edges for candidates failed", "err", e)
	} else {
		edgeRows = append(edgeRows, linkRows...)
	}
	edges := make([]proposals.Edge, 0, len(edgeRows))
	for _, e := range edgeRows {
		edges = append(edges, proposals.Edge{Source: e.Source, Target: e.Target, Traces: e.TraceCount})
	}

	opt := proposals.DefaultClusterOptions()
	toAPI := func(cs []proposals.Cluster) []IntegrationCandidate {
		out := make([]IntegrationCandidate, 0, len(cs))
		for _, c := range cs {
			out = append(out, IntegrationCandidate{
				Services:       c.Services,
				SharedServices: c.SharedServices,
				Traces:         c.InternalTraces,
				DedupKey:       proposals.DedupKey(c.Services),
			})
		}
		return out
	}

	httpserver.WriteJSON(w, http.StatusOK, IntegrationCandidatesResponse{
		Window:     tr.Window(),
		Candidates: toAPI(proposals.FindClusters(unassigned, edges, serviceTraces, opt)),
		Unassigned: len(unassigned),
		Skipped:    toAPI(proposals.OversizedClusters(unassigned, edges, serviceTraces, opt)),
		Partitions: h.partitionSuggestions(r, all, integs, integrationsOf, tr),
	})
}

// partitionSuggestions finds services that are several flows (issue #18).
// Every service the caller can see is considered, assigned or not:
// a shared runtime is usually already in a catch-all integration.
func (h *Handlers) partitionSuggestions(r *http.Request, all []store.ServiceRow, integs []integrations.Integration, integrationsOf map[string][]string, tr TimeRange) []PartitionSuggestion {
	ctx := r.Context()
	popt := proposals.DefaultPartitionOptions()
	names := make([]string, 0, len(all))
	for _, s := range all {
		if s.TraceCount >= popt.MinTraces {
			names = append(names, s.ServiceName)
		}
	}
	visible, ok := h.filterVisibleMembers(r, names)
	if !ok || len(visible) == 0 {
		return []PartitionSuggestion{}
	}
	rows, err := h.Store.AttrPartitionStats(ctx, visible, tr.From, tr.To, 20000, popt.MaxValues)
	if err != nil {
		h.Logger.Warn("partition stats failed", "err", err)
		return []PartitionSuggestion{}
	}
	stats := make([]proposals.AttrPartitionStats, 0, len(rows))
	for _, r := range rows {
		stats = append(stats, proposals.AttrPartitionStats{
			Service: r.Service, Key: r.Key, TracesWithKey: r.TracesWithKey, TracesTotal: r.TracesTotal,
			DistinctValues: r.DistinctValues, TraceValuePairs: r.TraceValuePairs, NumericValues: r.NumericValues, BooleanValues: r.BooleanValues,
		})
	}
	found := proposals.FindPartitions(stats, popt, 2, 20)

	// Which values an integration already selects: service.name = S AND
	// key = v on one rule. Matchers are loaded once.
	selected := map[string]string{} // service \x00 key \x00 value -> integration
	if len(found) > 0 {
		byID := map[uuid.UUID]string{}
		for _, i := range integs {
			byID[i.ID] = i.Name
		}
		if all, err := h.Integrations.AllMatchersWithIntegration(ctx, middleware.OrgID(r)); err == nil {
			type groupKey struct {
				id    uuid.UUID
				group int
			}
			groups := map[groupKey][]integrations.Matcher{}
			for _, mi := range all {
				k := groupKey{mi.Integration.ID, mi.Matcher.MatchGroup}
				groups[k] = append(groups[k], mi.Matcher)
			}
			for k, ms := range groups {
				var svc string
				for _, m := range ms {
					if m.IsServiceMatcher() && m.Operator == integrations.OperatorEquals {
						svc = m.Value
					}
				}
				if svc == "" {
					continue
				}
				for _, m := range ms {
					if !m.IsServiceMatcher() && m.Operator == integrations.OperatorEquals {
						selected[svc+"\x00"+m.Attribute+"\x00"+m.Value] = byID[k.id]
					}
				}
			}
		}
	}

	out := make([]PartitionSuggestion, 0, len(found))
	for _, c := range found {
		vals, err := h.Store.PartitionValues(ctx, c.Service, c.Key, tr.From, tr.To, proposals.MaxPartitionValues)
		if err != nil {
			h.Logger.Warn("partition values failed", "service", c.Service, "key", c.Key, "err", err)
			continue
		}
		sug := PartitionSuggestion{
			Service: c.Service, Attribute: c.Key, Coverage: c.Presence, ValuesPerMessage: c.PerTrace, NamedAsFlow: c.NamedAsFlow,
			InIntegrations: integrationsOf[c.Service], Values: make([]PartitionValueSuggestion, 0, len(vals)),
		}
		uncovered := 0
		for _, v := range vals {
			pv := PartitionValueSuggestion{
				Value: v.Value, Messages: v.Traces, Failed: v.ErrorTraces, FirstSeen: v.FirstSeen, LastSeen: v.LastSeen,
				Integration: selected[c.Service+"\x00"+c.Key+"\x00"+v.Value],
			}
			if pv.Integration == "" {
				uncovered++
			}
			sug.Values = append(sug.Values, pv)
		}
		// Every value already has its integration: nothing to suggest.
		if uncovered == 0 {
			continue
		}
		out = append(out, sug)
	}
	return out
}
