// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package proposals

import (
	"fmt"
	"testing"
)

func TestFindPartitions(t *testing.T) {
	stats := []AttrPartitionStats{
		// Node-RED: every run carries one flow id, twelve flows.
		{Service: "node-red", Key: "node_red.flow.id", TracesTotal: 1000, TracesWithKey: 1000, DistinctValues: 12, TraceValuePairs: 1000},
		// A node id: few values, but a run visits many nodes.
		{Service: "node-red", Key: "node_red.node.id", TracesTotal: 1000, TracesWithKey: 1000, DistinctValues: 40, TraceValuePairs: 4200},
		// An identifier: one per trace, far too many values.
		{Service: "node-red", Key: "order.id", TracesTotal: 1000, TracesWithKey: 1000, DistinctValues: 998, TraceValuePairs: 1000},
		// A constant.
		{Service: "node-red", Key: "node_red.version", TracesTotal: 1000, TracesWithKey: 1000, DistinctValues: 1, TraceValuePairs: 1000},
		// On a subset of the traffic: describes the subset.
		{Service: "node-red", Key: "retry.reason", TracesTotal: 1000, TracesWithKey: 60, DistinctValues: 3, TraceValuePairs: 60},
		// A dimension by definition, however neatly it splits.
		{Service: "node-red", Key: "http.request.method", TracesTotal: 1000, TracesWithKey: 1000, DistinctValues: 2, TraceValuePairs: 1000},
		// Too little traffic to read anything into.
		{Service: "quiet", Key: "flow", TracesTotal: 5, TracesWithKey: 5, DistinctValues: 2, TraceValuePairs: 5},
		// Camel routes on another service.
		{Service: "camel", Key: "camel.route.id", TracesTotal: 400, TracesWithKey: 380, DistinctValues: 5, TraceValuePairs: 395},
	}
	got := FindPartitions(stats, DefaultPartitionOptions(), 3, 20)
	if len(got) != 2 {
		t.Fatalf("want node_red.flow.id and camel.route.id, got %+v", got)
	}
	// Both named like flows; full coverage ranks first.
	if got[0].Key != "node_red.flow.id" || got[1].Key != "camel.route.id" {
		t.Errorf("order or picks: %+v", got)
	}
	if got[0].Values != 12 || got[0].Presence != 1 || !got[0].NamedAsFlow {
		t.Errorf("figures: %+v", got[0])
	}
}

func TestFindPartitionsCapsPerService(t *testing.T) {
	var stats []AttrPartitionStats
	for _, k := range []string{"a", "b", "c", "d"} {
		stats = append(stats, AttrPartitionStats{Service: "s", Key: k, TracesTotal: 100, TracesWithKey: 100, DistinctValues: 3, TraceValuePairs: 100})
	}
	if got := FindPartitions(stats, DefaultPartitionOptions(), 2, 0); len(got) != 2 {
		t.Errorf("cap: %d", len(got))
	}
}

// Measured on a demo estate: properties of the message outnumbered the
// flows until these two tests were added.
func TestMessagePropertiesAreNotFlows(t *testing.T) {
	shape := func(svc, key string, values, numeric uint64) AttrPartitionStats {
		return AttrPartitionStats{Service: svc, Key: key, TracesTotal: 100, TracesWithKey: 100, DistinctValues: values, TraceValuePairs: 100, NumericValues: numeric}
	}
	stats := []AttrPartitionStats{
		// The trading partner travels through every service of the flow.
		shape("b2b-gateway", "edi.partner_id", 3, 0),
		shape("erp-adapter", "edi.partner_id", 3, 0),
		shape("finance-ledger", "edi.partner_id", 3, 0),
		// A count, and a flag.
		shape("invoice-matcher", "edi.line_count", 40, 40),
		{Service: "notifier", Key: "rule.matched", TracesTotal: 100, TracesWithKey: 100, DistinctValues: 2, TraceValuePairs: 100, BooleanValues: 2},
		// The runtime's own flows, and one more property it alone has.
		shape("node-red-runtime", "node_red.flow.id", 3, 0),
		shape("carrier-dispatcher", "carrier.name", 4, 0),
		// Named like a flow and travelling: kept, it says what it is.
		shape("a", "integration.id", 4, 0),
		shape("b", "integration.id", 4, 0),
		shape("c", "integration.id", 4, 0),
	}
	got := FindPartitions(stats, DefaultPartitionOptions(), 2, 20)
	keys := map[string]int{}
	for _, c := range got {
		keys[c.Key]++
	}
	if keys["edi.partner_id"] != 0 || keys["edi.line_count"] != 0 || keys["rule.matched"] != 0 {
		t.Errorf("message properties kept: %v", keys)
	}
	if keys["node_red.flow.id"] != 1 || keys["carrier.name"] != 1 || keys["integration.id"] != 3 {
		t.Errorf("flows lost: %v", keys)
	}
	// Named flows first; the unnamed one last.
	if got[len(got)-1].Key != "carrier.name" {
		t.Errorf("ranking: %+v", got)
	}
}

// The cap applies after ranking: the alphabet does not decide.
func TestLimitKeepsTheStrongest(t *testing.T) {
	var stats []AttrPartitionStats
	for i := 0; i < 30; i++ {
		stats = append(stats, AttrPartitionStats{Service: fmt.Sprintf("a-svc-%02d", i), Key: fmt.Sprintf("prop%d", i), TracesTotal: 100, TracesWithKey: 100, DistinctValues: 3, TraceValuePairs: 100})
	}
	stats = append(stats, AttrPartitionStats{Service: "z-node-red", Key: "node_red.flow.id", TracesTotal: 100, TracesWithKey: 100, DistinctValues: 3, TraceValuePairs: 100})
	got := FindPartitions(stats, DefaultPartitionOptions(), 2, 20)
	if len(got) != 20 || got[0].Service != "z-node-red" {
		t.Errorf("the flow id was cut: first %+v of %d", got[0], len(got))
	}
}

func TestDimensionKeys(t *testing.T) {
	for _, k := range []string{"http.method", "code.function", "exception.type", "thread.name", "server.address"} {
		if !IsDimensionKey(k) {
			t.Errorf("%s should be a dimension", k)
		}
	}
	for _, k := range []string{"node_red.flow.id", "camel.route.id", "messaging.destination.name", "flow"} {
		if IsDimensionKey(k) {
			t.Errorf("%s is not a dimension", k)
		}
	}
}
