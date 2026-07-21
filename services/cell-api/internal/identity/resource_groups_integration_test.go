// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Integration coverage for the resource ⇄ group attachment façade
// (RBAC v2 phase 1) against real Postgres. The properties that matter:
// replace-set semantics, strict isolation from other policy kinds (the
// CE surface must never clobber an EE org's richer policies), cross-org
// rejection, and system-instance grants resolving through the policy
// engine with the instance id reaching the expander.
package identity_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// system seeds a systems row (no store constructor for tests).
func seedSystem(f *authzFixture, name string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO systems (org_id, name) VALUES ($1, $2) RETURNING id`,
		f.org, name,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed system %s: %v", name, err)
	}
	return id
}

func TestResourceGroupAttachment(t *testing.T) {
	f := newAuthzFixture(t)

	u := f.user("attach@acme", identity.RoleViewer)
	g1 := f.group("team-a", u, identity.RoleViewer)
	g2 := f.group("team-b", u, identity.RoleViewer)
	integ := f.integration("orders")
	sys := seedSystem(f, "warehouse-rabbit")

	t.Run("replace-set on integration", func(t *testing.T) {
		if err := f.store.SetIntegrationGroups(f.ctx, f.org, integ, []uuid.UUID{g1, g2}); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, err := f.store.ListGroupsForIntegration(f.ctx, f.org, integ)
		if err != nil || len(got) != 2 {
			t.Fatalf("list after set = %v (err %v), want 2", got, err)
		}
		if err := f.store.SetIntegrationGroups(f.ctx, f.org, integ, []uuid.UUID{g2}); err != nil {
			t.Fatalf("replace: %v", err)
		}
		got, _ = f.store.ListGroupsForIntegration(f.ctx, f.org, integ)
		if len(got) != 1 || got[0].GroupID != g2 {
			t.Fatalf("replace-set kept wrong groups: %v", got)
		}
	})

	t.Run("façade never touches other policy kinds", func(t *testing.T) {
		// Seed richer policies on g1: a service policy and an expression
		// policy, plus a system-by-KIND policy (not instance).
		f.policy(g1, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "orders-api"})
		expr := identity.PolicyExpr{Match: identity.MatchPrefix, Value: "abc"}
		f.policy(g1, identity.AccessPolicyInput{Kind: identity.PolicyExpression, Conditions: &expr})
		f.policy(g1, identity.AccessPolicyInput{Kind: identity.PolicySystem, TargetSystemKind: "rabbitmq"})

		// Clearing the integration attachment must leave all three intact.
		if err := f.store.SetIntegrationGroups(f.ctx, f.org, integ, nil); err != nil {
			t.Fatalf("clear: %v", err)
		}
		policies, err := f.store.ListPoliciesForGroup(f.ctx, g1)
		if err != nil {
			t.Fatalf("list policies: %v", err)
		}
		if len(policies) != 3 {
			t.Fatalf("façade clobbered richer policies: %d left, want 3", len(policies))
		}
	})

	t.Run("cross-org group rejected atomically", func(t *testing.T) {
		var otherOrg uuid.UUID
		if err := f.pool.QueryRow(f.ctx,
			`INSERT INTO orgs (slug, name) VALUES ('rg-other', 'Other') RETURNING id`).Scan(&otherOrg); err != nil {
			t.Fatalf("seed org: %v", err)
		}
		var foreignGroup uuid.UUID
		if err := f.pool.QueryRow(f.ctx,
			`INSERT INTO groups (org_id, slug, name) VALUES ($1, 'foreign', 'Foreign') RETURNING id`,
			otherOrg).Scan(&foreignGroup); err != nil {
			t.Fatalf("seed foreign group: %v", err)
		}
		err := f.store.SetIntegrationGroups(f.ctx, f.org, integ, []uuid.UUID{g2, foreignGroup})
		if err == nil {
			t.Fatal("foreign group accepted — cross-org leak")
		}
		// Atomic: the valid g2 must not have been half-applied either way;
		// prior state (empty from the previous subtest) must survive.
		got, _ := f.store.ListGroupsForIntegration(f.ctx, f.org, integ)
		if len(got) != 0 {
			t.Fatalf("failed set was not atomic: %v", got)
		}
	})

	t.Run("system attachment grants member services via resolution", func(t *testing.T) {
		// Fresh user+group so earlier subtests' policies on g1/g2 don't
		// contribute to the visible set.
		u2 := f.user("attach-sys@acme", identity.RoleViewer)
		g3 := f.group("team-c", u2, identity.RoleViewer)
		if err := f.store.SetSystemGroups(f.ctx, f.org, sys, []uuid.UUID{g3}); err != nil {
			t.Fatalf("set system groups: %v", err)
		}
		got, err := f.store.ListGroupsForSystem(f.ctx, f.org, sys)
		if err != nil || len(got) != 1 || got[0].GroupID != g3 {
			t.Fatalf("system groups = %v (err %v)", got, err)
		}
		// Resolution must hand the INSTANCE id to the system expander.
		var sawID *uuid.UUID
		expandSys := func(_ context.Context, _ uuid.UUID, kind string, id *uuid.UUID) ([]string, error) {
			if id != nil {
				sawID = id
				return []string{"broker-1", "broker-2"}, nil
			}
			return nil, nil
		}
		universe := func(context.Context, uuid.UUID) ([]string, error) {
			return []string{"broker-1", "broker-2", "unrelated"}, nil
		}
		set, wildcard, err := f.store.ResolveVisibleServiceSet(f.ctx, u2, f.org, stubExpand, expandSys, universe)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if sawID == nil || *sawID != sys {
			t.Fatalf("expander never saw the system instance id (saw %v, want %v)", sawID, sys)
		}
		assertVisible(t, set, wildcard, false, "broker-1", "broker-2")
	})
}

// TestResolveAccessSets covers the two-tier Visible/Managed resolution
// (RBAC v2 phase 2): viewer-groups grant visibility only, editor-groups
// grant manage; Managed ⊆ Visible; an editor-held all_org policy yields
// the managed wildcard.
func TestResolveAccessSets(t *testing.T) {
	f := newAuthzFixture(t)
	universe := func(context.Context, uuid.UUID) ([]string, error) {
		return []string{"svc-a", "svc-b", "svc-c"}, nil
	}
	resolve := func(u uuid.UUID) identity.AccessSets {
		t.Helper()
		sets, err := f.store.ResolveAccessSets(f.ctx, u, f.org, stubExpand, stubExpandSys, universe)
		if err != nil {
			t.Fatalf("ResolveAccessSets: %v", err)
		}
		return sets
	}

	t.Run("viewer group grants visibility, not manage", func(t *testing.T) {
		u := f.user("sets-viewer@acme", identity.RoleViewer)
		g := f.group("sets-g1", u, identity.RoleViewer)
		f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "svc-a"})
		sets := resolve(u)
		assertVisible(t, sets.Visible, sets.VisibleAll, false, "svc-a")
		assertVisible(t, sets.Managed, sets.ManagedAll, false)
	})

	t.Run("editor group grants manage of its scope only", func(t *testing.T) {
		u := f.user("sets-editor@acme", identity.RoleViewer)
		gE := f.group("sets-g2e", u, identity.RoleEditor)
		gV := f.group("sets-g2v", u, identity.RoleViewer)
		f.policy(gE, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "svc-a"})
		f.policy(gV, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "svc-b"})
		sets := resolve(u)
		assertVisible(t, sets.Visible, sets.VisibleAll, false, "svc-a", "svc-b")
		assertVisible(t, sets.Managed, sets.ManagedAll, false, "svc-a")
	})

	t.Run("editor all_org yields managed wildcard", func(t *testing.T) {
		u := f.user("sets-wild@acme", identity.RoleViewer)
		g := f.group("sets-g3", u, identity.RoleEditor)
		f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicyAllOrg})
		sets := resolve(u)
		if !sets.VisibleAll || !sets.ManagedAll {
			t.Fatalf("expected wildcard both tiers, got visibleAll=%v managedAll=%v", sets.VisibleAll, sets.ManagedAll)
		}
	})

	t.Run("expression policy in editor group is managed", func(t *testing.T) {
		u := f.user("sets-expr@acme", identity.RoleViewer)
		g := f.group("sets-g4", u, identity.RoleEditor)
		expr := identity.PolicyExpr{Match: identity.MatchPrefix, Value: "svc-"}
		f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicyExpression, Conditions: &expr})
		sets := resolve(u)
		assertVisible(t, sets.Managed, sets.ManagedAll, false, "svc-a", "svc-b", "svc-c")
	})
}

// TestResourceShares covers phase 3: shares grant Visible (never
// Managed), reach users directly and through groups, and stay inside
// the org.
func TestResourceShares(t *testing.T) {
	f := newAuthzFixture(t)
	integ := f.integration("shared-orders")
	sys := seedSystem(f, "shared-rabbit")

	expand := func(_ context.Context, _ uuid.UUID, id uuid.UUID) ([]string, error) {
		if id == integ {
			return []string{"orders-api"}, nil
		}
		return nil, nil
	}
	expandSys := func(_ context.Context, _ uuid.UUID, _ string, id *uuid.UUID) ([]string, error) {
		if id != nil && *id == sys {
			return []string{"rabbit-1"}, nil
		}
		return nil, nil
	}
	universe := func(context.Context, uuid.UUID) ([]string, error) {
		return []string{"orders-api", "rabbit-1", "other"}, nil
	}

	t.Run("direct user share grants Visible not Managed", func(t *testing.T) {
		u := f.user("share-direct@acme", identity.RoleViewer)
		if _, err := f.store.CreateShare(f.ctx, f.org, identity.ShareIntegration, integ, "user", u, nil); err != nil {
			t.Fatalf("create share: %v", err)
		}
		sets, err := f.store.ResolveAccessSets(f.ctx, u, f.org, expand, expandSys, universe)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		assertVisible(t, sets.Visible, sets.VisibleAll, false, "orders-api")
		assertVisible(t, sets.Managed, sets.ManagedAll, false)
	})

	t.Run("group share reaches members; system parity", func(t *testing.T) {
		u := f.user("share-group@acme", identity.RoleViewer)
		g := f.group("share-team", u, identity.RoleEditor) // even editor: share stays view-only
		if _, err := f.store.CreateShare(f.ctx, f.org, identity.ShareSystem, sys, "group", g, nil); err != nil {
			t.Fatalf("create group share: %v", err)
		}
		sets, err := f.store.ResolveAccessSets(f.ctx, u, f.org, expand, expandSys, universe)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		assertVisible(t, sets.Visible, sets.VisibleAll, false, "rabbit-1")
		// Editor role in the SHARE-receiving group grants no manage —
		// shares are not policies.
		assertVisible(t, sets.Managed, sets.ManagedAll, false)
	})

	t.Run("duplicate share rejected, delete works", func(t *testing.T) {
		u := f.user("share-dup@acme", identity.RoleViewer)
		id1, err := f.store.CreateShare(f.ctx, f.org, identity.ShareIntegration, integ, "user", u, nil)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := f.store.CreateShare(f.ctx, f.org, identity.ShareIntegration, integ, "user", u, nil); err != identity.ErrShareExists {
			t.Fatalf("duplicate accepted: %v", err)
		}
		if err := f.store.DeleteShare(f.ctx, f.org, identity.ShareIntegration, integ, id1); err != nil {
			t.Fatalf("delete: %v", err)
		}
	})

	t.Run("cross-org share invisible", func(t *testing.T) {
		var orgB uuid.UUID
		if err := f.pool.QueryRow(f.ctx,
			`INSERT INTO orgs (slug, name) VALUES ('share-b', 'B') RETURNING id`).Scan(&orgB); err != nil {
			t.Fatalf("seed org B: %v", err)
		}
		u := f.user("share-xorg@acme", identity.RoleViewer)
		// A share row in ORG B naming our org-A user must not leak into
		// org-A resolution.
		if _, err := f.store.CreateShare(f.ctx, orgB, identity.ShareIntegration, integ, "user", u, nil); err != nil {
			t.Fatalf("seed cross-org share: %v", err)
		}
		sets, err := f.store.ResolveAccessSets(f.ctx, u, f.org, expand, expandSys, universe)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		assertVisible(t, sets.Visible, sets.VisibleAll, false)
	})
}

// TestPerSignalVisibility covers phase 4: signal-narrowed policies grant
// their scope only for the named signals, never contribute to Managed,
// and the union (nav) tier still includes the services.
func TestPerSignalVisibility(t *testing.T) {
	f := newAuthzFixture(t)
	universe := func(context.Context, uuid.UUID) ([]string, error) {
		return []string{"svc-logs", "svc-full", "svc-other"}, nil
	}

	u := f.user("signals@acme", identity.RoleViewer)
	g := f.group("signals-team", u, identity.RoleEditor)
	// Logs-only grant on svc-logs; full-signal grant on svc-full.
	f.policy(g, identity.AccessPolicyInput{
		Kind: identity.PolicyService, TargetServiceName: "svc-logs",
		Signals: []identity.Signal{identity.SignalLogs},
	})
	f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "svc-full"})

	sets, err := f.store.ResolveAccessSets(f.ctx, u, f.org, stubExpand, stubExpandSys, universe)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Union/nav tier sees both.
	assertVisible(t, sets.Visible, sets.VisibleAll, false, "svc-logs", "svc-full")

	// Logs signal sees both; traces/metrics/messages see only the
	// full-signal service.
	logsSet, logsAll := sets.VisibleFor(identity.SignalLogs)
	assertVisible(t, logsSet, logsAll, false, "svc-logs", "svc-full")
	for _, sig := range []identity.Signal{identity.SignalTraces, identity.SignalMetrics, identity.SignalMessages} {
		set, all := sets.VisibleFor(sig)
		assertVisible(t, set, all, false, "svc-full")
	}

	// Managed: the signal-narrowed policy contributes NOTHING even though
	// the group role is editor; the full-signal one manages.
	assertVisible(t, sets.Managed, sets.ManagedAll, false, "svc-full")

	// "All four signals" normalises to nil at validation → still manages.
	u2 := f.user("signals-all4@acme", identity.RoleViewer)
	g2 := f.group("signals-all4", u2, identity.RoleEditor)
	f.policy(g2, identity.AccessPolicyInput{
		Kind: identity.PolicyService, TargetServiceName: "svc-other",
		Signals: []identity.Signal{identity.SignalTraces, identity.SignalLogs, identity.SignalMetrics, identity.SignalMessages},
	})
	sets2, err := f.store.ResolveAccessSets(f.ctx, u2, f.org, stubExpand, stubExpandSys, universe)
	if err != nil {
		t.Fatalf("resolve all4: %v", err)
	}
	assertVisible(t, sets2.Managed, sets2.ManagedAll, false, "svc-other")

	// Unknown signal rejected at validation.
	if _, err := f.store.CreatePolicy(f.ctx, g2, identity.AccessPolicyInput{
		Kind: identity.PolicyService, TargetServiceName: "svc-other",
		Signals: []identity.Signal{"spans"},
	}); err == nil {
		t.Fatal("unknown signal accepted")
	}
}
