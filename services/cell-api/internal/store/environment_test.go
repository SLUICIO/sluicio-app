// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"strings"
	"testing"
)

func TestEnvironmentExpr(t *testing.T) {
	t.Cleanup(func() { SetEnvironmentConfig(EnvironmentConfig{}) })

	SetEnvironmentConfig(EnvironmentConfig{})
	if got := EnvironmentExpr(); got != "Environment" {
		t.Errorf("default: %s", got)
	}

	SetEnvironmentConfig(EnvironmentConfig{Key: "k8s.namespace.name"})
	if got := EnvironmentExpr(); got != "ResourceAttributes['k8s.namespace.name']" {
		t.Errorf("custom key: %s", got)
	}

	// Aliases are case-insensitive on the way in, and a hostile setting
	// cannot break out of its literal.
	SetEnvironmentConfig(EnvironmentConfig{Aliases: map[string]string{"PRD": "production", "it's": "x'); DROP"}})
	got := EnvironmentExpr()
	for _, want := range []string{"transform(lower(Environment), ['it\\'s', 'prd'], ['x\\'); DROP', 'production'], Environment)"} {
		if got != want {
			t.Errorf("aliases:\n got %s\nwant %s", got, want)
		}
	}
	if CanonicalEnvironment("Prd") != "production" || CanonicalEnvironment("test") != "test" {
		t.Error("CanonicalEnvironment does not follow the aliases")
	}
}

func TestWithEnvironment(t *testing.T) {
	m := 1
	groups := [][]LogAttrFilter{
		{{Key: "service.name", Op: AttrOpEq, Value: "a", RequireAllGroups: true, Member: &m}},
		{{Key: "flow", Op: AttrOpEq, Value: "x"}},
	}
	out := WithEnvironment(groups, "production")
	if len(out) != 2 || len(out[0]) != 2 || len(out[1]) != 2 {
		t.Fatalf("shape: %+v", out)
	}
	if e := out[0][1]; e.Key != EnvironmentAttrKey || e.Value != "production" || !e.RequireAllGroups || e.Member != &m {
		t.Errorf("membership tags not carried: %+v", e)
	}
	if len(groups[0]) != 1 {
		t.Error("input modified")
	}
	if none := WithEnvironment(nil, NoEnvironment); len(none) != 1 || none[0][0].Value != "" {
		t.Errorf("no groups: %+v", none)
	}

	sql, args := attrGroupsClause("MetricAttributes", WithEnvironment(nil, "test"), zeroTime(), zeroTime())
	if !strings.Contains(sql, "(Environment) = ?") || len(args) != 1 || args[0] != "test" {
		t.Errorf("clause: %s %v", sql, args)
	}
	sql, _ = attrClauseIn("SpanAttributes", LogAttrFilter{Key: EnvironmentAttrKey, Op: AttrOpNotExists})
	if sql != "(Environment) = ''" {
		t.Errorf("not exists: %s", sql)
	}
}
