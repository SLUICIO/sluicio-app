// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// The monitored-entity limit against a real Postgres: the trigger is the
// enforcement, so it is the trigger that is tested.
package deploymentlimits_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/deploymentlimits"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
)

func newDB(t *testing.T) (*pgxpool.Pool, uuid.UUID, context.Context) {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("controlplane"),
		tcpostgres.WithUsername("controlplane"),
		tcpostgres.WithPassword("controlplane"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	pool, err := impostgres.Pool(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := impostgres.Migrate(ctx, pool, migrations.FS, migrations.Dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	orgs, err := identity.NewStore(pool).ListOrgs(ctx)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("no seeded org: %v", err)
	}
	return pool, orgs[0].Org.ID, ctx
}

func TestTheMonitoredEntityLimit(t *testing.T) {
	pool, org, ctx := newDB(t)
	limits := deploymentlimits.New(pool)
	n := 0
	integration := func() error {
		n++
		_, err := pool.Exec(ctx, `INSERT INTO integrations (organization_id, slug, name) VALUES ($1, $2, $2)`, org, fmt.Sprintf("i-%d", n))
		return err
	}
	system := func(name string) error {
		_, err := pool.Exec(ctx, `INSERT INTO systems (org_id, name, type_key) VALUES ($1, $2, 'broker')
			ON CONFLICT (org_id, name) DO UPDATE SET name = systems.name`, org, name)
		return err
	}
	base, err := limits.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	start := base.Integrations + base.Systems

	if err := limits.SetMonitoredEntityLimit(ctx, start+2); err != nil {
		t.Fatal(err)
	}
	if err := integration(); err != nil {
		t.Fatalf("first under the limit: %v", err)
	}
	if err := system("rabbit"); err != nil {
		t.Fatalf("a system counts toward the same limit and still fits: %v", err)
	}
	if err := integration(); !deploymentlimits.IsEntityLimit(err) {
		t.Fatalf("an integration over the combined limit: want the limit's refusal, got %v", err)
	}
	if err := system("another"); !deploymentlimits.IsEntityLimit(err) {
		t.Fatalf("a system over the combined limit: want the limit's refusal, got %v", err)
	}
	// Reusing an existing system through the conflict path creates
	// nothing, so it must not be refused at the limit.
	if err := system("rabbit"); err != nil {
		t.Fatalf("reusing an existing system at the limit was refused: %v", err)
	}

	// Lowering the limit below the count deletes nothing.
	if err := limits.SetMonitoredEntityLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if c, _ := limits.Count(ctx); c.Integrations+c.Systems != start+2 {
		t.Errorf("lowering the limit changed what exists: %d, want %d", c.Integrations+c.Systems, start+2)
	}

	// No limit: anything goes again.
	if err := limits.SetMonitoredEntityLimit(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := integration(); err != nil {
		t.Fatalf("with the limit cleared: %v", err)
	}

	// Two creators racing for the last place: exactly one gets it.
	c, _ := limits.Count(ctx)
	if err := limits.SetMonitoredEntityLimit(ctx, c.Integrations+c.Systems+1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = pool.Exec(ctx, `INSERT INTO integrations (organization_id, slug, name) VALUES ($1, $2, $2)`, org, fmt.Sprintf("race-%d", i))
		}(i)
	}
	wg.Wait()
	ok, refused := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case deploymentlimits.IsEntityLimit(err):
			refused++
		default:
			t.Errorf("race: unexpected error %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Errorf("race for the last place: want one created and one refused, got %d and %d", ok, refused)
	}
}
