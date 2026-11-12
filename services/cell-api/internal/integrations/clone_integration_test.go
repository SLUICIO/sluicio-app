// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// A clone carries what describes the integration. Its message columns,
// message filters, notification profile and sources were once left
// behind because nobody added them to Clone when they were introduced;
// the guard below fails when a new column or table of an integration has
// not been decided on.
package integrations_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
)

func newCloneDB(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
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
	return pool, org
}

func TestCloneCarriesColumnsFiltersProfileAndSources(t *testing.T) {
	ctx := context.Background()
	pool, org := newCloneDB(t)
	store := integrations.NewStore(pool)
	src, err := store.Create(ctx, integrations.IntegrationWithMatchers{Integration: integrations.Integration{OrganizationID: org, Slug: "orders", Name: "Orders"}})
	if err != nil {
		t.Fatal(err)
	}
	var profile uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO notification_profiles (id, organization_id, name) VALUES (gen_random_uuid(), $1, 'Orders on call') RETURNING id`, org).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE integrations SET
			message_columns = '[{"key":"order.id","label":"Order"}]'::jsonb,
			message_filters = '[{"attribute":"order.kind","operator":"equals","value":"b2b"}]'::jsonb,
			notification_profile_id = $2
		WHERE id = $1`, src.ID, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO integration_sources (integration_id, signal, origin_kind, origin, conditions, metric_names, position)
		VALUES ($1, 'metrics', 'service', 'rabbitmq-prod', '[{"attribute":"rabbitmq.queue.name","operator":"equals","value":"orders"}]'::jsonb, ARRAY['rabbitmq.queue.*'], 0)`, src.ID); err != nil {
		t.Fatal(err)
	}

	clone, err := store.Clone(ctx, org, src.ID, integrations.CloneOptions{Name: "Returns", Slug: "returns", Scope: integrations.PlanClone(false)})
	if err != nil {
		t.Fatal(err)
	}

	var same bool
	if err := pool.QueryRow(ctx, `
		SELECT c.message_columns = s.message_columns
		   AND c.message_filters = s.message_filters
		   AND c.notification_profile_id IS NOT DISTINCT FROM s.notification_profile_id
		FROM integrations c, integrations s WHERE c.id = $1 AND s.id = $2`, clone.ID, src.ID).Scan(&same); err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Error("the clone's message columns, filters or notification profile differ from the source's")
	}
	var origin string
	var names []string
	if err := pool.QueryRow(ctx, `SELECT origin, metric_names FROM integration_sources WHERE integration_id = $1`, clone.ID).Scan(&origin, &names); err != nil {
		t.Fatalf("the clone has no source: %v", err)
	}
	if origin != "rabbitmq-prod" || len(names) != 1 || names[0] != "rabbitmq.queue.*" {
		t.Errorf("cloned source: %s %v", origin, names)
	}
}

// Every column of integrations and every table that points at one is
// either copied by Clone or left out on purpose. A new one must be added
// to one of these lists, which means someone decided.
func TestCloneAccountsForEveryColumnAndTable(t *testing.T) {
	ctx := context.Background()
	pool, _ := newCloneDB(t)

	columns := map[string]string{
		"id": "new", "organization_id": "same org", "slug": "asked", "name": "asked",
		"created_at": "new", "updated_at": "new",
		"description": "copied", "rule_match": "copied", "declared_environments": "copied",
		"notify_unacknowledged_errors": "copied", "message_columns": "copied",
		"message_filters": "copied", "notification_profile_id": "copied",
		"badge_public": "never: a clone does not publish a second public URL",
	}
	tables := map[string]string{
		"integration_matchers": "copied", "integration_tags": "copied", "integration_metadata": "copied",
		"integration_sources": "copied", "alert_rules": "copied (health checks)",
		"group_access_policies":       "copied for an admin only",
		"integration_services":        "never: the reconciler derives it from the matchers",
		"integration_detected_facets": "never: detected from the clone's own telemetry",
		"dashboard_items":             "never: they belong to other people's dashboards",
	}

	var unknown []string
	rows, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_name = 'integrations'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c string
		_ = rows.Scan(&c)
		if _, ok := columns[c]; !ok {
			unknown = append(unknown, "column integrations."+c)
		}
	}
	rows.Close()
	rows, err = pool.Query(ctx, `SELECT DISTINCT conrelid::regclass::text FROM pg_constraint WHERE confrelid = 'integrations'::regclass`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var tbl string
		_ = rows.Scan(&tbl)
		if _, ok := tables[tbl]; !ok {
			unknown = append(unknown, "table "+tbl)
		}
	}
	rows.Close()
	sort.Strings(unknown)
	if len(unknown) > 0 {
		t.Errorf("Clone has not decided on: %s. Copy it in clone.go or say why not here.", strings.Join(unknown, ", "))
	}
}
