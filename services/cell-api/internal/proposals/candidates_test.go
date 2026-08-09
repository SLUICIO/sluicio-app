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

// The topology cases below pass a nil denominator, which disables the
// overlap test. That is deliberate: they were written to check the
// clustering rules, and supplying per-service traffic would make them
// also depend on the overlap threshold, so a change to that threshold
// would break tests that are not about it.

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

	got := FindClusters(svcs, edges, nil, DefaultClusterOptions())
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

	for _, c := range FindClusters(svcs, edges, nil, DefaultClusterOptions()) {
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

	got := FindClusters(svcs, edges, nil, DefaultClusterOptions())
	if len(got) != 2 {
		t.Fatalf("a single stray call fused the flows: %+v", got)
	}
}

func TestARealRelationshipDoesJoin(t *testing.T) {
	// The floor must not be so high that genuine traffic is ignored.
	svcs := all(shipping, notify)
	edges := append(chain(shipping, 100), chain(notify, 40)...)
	edges = append(edges, Edge{Source: "warehouse-picker", Target: "email-service", Traces: 500})

	if got := FindClusters(svcs, edges, nil, DefaultClusterOptions()); len(got) != 1 {
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
	got := FindClusters(svcs, edges, nil, DefaultClusterOptions())
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if has(got[0], "already-assigned") {
		t.Error("an assigned service was pulled into a proposal")
	}
}

func TestALonelyServiceIsNotAnIntegration(t *testing.T) {
	got := FindClusters([]string{"solo", "other"}, nil, nil, DefaultClusterOptions())
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

	if got := FindClusters(svcs, edges, nil, DefaultClusterOptions()); len(got) != 0 {
		t.Fatalf("an oversized component should not be proposed: %+v", got)
	}
	over := OversizedClusters(svcs, edges, nil, DefaultClusterOptions())
	if len(over) != 1 || len(over[0].Services) != 14 {
		t.Fatalf("the oversized component should be reportable: %+v", over)
	}
}

func TestBusiestClusterComesFirst(t *testing.T) {
	// The grouping carrying the most traffic is the one whose absence
	// from monitoring matters most.
	svcs := all(shipping, notify)
	edges := append(chain(shipping, 1000), chain(notify, 10)...)
	got := FindClusters(svcs, edges, nil, DefaultClusterOptions())
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
	a := FindClusters(svcs, edges, nil, DefaultClusterOptions())
	b := FindClusters(svcs, edges, nil, DefaultClusterOptions())
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

func TestATakenSlugBlocksApproval(t *testing.T) {
	// Applying would fail, or worse attach to somebody else's
	// integration. This is the one drift that must stop an approval.
	d := CheckCreateDrift([]string{"a"}, true, nil, nil)
	if !d.Any() || !d.Blocking() {
		t.Fatalf("a taken slug must block: %+v", d)
	}
}

func TestClaimedServicesWeakenButDoNotBlock(t *testing.T) {
	// An integration over four of five originally proposed services is
	// usually still the one the reviewer wanted. Blocking would turn a
	// useful suggestion into a dead row whenever somebody tidied one
	// service in the meantime.
	d := CheckCreateDrift([]string{"a", "b", "c"}, false, map[string]bool{"b": true}, nil)
	if !d.Any() {
		t.Fatal("a claimed service is drift")
	}
	if d.Blocking() {
		t.Error("claimed services must not block an approval")
	}
	if len(d.ClaimedServices) != 1 || d.ClaimedServices[0] != "b" {
		t.Errorf("got %v", d.ClaimedServices)
	}
}

func TestServicesThatWentQuietAreReported(t *testing.T) {
	// The evidence has expired: approving would create an integration
	// watching something that no longer reports.
	d := CheckCreateDrift([]string{"a", "b"}, false, nil, map[string]bool{"a": true})
	if len(d.MissingServices) != 1 || d.MissingServices[0] != "b" {
		t.Fatalf("got %v", d.MissingServices)
	}
	if d.Blocking() {
		t.Error("a quiet service is a judgement call, not a block")
	}
}

func TestAnUnknownReportingSetDoesNotCondemnEverything(t *testing.T) {
	// An empty map means "not established", not "nothing reports".
	// Treating it as the latter would block every approval on a cell
	// whose catalog had not finished loading.
	d := CheckCreateDrift([]string{"a", "b"}, false, nil, nil)
	if len(d.MissingServices) != 0 {
		t.Fatalf("an unestablished reporting set must not mark services missing: %v", d.MissingServices)
	}
	if d.Any() {
		t.Error("nothing drifted")
	}
}


// ── The shared-gateway case, from a real cell ────────────────────────
//
// The demo cell proposed one cluster of seven services. A human would
// have made three. Connected components could not tell them apart
// because a gateway fed all three, and at seven services it was well
// under the size cap, so nothing else was going to catch it.
//
// The numbers below are the ones actually observed, not invented.

var (
	gwSvcs = []string{
		"b2b-gateway", "order-validator", "erp-adapter",
		"invoice-matcher", "finance-ledger",
		"despatch-notifier", "warehouse-sync",
	}
	// b2b-gateway is in every trace; each downstream pair in a subset.
	// 33,327 + 19,829 + 13,312 = 66,468, exactly the gateway's total.
	gwTraces = map[string]uint64{
		"b2b-gateway": 66468,
		"order-validator": 33327, "erp-adapter": 33327,
		"invoice-matcher": 19829, "finance-ledger": 19829,
		"despatch-notifier": 13312, "warehouse-sync": 13312,
	}
	gwEdges = []Edge{
		{Source: "b2b-gateway", Target: "order-validator", Traces: 33327},
		{Source: "order-validator", Target: "erp-adapter", Traces: 33327},
		{Source: "b2b-gateway", Target: "invoice-matcher", Traces: 19829},
		{Source: "invoice-matcher", Target: "finance-ledger", Traces: 19829},
		{Source: "b2b-gateway", Target: "despatch-notifier", Traces: 13312},
		{Source: "despatch-notifier", Target: "warehouse-sync", Traces: 13312},
	}
)

func TestASharedGatewayNoLongerFusesThreeFlows(t *testing.T) {
	got := FindClusters(gwSvcs, gwEdges, gwTraces, DefaultClusterOptions())
	if len(got) != 3 {
		names := [][]string{}
		for _, c := range got {
			names = append(names, c.Services)
		}
		t.Fatalf("got %d clusters, want 3: %v", len(got), names)
	}
	for _, c := range got {
		if len(c.Services) != 2 {
			t.Errorf("each flow is a pair, got %v", c.Services)
		}
		if has(c, "b2b-gateway") {
			t.Errorf("the gateway must not be a member: %v", c.Services)
		}
	}
}

func TestTheGatewayIsNamedRatherThanHidden(t *testing.T) {
	// Dropping it silently would hide a real participant, which is the
	// opposite failure from fusing everything into one cluster.
	for _, c := range FindClusters(gwSvcs, gwEdges, gwTraces, DefaultClusterOptions()) {
		if len(c.SharedServices) != 1 || c.SharedServices[0] != "b2b-gateway" {
			t.Errorf("%v should name the gateway it is fed by, got %v", c.Services, c.SharedServices)
		}
	}
}

func TestWithoutTheDenominatorNothingIsExcluded(t *testing.T) {
	// Missing traffic data is not evidence of a hub. A cell that could
	// not supply it must degrade to the old behaviour rather than
	// refusing to group anything.
	got := FindClusters(gwSvcs, gwEdges, nil, DefaultClusterOptions())
	if len(got) != 1 || len(got[0].Services) != 7 {
		t.Fatalf("expected the old single cluster, got %+v", got)
	}
}

func TestAGenuineChainSurvivesTheOverlapTest(t *testing.T) {
	// The condition must not split a real flow. Every service here does
	// all of its work in the same traces.
	svcs := []string{"a", "b", "c", "d"}
	traces := map[string]uint64{"a": 500, "b": 500, "c": 500, "d": 500}
	edges := []Edge{
		{Source: "a", Target: "b", Traces: 500},
		{Source: "b", Target: "c", Traces: 500},
		{Source: "c", Target: "d", Traces: 500},
	}
	got := FindClusters(svcs, edges, traces, DefaultClusterOptions())
	if len(got) != 1 || len(got[0].Services) != 4 {
		t.Fatalf("a genuine chain was split: %+v", got)
	}
	if len(got[0].SharedServices) != 0 {
		t.Errorf("nothing was shared here, got %v", got[0].SharedServices)
	}
}

func TestAServiceOnTheBoundaryIsIncluded(t *testing.T) {
	// The threshold is a judgement, so it should err toward keeping a
	// service in: a missing member is harder for a reviewer to notice
	// than an extra one.
	svcs := []string{"a", "b"}
	traces := map[string]uint64{"a": 100, "b": 60}
	edges := []Edge{{Source: "a", Target: "b", Traces: 60}} // 0.60 exactly
	if got := FindClusters(svcs, edges, traces, DefaultClusterOptions()); len(got) != 1 {
		t.Fatalf("an edge exactly at the threshold should join: %+v", got)
	}
}
