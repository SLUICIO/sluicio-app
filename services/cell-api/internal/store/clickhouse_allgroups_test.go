// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// An integration's rules are a union by default: a span belongs if it
// satisfies any of them. With rule_match = all the question is asked of
// the trace instead, because a rule names a service and one span is only
// ever in one service - so "service a where a = 1 AND service b where
// b = 3" can never be satisfied by a single row.

package store

import (
	"strings"
	"testing"
	"time"
)

func twoRules(requireAll bool) [][]LogAttrFilter {
	return [][]LogAttrFilter{
		{
			{Key: "service.name", Op: AttrOpEq, Value: "svc-a", RequireAllGroups: requireAll},
			{Key: "a", Op: AttrOpEq, Value: "1", RequireAllGroups: requireAll},
		},
		{
			{Key: "service.name", Op: AttrOpEq, Value: "svc-b", RequireAllGroups: requireAll},
			{Key: "b", Op: AttrOpEq, Value: "3", RequireAllGroups: requireAll},
		},
	}
}

func TestUnionIsUnchanged(t *testing.T) {
	sql, args := attrGroupsClause("SpanAttributes", twoRules(false), subtreeFrom, subtreeTo)
	if strings.Contains(sql, "countIf") {
		t.Fatalf("a union grew a trace gate:\n%s", sql)
	}
	assertBinds(t, sql, args)
}

func TestRequireAllGatesOnTheTrace(t *testing.T) {
	sql, args := attrGroupsClause("SpanAttributes", twoRules(true), subtreeFrom, subtreeTo)
	assertBinds(t, sql, args)
	if !strings.Contains(sql, "TraceId IN (") || !strings.Contains(sql, "GROUP BY TraceId") {
		t.Errorf("expected a trace-level gate:\n%s", sql)
	}
	// One countIf per rule, all of them required.
	if n := strings.Count(sql, "countIf("); n != 2 {
		t.Errorf("want one countIf per rule, got %d:\n%s", n, sql)
	}
	if !strings.Contains(sql, ") > 0 AND countIf(") {
		t.Errorf("the rules must be AND-ed in the HAVING:\n%s", sql)
	}
	// The union stays in front of the gate: the gate decides which traces
	// qualify, the union decides which of their spans belong.
	if !strings.HasPrefix(sql, "(((") || !strings.Contains(sql, " OR (") {
		t.Errorf("the per-span union should still be there:\n%s", sql)
	}
}

// A single rule is already its own conjunction, and a gate would only add
// a pass that cannot remove anything.
func TestRequireAllWithOneRuleIsANoOp(t *testing.T) {
	one := [][]LogAttrFilter{{{Key: "a", Op: AttrOpEq, Value: "1", RequireAllGroups: true}}}
	sql, _ := attrGroupsClause("SpanAttributes", one, subtreeFrom, subtreeTo)
	if strings.Contains(sql, "countIf") {
		t.Errorf("one rule needs no gate:\n%s", sql)
	}
}

// Metrics carry no trace, so the question cannot be asked of them.
func TestRequireAllIgnoredOnMetrics(t *testing.T) {
	with, _ := attrGroupsClause("MetricAttributes", twoRules(true), subtreeFrom, subtreeTo)
	without, _ := attrGroupsClause("MetricAttributes", twoRules(false), subtreeFrom, subtreeTo)
	if with != without {
		t.Errorf("metrics have no trace to gate on:\n%s\n%s", with, without)
	}
}

// Logs do carry a trace id, so the gate applies there as well.
func TestRequireAllAppliesToLogs(t *testing.T) {
	sql, args := attrGroupsClause("LogAttributes", twoRules(true), subtreeFrom, subtreeTo)
	assertBinds(t, sql, args)
	if !strings.Contains(sql, "TraceId IN (") {
		t.Errorf("expected the gate on logs too:\n%s", sql)
	}
}

func TestRequireAllNeedsAWindow(t *testing.T) {
	sql, _ := attrGroupsClause("SpanAttributes", twoRules(true), subtreeFrom.AddDate(0, 0, 0), subtreeTo)
	if !strings.Contains(sql, "countIf") {
		t.Fatalf("expected a gate with a window:\n%s", sql)
	}
	unbounded, _ := attrGroupsClause("SpanAttributes", twoRules(true), zeroTime(), zeroTime())
	if strings.Contains(unbounded, "countIf") {
		t.Errorf("a gate without a window would scan everything:\n%s", unbounded)
	}
}

func zeroTime() time.Time { return time.Time{} }

// On logs the gate still reads the traces table, so its conditions have
// to be about spans. Compiled against the log attribute map it is not a
// wrong answer but a query ClickHouse refuses to run, which is how it was
// found: the Logs page 500'd the first time an integration used all.
func TestRequireAllOnLogsAsksAboutSpans(t *testing.T) {
	sql, _ := attrGroupsClause("LogAttributes", twoRules(true), subtreeFrom, subtreeTo)
	gate := sql[strings.Index(sql, "TraceId IN ("):]
	if strings.Contains(gate, "LogAttributes") {
		t.Errorf("the gate reads traces, so it cannot mention LogAttributes:\n%s", gate)
	}
	if !strings.Contains(gate, "SpanAttributes") {
		t.Errorf("expected the span attribute map in the gate:\n%s", gate)
	}
}
