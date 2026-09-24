// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integrations

import "testing"

func TestSelectsSlice(t *testing.T) {
	svc := func(name string, group int) Matcher {
		return Matcher{Attribute: ServiceNameAttribute, Operator: OperatorEquals, Value: name, MatchGroup: group}
	}
	dag := Matcher{Attribute: "airflow.dag_id", Operator: OperatorEquals, Value: "gl_posting"}
	cases := []struct {
		name string
		ms   []Matcher
		mode RuleMatch
		want bool
	}{
		{"no matchers", nil, RuleMatchAny, false},
		{"one service", []Matcher{svc("a", 0)}, RuleMatchAny, false},
		{"a legacy blank attribute is a service matcher", []Matcher{{Operator: OperatorEquals, Value: "a"}}, RuleMatchAny, false},
		{"several services, any", []Matcher{svc("a", 0), svc("b", 1)}, RuleMatchAny, false},
		{"a service and an attribute", []Matcher{svc("a", 0), dag}, RuleMatchAny, true},
		{"an attribute alone", []Matcher{dag}, RuleMatchAny, true},
		// Traces through both a AND b are a subset of a's traffic.
		{"several services, all", []Matcher{svc("a", 0), svc("b", 1)}, RuleMatchAll, true},
		{"all with one group is still the whole service", []Matcher{svc("a", 0)}, RuleMatchAll, false},
	}
	for _, c := range cases {
		if got := SelectsSlice(c.ms, c.mode); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
