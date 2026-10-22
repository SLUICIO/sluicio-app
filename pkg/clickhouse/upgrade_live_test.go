//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Reads across a schema upgrade, against a real ClickHouse: rows written
// before a migration must stay readable through the columns it adds.
// 0009 shipped without this test and failed every read of ExceptionType
// over a window reaching data written before it (fixed by 0011).
//
//	UPGRADE_PROBE_CH=localhost:9000 go test -tags liveprobe ./pkg/clickhouse/ -run TestReadsAcrossUpgrade -v
//
// Needs an EMPTY server: it creates and drops its own database.

package clickhouse

import (
	"context"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestReadsAcrossUpgrade(t *testing.T) {
	addr := os.Getenv("UPGRADE_PROBE_CH")
	if addr == "" {
		t.Skip("set UPGRADE_PROBE_CH")
	}
	ctx := context.Background()
	admin, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}})
	if err != nil {
		t.Fatal(err)
	}
	db := "upgrade_probe_" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	if err := admin.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Exec(context.Background(), "DROP DATABASE "+db) })
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: db}})
	if err != nil {
		t.Fatal(err)
	}

	if err := conn.Exec(ctx, `CREATE TABLE schema_migrations (version String, applied_at DateTime DEFAULT now()) ENGINE = MergeTree() ORDER BY version`); err != nil {
		t.Fatal(err)
	}
	entries, _ := fs.ReadDir(embeddedMigrations, "migrations")
	var files []string
	for _, e := range entries {
		files = append(files, e.Name())
	}
	sort.Strings(files)
	apply := func(name string) {
		t.Helper()
		body, err := fs.ReadFile(embeddedMigrations, path.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := applyMigration(ctx, conn, strings.TrimSuffix(name, ".sql"), string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	// The schema as of v0.11.175, a row written then, then every later
	// migration: the order an upgraded cell went through.
	i := 0
	for ; i < len(files) && files[i] < "0009"; i++ {
		apply(files[i])
	}
	if err := conn.Exec(ctx, `INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, StatusCode, OrganizationId)
		VALUES (now64(9), 'old', 's', 'op', 'svc', 'Error', 'o')`); err != nil {
		t.Fatal(err)
	}
	for ; i < len(files); i++ {
		apply(files[i])
	}

	var typ, msg, env string
	if err := conn.QueryRow(ctx, `SELECT ExceptionType, ExceptionMessage, Environment FROM traces WHERE TraceId = 'old'`).Scan(&typ, &msg, &env); err != nil {
		t.Fatalf("an old row is unreadable through the new columns: %v", err)
	}
	if typ != "" || msg != "" || env != "" {
		t.Errorf("old row: %q %q %q", typ, msg, env)
	}
}
