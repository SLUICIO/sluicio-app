// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A group marked Descendants selects the spans its conditions match and
// every span below them in the trace. The shape is checked here; that the
// SQL actually walks a tree is checked against ClickHouse by the liveprobe
// test beside this one.

package store

import (
	"strings"
	"testing"
	"time"
)

var (
	subtreeFrom = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	subtreeTo   = subtreeFrom.Add(time.Hour)
)

func abc123(descendants bool) [][]LogAttrFilter {
	return [][]LogAttrFilter{{{Key: "abc", Op: AttrOpEq, Value: "123", Descendants: descendants}}}
}

// Every `?` has a bind, in order. A subtree clause repeats its anchor, so
// an off-by-one here shifts every later argument of the outer query.
func assertBinds(t *testing.T, sql string, args []any) {
	t.Helper()
	if got := strings.Count(sql, "?"); got != len(args) {
		t.Fatalf("%d placeholders, %d binds:\n%s\n%v", got, len(args), sql, args)
	}
}

func TestWithoutDescendantsNothingChanges(t *testing.T) {
	sql, args := attrGroupsClause("SpanAttributes", abc123(false), subtreeFrom, subtreeTo)
	if strings.Contains(sql, "arrayFold") {
		t.Fatalf("a plain group grew a subtree lookup:\n%s", sql)
	}
	assertBinds(t, sql, args)
}

func TestDescendantsOnSpansIsTheSubtree(t *testing.T) {
	sql, args := attrGroupsClause("SpanAttributes", abc123(true), subtreeFrom, subtreeTo)
	for _, want := range []string{"(TraceId, SpanId) IN", "arrayFold", "ParentSpanId", "Timestamp >= ? AND Timestamp <= ?"} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q in:\n%s", want, sql)
		}
	}
	assertBinds(t, sql, args)
	// The anchor binds come first (groupArrayIf in the SELECT), then the
	// two windows, then the anchor again in the inner WHERE.
	if args[0] != "123" || args[len(args)-1] != "123" || args[1] != subtreeFrom || args[2] != subtreeTo {
		t.Errorf("binds out of order: %v", args)
	}
}

// One matcher in a group is enough, and the other conditions of the group
// choose the anchor rather than filtering the children.
func TestDescendantsIsReadPerGroup(t *testing.T) {
	groups := [][]LogAttrFilter{{
		{Key: "service.name", Op: AttrOpEq, Value: "order-gateway"},
		{Key: "abc", Op: AttrOpEq, Value: "123", Descendants: true},
	}}
	sql, args := attrGroupsClause("SpanAttributes", groups, subtreeFrom, subtreeTo)
	assertBinds(t, sql, args)
	if strings.Count(sql, "ServiceName") != 2 {
		t.Errorf("service.name should appear only inside the anchor (twice), got:\n%s", sql)
	}
	if strings.HasPrefix(strings.TrimPrefix(sql, "("), "((ServiceName") {
		t.Errorf("service.name was applied to the children as well:\n%s", sql)
	}
}

func TestDescendantsOnlyTouchesItsOwnGroup(t *testing.T) {
	groups := [][]LogAttrFilter{
		{{Key: "abc", Op: AttrOpEq, Value: "123", Descendants: true}},
		{{Key: "producer", Op: AttrOpEq, Value: "ttf"}},
	}
	sql, args := attrGroupsClause("SpanAttributes", groups, subtreeFrom, subtreeTo)
	assertBinds(t, sql, args)
	if strings.Count(sql, "arrayFold") != 1 || !strings.Contains(sql, ") OR ((if(mapContains(SpanAttributes, 'producer')") {
		t.Errorf("expected one subtree OR-ed with the plain group:\n%s", sql)
	}
}

// Logs keep their own match and add the logs of the subtree's spans. The
// anchor is a question about spans, so it reads the span attributes even
// though the outer query reads logs.
func TestDescendantsOnLogs(t *testing.T) {
	sql, args := attrGroupsClause("LogAttributes", abc123(true), subtreeFrom, subtreeTo)
	assertBinds(t, sql, args)
	if !strings.Contains(sql, "LogAttributes") || !strings.Contains(sql, " OR (TraceId, SpanId) IN") {
		t.Errorf("logs should keep their own match and OR the subtree:\n%s", sql)
	}
	inner := sql[strings.Index(sql, "(TraceId, SpanId) IN"):]
	if strings.Contains(inner, "LogAttributes") || !strings.Contains(inner, "SpanAttributes") {
		t.Errorf("the anchor must be evaluated against span attributes:\n%s", inner)
	}
}

func TestDescendantsIgnoredOnMetrics(t *testing.T) {
	with, _ := attrGroupsClause("MetricAttributes", abc123(true), subtreeFrom, subtreeTo)
	without, _ := attrGroupsClause("MetricAttributes", abc123(false), subtreeFrom, subtreeTo)
	if with != without {
		t.Errorf("metrics have no span tree, the flag should change nothing:\n%s\n%s", with, without)
	}
}

// With no window the anchor is rendered alone. That is exact for a
// question about the whole trace, and it must never be an unbounded scan.
func TestDescendantsWithoutAWindowIsTheAnchor(t *testing.T) {
	sql, args := attrGroupsClause("SpanAttributes", abc123(true), time.Time{}, time.Time{})
	plain, plainArgs := attrGroupsClause("SpanAttributes", abc123(false), time.Time{}, time.Time{})
	if sql != plain || len(args) != len(plainArgs) {
		t.Errorf("zero window should fall back to the anchor:\n%s\n%s", sql, plain)
	}
}
