//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The Advisor's flow attributes read from a real ClickHouse: a service
// whose traces each carry one of a few flow.name values is split by it,
// and only for its own organization.
//
//	FLOWATTR_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/api/ -run TestFlowAttrsLive -v

package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

func TestFlowAttrsLive(t *testing.T) {
	addr := os.Getenv("FLOWATTR_PROBE_CH")
	if addr == "" {
		t.Skip("set FLOWATTR_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.New()
	svc := fmt.Sprintf("flowprobe-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, SpanAttributes, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	flows := []string{"orders", "invoices", "refunds"}
	for i := 0; i < 60; i++ {
		attrs := map[string]string{"flow.name": flows[i%len(flows)], "request.id": fmt.Sprintf("r-%d", i)}
		if err := batch.Append(now.Add(-time.Duration(i)*time.Second), fmt.Sprintf("%s-%d", svc, i), "s", "handle", svc, attrs, org.String()); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	h := &Handlers{Store: store.New(conn), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	got := h.flowAttrs(ctx, org)
	if len(got[svc]) != 1 || got[svc][0] != "flow.name" {
		t.Fatalf("flow attributes of %s: %v, want [flow.name]", svc, got[svc])
	}
	if other := h.flowAttrs(ctx, uuid.New()); len(other[svc]) != 0 {
		t.Fatalf("another organization sees %s split: %v", svc, other[svc])
	}
}
