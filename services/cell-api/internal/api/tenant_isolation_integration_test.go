// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Multi-tenant isolation test against a real Postgres (testcontainers).
//
// Sluicio's auth layer resolves a Principal's OrgID from org_members and
// scopes every query to it. This test is the regression guard for that
// scoping: it seeds TWO orgs with their own integrations, systems, and
// services, then asserts — at the store layer where the `WHERE org_id`
// clauses actually live — that no org can list, read, or mutate another
// org's rows. If any store query drops its org filter, one of these
// assertions fails.
//
// Build-tagged `integration` so the fast `go test ./...` run never needs
// Docker/Podman. Run it with:
//
//	go test -tags integration ./services/cell-api/internal/api/...
//
// or `make test-integration`.
package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/integration-monitor/integration-monitor/pkg/postgres"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/catalog"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/integrations"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/migrations"
)

// newIsolationDB brings up a throwaway Postgres, applies the cell-api
// migrations, and returns a pool. Torn down via t.Cleanup.
func newIsolationDB(t *testing.T) (*pgxpool.Pool, context.Context) {
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
	return pool, ctx
}

// createOrg inserts a bare org row (there's no CreateOrg store helper — org
// creation isn't wired into the product yet, but the data model and auth
// layer are already multi-org, which is exactly what we're testing).
func createOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO orgs (slug, name) VALUES ($1, $2) RETURNING id`, slug, name,
	).Scan(&id); err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}

// createSystem inserts a system directly (no public Create method; systems
// are normally minted via the flag-a-service flow).
func createSystem(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO systems (org_id, name, type_key) VALUES ($1, $2, 'rabbitmq') RETURNING id`, orgID, name,
	).Scan(&id); err != nil {
		t.Fatalf("create system %q: %v", name, err)
	}
	return id
}

// TestTenantIsolation is the cross-org isolation guard for the three
// org-scoped resource types that carry a public read path (integrations,
// systems, services). For each it asserts list/get/mutate are org-scoped.
func TestTenantIsolation(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	ints := integrations.NewStore(pool)
	cat := catalog.NewStore(pool)

	orgA := createOrg(t, ctx, pool, "org-a", "Org A")
	orgB := createOrg(t, ctx, pool, "org-b", "Org B")

	t.Run("integrations", func(t *testing.T) {
		intA, err := ints.Create(ctx, integrations.IntegrationWithMatchers{
			Integration: integrations.Integration{OrganizationID: orgA, Slug: "a-int", Name: "A Integration"},
		})
		if err != nil {
			t.Fatalf("create intA: %v", err)
		}
		intB, err := ints.Create(ctx, integrations.IntegrationWithMatchers{
			Integration: integrations.Integration{OrganizationID: orgB, Slug: "b-int", Name: "B Integration"},
		})
		if err != nil {
			t.Fatalf("create intB: %v", err)
		}

		// List is org-scoped: each org sees only its own row.
		listA, err := ints.List(ctx, orgA)
		if err != nil {
			t.Fatalf("list orgA: %v", err)
		}
		if !containsIntegration(listA, intA.ID) || containsIntegration(listA, intB.ID) {
			t.Fatalf("orgA list leaked across tenants: got %v, want only %v", ids(listA), intA.ID)
		}
		listB, err := ints.List(ctx, orgB)
		if err != nil {
			t.Fatalf("list orgB: %v", err)
		}
		if !containsIntegration(listB, intB.ID) || containsIntegration(listB, intA.ID) {
			t.Fatalf("orgB list leaked across tenants: got %v, want only %v", ids(listB), intB.ID)
		}

		// Get across orgs must miss; same-org must hit.
		if _, err := ints.Get(ctx, orgA, intB.ID); !errors.Is(err, integrations.ErrNotFound) {
			t.Fatalf("cross-org Get: want ErrNotFound, got %v", err)
		}
		if _, err := ints.Get(ctx, orgA, intA.ID); err != nil {
			t.Fatalf("same-org Get: %v", err)
		}

		// Mutations across orgs must miss and must NOT change the target.
		if err := ints.SetBadgePublic(ctx, orgA, intB.ID, true); !errors.Is(err, integrations.ErrNotFound) {
			t.Fatalf("cross-org SetBadgePublic: want ErrNotFound, got %v", err)
		}
		if _, err := ints.Update(ctx, orgA, intB.ID, "hijacked", ""); !errors.Is(err, integrations.ErrNotFound) {
			t.Fatalf("cross-org Update: want ErrNotFound, got %v", err)
		}
		if err := ints.Delete(ctx, orgA, intB.ID); !errors.Is(err, integrations.ErrNotFound) {
			t.Fatalf("cross-org Delete: want ErrNotFound, got %v", err)
		}
		gotB, err := ints.Get(ctx, orgB, intB.ID)
		if err != nil {
			t.Fatalf("re-Get intB: %v", err)
		}
		if gotB.BadgePublic || gotB.Name != "B Integration" {
			t.Fatalf("intB mutated by orgA: badge=%v name=%q", gotB.BadgePublic, gotB.Name)
		}

		// Same-org mutation works.
		if err := ints.SetBadgePublic(ctx, orgB, intB.ID, true); err != nil {
			t.Fatalf("same-org SetBadgePublic: %v", err)
		}
		if gotB, _ := ints.Get(ctx, orgB, intB.ID); !gotB.BadgePublic {
			t.Fatalf("same-org SetBadgePublic did not take effect")
		}
	})

	t.Run("systems", func(t *testing.T) {
		sysA := createSystem(t, ctx, pool, orgA, "sys-a")
		sysB := createSystem(t, ctx, pool, orgB, "sys-b")

		listA, err := cat.ListSystems(ctx, orgA)
		if err != nil {
			t.Fatalf("list systems orgA: %v", err)
		}
		if !containsSystem(listA, sysA) || containsSystem(listA, sysB) {
			t.Fatalf("orgA system list leaked across tenants")
		}

		// Get across orgs must report not-found (ok=false), not another org's row.
		if _, ok, err := cat.GetSystem(ctx, orgA, sysB); err != nil || ok {
			t.Fatalf("cross-org GetSystem: want ok=false, got ok=%v err=%v", ok, err)
		}
		if _, ok, err := cat.GetSystem(ctx, orgA, sysA); err != nil || !ok {
			t.Fatalf("same-org GetSystem: want ok=true, got ok=%v err=%v", ok, err)
		}

		// Mutation across orgs must miss; same-org works.
		if err := cat.SetSystemBadgePublic(ctx, orgA, sysB, true); !errors.Is(err, catalog.ErrSystemNotFound) {
			t.Fatalf("cross-org SetSystemBadgePublic: want ErrSystemNotFound, got %v", err)
		}
		if err := cat.SetSystemBadgePublic(ctx, orgB, sysB, true); err != nil {
			t.Fatalf("same-org SetSystemBadgePublic: %v", err)
		}
	})

	t.Run("services", func(t *testing.T) {
		now := time.Unix(1_700_000_000, 0).UTC()
		// Same service name in BOTH orgs — they must be distinct rows.
		shared := []catalog.Discovery{{ServiceName: "checkout", FirstSeen: now, LastSeen: now}}
		if err := cat.UpsertServices(ctx, orgA, shared); err != nil {
			t.Fatalf("upsert orgA: %v", err)
		}
		if err := cat.UpsertServices(ctx, orgB, shared); err != nil {
			t.Fatalf("upsert orgB: %v", err)
		}
		// A service that exists ONLY in org B.
		if err := cat.UpsertServices(ctx, orgB, []catalog.Discovery{{ServiceName: "b-only", FirstSeen: now, LastSeen: now}}); err != nil {
			t.Fatalf("upsert b-only: %v", err)
		}

		// The shared name resolves to each org's own row.
		svcA, okA, err := cat.GetService(ctx, orgA, "checkout")
		if err != nil || !okA {
			t.Fatalf("get checkout orgA: ok=%v err=%v", okA, err)
		}
		if svcA.OrganizationID != orgA {
			t.Fatalf("checkout resolved to wrong org: %v", svcA.OrganizationID)
		}
		if _, okB, _ := cat.GetService(ctx, orgB, "checkout"); !okB {
			t.Fatalf("get checkout orgB: want ok=true")
		}

		// The org-B-only service is invisible to org A.
		if _, ok, err := cat.GetService(ctx, orgA, "b-only"); err != nil || ok {
			t.Fatalf("cross-org GetService: want ok=false, got ok=%v err=%v", ok, err)
		}

		// Badge mutation across orgs updates nothing; same-org updates one row.
		if ok, err := cat.SetServiceBadgePublic(ctx, orgA, "b-only", true); err != nil || ok {
			t.Fatalf("cross-org SetServiceBadgePublic: want ok=false, got ok=%v err=%v", ok, err)
		}
		if ok, err := cat.SetServiceBadgePublic(ctx, orgB, "b-only", true); err != nil || !ok {
			t.Fatalf("same-org SetServiceBadgePublic: want ok=true, got ok=%v err=%v", ok, err)
		}
		if svc, _, _ := cat.GetService(ctx, orgB, "b-only"); !svc.BadgePublic {
			t.Fatalf("same-org badge toggle did not take effect")
		}
		if _, ok, _ := cat.GetService(ctx, orgA, "b-only"); ok {
			t.Fatalf("org-B-only service became visible to org A after mutation")
		}
	})
}

func containsIntegration(list []integrations.Integration, id uuid.UUID) bool {
	for _, i := range list {
		if i.ID == id {
			return true
		}
	}
	return false
}

func containsSystem(list []catalog.System, id uuid.UUID) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

func ids(list []integrations.Integration) []uuid.UUID {
	out := make([]uuid.UUID, len(list))
	for i, in := range list {
		out[i] = in.ID
	}
	return out
}
