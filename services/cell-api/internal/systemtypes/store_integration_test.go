// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// A custom system type must survive the round trip through Postgres
// unchanged, which sounds too obvious to test until a field is added.
//
// detect_span_attrs was written, stored and exported correctly while the
// READ dropped it: the column was scanned into a variable that was never
// decoded. Go is happy - the variable is used, by Scan - and the type
// came back with the field empty. Everything downstream then behaved
// exactly as though the type had no detection rule at all: it was listed,
// it could be applied by hand, and it silently matched nothing.
//
// The property below is one line and would have caught it.
//
//	go test -tags integration ./services/cell-api/internal/systemtypes/...

package systemtypes_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/systemtypes"
)

func newTypesDB(t *testing.T) (*systemtypes.Store, uuid.UUID, context.Context) {
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
	ident := identity.NewStore(pool)
	orgs, err := ident.ListOrgs(ctx)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("no seeded org: %v", err)
	}
	return systemtypes.NewStore(pool), orgs[0].Org.ID, ctx
}

func TestDetectionRulesSurviveTheRoundTrip(t *testing.T) {
	store, org, ctx := newTypesDB(t)

	created, err := store.Create(ctx, org, "roundtrip", "Round trip", false,
		[]string{"rt."}, []string{"rt.flow."},
		[]systemtypes.Check{{Name: "A check", Metric: "rt.thing", Agg: "max", Op: "gt", Threshold: 1}},
		"runbook text")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The RETURNING clause goes through the same scan as every read, so
	// a decode that was never written shows up right here.
	if got := created.DetectSpanAttrs; len(got) != 1 || got[0] != "rt.flow." {
		t.Errorf("create returned detect_span_attrs = %v, want [rt.flow.]", got)
	}

	listed, err := store.List(ctx, org)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list = %d types (err %v), want 1", len(listed), err)
	}
	if got := listed[0].DetectSpanAttrs; len(got) != 1 || got[0] != "rt.flow." {
		t.Errorf("listed detect_span_attrs = %v, want [rt.flow.] - a type that detects nothing looks exactly like this", got)
	}
	if got := listed[0].DetectPrefixes; len(got) != 1 || got[0] != "rt." {
		t.Errorf("listed detect_prefixes = %v, want [rt.]", got)
	}

	updated, ok, err := store.Update(ctx, org, created.ID, "Round trip", false,
		[]string{"rt."}, []string{"rt.flow.", "rt.node."}, listed[0].Checks, "runbook text")
	if err != nil || !ok {
		t.Fatalf("update: %v (ok %v)", err, ok)
	}
	if len(updated.DetectSpanAttrs) != 2 {
		t.Errorf("update returned detect_span_attrs = %v, want two entries", updated.DetectSpanAttrs)
	}
}

func TestATypeWithoutSpanAttrsReadsBackEmptyNotNil(t *testing.T) {
	// Every type that predates the column takes this path. Empty rather
	// than nil keeps the JSON an array, so a client that iterates does
	// not have to special-case null.
	store, org, ctx := newTypesDB(t)
	created, err := store.Create(ctx, org, "metrics-only", "Metrics only", true,
		[]string{"mo."}, nil, []systemtypes.Check{}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.DetectSpanAttrs == nil {
		t.Error("detect_span_attrs came back nil; want an empty slice")
	}
	if len(created.DetectSpanAttrs) != 0 {
		t.Errorf("detect_span_attrs = %v, want empty", created.DetectSpanAttrs)
	}
}
