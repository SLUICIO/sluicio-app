// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// serviceNeighbors: GET /api/v1/services/{name}/neighbors?range=1h
//
// Returns the focal service's direct callers (upstream) and callees
// (downstream) over the requested window, both sorted by trace count
// descending. Used by the integration-builder UI to suggest dependent
// services when a user pins an "equals" matcher to a known service:
// the trace graph already knows who else is involved in that service's
// flows, so we can offer those as one-click additions to the
// integration's matcher list.
//
// Counts are at trace granularity to match the rest of the flow API
// (FlowEdge, ServiceEdges). The endpoint deliberately does not consult
// any integration's matcher list; it returns the raw neighborhood and
// leaves "is this service already covered" filtering to the caller,
// because the new-integration page doesn't yet have an integration to
// filter against.
func (h *Handlers) serviceNeighbors(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	tr := ParseRange(r, time.Hour)

	rows, err := h.Store.ServiceNeighbors(r.Context(), name, tr.From, tr.To)
	if err != nil {
		h.Logger.Error("service neighbors failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	upstream, downstream := groupNeighborRows(rows)
	// RBAC: neighbors reveal OTHER services' names + traffic. The route
	// gate covers the focal service; the adjacency list must additionally
	// be trimmed to the caller's visible set — invisible services read as
	// nonexistent here like everywhere else, so a scoped viewer can't map
	// the org's topology from a service they do see.
	if allowed, hasFilter := h.visibleServiceFilter(r); hasFilter {
		allowedSet := make(map[string]struct{}, len(allowed))
		for _, n := range allowed {
			allowedSet[n] = struct{}{}
		}
		upstream = filterVisibleNeighbors(upstream, allowedSet)
		downstream = filterVisibleNeighbors(downstream, allowedSet)
	}
	httpserver.WriteJSON(w, http.StatusOK, NeighborsResponse{
		ServiceName: name,
		Window:      tr.Window(),
		Upstream:    upstream,
		Downstream:  downstream,
	})
}

// groupNeighborRows splits store rows into upstream and downstream
// slices and deduplicates by service name within each direction.
// Duplicates shouldn't happen given the GROUP BY in the SQL, but a
// pathological trace where the same service appears as both parent
// and child of the focal service in different traces will produce
// one row per direction — that's correct and intentional. Same
// service appearing twice in the *same* direction is collapsed into
// one entry with summed counts as a defensive measure.
//
// Pulled out of the handler so it's pure and unit-testable without a
// ClickHouse harness.
func groupNeighborRows(rows []store.ServiceNeighborRow) (upstream, downstream []ServiceNeighbor) {
	upIndex := map[string]int{}
	downIndex := map[string]int{}
	for _, r := range rows {
		switch r.Direction {
		case "upstream":
			if idx, ok := upIndex[r.ServiceName]; ok {
				upstream[idx].TraceCount += r.TraceCount
				upstream[idx].ErrorCount += r.ErrorCount
				continue
			}
			upIndex[r.ServiceName] = len(upstream)
			upstream = append(upstream, ServiceNeighbor{
				ServiceName: r.ServiceName,
				TraceCount:  r.TraceCount,
				ErrorCount:  r.ErrorCount,
			})
		case "downstream":
			if idx, ok := downIndex[r.ServiceName]; ok {
				downstream[idx].TraceCount += r.TraceCount
				downstream[idx].ErrorCount += r.ErrorCount
				continue
			}
			downIndex[r.ServiceName] = len(downstream)
			downstream = append(downstream, ServiceNeighbor{
				ServiceName: r.ServiceName,
				TraceCount:  r.TraceCount,
				ErrorCount:  r.ErrorCount,
			})
		}
		// Unknown directions are silently dropped — the query controls
		// the vocabulary, so this branch is unreachable in practice.
	}
	// Ensure non-nil slices in the response — the frontend always
	// iterates the arrays and the API contract is "always an array".
	if upstream == nil {
		upstream = []ServiceNeighbor{}
	}
	if downstream == nil {
		downstream = []ServiceNeighbor{}
	}
	return upstream, downstream
}

// filterVisibleNeighbors keeps only neighbors in the caller's visible
// service set. Pure for unit tests.
func filterVisibleNeighbors(in []ServiceNeighbor, visible map[string]struct{}) []ServiceNeighbor {
	out := make([]ServiceNeighbor, 0, len(in))
	for _, n := range in {
		if _, ok := visible[n.ServiceName]; ok {
			out = append(out, n)
		}
	}
	return out
}
