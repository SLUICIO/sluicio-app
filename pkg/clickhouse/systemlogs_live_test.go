//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// DropIdleSystemLogs against a real ClickHouse: it drops only log tables
// nothing writes to any more, never one still in use.
//
//	SYSLOG_PROBE_CH=localhost:9000 [SYSLOG_PROBE_USER=u SYSLOG_PROBE_PASSWORD=p] \
//	  go test -tags liveprobe ./pkg/clickhouse/ -run TestDropIdleSystemLogs -v
//
// It drops tables in the server's system database: point it at a
// throwaway ClickHouse, best one started first without and then with
// deploy/clickhouse/sluicio-logs.xml, which leaves idle logs to drop.

package clickhouse

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestDropIdleSystemLogs(t *testing.T) {
	addr := os.Getenv("SYSLOG_PROBE_CH")
	if addr == "" {
		t.Skip("set SYSLOG_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr},
		Auth: clickhouse.Auth{Username: os.Getenv("SYSLOG_PROBE_USER"), Password: os.Getenv("SYSLOG_PROBE_PASSWORD")}})
	if err != nil {
		t.Fatal(err)
	}
	// query_log is on in every configuration: write to it now.
	if err := conn.Exec(ctx, "SYSTEM FLUSH LOGS"); err != nil {
		t.Fatal(err)
	}
	dropped, err := DropIdleSystemLogs(ctx, conn, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("dropped %v", dropped)
	if slices.Contains(dropped, "query_log") {
		t.Error("query_log, still written to, was dropped")
	}
	var n uint64
	if err := conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database = 'system' AND name = 'query_log'").Scan(&n); err != nil || n != 1 {
		t.Errorf("query_log is gone: %d, %v", n, err)
	}
	for _, d := range dropped {
		if !systemLogName.MatchString(d) {
			t.Errorf("dropped %s, which is not a log it may drop", d)
		}
	}
}
