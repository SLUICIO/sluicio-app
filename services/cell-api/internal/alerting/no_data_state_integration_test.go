// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// A metric check that finds no data must not look healthy. It cannot
// fire, so "ok" hid checks that were blind: integration checks scoped to
// production on an integration whose metrics carried no environment, a
// service check whose source was renamed. Every evaluation is recorded,
// with or without data, and the check reads "no_data" until it finds
// some. A split check records its reading whether or not it is shown on
// the service page, so it shows an evaluation and its no-data alert can
// arm.
package alerting

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/envrole"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
)

// samplesEvaluator answers every aggregate with a fixed value and sample
// count, and every grouped aggregate with fixed groups.
type samplesEvaluator struct {
	value   float64
	samples uint64
	groups  []MetricGroup
}

func (f *samplesEvaluator) MetricAggregate(context.Context, string, []AttrFilter, string, string, *uuid.UUID, *uuid.UUID, time.Time, time.Time) (float64, uint64, error) {
	return f.value, f.samples, nil
}

func (f *samplesEvaluator) MetricAggregateGrouped(context.Context, string, []AttrFilter, string, string, string, *uuid.UUID, *uuid.UUID, time.Time, time.Time) ([]MetricGroup, error) {
	return f.groups, nil
}

func TestBlindMetricChecksReadNoData(t *testing.T) {
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
	var org uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orgs ORDER BY created_at LIMIT 1`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	var integID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO integrations (organization_id, slug, name) VALUES ($1, 'goods-receive', 'Goods receive') RETURNING id`, org,
	).Scan(&integID); err != nil {
		t.Fatal(err)
	}

	store := NewStore(pool)
	newRule := func(r AlertRule) AlertRule {
		t.Helper()
		r.OrganizationID, r.Signal, r.Severity, r.Enabled, r.ResolveMode = org, "metric", "warning", true, ResolveAuto
		created, err := store.CreateRule(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	spec := MetricRuleSpec{MetricName: "servicebus.queue.deadletter_messages", Aggregation: "max", Operator: OpGT, Threshold: 0, ForWindow: "10m"}
	prodOnly := newRule(AlertRule{Name: "DLQ (production)", IntegrationID: &integID, Spec: spec,
		EnvironmentMode: EnvironmentOnly, Environments: []string{envrole.ProductionToken}})
	service := newRule(AlertRule{Name: "DLQ (service)", ServiceName: "azureservicebus", Spec: spec})
	splitSpec := spec
	splitSpec.SplitBy = "queue"
	split := newRule(AlertRule{Name: "DLQ by queue", ServiceName: "azureservicebus", Spec: splitSpec})

	eval := &samplesEvaluator{}
	// The integration has telemetry only from test: "production" names no
	// environment to evaluate in.
	e := NewEngine(store, eval, nil, nil, org, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.SetEnvironmentLister(&fixedLister{envs: []string{"test"}})

	states := func() map[uuid.UUID]RuleState {
		t.Helper()
		st, err := store.RuleStates(ctx, org)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	before := states()
	if before[service.ID].State != "ok" || before[service.ID].LastEvaluatedAt != nil {
		t.Errorf("before any evaluation = %+v, want ok and never evaluated", before[service.ID])
	}

	// Nothing to read anywhere.
	e.evaluateOnce(ctx)
	st := states()
	for _, r := range []AlertRule{prodOnly, service, split} {
		if st[r.ID].State != "no_data" || st[r.ID].LastEvaluatedAt == nil {
			t.Errorf("%s with no data = %+v, want no_data with an evaluation time", r.Name, st[r.ID])
		}
	}

	// Data arrives (below the threshold): healthy, and the split check,
	// not shown on the service page, records its reading.
	eval.samples, eval.value = 3, 0
	eval.groups = []MetricGroup{{Label: "orders", Value: 0, Samples: 3}}
	e.evaluateOnce(ctx)
	st = states()
	if st[service.ID].State != "ok" || st[split.ID].State != "ok" {
		t.Errorf("with data: service %+v, split %+v; want ok", st[service.ID], st[split.ID])
	}
	if r, err := store.LatestReading(ctx, split.ID); err != nil || r == nil {
		t.Errorf("split check reading = %v, %v; want one recorded", r, err)
	}
	// Still no production environment to read.
	if st[prodOnly.ID].State != "no_data" {
		t.Errorf("production-only check = %+v, want no_data", st[prodOnly.ID])
	}
	// Disabled wins over no data.
	prodOnly.Enabled = false
	if _, err := store.UpdateRule(ctx, org, prodOnly); err != nil {
		t.Fatal(err)
	}
	if got := states()[prodOnly.ID].State; got != "disabled" {
		t.Errorf("disabled check reads %q", got)
	}
}
