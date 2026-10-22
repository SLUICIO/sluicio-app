//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Span events end to end on a real ClickHouse: the migration, the insert,
// and the exception columns derived from the events (issue #42). Point it
// at a THROWAWAY server - it migrates the database it is given.
//
//	EVENTS_PROBE_CH=localhost:19000 go test -tags liveprobe ./services/cell-ingest/internal/ingest/ -run TestEventsLive -v

package ingest

import (
	"context"
	"os"
	"testing"
	"time"

	imclickhouse "github.com/sluicio/sluicio-app/pkg/clickhouse"
)

func TestEventsLive(t *testing.T) {
	addr := os.Getenv("EVENTS_PROBE_CH")
	if addr == "" {
		t.Skip("set EVENTS_PROBE_CH to a throwaway ClickHouse")
	}
	ctx := context.Background()
	cfg := imclickhouse.Config{Endpoint: addr, Database: "telemetry", Username: "default", DialTimeout: 5 * time.Second}
	if err := imclickhouse.EnsureDatabase(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	conn, err := imclickhouse.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := imclickhouse.Migrate(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Now().UTC()
	rows := []SpanRow{
		{Timestamp: now, TraceID: "t1", SpanID: "s1", SpanName: "Process invoice", ServiceName: "invoices", OrganizationID: "o",
			StatusCode: "Error", ResourceAttributes: map[string]string{}, SpanAttributes: map[string]string{},
			EventsTimestamp:  []time.Time{now, now.Add(time.Millisecond)},
			EventsName:       []string{"retry", "exception"},
			EventsAttributes: []map[string]string{{"attempt": "2"}, {"exception.type": "System.TimeoutException", "exception.message": "The operation has timed out.", "exception.stacktrace": "at Invoices.Send()"}},
			EventsTotal:      2},
		{Timestamp: now, TraceID: "t2", SpanID: "s2", SpanName: "Fine", ServiceName: "invoices", OrganizationID: "o",
			StatusCode: "Ok", ResourceAttributes: map[string]string{}, SpanAttributes: map[string]string{}},
	}
	if err := NewStore(conn).InsertSpans(ctx, rows); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var etype, emsg string
	var total uint32
	var n uint64
	if err := conn.QueryRow(ctx, `SELECT ExceptionType, ExceptionMessage, EventsTotal, length(EventsName) FROM traces WHERE TraceId = 't1'`).Scan(&etype, &emsg, &total, &n); err != nil {
		t.Fatal(err)
	}
	if etype != "System.TimeoutException" || emsg != "The operation has timed out." || total != 2 || n != 2 {
		t.Errorf("t1: type %q message %q total %d stored %d", etype, emsg, total, n)
	}
	if err := conn.QueryRow(ctx, `SELECT ExceptionType, EventsTotal FROM traces WHERE TraceId = 't2'`).Scan(&etype, &total); err != nil {
		t.Fatal(err)
	}
	if etype != "" || total != 0 {
		t.Errorf("a span without events: type %q total %d", etype, total)
	}
}
