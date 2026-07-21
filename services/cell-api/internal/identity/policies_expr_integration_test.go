// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Integration coverage for kind='expression' access policies against a
// real Postgres. Where policies_expr_test.go unit-tests the evaluator on
// an in-memory fixture, this exercises the WHOLE stack: CreatePolicy
// marshals the tree to JSONB → the row round-trips → ResolveVisibleService
// Set loads the org's resource attributes via SQL → evalExpr composes the
// result → it UNIONs with the user's other policies. Every operator, the
// boolean combinators, absence-under-negation, cross-group union, and
// cross-org isolation are asserted end to end.
//
// Run with: go test -tags integration ./services/cell-api/internal/identity/...
package identity_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// exprFixture seeds a fixed service catalog with resource attributes and
// returns a resolver that, given one expression, creates a fresh
// viewer+group bound to that expression policy and returns the visible
// service set. Each resolve is isolated: a new user sees only their own
// group's single policy.
type exprFixture struct {
	f        *authzFixture
	universe []string
	n        int
}

func newExprFixture(t *testing.T) *exprFixture {
	f := newAuthzFixture(t)
	// Service catalog + attributes. "billing" deliberately has NO
	// attributes so absence semantics are exercised against a real row-
	// less service.
	f.attr("abc-api", "team", "orders")
	f.attr("abc-api", "env", "prod")
	f.attr("abc-worker", "team", "orders")
	f.attr("abc-worker", "env", "sandbox")
	f.attr("abc-batch", "team", "payments")
	f.attr("abc-batch", "env", "prod")
	f.attr("xyz-api", "team", "payments")
	f.attr("xyz-api", "env", "prod")
	return &exprFixture{
		f:        f,
		universe: []string{"abc-api", "abc-worker", "abc-batch", "xyz-api", "billing"},
	}
}

func stubExpand(context.Context, uuid.UUID, uuid.UUID) ([]string, error) { return nil, nil }
func stubExpandSys(context.Context, uuid.UUID, string, *uuid.UUID) ([]string, error) {
	return nil, nil
}

func (x *exprFixture) universeProvider(context.Context, uuid.UUID) ([]string, error) {
	return x.universe, nil
}

// resolve creates a fresh viewer + group carrying exactly `expr` and
// returns the sorted visible service set.
func (x *exprFixture) resolve(t *testing.T, expr identity.PolicyExpr) []string {
	t.Helper()
	x.n++
	u := x.f.user(fmt.Sprintf("expr%d@acme", x.n), identity.RoleViewer)
	g := x.f.group(fmt.Sprintf("exprg%d", x.n), u, identity.RoleViewer)
	x.f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicyExpression, Conditions: &expr})
	set, wildcard, err := x.f.store.ResolveVisibleServiceSet(
		x.f.ctx, u, x.f.org, stubExpand, stubExpandSys, x.universeProvider)
	if err != nil {
		t.Fatalf("ResolveVisibleServiceSet: %v", err)
	}
	if wildcard {
		t.Fatalf("expression policy must not yield wildcard")
	}
	return keys(set)
}

// tree-builder shorthands.
func svc(match, value string) identity.PolicyExpr {
	return identity.PolicyExpr{Match: match, Value: value}
}
func at(k, match, value string) identity.PolicyExpr {
	return identity.PolicyExpr{Attr: k, Match: match, Value: value}
}
func and(kids ...identity.PolicyExpr) identity.PolicyExpr {
	return identity.PolicyExpr{Op: "and", Children: kids}
}
func or(kids ...identity.PolicyExpr) identity.PolicyExpr {
	return identity.PolicyExpr{Op: "or", Children: kids}
}
func not(kid identity.PolicyExpr) identity.PolicyExpr {
	return identity.PolicyExpr{Op: "not", Children: []identity.PolicyExpr{kid}}
}

func wantSet(t *testing.T, name string, got []string, want ...string) {
	t.Helper()
	set := map[string]struct{}{}
	for _, g := range got {
		set[g] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("%s: got %v want %v", name, got, want)
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
	}
}

func TestExpressionPoliciesEndToEnd(t *testing.T) {
	x := newExprFixture(t)

	cases := []struct {
		name string
		expr identity.PolicyExpr
		want []string
	}{
		// Service-name operators.
		{"service prefix", svc(identity.MatchPrefix, "abc-"), []string{"abc-api", "abc-worker", "abc-batch"}},
		{"service suffix", svc(identity.MatchSuffix, "-api"), []string{"abc-api", "xyz-api"}},
		{"service contains", svc(identity.MatchContains, "bill"), []string{"billing"}},
		{"service regex", svc(identity.MatchRegex, "^abc-(api|batch)$"), []string{"abc-api", "abc-batch"}},
		{"service equals", svc(identity.MatchEquals, "xyz-api"), []string{"xyz-api"}},
		{"service in", identity.PolicyExpr{Match: identity.MatchIn, Values: []string{"billing", "xyz-api"}}, []string{"billing", "xyz-api"}},

		// Attribute operators.
		{"attr equals", at("team", identity.MatchEquals, "orders"), []string{"abc-api", "abc-worker"}},
		{"attr in", identity.PolicyExpr{Attr: "team", Match: identity.MatchIn, Values: []string{"payments"}}, []string{"abc-batch", "xyz-api"}},
		{"attr exists", at("env", identity.MatchExists, ""), []string{"abc-api", "abc-worker", "abc-batch", "xyz-api"}},
		{"attr not_exists", at("env", identity.MatchNotExists, ""), []string{"billing"}},

		// Absence-under-negation: both readings.
		{"not_equals includes absence", at("env", identity.MatchNotEquals, "prod"), []string{"abc-worker", "billing"}},
		{"NOT(equals) includes absence", not(at("env", identity.MatchEquals, "prod")), []string{"abc-worker", "billing"}},
		{"exists AND not_equals excludes absence", and(at("env", identity.MatchExists, ""), at("env", identity.MatchNotEquals, "prod")), []string{"abc-worker"}},

		// Boolean composition.
		{"OR", or(at("team", identity.MatchEquals, "orders"), at("team", identity.MatchEquals, "payments")),
			[]string{"abc-api", "abc-worker", "abc-batch", "xyz-api"}},
		{"AND", and(svc(identity.MatchPrefix, "abc-"), at("env", identity.MatchEquals, "prod")),
			[]string{"abc-api", "abc-batch"}},
		{
			"headline: prefix AND (team orders|payments) AND NOT sandbox",
			and(
				svc(identity.MatchPrefix, "abc-"),
				or(at("team", identity.MatchEquals, "orders"), at("team", identity.MatchEquals, "payments")),
				not(at("env", identity.MatchEquals, "sandbox")),
			),
			[]string{"abc-api", "abc-batch"},
		},

		// Fail-closed: matches nothing → empty set.
		{"no match yields empty", svc(identity.MatchPrefix, "zzz"), []string{}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantSet(t, c.name, x.resolve(t, c.expr), c.want...)
		})
	}
}

// TestExpressionUnionAcrossGroups proves an expression policy on one group
// UNIONs with a different-kind policy on another group the same user is in.
func TestExpressionUnionAcrossGroups(t *testing.T) {
	x := newExprFixture(t)
	u := x.f.user("multi@acme", identity.RoleViewer)

	// Group 1: expression → services starting "abc-".
	g1 := x.f.group("g-expr", u, identity.RoleViewer)
	expr := svc(identity.MatchPrefix, "abc-")
	x.f.policy(g1, identity.AccessPolicyInput{Kind: identity.PolicyExpression, Conditions: &expr})

	// Group 2: plain service policy → billing.
	g2 := x.f.group("g-svc", u, identity.RoleViewer)
	x.f.policy(g2, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "billing"})

	set, wildcard, err := x.f.store.ResolveVisibleServiceSet(
		x.f.ctx, u, x.f.org, stubExpand, stubExpandSys, x.universeProvider)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	assertVisible(t, set, wildcard, false, "abc-api", "abc-worker", "abc-batch", "billing")
}

// TestExpressionCrossOrgIsolation proves a NOT (which complements against
// the org universe) and attribute matching never surface another org's
// services, even when names/attributes collide.
func TestExpressionCrossOrgIsolation(t *testing.T) {
	x := newExprFixture(t)

	// A second org with a service that has env=prod, same as org A's.
	var orgB uuid.UUID
	if err := x.f.pool.QueryRow(x.f.ctx,
		`INSERT INTO orgs (slug, name) VALUES ('orgb', 'Org B') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatalf("seed org B: %v", err)
	}
	if _, err := x.f.pool.Exec(x.f.ctx,
		`INSERT INTO service_resource_attributes (org_id, service_name, attr_key, attr_value)
		 VALUES ($1, 'secret-b', 'env', 'prod')`, orgB); err != nil {
		t.Fatalf("seed org B attr: %v", err)
	}

	// Org-A user whose expression is "attr env = prod": must match org A's
	// prod services only — never secret-b.
	got := x.resolve(t, at("env", identity.MatchEquals, "prod"))
	wantSet(t, "cross-org equals", got, "abc-api", "abc-batch", "xyz-api")

	// A NOT expression complements against org A's universe only.
	gotNot := x.resolve(t, not(at("env", identity.MatchEquals, "nonexistent")))
	wantSet(t, "cross-org NOT", gotNot, "abc-api", "abc-worker", "abc-batch", "xyz-api", "billing")
	for _, s := range gotNot {
		if s == "secret-b" {
			t.Fatal("cross-org leak: secret-b visible to org A user")
		}
	}
}
