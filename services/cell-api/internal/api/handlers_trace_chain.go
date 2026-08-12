// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The whole hand-off chain around one message — "Include all" (#24).
//
// A chain of messages, NOT a merged trace. Each hop stays its own
// message with its own summary, because one message is one trace and
// the counting, SLAs and health all follow from that. Drawing four
// traces as one continuous picture would make a chain look like a
// single long message, which is exactly the reading the model forbids.
//
// The walk is breadth-first from the origin, in both directions at
// once: one step back is a read of your own spans, one step forward is
// a search for whoever points at you.

package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// Bounds on the walk. Both exist to stop one query answering a question
// nobody asked, and both are reported when they bite — a chain that
// stopped because it ran out of budget must not look like a chain that
// ended.
const (
	// maxChainDepth caps hops from the origin in either direction. A
	// retry loop that ran forty times is a real shape, and forty
	// messages is not a picture.
	maxChainDepth = 8
	// maxChainNodes caps the whole result. Depth alone does not bound
	// it: one message fanning out to two hundred is depth 1.
	maxChainNodes = 40
)

// ChainEdge is one hand-off, always oriented along the FLOW: From ran
// first and handed to Into. Not along the link pointer, which runs the
// other way — see linkedTraces for why that distinction keeps biting.
type ChainEdge struct {
	From string `json:"from"`
	Into string `json:"into"`
}

// ChainNode is one message in the chain, with its distance from the
// origin so the UI can lay the chain out without re-deriving it.
type ChainNode struct {
	LinkedTrace
	// Depth is hops from the origin; 0 is the origin itself. Unsigned
	// on purpose: direction is carried by the edges, not by a sign, so
	// a message reachable both ways cannot end up with two depths.
	Depth int `json:"depth"`
}

// TraceChainResponse is the whole reachable chain around one message.
type TraceChainResponse struct {
	Origin string      `json:"origin"`
	Nodes  []ChainNode `json:"nodes"`
	Edges  []ChainEdge `json:"edges"`
	// Hidden counts messages withheld by the caller's policy. Reported
	// for the same reason as everywhere else in this feature: a chain
	// that is quietly short looks complete.
	Hidden int `json:"hidden,omitempty"`
	// TruncatedDepth / TruncatedNodes say WHICH bound stopped the walk,
	// because the two mean different things to whoever is reading: one
	// says "the chain is longer", the other says "the chain is wider".
	TruncatedDepth bool `json:"truncated_depth,omitempty"`
	TruncatedNodes bool `json:"truncated_nodes,omitempty"`
}

// traceChain: GET /api/v1/traces/{traceId}/chain
func (h *Handlers) traceChain(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(strings.TrimSpace(r.PathValue("traceId")))
	if origin == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "trace id is required")
		return
	}

	depthSeen := map[string]int{origin: 0}
	edgeSet := map[ChainEdge]struct{}{}
	frontier := []string{origin}
	out := TraceChainResponse{Origin: origin}

	for d := 1; d <= maxChainDepth; d++ {
		if len(frontier) == 0 {
			break
		}
		var next []string
		note := func(id string) {
			// Cycle protection is this map, not a separate check: a
			// message already seen is not walked again, so A → B → A
			// terminates. Legal shape, and the reason "follow the whole
			// chain" cannot be a naive recursion.
			if _, ok := depthSeen[id]; ok || id == "" {
				return
			}
			if len(depthSeen) >= maxChainNodes {
				out.TruncatedNodes = true
				return
			}
			depthSeen[id] = d
			next = append(next, id)
		}

		// One step BACK: the frontier's own links are its predecessors.
		if back, err := h.Store.LinksFrom(r.Context(), frontier); err == nil {
			for trace, preds := range back {
				for _, p := range preds {
					edgeSet[ChainEdge{From: p, Into: trace}] = struct{}{}
					note(p)
				}
			}
		} else {
			h.Logger.Warn("trace chain: links from failed", "err", err)
		}

		// One step FORWARD: who points at the frontier. Unbounded in
		// time — the frontier holds traces with different starts, and
		// the cheapest correct answer is not to bound at all.
		if fwd, err := h.Store.TracesLinkingTo(r.Context(), frontier, time.Time{}); err == nil {
			for _, in := range fwd {
				edgeSet[ChainEdge{From: in.LinksTo, Into: in.TraceID}] = struct{}{}
				note(in.TraceID)
			}
		} else {
			h.Logger.Warn("trace chain: traces linking to failed", "err", err)
		}

		if len(next) > 0 && d == maxChainDepth {
			// There was more to walk and the depth bound stopped it.
			out.TruncatedDepth = true
		}
		frontier = next
	}

	ids := make([]string, 0, len(depthSeen))
	for id := range depthSeen {
		ids = append(ids, id)
	}
	heads, err := h.Store.TraceHeads(r.Context(), ids)
	if err != nil {
		h.Logger.Error("trace chain: trace heads failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	allowed, hasFilter := h.signalServiceFilter(r, identity.SignalTraces)
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, n := range allowed {
		allowedSet[n] = struct{}{}
	}

	visible := map[string]bool{}
	for _, hd := range heads {
		if hasFilter && !anyVisible(hd.Services, allowedSet) {
			out.Hidden++
			continue
		}
		visible[hd.TraceID] = true
		out.Nodes = append(out.Nodes, ChainNode{
			LinkedTrace: LinkedTrace{
				TraceID: hd.TraceID, ServiceName: hd.ServiceName, SpanName: hd.SpanName,
				StartedAt: hd.StartedAt, SpanCount: hd.SpanCount, HasError: hd.HasError,
			},
			Depth: depthSeen[hd.TraceID],
		})
	}
	// The origin itself can be invisible — a trace id from a log line,
	// belonging to services the caller cannot read. Then there is no
	// chain to show and no hint of one.
	if !visible[origin] {
		httpserver.WriteError(w, http.StatusNotFound, "no spans found for this trace")
		return
	}
	// Only edges whose BOTH ends survived. An edge to a withheld node
	// would draw a line to nothing and re-leak what the filter removed.
	for e := range edgeSet {
		if visible[e.From] && visible[e.Into] {
			out.Edges = append(out.Edges, e)
		}
	}
	if out.Nodes == nil {
		out.Nodes = []ChainNode{}
	}
	if out.Edges == nil {
		out.Edges = []ChainEdge{}
	}

	httpserver.WriteJSON(w, http.StatusOK, out)
}
