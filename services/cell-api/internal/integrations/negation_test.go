// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integrations

import "testing"

// exists / not_exists carry no value, so the empty-value rule that
// protects every other operator must not reject them - and they need an
// attribute, since "the service name is absent" is not a question and a
// matcher with no attribute is taken for a service matcher.
func TestValuelessOperatorsNeedAnAttributeAndNoValue(t *testing.T) {
	for _, op := range []Operator{OperatorExists, OperatorNotExists} {
		ok := Matcher{Attribute: "rpc.service", Operator: op}
		if err := ok.Validate(); err != nil {
			t.Errorf("%s with an attribute and no value was rejected: %v", op, err)
		}
		bare := Matcher{Operator: op}
		if err := bare.Validate(); err == nil {
			t.Errorf("%s with no attribute was accepted; it would read as a service matcher", op)
		}
	}
}

// The protection every other operator still needs: an empty value would
// match every row.
func TestValuedOperatorsStillRejectAnEmptyValue(t *testing.T) {
	for _, op := range []Operator{OperatorEquals, OperatorNotEquals, OperatorContains, OperatorNotContains, OperatorPrefix, OperatorRegex} {
		if err := (Matcher{Attribute: "k", Operator: op}).Validate(); err == nil {
			t.Errorf("%s accepted an empty value", op)
		}
	}
}

func TestNegatedServiceMatchers(t *testing.T) {
	ne := Matcher{Operator: OperatorNotEquals, Value: "checkout-api"}
	if ne.Match("checkout-api") {
		t.Error("not_equals matched the excluded name")
	}
	if !ne.Match("orders") {
		t.Error("not_equals did not match a different name")
	}
	nc := Matcher{Operator: OperatorNotContains, Value: "test"}
	if nc.Match("integration-test-svc") {
		t.Error("not_contains matched a name containing the value")
	}
	if !nc.Match("orders") {
		t.Error("not_contains did not match a name without the value")
	}
}

// A valueless operator must never reach the service-name path. Validate
// keeps it out, and Match fails closed if one ever does.
func TestValuelessOperatorsDoNotMatchAServiceName(t *testing.T) {
	for _, op := range []Operator{OperatorExists, OperatorNotExists} {
		if (Matcher{Operator: op}).Match("anything") {
			t.Errorf("%s matched a service name", op)
		}
	}
}

func TestAllOperatorsCoversTheNewOnes(t *testing.T) {
	seen := map[Operator]bool{}
	for _, o := range AllOperators {
		seen[o] = true
	}
	for _, want := range []Operator{OperatorNotEquals, OperatorNotContains, OperatorExists, OperatorNotExists} {
		if !seen[want] {
			t.Errorf("AllOperators is missing %s, so validation and the UI hints will not know it", want)
		}
	}
}
