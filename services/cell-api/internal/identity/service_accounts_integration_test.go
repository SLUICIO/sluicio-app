// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Integration test for service-account scoping
// (docs/service-account-scoping-design.md): service accounts are group
// members, and the SAME policy resolver that scopes users scopes them —
// keyed by MemberRef instead of a raw user id. Companion to
// groups_integration_test.go; run with `make test-integration`.
package identity_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

func TestServiceAccountScoping(t *testing.T) {
	f := newAuthzFixture(t)

	expand := func(_ context.Context, _ uuid.UUID, _ uuid.UUID) ([]string, error) { return nil, nil }
	expandSystem := func(_ context.Context, _ uuid.UUID, _ string, _ *uuid.UUID) ([]string, error) { return nil, nil }
	universe := func(_ context.Context, _ uuid.UUID) ([]string, error) {
		return []string{"orders-api", "billing-api", "web"}, nil
	}
	resolveSA := func(saID uuid.UUID) (map[string]struct{}, bool) {
		t.Helper()
		set, wildcard, err := f.store.ResolveVisibleServiceSetMember(
			f.ctx, identity.ServiceAccountRef(saID), f.org, expand, expandSystem, universe)
		if err != nil {
			t.Fatalf("ResolveVisibleServiceSetMember: %v", err)
		}
		return set, wildcard
	}

	sa, err := f.store.CreateServiceAccount(f.ctx, f.org, "ci-bot", "", identity.RoleViewer, identity.SAScopeScoped, nil)
	if err != nil {
		t.Fatalf("create service account: %v", err)
	}
	if sa.Scope != identity.SAScopeScoped {
		t.Fatalf("scope = %q, want scoped", sa.Scope)
	}

	t.Run("group-less SA resolves to nothing", func(t *testing.T) {
		set, wildcard := resolveSA(sa.ID)
		if wildcard || len(set) != 0 {
			t.Fatalf("group-less SA: set=%v wildcard=%v, want empty/false", keys(set), wildcard)
		}
	})

	// A group with a service policy; a USER member proves the two
	// membership kinds resolve independently through the same rows.
	user := f.user("teammate@acme", identity.RoleViewer)
	gid := f.group("payments", user, identity.RoleViewer)
	f.policy(gid, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "orders-api"})

	t.Run("membership grants the group's scope", func(t *testing.T) {
		if err := f.store.AddGroupServiceAccount(f.ctx, sa.ID, gid, identity.RoleViewer); err != nil {
			t.Fatalf("add group service account: %v", err)
		}
		set, wildcard := resolveSA(sa.ID)
		if wildcard || len(set) != 1 {
			t.Fatalf("scoped SA: set=%v wildcard=%v, want exactly orders-api", keys(set), wildcard)
		}
		if _, ok := set["orders-api"]; !ok {
			t.Fatalf("scoped SA missing orders-api: %v", keys(set))
		}
	})

	t.Run("duplicate membership is ErrAlreadyMember", func(t *testing.T) {
		if err := f.store.AddGroupServiceAccount(f.ctx, sa.ID, gid, identity.RoleViewer); err != identity.ErrAlreadyMember {
			t.Fatalf("duplicate add: err=%v, want ErrAlreadyMember", err)
		}
	})

	t.Run("member list carries both kinds", func(t *testing.T) {
		members, err := f.store.ListGroupMembers(f.ctx, gid)
		if err != nil {
			t.Fatalf("list group members: %v", err)
		}
		var users, sas int
		for _, m := range members {
			if m.User != nil {
				users++
			}
			if m.ServiceAccount != nil {
				sas++
				if m.ServiceAccount.ID != sa.ID {
					t.Fatalf("unexpected SA member %s", m.ServiceAccount.ID)
				}
			}
		}
		if users != 1 || sas != 1 {
			t.Fatalf("members: users=%d sas=%d, want 1/1", users, sas)
		}
	})

	t.Run("SA group listing", func(t *testing.T) {
		groups, err := f.store.ListGroupsForServiceAccount(f.ctx, sa.ID, f.org)
		if err != nil {
			t.Fatalf("list SA groups: %v", err)
		}
		if len(groups) != 1 || groups[0].Group.ID != gid || groups[0].Role != identity.RoleViewer {
			t.Fatalf("SA groups = %+v, want [payments/viewer]", groups)
		}
	})

	t.Run("user resolution unaffected by SA rows", func(t *testing.T) {
		set, wildcard, err := f.store.ResolveVisibleServiceSet(f.ctx, user, f.org, expand, expandSystem, universe)
		if err != nil {
			t.Fatalf("ResolveVisibleServiceSet: %v", err)
		}
		if wildcard || len(set) != 1 {
			t.Fatalf("user set=%v wildcard=%v, want exactly orders-api", keys(set), wildcard)
		}
	})

	t.Run("removal restores deny-by-default", func(t *testing.T) {
		if err := f.store.RemoveGroupServiceAccount(f.ctx, sa.ID, gid); err != nil {
			t.Fatalf("remove group service account: %v", err)
		}
		set, _ := resolveSA(sa.ID)
		if len(set) != 0 {
			t.Fatalf("after removal: set=%v, want empty", keys(set))
		}
	})

	t.Run("deleting the SA cascades its memberships", func(t *testing.T) {
		if err := f.store.AddGroupServiceAccount(f.ctx, sa.ID, gid, identity.RoleViewer); err != nil {
			t.Fatalf("re-add group service account: %v", err)
		}
		if err := f.store.DeleteServiceAccount(f.ctx, f.org, sa.ID); err != nil {
			t.Fatalf("delete service account: %v", err)
		}
		members, err := f.store.ListGroupMembers(f.ctx, gid)
		if err != nil {
			t.Fatalf("list group members: %v", err)
		}
		for _, m := range members {
			if m.ServiceAccount != nil {
				t.Fatalf("membership survived SA delete: %s", m.ServiceAccount.ID)
			}
		}
	})
}
