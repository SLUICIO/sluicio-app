// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Checks per environment (#40) against a real Postgres: one instance
// per environment, the whole-scope instance retired without a page, and
// an integration with no environments evaluated as it always was.
package alerting

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/envctx"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/envrole"
)

// envEvaluator answers a metric aggregate per environment, and records
// which environments it was asked about.
type envEvaluator struct {
	mu     sync.Mutex
	values map[string]float64 // env -> value; "*" for a whole-scope read
	asked  []string
}

func (f *envEvaluator) MetricAggregate(ctx context.Context, _ string, _ []AttrFilter, _, _ string, _, _ *uuid.UUID, _, _ time.Time) (float64, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := "*"
	if env, ok := envctx.From(ctx); ok {
		key = env
	}
	f.asked = append(f.asked, key)
	return f.values[key], 1, nil
}

func (f *envEvaluator) MetricAggregateGrouped(context.Context, string, []AttrFilter, string, string, string, *uuid.UUID, *uuid.UUID, time.Time, time.Time) ([]MetricGroup, error) {
	return nil, nil
}

func (f *envEvaluator) reset(values map[string]float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values, f.asked = values, nil
}

type fixedLister struct{ envs []string }

func (l *fixedLister) IntegrationEnvironments(context.Context, uuid.UUID) ([]string, error) {
	return l.envs, nil
}

func TestChecksPerEnvironment(t *testing.T) {
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
		t.Fatalf("seeded org: %v", err)
	}
	var integID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO integrations (organization_id, slug, name) VALUES ($1, 'order-sync', 'Order sync') RETURNING id`, org,
	).Scan(&integID); err != nil {
		t.Fatal(err)
	}

	store := NewStore(pool)
	rule, err := store.CreateRule(ctx, AlertRule{
		OrganizationID: org, IntegrationID: &integID, Name: "Queue depth", Signal: "metric",
		Spec:     MetricRuleSpec{MetricName: "queue.depth", Aggregation: "max", Operator: OpGT, Threshold: 10, ForWindow: "5m"},
		Severity: "critical", Enabled: true, ResolveMode: ResolveAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	// An instance from before checks were evaluated per environment.
	if _, err := store.OpenInstance(ctx, rule.ID, legacyFingerprint, map[string]string{}, "Queue depth breaching"); err != nil {
		t.Fatal(err)
	}

	eval := &envEvaluator{}
	lister := &fixedLister{envs: []string{"", "production", "test"}}
	e := NewEngine(store, eval, nil, nil, org, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.SetEnvironmentLister(lister)

	firing := func() map[string]string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT fingerprint, summary FROM alert_instances WHERE alert_rule_id = $1 AND state = 'firing'`, rule.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var fp, summary string
			if err := rows.Scan(&fp, &summary); err != nil {
				t.Fatal(err)
			}
			out[fp] = summary
		}
		return out
	}

	// Test backs up while production is fine: only test fires.
	eval.reset(map[string]float64{"": 0, "production": 2, "test": 50})
	e.evaluateOnce(ctx)
	got := firing()
	if len(got) != 1 || got["env:test"] == "" {
		t.Fatalf("firing after first tick: %v", got)
	}
	if got["env:test"] != "" && !contains(got["env:test"], "[test]") {
		t.Errorf("summary does not name the environment: %q", got["env:test"])
	}
	var label string
	if err := pool.QueryRow(ctx, `SELECT labels->>'environment' FROM alert_instances WHERE alert_rule_id = $1 AND fingerprint = 'env:test'`, rule.ID).Scan(&label); err != nil || label != "test" {
		t.Errorf("environment label: %q %v", label, err)
	}
	// The whole-scope instance was retired without a page.
	var jobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_jobs j JOIN alert_instances i ON i.id = j.alert_instance_id WHERE i.fingerprint = 'all'`).Scan(&jobs); err != nil || jobs != 0 {
		t.Errorf("retired instance paged: %d %v", jobs, err)
	}

	// Production now breaches too, test recovers: two instances, resolved
	// independently.
	eval.reset(map[string]float64{"": 0, "production": 60, "test": 1})
	e.evaluateOnce(ctx)
	got = firing()
	if len(got) != 1 || got["env:production"] == "" {
		t.Fatalf("after test recovered: %v", got)
	}

	// The per-environment firing reading for status.
	byEnv, err := store.FiringHealthIntegrationEnvironments(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if !FiringIn(byEnv, integID, "production") || FiringIn(byEnv, integID, "test") {
		t.Errorf("firing by environment: %v", byEnv[integID])
	}

	// Production goes quiet entirely: its instance is still evaluated
	// (and resolves) although the environment is no longer listed.
	lister.envs = []string{"", "test"}
	eval.reset(map[string]float64{"": 0, "production": 0, "test": 1})
	e.evaluateOnce(ctx)
	if got = firing(); len(got) != 0 {
		t.Fatalf("production did not resolve: %v", got)
	}

	// An integration with no environments: evaluated once, as before.
	lister.envs = []string{""}
	eval.reset(map[string]float64{"*": 99})
	e.evaluateOnce(ctx)
	if got = firing(); len(got) != 1 || got["all"] == "" || contains(got["all"], "[") {
		t.Fatalf("no-environment integration: %v (asked %v)", got, eval.asked)
	}

	// "Only these": production alone is evaluated.
	if _, err := pool.Exec(ctx, `UPDATE alert_instances SET state = 'resolved', ended_at = now() WHERE alert_rule_id = $1`, rule.ID); err != nil {
		t.Fatal(err)
	}
	rule.EnvironmentMode, rule.Environments = EnvironmentOnly, []string{"production"}
	if _, err := store.UpdateRule(ctx, org, rule); err != nil {
		t.Fatal(err)
	}
	lister.envs = []string{"", "production", "test"}
	eval.reset(map[string]float64{"production": 1, "test": 50})
	e.evaluateOnce(ctx)
	if len(eval.asked) != 1 || eval.asked[0] != "production" {
		t.Errorf("only mode evaluated %v", eval.asked)
	}
	if got = firing(); len(got) != 0 {
		t.Errorf("only mode fired for an environment it does not watch: %v", got)
	}

	// "Production environments": whatever has the role now, so prod-us
	// joins without the check being edited.
	rule.Environments = []string{envrole.ProductionToken}
	if _, err := store.UpdateRule(ctx, org, rule); err != nil {
		t.Fatal(err)
	}
	lister.envs = []string{"", "production", "prod-us", "test"}
	eval.reset(map[string]float64{"production": 1, "prod-us": 1, "test": 1})
	e.evaluateOnce(ctx)
	sort.Strings(eval.asked)
	if len(eval.asked) != 2 || eval.asked[0] != "prod-us" || eval.asked[1] != "production" {
		t.Errorf("production environments evaluated %v", eval.asked)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
