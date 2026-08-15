// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The bug these pin is not a crash, it is a wrong answer that looks
// plausible: every integration on a shared runtime showing the same
// facets. It survives a type check and every existing test, because the
// old code was correct code computing the wrong thing.
//
// What is checked here is the SQL-shaping decision — that an
// integration's attribute predicate reaches the profile query — and the
// degenerate case that must keep behaving exactly as before. The
// ClickHouse round-trip itself is exercised by the integration tests.

package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/servicetypes"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// The three Node-RED integrations that motivated this: one member
// service, told apart only by an attribute matcher.
func sharedRuntimeMatchers(flowKey, flowValue string) []integrations.Matcher {
	return []integrations.Matcher{
		{Attribute: "service.name", Operator: "equals", Value: "romaitab-nodered", MatchGroup: 0},
		{Attribute: flowKey, Operator: "equals", Value: flowValue, MatchGroup: 0},
	}
}

func TestIntegrationPredicateDistinguishesFlowsOnOneService(t *testing.T) {
	// Each integration must produce a DIFFERENT predicate, or classifying
	// them separately buys nothing: the three would profile the same span
	// set and land on the same facets, which is the bug.
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"export to lundify", "node_red.flow.id", "tab_paperless"},
		{"monthly archive", "node_red.flow.id", "tab_paperless_monthly"},
		{"wordpress messages", "node_red.flow.name", "Sluicio contact messages"},
	}
	seen := map[string]string{}
	for _, c := range cases {
		groups := AttrGroupsFromMatchers(sharedRuntimeMatchers(c.key, c.value))
		sql, args := store.SpanAttrGroupsClause(groups)
		if sql == "" {
			t.Fatalf("%s: no predicate, so its facets would be the whole service's", c.name)
		}
		sig := sql + "\x00" + strings.Join(argStrings(args), "\x00")
		if prev, dup := seen[sig]; dup {
			t.Errorf("%s and %s produce the same predicate, so they would be classified identically", c.name, prev)
		}
		seen[sig] = c.name
	}
}

func TestServiceNameMatcherAloneLeavesTheSliceUnnarrowed(t *testing.T) {
	// An integration defined by membership alone must end up classified
	// over ALL of its members' traffic, so the new path agrees with the
	// old rollup for the simple case. The DNF still renders the
	// service.name matcher — it is a column predicate, redundant with the
	// IN clause the profile query already applies — but it must not reach
	// into the attribute maps, because that is what narrows a slice.
	//
	// If this ever starts emitting an attribute term, every simple
	// integration silently loses facets.
	groups := AttrGroupsFromMatchers([]integrations.Matcher{
		{Attribute: "service.name", Operator: "equals", Value: "romaitab-nodered", MatchGroup: 0},
	})
	sql, _ := store.SpanAttrGroupsClause(groups)
	for _, attrMap := range []string{"SpanAttributes", "ResourceAttributes"} {
		if strings.Contains(sql, attrMap) {
			t.Errorf("service-only integration narrows on %s: %q", attrMap, sql)
		}
	}
	if !strings.Contains(sql, "ServiceName") {
		t.Errorf("predicate lost the service term entirely: %q", sql)
	}
}

func TestSharedRuntimeIntegrationNarrowsOnAnAttribute(t *testing.T) {
	// The converse, and the whole point: once a flow matcher is present
	// the predicate must reach into the attribute map, or the three
	// Node-RED integrations are profiled over the same spans and get the
	// same facets.
	groups := AttrGroupsFromMatchers(sharedRuntimeMatchers("node_red.flow.id", "tab_paperless"))
	sql, _ := store.SpanAttrGroupsClause(groups)
	if !strings.Contains(sql, "SpanAttributes") {
		t.Errorf("flow matcher did not narrow the slice: %q", sql)
	}
	if !strings.Contains(sql, "ServiceName") {
		t.Errorf("flow matcher dropped the service term: %q", sql)
	}
}

func TestWithoutCoreDropsTheFacetThatMatchesEverything(t *testing.T) {
	// core is always-on by definition, so a chip for it distinguishes
	// nothing and a filter entry for it would select every integration.
	in := []ServiceFacetRef{
		{Slug: "http-input", Name: "HTTP input"},
		{Slug: servicetypes.CoreSlug, Name: "Core"},
		{Slug: "queue-input", Name: "Queue input"},
	}
	got := withoutCore(in)
	if len(got) != 2 {
		t.Fatalf("got %d facets, want 2: %+v", len(got), got)
	}
	// Sorted by name, so the list is stable between requests.
	if got[0].Name != "HTTP input" || got[1].Name != "Queue input" {
		t.Errorf("unsorted or wrong facets: %+v", got)
	}
	for _, f := range got {
		if f.Slug == servicetypes.CoreSlug {
			t.Error("core survived")
		}
	}
}

// argStrings renders the bound arguments so two predicates that differ
// only in their VALUES (the same flow key, a different flow) still count
// as distinct — which is exactly the paperless / paperless_monthly pair.
func argStrings(args []any) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, fmt.Sprint(a))
	}
	return out
}
