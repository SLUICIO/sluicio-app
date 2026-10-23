// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"strings"
	"testing"
	"time"
)

// An integration's sources (#43) are OR-ed beside its members and never
// reach the one-trace gate: a broker's queue gauges belong to no trace.
func TestSourceGroupsBesideTheGate(t *testing.T) {
	m0, m1 := 0, 1
	members := [][]LogAttrFilter{
		{{Key: "service.name", Op: AttrOpEq, Value: "a", RequireAllGroups: true, Member: &m0}},
		{{Key: "service.name", Op: AttrOpEq, Value: "b", RequireAllGroups: true, Member: &m1}},
	}
	source := []LogAttrFilter{
		{Key: "service.name", Op: AttrOpMatches, Value: "^(?:rabbitmq)$", Source: true},
		{Key: "rabbitmq.queue.name", Op: AttrOpEq, Value: "orders", Source: true},
		{Key: MetricNameAttrKey, Op: AttrOpMatches, Value: "^rabbitmq\\.queue\\..*$", Source: true},
	}
	from, to := time.Now().Add(-time.Hour), time.Now()

	logs, args := attrGroupsClause("LogAttributes", append(members, source), from, to)
	gated, _ := attrGroupsClause("LogAttributes", members, from, to)
	if !strings.HasPrefix(logs, "("+gated+" OR (") {
		t.Fatalf("the source is not beside the gated members:\n%s", logs)
	}
	if strings.Count(logs, "match(ServiceName, ?)") != 1 {
		t.Errorf("source service: %s", logs)
	}
	if args[len(args)-3] != "^(?:rabbitmq)$" || args[len(args)-2] != "orders" {
		t.Errorf("args out of order: %v", args)
	}

	metrics, _ := attrGroupsClause("MetricAttributes", [][]LogAttrFilter{source}, from, to)
	if !strings.Contains(metrics, "match(MetricName, ?)") {
		t.Errorf("metric.name reads the MetricName column: %s", metrics)
	}
	if only, _ := attrGroupsClause("LogAttributes", [][]LogAttrFilter{source}, from, to); strings.Contains(only, "EXISTS") || strings.Contains(only, "IN (SELECT") {
		t.Errorf("a source alone is gated: %s", only)
	}
}
