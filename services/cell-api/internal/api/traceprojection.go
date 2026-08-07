// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Projecting ONE trace onto an integration's flow graph (issue #15).
//
// The flow graph answers how messages generally move through an
// integration. This answers where one specific message got to — the
// question asked while an incident is open, and the one Sluicio could
// not answer before.
//
// The honest limit, and the reason the state set is what it is:
//
// A span reaches ClickHouse when it ENDS. There is no such thing as an
// open span in the store, so "this message is currently being processed
// by X" is not observable, and neither is "it is sitting in a queue in
// front of Y". Anything claiming otherwise would be inventing a state
// out of an absence, and the absence has several possible causes: still
// running, never sent, crashed before export, dropped by sampling.
//
// So nothing here reports waiting. What it reports instead is position:
//
//   - REACHED — the trace has at least one span on this service. A fact.
//   - FAILED — reached, and at least one of those spans has error
//     status. This is usually the answer to "where did it stop".
//   - NEXT — no span here, but an immediate upstream WAS reached. This
//     is the frontier: the place to look. It is derived from the graph's
//     own edges, not from any claim about what the message is doing.
//   - NOT REACHED — no span, and no reached upstream either. Neutral;
//     the message never got near this part of the flow.
//
// The distinction between NEXT and NOT REACHED is the whole value. It
// turns "eleven grey nodes" into "look at these two".

package api

import (
	"sort"
	"time"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// Trace-projection node states. Strings rather than an enum because
// they cross the API boundary and are read by a human debugging a
// response.
const (
	TraceNodeReached    = "reached"
	TraceNodeFailed     = "failed"
	TraceNodeNext       = "next"
	TraceNodeNotReached = "not_reached"
)

// TraceNodeState is one member service's relationship to one trace.
type TraceNodeState struct {
	ServiceName string `json:"service_name"`
	State       string `json:"state"`
	// SpanCount is how many of the trace's spans landed on this service.
	// Zero for next/not_reached.
	SpanCount int `json:"span_count"`
	// FirstSeen / LastSeen bound this service's part of the trace. Zero
	// when not reached. LastSeen is the END of the last span, which is
	// what "it was still here at" means to a reader.
	FirstSeen *time.Time `json:"first_seen,omitempty"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
	// ErrorCount is spans with error status on this service.
	ErrorCount int `json:"error_count"`
	// AfterService names the reached upstream that makes this node the
	// frontier. Only set for state=next, and it is what lets the UI say
	// "expected after order-api" rather than just colouring a box.
	AfterService string `json:"after_service,omitempty"`
}

// TraceProjection is one trace laid over an integration's members.
type TraceProjection struct {
	TraceID string           `json:"trace_id"`
	Nodes   []TraceNodeState `json:"nodes"`
	// Edges the trace actually traversed, as "source|target" keys, so
	// the UI can draw the taken path solidly and the rest faintly.
	TraversedEdges []string `json:"traversed_edges"`
	// LastReached is the deepest service the trace is known to have
	// reached, by end time. Empty if the trace touched no member.
	LastReached string `json:"last_reached,omitempty"`
	// SpansOutsideIntegration counts spans in the trace that belong to
	// services which are not members. Reported rather than hidden: a
	// trace that mostly ran somewhere else is a sign the graph is not
	// the whole story, and silently dropping those spans would make the
	// projection look more complete than it is.
	SpansOutsideIntegration int `json:"spans_outside_integration"`
	// Started / Ended bound the whole trace across member services.
	Started *time.Time `json:"started,omitempty"`
	Ended   *time.Time `json:"ended,omitempty"`
}

// spanEnd is the span's end instant. Timestamp is the start; duration
// carries the rest. Spans are only stored once ended, so this is always
// meaningful.
func spanEnd(r store.SpanRow) time.Time {
	return r.Timestamp.Add(time.Duration(r.DurationNs))
}

// isErrorSpan reports whether a span carries error status. ClickHouse
// stores the OTel status code as a string; both the enum name and the
// numeric form appear in the wild depending on the exporter version.
func isErrorSpan(r store.SpanRow) bool {
	switch r.StatusCode {
	case "STATUS_CODE_ERROR", "Error", "ERROR", "2":
		return true
	}
	return false
}

// ProjectTraceOntoFlow maps one trace's spans onto an integration's
// member services.
//
// members is the node set of the graph; edges are its directed hops
// (aggregate, not trace-specific) and are used only to work out which
// unreached nodes are the frontier. rows are the trace's spans, already
// filtered to what the caller may see — this function does no access
// control and must never be given spans the caller cannot read.
func ProjectTraceOntoFlow(traceID string, members []string, edges []FlowEdge, rows []store.SpanRow) TraceProjection {
	memberSet := make(map[string]bool, len(members))
	for _, m := range members {
		memberSet[m] = true
	}

	type agg struct {
		count       int
		errors      int
		first, last time.Time
	}
	byService := make(map[string]*agg, len(members))
	outside := 0
	// spanID -> service, so a parent hop can be resolved to an edge.
	spanService := make(map[string]string, len(rows))

	for _, r := range rows {
		if !memberSet[r.ServiceName] {
			outside++
			continue
		}
		spanService[r.SpanID] = r.ServiceName
		a := byService[r.ServiceName]
		if a == nil {
			a = &agg{first: r.Timestamp, last: spanEnd(r)}
			byService[r.ServiceName] = a
		}
		a.count++
		if isErrorSpan(r) {
			a.errors++
		}
		if r.Timestamp.Before(a.first) {
			a.first = r.Timestamp
		}
		if e := spanEnd(r); e.After(a.last) {
			a.last = e
		}
	}

	// Edges this trace actually took: a parent/child pair whose two
	// spans sit on different member services. Derived from the trace's
	// own parent links rather than from the aggregate edge list, so a
	// hop the graph has never seen before still draws.
	traversed := map[string]bool{}
	for _, r := range rows {
		child := spanService[r.SpanID]
		if child == "" || r.ParentSpanID == "" {
			continue
		}
		parent := spanService[r.ParentSpanID]
		if parent == "" || parent == child {
			continue
		}
		traversed[parent+"|"+child] = true
	}

	// The frontier. A member with no spans is NEXT when some immediate
	// upstream WAS reached — that is the graph saying "the message
	// should have arrived here". Everything else unreached stays
	// neutral, which is what keeps a branching flow from reading as
	// eleven simultaneous failures.
	upstreamReached := make(map[string]string, len(members))
	for _, e := range edges {
		if byService[e.Target] != nil || byService[e.Source] == nil {
			continue
		}
		// Prefer the upstream that got furthest in time, so the named
		// service is the one the operator was just looking at.
		cur, ok := upstreamReached[e.Target]
		if !ok || byService[e.Source].last.After(byService[cur].last) {
			upstreamReached[e.Target] = e.Source
		}
	}

	out := TraceProjection{TraceID: traceID, SpansOutsideIntegration: outside}
	for _, m := range members {
		st := TraceNodeState{ServiceName: m, State: TraceNodeNotReached}
		if a := byService[m]; a != nil {
			st.State = TraceNodeReached
			if a.errors > 0 {
				st.State = TraceNodeFailed
			}
			st.SpanCount = a.count
			st.ErrorCount = a.errors
			first, last := a.first, a.last
			st.FirstSeen, st.LastSeen = &first, &last
		} else if up, ok := upstreamReached[m]; ok {
			st.State = TraceNodeNext
			st.AfterService = up
		}
		out.Nodes = append(out.Nodes, st)
	}

	for k := range traversed {
		out.TraversedEdges = append(out.TraversedEdges, k)
	}
	// Deterministic order: the response is compared in tests and read by
	// humans, and a map's iteration order is neither.
	sort.Strings(out.TraversedEdges)

	// Trace bounds and the deepest point reached, both by END time —
	// "how far did it get" is about where it last had activity, not
	// where a span happened to start earliest.
	for name, a := range byService {
		if out.Started == nil || a.first.Before(*out.Started) {
			f := a.first
			out.Started = &f
		}
		if out.Ended == nil || a.last.After(*out.Ended) {
			l := a.last
			out.Ended = &l
			out.LastReached = name
		} else if a.last.Equal(*out.Ended) && name < out.LastReached {
			// Ties broken by name so the answer is stable across runs.
			out.LastReached = name
		}
	}
	return out
}
