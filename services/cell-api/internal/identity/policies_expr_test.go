// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package identity

import (
	"sort"
	"testing"
)

// Fixed fixture: four services, one (billing) with NO resource attributes,
// so absence-under-negation semantics are exercised.
var (
	exprUniverse = []string{"ABC-api", "ABC-worker", "XYZ-api", "billing"}
	exprAttrs    = map[string]map[string]string{
		"ABC-api":    {"team": "orders", "env": "prod"},
		"ABC-worker": {"team": "orders", "env": "sandbox"},
		"XYZ-api":    {"team": "payments", "env": "prod"},
		// billing: no attributes at all.
	}
)

func svcLeaf(match, value string) PolicyExpr { return PolicyExpr{Match: match, Value: value} }
func attrLeaf(k, match, value string) PolicyExpr {
	return PolicyExpr{Attr: k, Match: match, Value: value}
}
func node(op string, kids ...PolicyExpr) PolicyExpr { return PolicyExpr{Op: op, Children: kids} }

func gotSet(e PolicyExpr) []string {
	m := evalExpr(&e, exprUniverse, exprAttrs)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func eqSet(t *testing.T, name string, got, want []string) {
	t.Helper()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("%s: got %v want %v", name, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
	}
}

func TestEvalExpr(t *testing.T) {
	cases := []struct {
		name string
		expr PolicyExpr
		want []string
	}{
		{"service prefix", svcLeaf(MatchPrefix, "ABC"), []string{"ABC-api", "ABC-worker"}},
		{"service regex", svcLeaf(MatchRegex, "^ABC-"), []string{"ABC-api", "ABC-worker"}},
		{"service suffix", svcLeaf(MatchSuffix, "-api"), []string{"ABC-api", "XYZ-api"}},
		{"service contains", svcLeaf(MatchContains, "ill"), []string{"billing"}},
		{"attr equals", attrLeaf("team", MatchEquals, "orders"), []string{"ABC-api", "ABC-worker"}},
		{
			"attr OR",
			node("or", attrLeaf("team", MatchEquals, "orders"), attrLeaf("team", MatchEquals, "payments")),
			[]string{"ABC-api", "ABC-worker", "XYZ-api"},
		},
		{
			// not_equals INCLUDES the attribute-less service (billing).
			"not_equals includes absence",
			attrLeaf("env", MatchNotEquals, "sandbox"),
			[]string{"ABC-api", "XYZ-api", "billing"},
		},
		{
			// NOT(env=sandbox) — same absence semantics via the NOT node.
			"NOT equals includes absence",
			node("not", attrLeaf("env", MatchEquals, "sandbox")),
			[]string{"ABC-api", "XYZ-api", "billing"},
		},
		{
			// Excluding absence: require the attribute to exist AND differ.
			"exists AND not_equals excludes absence",
			node("and", attrLeaf("env", MatchExists, ""), attrLeaf("env", MatchNotEquals, "sandbox")),
			[]string{"ABC-api", "XYZ-api"},
		},
		{"exists", attrLeaf("env", MatchExists, ""), []string{"ABC-api", "ABC-worker", "XYZ-api"}},
		{"not_exists", attrLeaf("env", MatchNotExists, ""), []string{"billing"}},
		{
			"in",
			PolicyExpr{Attr: "team", Match: MatchIn, Values: []string{"orders", "nope"}},
			[]string{"ABC-api", "ABC-worker"},
		},
		{
			// The headline example: services starting ABC, team orders OR
			// payments, but NOT the sandbox ones.
			"full compound example",
			node("and",
				svcLeaf(MatchPrefix, "ABC"),
				node("or", attrLeaf("team", MatchEquals, "orders"), attrLeaf("team", MatchEquals, "payments")),
				node("not", attrLeaf("env", MatchEquals, "sandbox")),
			),
			[]string{"ABC-api"},
		},
		{"empty AND yields nothing", node("and"), []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eqSet(t, c.name, gotSet(c.expr), c.want)
		})
	}
}

func TestValidateExpr(t *testing.T) {
	ok := []PolicyExpr{
		svcLeaf(MatchPrefix, "ABC"),
		node("and", attrLeaf("a", MatchEquals, "1"), node("not", attrLeaf("b", MatchExists, ""))),
		{Attr: "x", Match: MatchIn, Values: []string{"a", "b"}},
	}
	for i, e := range ok {
		e := e
		if err := ValidateExpr(&e); err != nil {
			t.Errorf("valid[%d] rejected: %v", i, err)
		}
	}

	bad := []struct {
		name string
		expr PolicyExpr
	}{
		{"nil-ish empty leaf", PolicyExpr{}},
		{"unknown op", node("nand", svcLeaf(MatchEquals, "x"))},
		{"not needs one child", node("not", svcLeaf(MatchEquals, "a"), svcLeaf(MatchEquals, "b"))},
		{"and needs a child", node("and")},
		{"unknown match", PolicyExpr{Attr: "a", Match: "startswith", Value: "x"}},
		{"exists on service leaf", svcLeaf(MatchExists, "")},
		{"regex must compile", attrLeaf("a", MatchRegex, "(")},
		{"in needs values", PolicyExpr{Attr: "a", Match: MatchIn}},
		{"equals needs value", attrLeaf("a", MatchEquals, "")},
	}
	for _, c := range bad {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateExpr(&c.expr); err == nil {
				t.Errorf("%s: expected validation error, got nil", c.name)
			}
		})
	}

	// Depth bomb: a chain of NOTs past the cap must be rejected.
	deep := attrLeaf("a", MatchEquals, "1")
	for i := 0; i < exprMaxDepth+2; i++ {
		deep = node("not", deep)
	}
	if err := ValidateExpr(&deep); err == nil {
		t.Error("deep tree should be rejected")
	}
}
