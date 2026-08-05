// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// An integration is not just its member services.
//
// service.name decides MEMBERSHIP; every other matcher is a row-level
// predicate narrowing which of that service's traces belong to it. Two
// integrations can share one service and own disjoint slices of its
// traffic — one service with an integration per flow is the normal shape
// for a Node-RED cell, not an edge case.
//
// Forgetting the predicate has now caused the same bug three times: an
// integration reporting a sibling's failures, error counts spanning both
// flows, and a low-traffic check staying silent on an integration with
// zero traces because the shared service was busy. This pins the
// conversion every telemetry query depends on.

package api

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

func TestAttrGroupsCarriesNonServiceMatchers(t *testing.T) {
	// The live shape that broke: one service, narrowed by flow id.
	groups := AttrGroupsFromMatchers([]integrations.Matcher{
		{Attribute: "service.name", Operator: integrations.OperatorEquals, Value: "romaitab-nodered"},
		{Attribute: "node_red.flow.id", Operator: integrations.OperatorEquals, Value: "tab_paperless"},
	})
	if len(groups) != 1 {
		t.Fatalf("want one AND-group, got %d", len(groups))
	}
	var sawFlow bool
	for _, f := range groups[0] {
		if f.Key == "node_red.flow.id" && f.Value == "tab_paperless" && f.Op == store.AttrOpEq {
			sawFlow = true
		}
	}
	if !sawFlow {
		t.Errorf("the flow predicate was dropped; a query built from this counts the whole service: %+v", groups[0])
	}
}

func TestAttrGroupsSeparatesMatchGroups(t *testing.T) {
	// Distinct match_groups are alternatives (DNF): flattening them into
	// one group would AND predicates that were meant to be ORed, and the
	// integration would match nothing at all.
	groups := AttrGroupsFromMatchers([]integrations.Matcher{
		{Attribute: "a", Operator: integrations.OperatorEquals, Value: "1", MatchGroup: 0},
		{Attribute: "b", Operator: integrations.OperatorEquals, Value: "2", MatchGroup: 1},
	})
	if len(groups) != 2 {
		t.Fatalf("want two alternatives, got %d: %+v", len(groups), groups)
	}
}

func TestAttrGroupsIsNilWithoutMatchers(t *testing.T) {
	// nil means "no predicate", which the query layer reads as "every
	// trace on these services". An empty non-nil slice risks being
	// rendered as an unsatisfiable WHERE.
	if got := AttrGroupsFromMatchers(nil); got != nil {
		t.Errorf("want nil, got %+v", got)
	}
}

func TestAttrGroupsMapsEveryOperator(t *testing.T) {
	// A matcher whose operator silently became "equals" would quietly
	// change which traces an integration owns.
	cases := map[integrations.Operator]string{
		integrations.OperatorEquals:   store.AttrOpEq,
		integrations.OperatorPrefix:   store.AttrOpStartsWith,
		integrations.OperatorSuffix:   store.AttrOpEndsWith,
		integrations.OperatorContains: store.AttrOpContains,
		integrations.OperatorRegex:    store.AttrOpMatches,
	}
	for op, want := range cases {
		groups := AttrGroupsFromMatchers([]integrations.Matcher{
			{Attribute: "k", Operator: op, Value: "v"},
		})
		if len(groups) != 1 || len(groups[0]) != 1 {
			t.Fatalf("%s: unexpected shape %+v", op, groups)
		}
		if groups[0][0].Op != want {
			t.Errorf("%s mapped to %q, want %q", op, groups[0][0].Op, want)
		}
	}
}
