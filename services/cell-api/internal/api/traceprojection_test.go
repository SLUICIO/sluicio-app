// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The projection's value is entirely in what it refuses to claim, so
// most of these tests are about the states it must NOT assign.

package api

import (
	"testing"
	"time"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

var base = time.Date(2026, 8, 7, 14, 0, 0, 0, time.UTC)

func span(svc, id, parent string, offsetSec int, durMs int, status string) store.SpanRow {
	return store.SpanRow{
		TraceID:      "t1",
		SpanID:       id,
		ParentSpanID: parent,
		ServiceName:  svc,
		Timestamp:    base.Add(time.Duration(offsetSec) * time.Second),
		DurationNs:   uint64(durMs) * uint64(time.Millisecond),
		StatusCode:   status,
	}
}

// A linear flow: gateway -> order-api -> warehouse-sync -> ledger
var members = []string{"gateway", "order-api", "warehouse-sync", "ledger"}
var edges = []FlowEdge{
	{Source: "gateway", Target: "order-api"},
	{Source: "order-api", Target: "warehouse-sync"},
	{Source: "warehouse-sync", Target: "ledger"},
}

func stateOf(p TraceProjection, svc string) TraceNodeState {
	for _, n := range p.Nodes {
		if n.ServiceName == svc {
			return n
		}
	}
	return TraceNodeState{}
}

func TestStuckMessageNamesTheFrontier(t *testing.T) {
	// The headline case from the issue: the message got to order-api and
	// stopped. The operator needs "look at warehouse-sync", not four
	// grey boxes.
	rows := []store.SpanRow{
		span("gateway", "a", "", 0, 20, "STATUS_CODE_OK"),
		span("order-api", "b", "a", 1, 50, "STATUS_CODE_OK"),
	}
	p := ProjectTraceOntoFlow("t1", members, edges, rows)

	if got := stateOf(p, "order-api").State; got != TraceNodeReached {
		t.Errorf("order-api: got %q, want reached", got)
	}
	next := stateOf(p, "warehouse-sync")
	if next.State != TraceNodeNext {
		t.Errorf("warehouse-sync is the frontier: got %q", next.State)
	}
	if next.AfterService != "order-api" {
		t.Errorf("the frontier must name what it follows: got %q", next.AfterService)
	}

	// ledger is two hops away and must stay neutral. Painting every
	// unreached node as "expected" is what makes a branching flow
	// unreadable.
	if got := stateOf(p, "ledger").State; got != TraceNodeNotReached {
		t.Errorf("ledger is not the frontier: got %q", got)
	}

	if p.LastReached != "order-api" {
		t.Errorf("last reached: got %q, want order-api", p.LastReached)
	}
}

func TestNothingIsEverReportedAsWaiting(t *testing.T) {
	// Guards the design decision. A node with no span has no observable
	// state beyond position: spans only exist once they have ENDED, so
	// "currently processing" and "queued" are both unobservable. If a
	// future state string implies duration or activity, this should be
	// reconsidered deliberately rather than by accident.
	rows := []store.SpanRow{span("gateway", "a", "", 0, 20, "STATUS_CODE_OK")}
	p := ProjectTraceOntoFlow("t1", members, edges, rows)
	for _, n := range p.Nodes {
		switch n.State {
		case TraceNodeReached, TraceNodeFailed, TraceNodeNext, TraceNodeNotReached:
		default:
			t.Fatalf("%s has unknown state %q", n.ServiceName, n.State)
		}
		if n.State == TraceNodeNext || n.State == TraceNodeNotReached {
			if n.SpanCount != 0 || n.FirstSeen != nil || n.LastSeen != nil {
				t.Errorf("%s is unreached but carries timing", n.ServiceName)
			}
		}
	}
}

func TestFailedBeatsReached(t *testing.T) {
	// Where it stopped is usually where it errored, so an error span
	// must not be averaged away by successful siblings on the same node.
	rows := []store.SpanRow{
		span("gateway", "a", "", 0, 20, "STATUS_CODE_OK"),
		span("order-api", "b", "a", 1, 50, "STATUS_CODE_OK"),
		span("order-api", "c", "a", 2, 10, "STATUS_CODE_ERROR"),
	}
	p := ProjectTraceOntoFlow("t1", members, edges, rows)
	n := stateOf(p, "order-api")
	if n.State != TraceNodeFailed {
		t.Errorf("got %q, want failed", n.State)
	}
	if n.SpanCount != 2 || n.ErrorCount != 1 {
		t.Errorf("got %d spans / %d errors, want 2/1", n.SpanCount, n.ErrorCount)
	}
}

func TestNumericStatusCodeIsAlsoAnError(t *testing.T) {
	// Exporter versions differ on whether this is the enum name or "2".
	// Missing the numeric form would silently downgrade every failure to
	// a plain "reached", which is the wrong answer in the exact case
	// this feature exists for.
	rows := []store.SpanRow{span("gateway", "a", "", 0, 5, "2")}
	if got := stateOf(ProjectTraceOntoFlow("t1", members, edges, rows), "gateway").State; got != TraceNodeFailed {
		t.Errorf("got %q, want failed", got)
	}
}

func TestTraversedEdgesComeFromTheTraceNotTheGraph(t *testing.T) {
	// A hop the aggregate graph has never recorded still happened, and
	// must draw. Here gateway calls warehouse-sync directly, skipping
	// order-api; no such edge exists in `edges`.
	rows := []store.SpanRow{
		span("gateway", "a", "", 0, 20, "STATUS_CODE_OK"),
		span("warehouse-sync", "b", "a", 1, 30, "STATUS_CODE_OK"),
	}
	p := ProjectTraceOntoFlow("t1", members, edges, rows)
	if len(p.TraversedEdges) != 1 || p.TraversedEdges[0] != "gateway|warehouse-sync" {
		t.Errorf("got %v, want [gateway|warehouse-sync]", p.TraversedEdges)
	}
}

func TestSpansWithinOneServiceAreNotAnEdge(t *testing.T) {
	// Nodes are services, so a parent/child pair inside one service is
	// not a hop. Counting it would draw a self-loop on every node.
	rows := []store.SpanRow{
		span("gateway", "a", "", 0, 20, "STATUS_CODE_OK"),
		span("gateway", "b", "a", 1, 5, "STATUS_CODE_OK"),
	}
	p := ProjectTraceOntoFlow("t1", members, edges, rows)
	if len(p.TraversedEdges) != 0 {
		t.Errorf("got %v, want none", p.TraversedEdges)
	}
	if got := stateOf(p, "gateway").SpanCount; got != 2 {
		t.Errorf("both spans belong to gateway: got %d", got)
	}
}

func TestSpansOutsideTheIntegrationAreCountedNotHidden(t *testing.T) {
	// A trace that mostly ran elsewhere means the graph is not the whole
	// story. Dropping those spans silently would make the projection
	// look more complete than it is.
	rows := []store.SpanRow{
		span("gateway", "a", "", 0, 20, "STATUS_CODE_OK"),
		span("some-other-system", "x", "a", 1, 30, "STATUS_CODE_OK"),
		span("third-party", "y", "x", 2, 30, "STATUS_CODE_OK"),
	}
	p := ProjectTraceOntoFlow("t1", members, edges, rows)
	if p.SpansOutsideIntegration != 2 {
		t.Errorf("got %d, want 2", p.SpansOutsideIntegration)
	}
	// And they must not create nodes or edges.
	if len(p.Nodes) != len(members) {
		t.Errorf("got %d nodes, want %d", len(p.Nodes), len(members))
	}
	if len(p.TraversedEdges) != 0 {
		t.Errorf("got edges %v, want none", p.TraversedEdges)
	}
}

func TestBranchingFlowDoesNotPaintEveryBranch(t *testing.T) {
	// Question 3 in the issue. gateway fans out to two alternatives; the
	// trace took one. The untaken branch is the frontier (it is directly
	// downstream of a reached node), but its own downstream must stay
	// neutral rather than reading as a chain of failures.
	m := []string{"gateway", "path-a", "path-b", "after-b"}
	e := []FlowEdge{
		{Source: "gateway", Target: "path-a"},
		{Source: "gateway", Target: "path-b"},
		{Source: "path-b", Target: "after-b"},
	}
	rows := []store.SpanRow{
		span("gateway", "a", "", 0, 20, "STATUS_CODE_OK"),
		span("path-a", "b", "a", 1, 30, "STATUS_CODE_OK"),
	}
	p := ProjectTraceOntoFlow("t1", m, e, rows)
	if got := stateOf(p, "path-a").State; got != TraceNodeReached {
		t.Errorf("path-a: got %q", got)
	}
	if got := stateOf(p, "path-b").State; got != TraceNodeNext {
		t.Errorf("path-b: got %q, want next", got)
	}
	if got := stateOf(p, "after-b").State; got != TraceNodeNotReached {
		t.Errorf("after-b must stay neutral: got %q", got)
	}
}

func TestFrontierNamesTheFurthestUpstream(t *testing.T) {
	// Two reached upstreams converge on one unreached node. The one the
	// operator was just looking at is the later one, so that is the one
	// worth naming.
	m := []string{"early", "late", "target"}
	e := []FlowEdge{{Source: "early", Target: "target"}, {Source: "late", Target: "target"}}
	rows := []store.SpanRow{
		span("early", "a", "", 0, 10, "STATUS_CODE_OK"),
		span("late", "b", "", 60, 10, "STATUS_CODE_OK"),
	}
	p := ProjectTraceOntoFlow("t1", m, e, rows)
	if got := stateOf(p, "target").AfterService; got != "late" {
		t.Errorf("got %q, want late", got)
	}
}

func TestTraceThatTouchedNoMemberIsEmptyNotWrong(t *testing.T) {
	// A trace id pasted from elsewhere, or one whose spans the caller
	// cannot see. Every node must read not_reached, and nothing may be
	// nominated as a frontier — there is no reached node to follow.
	rows := []store.SpanRow{span("stranger", "a", "", 0, 10, "STATUS_CODE_OK")}
	p := ProjectTraceOntoFlow("t1", members, edges, rows)
	for _, n := range p.Nodes {
		if n.State != TraceNodeNotReached {
			t.Errorf("%s: got %q, want not_reached", n.ServiceName, n.State)
		}
	}
	if p.LastReached != "" {
		t.Errorf("last reached: got %q, want empty", p.LastReached)
	}
	if p.Started != nil || p.Ended != nil {
		t.Error("an untouched projection must not report bounds")
	}
}

func TestBoundsSpanTheWholeTrace(t *testing.T) {
	rows := []store.SpanRow{
		span("gateway", "a", "", 0, 100, "STATUS_CODE_OK"),
		span("order-api", "b", "a", 1, 2000, "STATUS_CODE_OK"),
	}
	p := ProjectTraceOntoFlow("t1", members, edges, rows)
	if p.Started == nil || !p.Started.Equal(base) {
		t.Errorf("started: got %v, want %v", p.Started, base)
	}
	// order-api starts at +1s and runs 2s, so the trace ends at +3s —
	// the END of the last span, not its start.
	want := base.Add(3 * time.Second)
	if p.Ended == nil || !p.Ended.Equal(want) {
		t.Errorf("ended: got %v, want %v", p.Ended, want)
	}
}
