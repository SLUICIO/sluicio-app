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

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
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
	if integs, err := h.Integrations.List(r.Context(), orgID); err == nil {
		for _, i := range integs {
			members, mErr := h.Catalog.IntegrationServices(r.Context(), i.ID)
			if mErr != nil {
				continue
			}
			for _, m := range members {
				assigned[m] = true
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
	})
}
