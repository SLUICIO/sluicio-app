// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Migration 0101 gives every matcher group a member (#39) without
// changing what any stored integration means.
package integrations_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
)

// migrationsBefore is the migration set without 0101 and anything after.
func migrationsBefore(t *testing.T, version string) fs.FS {
	t.Helper()
	sub, err := fs.Sub(migrations.FS, migrations.Dir)
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() >= version {
			continue
		}
		b, err := fs.ReadFile(sub, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out["sql/"+e.Name()] = &fstest.MapFile{Data: b}
	}
	return out
}

func TestMembersBackfillKeepsEveryIntegrationsMeaning(t *testing.T) {
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
	if err := impostgres.Migrate(ctx, pool, migrationsBefore(t, "0101_"), "sql"); err != nil {
		t.Fatalf("migrate to 0100: %v", err)
	}
	var org uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orgs LIMIT 1`).Scan(&org); err != nil {
		t.Fatalf("org: %v", err)
	}
	integ := func(slug, mode string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO integrations (organization_id, slug, name, rule_match) VALUES ($1,$2,$2,$3) RETURNING id`, org, slug, mode).Scan(&id); err != nil {
			t.Fatalf("integration %s: %v", slug, err)
		}
		return id
	}
	row := func(id uuid.UUID, group int, attr, value string) {
		if _, err := pool.Exec(ctx, `INSERT INTO integration_matchers (integration_id, attribute, operator, value, match_group) VALUES ($1,$2,'equals',$3,$4)`, id, attr, value, group); err != nil {
			t.Fatalf("matcher: %v", err)
		}
	}
	// "any": svc-a where x=1 or x=2 (two groups, one row in the editor),
	// and svc-b on its own.
	anyID := integ("any-mode", "any")
	row(anyID, 0, "service.name", "svc-a")
	row(anyID, 0, "x", "1")
	row(anyID, 1, "service.name", "svc-a")
	row(anyID, 1, "x", "2")
	row(anyID, 2, "service.name", "svc-b")
	// "all": the same shape, which until now meant x=1 AND x=2.
	allID := integ("all-mode", "all")
	row(allID, 0, "service.name", "svc-a")
	row(allID, 0, "x", "1")
	row(allID, 1, "service.name", "svc-a")
	row(allID, 1, "x", "2")

	if err := impostgres.Migrate(ctx, pool, migrations.FS, migrations.Dir); err != nil {
		t.Fatalf("migrate 0101: %v", err)
	}
	members := func(id uuid.UUID) string {
		rows, err := pool.Query(ctx, `SELECT match_group, member FROM integration_matchers WHERE integration_id=$1 AND attribute='service.name' ORDER BY match_group`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var parts []string
		for rows.Next() {
			var g, m int
			if err := rows.Scan(&g, &m); err != nil {
				t.Fatal(err)
			}
			parts = append(parts, string(rune('0'+g))+"->"+string(rune('0'+m)))
		}
		return strings.Join(parts, " ")
	}
	// "any": the two svc-a groups are one member, as the editor shows them.
	if got := members(anyID); got != "0->0 1->0 2->2" {
		t.Errorf("any-mode members: got %q, want %q", got, "0->0 1->0 2->2")
	}
	// "all": every group stays its own member, so the stored meaning holds.
	if got := members(allID); got != "0->0 1->1" {
		t.Errorf("all-mode members: got %q, want %q", got, "0->0 1->1")
	}
	// A writer that knows nothing of members gets member = match_group.
	row(anyID, 7, "service.name", "svc-c")
	var m int
	if err := pool.QueryRow(ctx, `SELECT member FROM integration_matchers WHERE integration_id=$1 AND match_group=7`, anyID).Scan(&m); err != nil || m != 7 {
		t.Errorf("a row written without a member: want member 7, got %d (%v)", m, err)
	}
}
