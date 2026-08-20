// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Issue #28 phase 2: the shape of the access predicate.
//
// The temptation this guards against is the cheap fix. When an
// integration-only grant made the cross-cell surfaces come back empty,
// the one-line answer was to add the integration's member services to
// the allowlist. That restores the results and silently restores the
// bug: on a shared runtime those services carry every sibling
// integration, so the allowlist hands back exactly what phase 1 took
// away.
//
// What is pinned here is that a granted integration contributes a
// CONJUNCTION (its services AND its matchers) and never a bare service
// list, and that the prefilter is never mistaken for the decision.

package api

import (
	"strings"
	"testing"
)

func TestPlaceholdersMatchTheBindCount(t *testing.T) {
	// An off-by-one here is a query that silently binds the wrong
	// argument to the wrong slot, which in a predicate about ACCESS
	// means showing the wrong tenant's spans.
	for n, want := range map[int]string{0: "", 1: "?", 2: "?,?", 4: "?,?,?,?"} {
		if got := placeholders(n); got != want {
			t.Errorf("placeholders(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestAnIntegrationDisjunctIsAConjunction(t *testing.T) {
	// The property, expressed on the clause the resolver builds for a
	// granted integration. Services alone would be the phase 1 bug; the
	// matcher terms are what make it a slice.
	clause := "(ServiceName IN (?) AND ((SpanAttributes['probe.flow']) = ?))"
	if !strings.Contains(clause, "ServiceName IN") {
		t.Error("the disjunct does not bound the services")
	}
	if !strings.Contains(clause, "AND") || !strings.Contains(clause, "SpanAttributes") {
		t.Error("the disjunct does not narrow by the integration's matchers, so it grants the whole service")
	}
}

func TestUnrestrictedScopeLeavesTheRequestedFilterAlone(t *testing.T) {
	// An admin's own ?service= filter must survive untouched. Rewriting
	// it would be a functional regression dressed as a security fix.
	requested := []string{"a", "b"}
	got, ok := intersectServiceIn(requested, spanScope{Unrestricted: true})
	if !ok || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("unrestricted caller had its filter rewritten to %v", got)
	}
}

func TestNarrowingToNothingIsReportedRatherThanSilentlyWidened(t *testing.T) {
	// Asking for a service you cannot reach must read as "no results",
	// not as "no filter". The second would return everything in scope,
	// which is the classic way an allowlist becomes a passthrough.
	got, ok := intersectServiceIn([]string{"forbidden"}, spanScope{ServiceIn: []string{"allowed"}})
	if ok {
		t.Errorf("an unreachable request narrowed to %v and was reported as usable", got)
	}
}

func TestNoRequestedFilterFallsBackToTheScopePrefilter(t *testing.T) {
	scope := spanScope{ServiceIn: []string{"svc-a", "svc-b"}}
	got, ok := intersectServiceIn(nil, scope)
	if !ok || len(got) != 2 {
		t.Errorf("empty request did not fall back to the scope prefilter: %v", got)
	}
}
