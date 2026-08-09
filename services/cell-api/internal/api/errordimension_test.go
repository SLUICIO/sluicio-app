// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The bug this feature exists for is a breakdown that is technically
// correct and useless. These tests pin the cases where that happens.

package api

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
)

func m(attr string) integrations.Matcher {
	return integrations.Matcher{Attribute: attr}
}

func TestDefiningAttributesIgnoresMembership(t *testing.T) {
	// service.name defines WHICH services belong, not which slice of
	// their traffic. Breaking down by it would reproduce the service
	// breakdown under a different name.
	got := DefiningAttributes([]integrations.Matcher{
		m("service.name"),
		m("node_red.flow.id"),
	})
	if len(got) != 1 || got[0] != "node_red.flow.id" {
		t.Fatalf("got %v, want [node_red.flow.id]", got)
	}
}

func TestDefiningAttributesAreStable(t *testing.T) {
	// Matcher rows come back in whatever order the database returns
	// them. A breakdown that regrouped between page loads would be
	// untrustworthy even while every individual number was right.
	a := DefiningAttributes([]integrations.Matcher{m("zeta"), m("alpha"), m("mid")})
	b := DefiningAttributes([]integrations.Matcher{m("mid"), m("zeta"), m("alpha")})
	if len(a) != 3 || a[0] != "alpha" || a[2] != "zeta" {
		t.Fatalf("not sorted: %v", a)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("order depends on input: %v vs %v", a, b)
		}
	}
}

func TestDefiningAttributesDedupes(t *testing.T) {
	// One key constrained by several matchers (a DNF group, or a range)
	// is still one dimension.
	got := DefiningAttributes([]integrations.Matcher{m("flow"), m("flow"), m("flow")})
	if len(got) != 1 {
		t.Fatalf("got %v, want one", got)
	}
}

func TestDefiningAttributesSkipsBlanks(t *testing.T) {
	got := DefiningAttributes([]integrations.Matcher{m(""), m("   "), m("real")})
	if len(got) != 1 || got[0] != "real" {
		t.Fatalf("got %v, want [real]", got)
	}
}

func TestSeveralServicesKeepsTheServiceBreakdown(t *testing.T) {
	// The existing question is the right one here, and the existing
	// answer is useful. This feature must not degrade it.
	c := ChooseErrorDimension(4, []string{"node_red.flow.id"}, true)
	if c.Kind != ErrorDimService {
		t.Fatalf("got %q, want service", c.Kind)
	}
	if c.AttributeKey != "" {
		t.Errorf("service breakdown must not carry an attribute key: %q", c.AttributeKey)
	}
}

func TestOneServiceWithADiscriminatingAttributeSplitsByIt(t *testing.T) {
	// The headline case: a Node-RED runtime matched by a regex or an
	// `in` list across several flows.
	c := ChooseErrorDimension(1, []string{"node_red.flow.id"}, true)
	if c.Kind != ErrorDimAttribute {
		t.Fatalf("got %q, want attribute", c.Kind)
	}
	if c.AttributeKey != "node_red.flow.id" {
		t.Fatalf("got key %q", c.AttributeKey)
	}
}

func TestOneServiceWithAPinnedAttributeFallsBackToSpan(t *testing.T) {
	// The COMMON case, and the one most likely to be got wrong. Most
	// attribute-defined integrations pin their attribute to a single
	// value with `equals`, so grouping by it would give exactly one row
	// again — the original bug wearing a new label.
	c := ChooseErrorDimension(1, []string{"node_red.flow.id"}, false)
	if c.Kind != ErrorDimSpan {
		t.Fatalf("got %q, want span", c.Kind)
	}
	if c.AttributeKey != "" {
		t.Errorf("span breakdown must not carry an attribute key: %q", c.AttributeKey)
	}
}

func TestOneServiceWithNoDefiningAttributeSplitsBySpan(t *testing.T) {
	// A plain single-service integration. "100% from the one service"
	// is just as useless here, and the operation is still actionable.
	c := ChooseErrorDimension(1, nil, false)
	if c.Kind != ErrorDimSpan {
		t.Fatalf("got %q, want span", c.Kind)
	}
}

func TestEveryChoiceExplainsItself(t *testing.T) {
	// The breakdown's meaning changes between integrations, so the
	// reason travels with it. A number whose meaning is implicit is not
	// interpretable.
	for _, c := range []ErrorDimensionChoice{
		ChooseErrorDimension(3, nil, false),
		ChooseErrorDimension(1, []string{"k"}, true),
		ChooseErrorDimension(1, nil, false),
	} {
		if c.Reason == "" {
			t.Errorf("%q has no reason", c.Kind)
		}
	}
}
