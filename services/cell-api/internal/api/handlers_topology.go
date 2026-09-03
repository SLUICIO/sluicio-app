// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The org-wide topology graph at two granularities (?view=):
//   - services (default): every visible service as a node, caller→callee hops
//     as edges.
//   - integrations: those services + hops rolled up to the integration level —
//     integration nodes, with an edge A→B when a service in A calls a service
//     in B.
// Both are window-scoped with a historical fallback so the structure renders
// when quiet, visibility-filtered, and reuse the FlowResponse shape + the
// frontend IntegrationFlow renderer. (The metadata perspective is its own
// endpoint, /api/v1/metadata-graph.)

package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// topologyGraph: GET /api/v1/topology?range=&view=services|integrations
func (h *Handlers) topologyGraph(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)
	orgID := middleware.OrgID(r)

	catalogRows, err := h.Catalog.AllServices(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("topology: list catalog services failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	canSee, _, err := h.visibleServiceChecker(r)
	if err != nil {
		h.Logger.Error("topology: visibility resolve failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	windowed, err := h.Store.ListServices(r.Context(), tr.From, tr.To)
	if err != nil {
		h.Logger.Warn("topology: window service counts failed", "err", err)
		windowed = nil
	}
	byName := make(map[string]store.ServiceRow, len(windowed))
	for _, s := range windowed {
		byName[s.ServiceName] = s
	}
	firing, fErr := h.Alerts.FiringHealthServices(r.Context(), orgID)
	if fErr != nil {
		h.Logger.Warn("topology: firing health services failed", "err", fErr)
		firing = map[string]bool{}
	}

	// Visible service set + per-service health.
	names := make([]string, 0, len(catalogRows))
	statusOf := make(map[string]string, len(catalogRows))
	for _, c := range catalogRows {
		if !canSee(c.ServiceName) {
			continue
		}
		names = append(names, c.ServiceName)
		statusOf[c.ServiceName] = computeServiceStatus(0, firing[c.ServiceName])
	}

	// Edges in the window; historical fallback (counts zeroed) when quiet.
	edgeRows, err := h.Store.ServiceEdges(r.Context(), names, tr.From, tr.To, nil)
	if err != nil {
		h.Logger.Error("topology: service edges failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Hand-off edges alongside the call edges (issue #25). A service
	// reached only by a span link is a real dependency, and a topology
	// that omits it draws a disconnected island where there is a flow.
	// Degraded, not fatal: call edges are worth drawing on their own.
	if linkRows, e := h.Store.ServiceLinkEdges(r.Context(), names, tr.From, tr.To); e != nil {
		h.Logger.Warn("topology: service link edges failed", "err", e)
	} else {
		edgeRows = append(edgeRows, linkRows...)
	}
	historical := false
	// Whether a structural shape COULD be drawn from a wide window,
	// which costs nothing to know.
	historicalAvailable := len(edgeRows) == 0 && len(names) > 1

	// Opt-in, as on an integration's flow graph and for the same reason:
	// this is a self-join across ninety days of the traces table, and the
	// condition that reaches it - a quiet window - is ordinary rather
	// than exceptional. It is wider here than on an integration, because
	// the topology spans every service the reader can see rather than
	// one integration's members.
	if historicalAvailable && r.URL.Query().Get("historical") == "1" {
		histFrom := time.Now().UTC().Add(-90 * 24 * time.Hour)
		histTo := time.Now().UTC()
		if he, e := h.Store.ServiceEdges(r.Context(), names, histFrom, histTo, nil); e == nil && len(he) > 0 {
			edgeRows = he
			historical = true
		}
		if hl, e := h.Store.ServiceLinkEdges(r.Context(), names, histFrom, histTo); e == nil && len(hl) > 0 {
			edgeRows = append(edgeRows, hl...)
			historical = true
		}
	}

	if r.URL.Query().Get("view") == "integrations" {
		h.writeIntegrationTopology(w, r, orgID, tr, names, byName, statusOf, edgeRows, historical, historicalAvailable)
		return
	}

	// Services view (default).
	nodes := make([]FlowNode, 0, len(names))
	for _, n := range names {
		row := byName[n]
		nodes = append(nodes, FlowNode{ServiceName: n, TraceCount: row.TraceCount, ErrorTraceCount: row.ErrorTraceCount, Status: statusOf[n]})
	}
	edges := make([]FlowEdge, 0, len(edgeRows))
	for _, e := range edgeRows {
		cc, ec := e.TraceCount, e.ErrorCount
		if historical {
			cc, ec = 0, 0
		}
		edges = append(edges, FlowEdge{Source: e.Source, Target: e.Target, CallCount: cc, ErrorCount: ec, Kind: e.Kind})
	}
	httpserver.WriteJSON(w, http.StatusOK, FlowResponse{
		Window: tr.Window(), Nodes: nodes, Edges: edges,
		Historical: historical, HistoricalAvailable: historicalAvailable,
	})
}

// writeIntegrationTopology rolls the service graph up to the integration level:
// nodes are integrations (traffic = sum of member traffic, status = aggregate
// of member health), and an edge A→B exists when a service in A calls a service
// in B (counts summed over the underlying service hops; self-edges dropped).
func (h *Handlers) writeIntegrationTopology(w http.ResponseWriter, r *http.Request, orgID uuid.UUID, tr TimeRange, names []string, byName map[string]store.ServiceRow, statusOf map[string]string, edgeRows []store.ServiceEdgeRow, historical, historicalAvailable bool) {
	members, err := h.Catalog.IntegrationServicesBulk(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("topology(int): membership failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	integ, err := h.Integrations.List(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("topology(int): list integrations failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	nameByID := make(map[uuid.UUID]string, len(integ))
	for _, ig := range integ {
		nameByID[ig.ID] = ig.Name
	}

	visible := make(map[string]bool, len(names))
	for _, n := range names {
		visible[n] = true
	}

	type agg struct {
		traces, errs uint64
		statuses     []string
	}
	svcToInts := map[string][]string{}
	intAgg := map[string]*agg{}
	for intID, svcs := range members {
		iname := nameByID[intID]
		if iname == "" {
			continue
		}
		for _, s := range svcs {
			if !visible[s] {
				continue
			}
			svcToInts[s] = append(svcToInts[s], iname)
			a := intAgg[iname]
			if a == nil {
				a = &agg{}
				intAgg[iname] = a
			}
			row := byName[s]
			a.traces += row.TraceCount
			a.errs += row.ErrorTraceCount
			a.statuses = append(a.statuses, statusOf[s])
		}
	}

	nodes := make([]FlowNode, 0, len(intAgg))
	for iname, a := range intAgg {
		nodes = append(nodes, FlowNode{ServiceName: iname, TraceCount: a.traces, ErrorTraceCount: a.errs, Status: aggregateStatus(a.statuses)})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ServiceName < nodes[j].ServiceName })

	type ekey struct{ s, t string }
	edgeAgg := map[ekey]*[2]uint64{}
	allLinks := map[ekey]bool{}
	for _, e := range edgeRows {
		for _, a := range svcToInts[e.Source] {
			for _, b := range svcToInts[e.Target] {
				if a == b {
					continue
				}
				k := ekey{a, b}
				v := edgeAgg[k]
				if v == nil {
					v = &[2]uint64{}
					edgeAgg[k] = v
					// Assume link until a call hop proves otherwise.
					allLinks[k] = true
				}
				v[0] += e.TraceCount
				v[1] += e.ErrorCount
				if e.Kind != store.EdgeKindLink {
					allLinks[k] = false
				}
			}
		}
	}
	edges := make([]FlowEdge, 0, len(edgeAgg))
	for k, v := range edgeAgg {
		cc, ec := v[0], v[1]
		if historical {
			cc, ec = 0, 0
		}
		// An integration edge is a hand-off only when EVERY underlying
		// hop was one. Mixed reads as a call, because a pair connected
		// by both a call and a queue is not purely asynchronous and
		// drawing it as such would understate the coupling.
		kind := ""
		if allLinks[k] {
			kind = store.EdgeKindLink
		}
		edges = append(edges, FlowEdge{Source: k.s, Target: k.t, CallCount: cc, ErrorCount: ec, Kind: kind})
	}

	httpserver.WriteJSON(w, http.StatusOK, FlowResponse{
		Window: tr.Window(), Nodes: nodes, Edges: edges,
		Historical: historical, HistoricalAvailable: historicalAvailable,
	})
}
