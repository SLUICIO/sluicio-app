// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Unacknowledged-error notifications are off until an integration turns
// them on, and only the integrations that did are reported to the notifier.
package integrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
)

func TestErrorNotificationsAreOffUntilTurnedOn(t *testing.T) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("controlplane"), tcpostgres.WithUsername("controlplane"), tcpostgres.WithPassword("controlplane"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })
	dsn, _ := pg.ConnectionString(ctx, "sslmode=disable")
	pool, err := impostgres.Pool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := impostgres.Migrate(ctx, pool, migrations.FS, migrations.Dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var org uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orgs LIMIT 1`).Scan(&org); err != nil {
		t.Fatalf("seeded org: %v", err)
	}

	store := integrations.NewStore(pool)
	create := func(slug string) uuid.UUID {
		t.Helper()
		got, err := store.Create(ctx, integrations.IntegrationWithMatchers{Integration: integrations.Integration{OrganizationID: org, Slug: slug, Name: slug}})
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		return got.ID
	}
	orders, invoices := create("orders"), create("invoices")

	got, err := store.Get(ctx, org, orders)
	if err != nil {
		t.Fatal(err)
	}
	if got.NotifyUnacknowledgedErrors {
		t.Error("a new integration notifies about unacknowledged errors, want off by default")
	}
	if on, err := store.NotifyingUnacknowledgedErrors(ctx, []uuid.UUID{orders, invoices}); err != nil || len(on) != 0 {
		t.Fatalf("before turning it on: %v, %v, want none", on, err)
	}

	if err := store.SetNotifyUnacknowledgedErrors(ctx, org, orders, true); err != nil {
		t.Fatal(err)
	}
	on, err := store.NotifyingUnacknowledgedErrors(ctx, []uuid.UUID{orders, invoices})
	if err != nil || len(on) != 1 || on[0] != orders {
		t.Fatalf("after turning it on for orders: %v, %v, want [orders]", on, err)
	}
	if got, _ := store.Get(ctx, org, orders); !got.NotifyUnacknowledgedErrors {
		t.Error("the detail does not show the setting as on")
	}

	// Another organization's id is not found, so it cannot be switched.
	if err := store.SetNotifyUnacknowledgedErrors(ctx, uuid.New(), invoices, true); err == nil {
		t.Error("switching an integration from another organization succeeded")
	}
}
