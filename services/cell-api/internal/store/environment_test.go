// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"strings"
	"testing"
)

func TestEnvironmentExpr(t *testing.T) {
	t.Cleanup(func() { SetEnvironmentConfig(EnvironmentConfig{}) })

	SetEnvironmentConfig(EnvironmentConfig{})
	if got := EnvironmentExprIn(""); got != "coalesce(nullIf(Environment, ''), '')" {
		t.Errorf("resource only: %s", got)
	}
	// The resource first, then the record: a metric labelled per data
	// point still has an environment.
	want := "coalesce(nullIf(Environment, ''), nullIf(MetricAttributes['deployment.environment.name'], ''), nullIf(MetricAttributes['deployment.environment'], ''), '')"
	if got := EnvironmentExprIn(MetricsPrimaryMap); got != want {
		t.Errorf("default on metrics:\n got %s\nwant %s", got, want)
	}

	// A configured key comes first, on the resource and then the record,
	// and the defaults still apply where it is absent.
	SetEnvironmentConfig(EnvironmentConfig{Key: "environment"})
	want = "coalesce(nullIf(ResourceAttributes['environment'], ''), nullIf(Environment, ''), " +
		"nullIf(SpanAttributes['environment'], ''), nullIf(SpanAttributes['deployment.environment.name'], ''), nullIf(SpanAttributes['deployment.environment'], ''), '')"
	if got := EnvironmentExprIn(SpansPrimaryMap); got != want {
		t.Errorf("custom key on spans:\n got %s\nwant %s", got, want)
	}

	// Aliases are case-insensitive on the way in, and a hostile setting
	// cannot break out of its literal.
	SetEnvironmentConfig(EnvironmentConfig{Aliases: map[string]string{"PRD": "production", "it's": "x'); DROP"}})
	raw := "coalesce(nullIf(Environment, ''), '')"
	if got, want := EnvironmentExprIn(""), "transform(lower("+raw+"), ['it\\'s', 'prd'], ['x\\'); DROP', 'production'], "+raw+")"; got != want {
		t.Errorf("aliases:\n got %s\nwant %s", got, want)
	}
	SetEnvironmentConfig(EnvironmentConfig{Key: "it's"})
	if got := EnvironmentExprIn(LogsPrimaryMap); !strings.Contains(got, "ResourceAttributes['it\\'s']") || !strings.Contains(got, "LogAttributes['it\\'s']") {
		t.Errorf("a hostile key is not escaped: %s", got)
	}
	SetEnvironmentConfig(EnvironmentConfig{Aliases: map[string]string{"PRD": "production"}})
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
	if !strings.Contains(sql, "("+EnvironmentExprIn(MetricsPrimaryMap)+") = ?") || len(args) != 1 || args[0] != "test" {
		t.Errorf("clause: %s %v", sql, args)
	}
	sql, _ = attrClauseIn("SpanAttributes", LogAttrFilter{Key: EnvironmentAttrKey, Op: AttrOpNotExists})
	if sql != "("+EnvironmentExprIn(SpansPrimaryMap)+") = ''" {
		t.Errorf("not exists: %s", sql)
	}
}

// A dashboard reading production and test together (#40).
func TestWithEnvironments(t *testing.T) {
	groups := [][]LogAttrFilter{{{Key: "flow", Op: AttrOpEq, Value: "x"}}}
	if one := WithEnvironments(groups, []string{"test"}); one[0][1].Op != AttrOpEq || one[0][1].Value != "test" {
		t.Errorf("one environment is an equality: %+v", one)
	}
	two := WithEnvironments(groups, []string{"production", "a.b", NoEnvironment})
	if e := two[0][1]; e.Key != EnvironmentAttrKey || e.Op != AttrOpMatches || e.Value != `^(?:production|a\.b|)$` {
		t.Errorf("several: %+v", e)
	}
	if len(groups[0]) != 1 {
		t.Error("input modified")
	}
	if got := WithEnvironments(groups, nil); len(got[0]) != 1 {
		t.Errorf("none narrows nothing: %+v", got)
	}
}
