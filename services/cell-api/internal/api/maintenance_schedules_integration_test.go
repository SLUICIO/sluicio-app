// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Recurring maintenance windows (#9) against a real Postgres: occurrences
// are created once, a skip survives the next run, a deleted series keeps
// the history of what it silenced.
package api

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/maintenance"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
)

func TestRecurringMaintenanceAgainstPostgres(t *testing.T) {
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
		t.Fatal(err)
	}
	pool, err := impostgres.Pool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := impostgres.Migrate(ctx, pool, migrations.FS, migrations.Dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	orgs, err := identity.NewStore(pool).ListOrgs(ctx)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("no seeded org: %v", err)
	}
	orgID := orgs[0].Org.ID

	store := maintenance.NewStore(pool)
	h := &Handlers{Maintenance: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// Every day, starting an hour from now (UTC), for 30 minutes: the
	// first occurrence is upcoming, so it can be skipped.
	start := time.Now().UTC().Add(time.Hour).Truncate(time.Minute)
	sc, err := store.CreateSchedule(ctx, maintenance.Schedule{
		OrgID: orgID, Name: "Patch night",
		Scope:    maintenance.WindowScope{Kind: "all_org"},
		Announce: true,
		Recurrence: maintenance.Recurrence{
			Frequency: "weekly", Weekdays: []int{0, 1, 2, 3, 4, 5, 6},
			StartTime: start.Format("15:04"), DurationMinutes: 30, Timezone: "UTC",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	occurrences := func() []maintenance.Window {
		t.Helper()
		w, err := store.ScheduleWindows(ctx, orgID, sc.ID)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}

	// Created once, however often the loop runs.
	for i := 0; i < 3; i++ {
		if err := h.materializeSchedule(ctx, sc); err != nil {
			t.Fatal(err)
		}
	}
	got := occurrences()
	if len(got) != 1 || !got[0].StartsAt.Equal(start) || got[0].ScheduleID == nil || *got[0].ScheduleID != sc.ID {
		t.Fatalf("first occurrence: %+v", got)
	}
	if got[0].AnnouncementID == nil {
		t.Error("announce was set but the occurrence has no banner")
	}
	if got[0].Active {
		t.Error("an occurrence an hour away reads as active")
	}

	// Skipping it records the skip and creates the next day's instead.
	if _, err := store.EndWindow(ctx, orgID, got[0].ID); err != nil {
		t.Fatal(err)
	}
	sc, err = store.GetSchedule(ctx, orgID, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.SkippedStarts) != 1 || !sc.SkippedStarts[0].Equal(start) {
		t.Fatalf("skip not recorded: %v", sc.SkippedStarts)
	}
	if err := h.materializeSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	got = occurrences()
	if len(got) != 1 || !got[0].StartsAt.Equal(start.AddDate(0, 0, 1)) {
		t.Fatalf("after the skip: %+v", got)
	}

	// An occurrence that ended keeps its row when the series goes.
	past, err := store.CreateWindow(ctx, maintenance.Window{
		OrgID: orgID, Name: "Patch night", Scope: maintenance.WindowScope{Kind: "all_org"},
		StartsAt: start.AddDate(0, 0, -1), EndsAt: start.AddDate(0, 0, -1).Add(30 * time.Minute), ScheduleID: &sc.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSchedule(ctx, orgID, sc.ID); err != nil {
		t.Fatal(err)
	}
	kept, err := store.GetWindow(ctx, orgID, past.ID)
	if err != nil {
		t.Fatalf("past occurrence lost with its series: %v", err)
	}
	if kept.ScheduleID != nil {
		t.Errorf("past occurrence still points at a deleted series")
	}
}
