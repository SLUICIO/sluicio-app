// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// The open delayed traces against a real Postgres: a delay that fired
// before the window is still open and still listed, with the
// environment of its trace, while the windowed read leaves it out.
package tracecompletion

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
)

func TestOpenFiringsIgnoreTheWindow(t *testing.T) {
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
	var integ uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO integrations (organization_id, slug, name) VALUES ($1, 'part-sync', 'Part sync') RETURNING id`, org,
	).Scan(&integ); err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool)
	spec := RuleSpec{StartSpanName: "receive", Stages: []Stage{{Spans: []SpanMatch{{SpanName: "send"}}, TimeoutSeconds: 1200}}}
	spec.Normalize()
	rule, err := store.Create(ctx, org, RuleInput{
		IntegrationID: integ, Name: "Send step must complete", Severity: "warning", Enabled: true, Spec: spec,
	})
	if err != nil {
		t.Fatal(err)
	}
	open := func(trace, state, env string, ago time.Duration) {
		t.Helper()
		labels := `{"rule_kind":"trace_completion","trace_id":"` + trace + `"`
		if env != "" {
			labels += `,"environment":"` + env + `"`
		}
		labels += `}`
		if _, err := pool.Exec(ctx,
			`INSERT INTO alert_instances (alert_rule_id, state, fingerprint, labels, summary, started_at)
			 VALUES ($1, $2::alert_state, $3, $4::jsonb, 'delayed', now() - $5::interval)`,
			rule.ID, state, trace+"#1", labels, ago.String()); err != nil {
			t.Fatal(err)
		}
	}
	open("aaa1", "firing", "prod", 9*time.Hour)      // delayed before a one-hour window
	open("bbb2", "firing", "", 10*time.Minute)       // inside it, from before firings named one
	open("ccc3", "resolved", "prod", 20*time.Minute) // arrived late: not open

	got, err := store.ListOpenFirings(ctx, org, integ, 100)
	if err != nil {
		t.Fatal(err)
	}
	byTrace := map[string]Firing{}
	for _, f := range got {
		byTrace[f.TraceID] = f
	}
	if len(got) != 2 || byTrace["aaa1"].TraceID == "" || byTrace["bbb2"].TraceID == "" {
		t.Fatalf("open firings: %+v, want aaa1 and bbb2", got)
	}
	if e := byTrace["aaa1"].Environment; e == nil || *e != "prod" {
		t.Errorf("aaa1 environment %v, want prod", e)
	}
	if e := byTrace["bbb2"].Environment; e != nil {
		t.Errorf("bbb2 environment %q, want none", *e)
	}

	now := time.Now()
	windowed, err := store.ListFirings(ctx, org, integ, now.Add(-time.Hour), now, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range windowed {
		if f.TraceID == "aaa1" {
			t.Error("the windowed read lists a firing from before the window")
		}
	}
}
