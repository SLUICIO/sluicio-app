// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Integration test for the role + team (group) authorization model
// against a real Postgres (testcontainers). This is the companion to the
// tenant-isolation guard: that one proves org_id scoping; this one proves
// the two authorization DECISIONS the auth layer makes inside an org:
//
//   - CanUserWriteAnywhere — the write gate. True for org admin/editor,
//     OR for an org-viewer who is editor/admin in ANY group. This is what
//     lets a NOC-viewer who owns "Team Payments" still create resources.
//
//   - ResolveVisibleServiceSet — the read/visibility filter. For a
//     non-admin, the set of services they can see is the UNION of every
//     access policy on every group they belong to (six policy kinds).
//
// NOTE on the org-admin bypass: an org admin sees *everything*, but that
// wildcard is applied in the HANDLER (visibleServiceChecker →
// ReadRole().CanAdmin()), NOT in ResolveVisibleServiceSet — the store
// engine only knows about group policies. So this test asserts the group
// engine directly and does not expect admin to short-circuit here.
//
// Run with:
//
//	go test -tags integration ./services/cell-api/internal/identity/...
//
// or `make test-integration`.
package identity_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/integration-monitor/integration-monitor/pkg/postgres"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/migrations"
)

// newAuthzDB brings up a throwaway migrated Postgres and returns the
// identity Store plus the underlying pool (needed for the few rows —
// orgs, integrations, service_resource_attributes — with no store
// constructor). Torn down via t.Cleanup.
func newAuthzDB(t *testing.T) (*identity.Store, *pgxpool.Pool, context.Context) {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("controlplane"),
		tcpostgres.WithUsername("controlplane"),
		tcpostgres.WithPassword("controlplane"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(pg); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := impostgres.Pool(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := impostgres.Migrate(ctx, pool, migrations.FS, migrations.Dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return identity.NewStore(pool), pool, ctx
}

// authzFixture seeds one org and exposes seeding helpers scoped to it.
type authzFixture struct {
	t     *testing.T
	ctx   context.Context
	store *identity.Store
	pool  *pgxpool.Pool
	org   uuid.UUID
}

func newAuthzFixture(t *testing.T) *authzFixture {
	store, pool, ctx := newAuthzDB(t)
	f := &authzFixture{t: t, ctx: ctx, store: store, pool: pool}
	if err := pool.QueryRow(ctx,
		`INSERT INTO orgs (slug, name) VALUES ('acme', 'Acme') RETURNING id`,
	).Scan(&f.org); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return f
}

// user creates a user and assigns their org-level role.
func (f *authzFixture) user(email string, role identity.Role) uuid.UUID {
	f.t.Helper()
	u, err := f.store.CreateUser(f.ctx, email, email)
	if err != nil {
		f.t.Fatalf("create user %s: %v", email, err)
	}
	if err := f.store.AddMember(f.ctx, u.ID, f.org, role); err != nil {
		f.t.Fatalf("add member %s: %v", email, err)
	}
	return u.ID
}

// group creates a group and adds userID to it with groupRole.
func (f *authzFixture) group(slug string, userID uuid.UUID, groupRole identity.Role) uuid.UUID {
	f.t.Helper()
	g, err := f.store.CreateGroup(f.ctx, f.org, identity.GroupInput{Name: slug, Slug: slug})
	if err != nil {
		f.t.Fatalf("create group %s: %v", slug, err)
	}
	if err := f.store.AddGroupMember(f.ctx, userID, g.ID, groupRole); err != nil {
		f.t.Fatalf("add group member: %v", err)
	}
	return g.ID
}

func (f *authzFixture) policy(groupID uuid.UUID, in identity.AccessPolicyInput) {
	f.t.Helper()
	if _, err := f.store.CreatePolicy(f.ctx, groupID, in); err != nil {
		f.t.Fatalf("create policy %+v: %v", in, err)
	}
}

// integration inserts a bare integration row (needed because a
// PolicyIntegration target_integration_id FKs to integrations).
func (f *authzFixture) integration(slug string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO integrations (organization_id, slug, name) VALUES ($1, $2, $2) RETURNING id`,
		f.org, slug,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed integration %s: %v", slug, err)
	}
	return id
}

// attr records a resource attribute for a service (backs attribute policies).
func (f *authzFixture) attr(service, key, value string) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO service_resource_attributes (org_id, service_name, attr_key, attr_value)
		 VALUES ($1, $2, $3, $4)`,
		f.org, service, key, value,
	); err != nil {
		f.t.Fatalf("seed attr %s %s=%s: %v", service, key, value, err)
	}
}

// TestWriteGate is the CanUserWriteAnywhere matrix: org role × group role.
func TestWriteGate(t *testing.T) {
	f := newAuthzFixture(t)

	admin := f.user("admin@acme", identity.RoleAdmin)
	editor := f.user("editor@acme", identity.RoleEditor)
	viewerLone := f.user("viewer-lone@acme", identity.RoleViewer)
	viewerGroupEd := f.user("viewer-ge@acme", identity.RoleViewer)
	viewerGroupVw := f.user("viewer-gv@acme", identity.RoleViewer)

	f.group("payments", viewerGroupEd, identity.RoleEditor) // viewer org-wide, editor in a team
	f.group("readonly", viewerGroupVw, identity.RoleViewer) // viewer everywhere

	cases := []struct {
		name string
		user uuid.UUID
		want bool
	}{
		{"org admin", admin, true},
		{"org editor", editor, true},
		{"org viewer, no groups", viewerLone, false},
		{"org viewer, editor in a group", viewerGroupEd, true},
		{"org viewer, viewer in a group", viewerGroupVw, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.store.CanUserWriteAnywhere(f.ctx, tc.user, f.org)
			if err != nil {
				t.Fatalf("CanUserWriteAnywhere: %v", err)
			}
			if got != tc.want {
				t.Fatalf("CanUserWriteAnywhere = %v, want %v", got, tc.want)
			}
		})
	}

	// A user in a DIFFERENT org's group must not gain write here.
	var otherOrg uuid.UUID
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO orgs (slug, name) VALUES ('other', 'Other') RETURNING id`,
	).Scan(&otherOrg); err != nil {
		t.Fatalf("seed other org: %v", err)
	}
	if got, err := f.store.CanUserWriteAnywhere(f.ctx, viewerGroupEd, otherOrg); err != nil || got {
		t.Fatalf("cross-org write leak: got=%v err=%v, want false", got, err)
	}
}

// TestVisibilityScoping exercises ResolveVisibleServiceSet across the
// policy kinds. Expanders are stubbed — the integration→services and
// systemKind→services expansion is a separate concern tested elsewhere;
// here we assert the policy resolver folds their output in correctly.
func TestVisibilityScoping(t *testing.T) {
	f := newAuthzFixture(t)

	integID := f.integration("orders")
	// Stub expanders: integration "orders" → two services; system kind
	// "rabbitmq" → one service.
	expand := func(_ context.Context, _ uuid.UUID, id uuid.UUID) ([]string, error) {
		if id == integID {
			return []string{"orders-api", "orders-worker"}, nil
		}
		return nil, nil
	}
	expandSystem := func(_ context.Context, _ uuid.UUID, kind string, _ *uuid.UUID) ([]string, error) {
		if kind == "rabbitmq" {
			return []string{"broker"}, nil
		}
		return nil, nil
	}
	universe := func(_ context.Context, _ uuid.UUID) ([]string, error) {
		return []string{"orders-api", "orders-worker", "broker", "billing-api", "web"}, nil
	}
	resolve := func(userID uuid.UUID) (map[string]struct{}, bool) {
		t.Helper()
		set, wildcard, err := f.store.ResolveVisibleServiceSet(f.ctx, userID, f.org, expand, expandSystem, universe)
		if err != nil {
			t.Fatalf("ResolveVisibleServiceSet: %v", err)
		}
		return set, wildcard
	}

	t.Run("no groups sees nothing", func(t *testing.T) {
		u := f.user("v-none@acme", identity.RoleViewer)
		set, wildcard := resolve(u)
		if wildcard {
			t.Fatalf("lone viewer should not get wildcard")
		}
		if len(set) != 0 {
			t.Fatalf("lone viewer should see no services, got %v", keys(set))
		}
	})

	t.Run("service policy sees only that service", func(t *testing.T) {
		u := f.user("v-svc@acme", identity.RoleViewer)
		g := f.group("svc-team", u, identity.RoleViewer)
		f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "checkout"})
		set, wildcard := resolve(u)
		assertVisible(t, set, wildcard, false, "checkout")
	})

	t.Run("integration policy expands to member services", func(t *testing.T) {
		u := f.user("v-int@acme", identity.RoleViewer)
		g := f.group("int-team", u, identity.RoleViewer)
		f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicyIntegration, TargetIntegrationID: integID.String()})
		set, wildcard := resolve(u)
		assertVisible(t, set, wildcard, false, "orders-api", "orders-worker")
	})

	t.Run("system policy expands by kind", func(t *testing.T) {
		u := f.user("v-sys@acme", identity.RoleViewer)
		g := f.group("sys-team", u, identity.RoleViewer)
		f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicySystem, TargetSystemKind: "rabbitmq"})
		set, wildcard := resolve(u)
		assertVisible(t, set, wildcard, false, "broker")
	})

	t.Run("attribute policy matches by resource attrs", func(t *testing.T) {
		f.attr("prod-a", "env", "prod")
		f.attr("prod-b", "env", "prod")
		f.attr("dev-c", "env", "dev")
		u := f.user("v-attr@acme", identity.RoleViewer)
		g := f.group("attr-team", u, identity.RoleViewer)
		f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicyAttributes, AttributeMatch: map[string]string{"env": "prod"}})
		set, wildcard := resolve(u)
		assertVisible(t, set, wildcard, false, "prod-a", "prod-b")
	})

	t.Run("all_org policy is a wildcard", func(t *testing.T) {
		u := f.user("v-all@acme", identity.RoleViewer)
		g := f.group("all-team", u, identity.RoleViewer)
		f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicyAllOrg})
		_, wildcard := resolve(u)
		if !wildcard {
			t.Fatalf("all_org policy must resolve to wildcard")
		}
	})

	t.Run("union across two groups", func(t *testing.T) {
		u := f.user("v-union@acme", identity.RoleViewer)
		g1 := f.group("union-a", u, identity.RoleViewer)
		f.policy(g1, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "svc-a"})
		// A second group the same user belongs to.
		g2, err := f.store.CreateGroup(f.ctx, f.org, identity.GroupInput{Name: "union-b", Slug: "union-b"})
		if err != nil {
			t.Fatalf("create group union-b: %v", err)
		}
		if err := f.store.AddGroupMember(f.ctx, u, g2.ID, identity.RoleViewer); err != nil {
			t.Fatalf("add to union-b: %v", err)
		}
		f.policy(g2.ID, identity.AccessPolicyInput{Kind: identity.PolicyService, TargetServiceName: "svc-b"})
		set, wildcard := resolve(u)
		assertVisible(t, set, wildcard, false, "svc-a", "svc-b")
	})
}

// assertVisible checks a resolved set has exactly wantServices and the
// expected wildcard flag.
func assertVisible(t *testing.T, set map[string]struct{}, gotWildcard, wantWildcard bool, wantServices ...string) {
	t.Helper()
	if gotWildcard != wantWildcard {
		t.Fatalf("wildcard = %v, want %v", gotWildcard, wantWildcard)
	}
	got := keys(set)
	want := append([]string(nil), wantServices...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("visible services = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("visible services = %v, want %v", got, want)
		}
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
