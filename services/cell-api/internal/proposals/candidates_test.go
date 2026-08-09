// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The measured case from the issue is the fixture: a real cell's
// unassigned services, where naming found 2 of 17 and got three
// groupings wrong. Topology has to find the shipping cluster that
// naming misses entirely, and must not fuse the flows that naming
// wrongly joined.

package proposals

import "testing"

// The groupings a human would make on that cell.
var (
	shipping = []string{"carrier-dispatcher", "shipment-orchestrator", "warehouse-picker", "tracking-processor", "fulfillment-worker"}
	intake   = []string{"order-intake", "partner-edi", "document-intake", "invoice-router"}
	notify   = []string{"email-service", "notification-dispatcher"}
)

func chain(names []string, traces uint64) []Edge {
	out := []Edge{}
	for i := 0; i+1 < len(names); i++ {
		out = append(out, Edge{Source: names[i], Target: names[i+1], Traces: traces})
	}
	return out
}

func all(groups ...[]string) []string {
	out := []string{}
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func has(c Cluster, name string) bool {
	for _, s := range c.Services {
		if s == name {
			return true
		}
	}
	return false
}

func TestTopologyFindsWhatNamingMisses(t *testing.T) {
	// The shipping cluster shares no lexical structure, so naming found
	// none of it. The call graph does.
	svcs := all(shipping, intake, notify)
	edges := append(chain(shipping, 100), chain(intake, 80)...)
	edges = append(edges, chain(notify, 40)...)

	got := FindClusters(svcs, edges, DefaultClusterOptions())
	if len(got) != 3 {
		t.Fatalf("got %d clusters, want 3: %+v", len(got), got)
	}
	var found bool
	for _, c := range got {
		if has(c, "carrier-dispatcher") && has(c, "warehouse-picker") && len(c.Services) == 5 {
			found = true
		}
	}
	if !found {
		t.Errorf("the shipping cluster was not found: %+v", got)
	}
}

func TestNamingsWrongGroupingsAreNotProduced(t *testing.T) {
	// Trailing-token clustering joined carrier-dispatcher with
	// notification-dispatcher (shipping with email). They never call
	// each other, so topology must keep them apart.
	svcs := all(shipping, notify)
	edges := append(chain(shipping, 100), chain(notify, 40)...)

	for _, c := range FindClusters(svcs, edges, DefaultClusterOptions()) {
		if has(c, "carrier-dispatcher") && has(c, "notification-dispatcher") {
			t.Fatalf("shipping and email were fused: %+v", c)
		}
	}
}

func TestAStrayCallDoesNotFuseTwoFlows(t *testing.T) {
	// One service calling another once is a coincidence. Without the
	// floor, a single stray hop merges two unrelated integrations into
	// one suggestion, and a wrong grouping costs more to review than
	// none at all.
	svcs := all(shipping, notify)
	edges := append(chain(shipping, 100), chain(notify, 40)...)
	edges = append(edges, Edge{Source: "warehouse-picker", Target: "email-service", Traces: 1})

	got := FindClusters(svcs, edges, DefaultClusterOptions())
	if len(got) != 2 {
		t.Fatalf("a single stray call fused the flows: %+v", got)
	}
}

func TestARealRelationshipDoesJoin(t *testing.T) {
	// The floor must not be so high that genuine traffic is ignored.
	svcs := all(shipping, notify)
	edges := append(chain(shipping, 100), chain(notify, 40)...)
	edges = append(edges, Edge{Source: "warehouse-picker", Target: "email-service", Traces: 500})

	if got := FindClusters(svcs, edges, DefaultClusterOptions()); len(got) != 1 {
		t.Fatalf("heavy traffic should join them, got %d clusters", len(got))
	}
}

func TestAssignedServicesAreNotPulledIn(t *testing.T) {
	// A service already in an integration is not evidence about services
	// that are not, and including it would propose overlapping
	// memberships for something a human already decided.
	svcs := []string{"a", "b"}
	edges := []Edge{
		{Source: "a", Target: "b", Traces: 50},
		{Source: "b", Target: "already-assigned", Traces: 900},
	}
	got := FindClusters(svcs, edges, DefaultClusterOptions())
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if has(got[0], "already-assigned") {
		t.Error("an assigned service was pulled into a proposal")
	}
}

func TestALonelyServiceIsNotAnIntegration(t *testing.T) {
	got := FindClusters([]string{"solo", "other"}, nil, DefaultClusterOptions())
	if len(got) != 0 {
		t.Fatalf("isolated services should not be proposed: %+v", got)
	}
}

func TestAHubThatChainedTheEstateIsSkippedAndReported(t *testing.T) {
	// An auth sidecar or shared gateway can connect everything. The
	// resulting component is not an integration, it is the estate, and
	// proposing it would be confidently useless. But a silent cap reads
	// as "nothing else to find", so it must be reportable.
	svcs := []string{}
	edges := []Edge{}
	for _, n := range []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10", "s11", "s12", "s13"} {
		svcs = append(svcs, n)
		edges = append(edges, Edge{Source: "hub", Target: n, Traces: 100})
	}
	svcs = append(svcs, "hub")

	if got := FindClusters(svcs, edges, DefaultClusterOptions()); len(got) != 0 {
		t.Fatalf("an oversized component should not be proposed: %+v", got)
	}
	over := OversizedClusters(svcs, edges, DefaultClusterOptions())
	if len(over) != 1 || len(over[0].Services) != 14 {
		t.Fatalf("the oversized component should be reportable: %+v", over)
	}
}

func TestBusiestClusterComesFirst(t *testing.T) {
	// The grouping carrying the most traffic is the one whose absence
	// from monitoring matters most.
	svcs := all(shipping, notify)
	edges := append(chain(shipping, 1000), chain(notify, 10)...)
	got := FindClusters(svcs, edges, DefaultClusterOptions())
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if !has(got[0], "carrier-dispatcher") {
		t.Errorf("busiest cluster should lead, got %+v", got[0])
	}
}

func TestResultIsDeterministic(t *testing.T) {
	// Proposals are deduped by their member list, so an unstable order
	// would file the same suggestion repeatedly under different keys.
	svcs := all(shipping, intake)
	edges := append(chain(shipping, 100), chain(intake, 80)...)
	a := FindClusters(svcs, edges, DefaultClusterOptions())
	b := FindClusters(svcs, edges, DefaultClusterOptions())
	if len(a) != len(b) {
		t.Fatal("cluster count varies between runs")
	}
	for i := range a {
		if DedupKey(a[i].Services) != DedupKey(b[i].Services) {
			t.Fatalf("run %d differs: %v vs %v", i, a[i].Services, b[i].Services)
		}
	}
}

func TestDedupKeyIgnoresOrder(t *testing.T) {
	// Creates never supersede, so without a stable key a re-proposing
	// agent floods the inbox every run.
	if DedupKey([]string{"b", "a"}) != DedupKey([]string{"a", "b"}) {
		t.Error("the same grouping must produce the same key")
	}
	if DedupKey([]string{"a", "b"}) == DedupKey([]string{"a", "c"}) {
		t.Error("different groupings must not collide")
	}
}

func TestDedupKeySeparatorCannotBeForged(t *testing.T) {
	// A service name containing the separator would otherwise let two
	// different groupings produce one key.
	if DedupKey([]string{"a\x1fb"}) == DedupKey([]string{"a", "b"}) {
		t.Error("a name containing the separator collided with two names")
	}
}
