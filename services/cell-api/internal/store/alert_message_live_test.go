//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Reads the message an alert is about from a real ClickHouse: the newest
// failing span that met the rule's filter, its values for the filter's
// keys, and its environment.
//
//	SUBTREE_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestLatestTriggeringSpanLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestLatestTriggeringSpanLive(t *testing.T) {
	addr := os.Getenv("SUBTREE_PROBE_CH")
	if addr == "" {
		t.Skip("set SUBTREE_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	run := time.Now().UnixNano()
	svc := fmt.Sprintf("trigprobe-%d", run)
	now := time.Now().UTC()
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, SpanAttributes, ResourceAttributes, StatusCode, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	res := map[string]string{"deployment.environment.name": "production"}
	spans := []struct {
		trace, order, failed, status string
		at                           time.Time
	}{
		{"older", "ORD-1", "2", "Error", now.Add(-30 * time.Second)},
		{"newest-ok", "ORD-3", "5", "Ok", now.Add(-5 * time.Second)},
		{"newest-error", "ORD-2", "3", "Error", now.Add(-10 * time.Second)},
		{"no-failures", "ORD-4", "0", "Error", now.Add(-1 * time.Second)},
	}
	for _, s := range spans {
		attrs := map[string]string{"order.id": s.order, "documents.failed": s.failed}
		if err := batch.Append(s.at, fmt.Sprintf("%s-%d", s.trace, run), "s", "ingest", svc, attrs, res, s.status, "trigprobe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}

	rec, ok, err := New(conn).LatestTriggeringSpan(ctx, TriggerQuery{
		Services: []string{svc}, From: now.Add(-time.Minute), To: now.Add(time.Minute),
		Attrs:      []LogAttrFilter{{Key: "documents.failed", Op: AttrOpGt, Value: "0"}},
		ErrorsOnly: true,
		Keys:       []string{"documents.failed", "order.id"},
	})
	if err != nil || !ok {
		t.Fatalf("no triggering span: %v", err)
	}
	t.Logf("%+v", rec)
	if rec.TraceID != fmt.Sprintf("newest-error-%d", run) {
		t.Errorf("trace %s, want the newest failing span that met the filter", rec.TraceID)
	}
	if rec.Values["documents.failed"] != "3" || rec.Values["order.id"] != "ORD-2" || rec.Environment != "production" {
		t.Errorf("values %v, environment %q", rec.Values, rec.Environment)
	}
}
