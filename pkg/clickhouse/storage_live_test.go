//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// DeleteOldestDay against a real ClickHouse: it must drop the oldest day
// from every telemetry table and never today.
//
//	STORAGE_PROBE_CH=localhost:9000 go test -tags liveprobe ./pkg/clickhouse/ -run TestDeleteOldestDay -v
//
// Creates and drops its own database.

package clickhouse

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestDeleteOldestDay(t *testing.T) {
	addr := os.Getenv("STORAGE_PROBE_CH")
	if addr == "" {
		t.Skip("set STORAGE_PROBE_CH")
	}
	ctx := context.Background()
	admin, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}})
	if err != nil {
		t.Fatal(err)
	}
	db := "storage_probe_" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	if err := admin.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Exec(context.Background(), "DROP DATABASE "+db) })
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: db}})
	if err != nil {
		t.Fatal(err)
	}
	// The shape that matters: one partition per day, as the real tables.
	for _, tbl := range telemetryTables {
		if err := conn.Exec(ctx, "CREATE TABLE "+tbl+" (Timestamp DateTime64(9)) ENGINE = MergeTree PARTITION BY toDate(Timestamp) ORDER BY Timestamp"); err != nil {
			t.Fatal(err)
		}
	}
	// traces has three days, logs two, metrics only today.
	for tbl, ago := range map[string][]int{"traces": {3, 2, 0}, "logs": {3, 0}, "metrics": {0}} {
		for _, d := range ago {
			if err := conn.Exec(ctx, "INSERT INTO "+tbl+" SELECT now() - INTERVAL ? DAY", d); err != nil {
				t.Fatal(err)
			}
		}
	}
	count := func(tbl string) uint64 {
		var n uint64
		if err := conn.QueryRow(ctx, "SELECT count() FROM "+tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	want := time.Now().AddDate(0, 0, -3).Format("20060102")
	day, err := DeleteOldestDay(ctx, conn)
	if err != nil || day != want {
		t.Fatalf("first delete: got %q, %v; want %s", day, err, want)
	}
	if count("traces") != 2 || count("logs") != 1 || count("metrics") != 1 {
		t.Fatalf("after the first delete: traces %d logs %d metrics %d", count("traces"), count("logs"), count("metrics"))
	}
	if _, err := DeleteOldestDay(ctx, conn); err != nil {
		t.Fatal(err)
	}
	// Only today is left, and today is never deleted.
	day, err = DeleteOldestDay(ctx, conn)
	if err != nil || day != "" {
		t.Fatalf("with only today left: got %q, %v; want nothing deleted", day, err)
	}
	if count("traces") != 1 || count("logs") != 1 || count("metrics") != 1 {
		t.Errorf("today's rows were touched: traces %d logs %d metrics %d", count("traces"), count("logs"), count("metrics"))
	}
}
