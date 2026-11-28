// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// A span name is reserved when a span.name condition could select it,
// and every condition that cannot be read as a set of names reserves
// them all.
func TestSpanNameSelected(t *testing.T) {
	matcher := func(op integrations.Operator, v string) store.LogAttrFilter {
		return attrFilterFromMatcher(integrations.Matcher{Attribute: "span.name", Operator: op, Value: v})
	}
	cases := []struct {
		name    string
		filters []store.LogAttrFilter
		span    string
		want    bool
	}{
		{"equals, same name", []store.LogAttrFilter{matcher(integrations.OperatorEquals, "serialize")}, "serialize", true},
		{"equals, other name", []store.LogAttrFilter{matcher(integrations.OperatorEquals, "serialize")}, "compress", false},
		{"prefix", []store.LogAttrFilter{matcher(integrations.OperatorPrefix, "seri")}, "serialize", true},
		{"suffix", []store.LogAttrFilter{matcher(integrations.OperatorSuffix, "ize")}, "serialize", true},
		{"contains", []store.LogAttrFilter{matcher(integrations.OperatorContains, "rial")}, "serialize", true},
		{"regex", []store.LogAttrFilter{matcher(integrations.OperatorRegex, "^ser.*e$")}, "serialize", true},
		{"regex, no match", []store.LogAttrFilter{matcher(integrations.OperatorRegex, "^ord")}, "serialize", false},
		{"a negation reads every name", []store.LogAttrFilter{matcher(integrations.OperatorNotEquals, "heartbeat")}, "serialize", true},
		{"not_contains reads every name", []store.LogAttrFilter{matcher(integrations.OperatorNotContains, "x")}, "serialize", true},
		{"exists reads every name", []store.LogAttrFilter{matcher(integrations.OperatorExists, "")}, "serialize", true},
		{"not_exists selects none", []store.LogAttrFilter{matcher(integrations.OperatorNotExists, "")}, "serialize", false},
		{"a rule's eq", []store.LogAttrFilter{{Key: "span.name", Op: store.AttrOpEq, Value: "Error Detected"}}, "Error Detected", true},
		{"a rule with no op is eq", []store.LogAttrFilter{{Key: "span.name", Value: "a"}}, "b", false},
		{"a broken pattern reads every name", []store.LogAttrFilter{{Key: "span.name", Op: store.AttrOpMatches, Value: "("}}, "serialize", true},
		{"no filters", nil, "serialize", false},
	}
	for _, c := range cases {
		if got := spanNameSelected(c.filters, c.span); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
