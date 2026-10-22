//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// #18 against a real ClickHouse: a Node-RED-shaped service whose runs
// each carry one flow id on their node spans.
//
//	PARTITION_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestPartitionsLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestPartitionsLive(t *testing.T) {
	addr := os.Getenv("PARTITION_PROBE_CH")
	if addr == "" {
		t.Skip("set PARTITION_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	s := New(conn)
	svc := fmt.Sprintf("node-red-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, SpanAttributes, StatusCode, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	flows := []string{"orders", "invoices", "shipments"}
	for run := 0; run < 30; run++ {
		tid := fmt.Sprintf("%s-run-%d", svc, run)
		flow := flows[run%3]
		ts := now.Add(-time.Duration(run) * time.Second)
		status := "Ok"
		if flow == "invoices" && run%2 == 0 {
			status = "Error"
		}
		// The run span carries no flow id (the library sets it on node
		// spans only); each node span does, with its own node id.
		_ = batch.Append(ts, tid, "run", "run", svc, map[string]string{"http.request.method": "POST"}, "Ok", "partition-probe")
		for n := 0; n < 3; n++ {
			_ = batch.Append(ts, tid, fmt.Sprintf("n%d", n), "node", svc,
				map[string]string{"node_red.flow.id": flow, "node_red.node.id": fmt.Sprintf("%s-node-%d", flow, n)}, status, "partition-probe")
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	from, to := now.Add(-time.Hour), now.Add(time.Minute)

	rows, err := s.AttrPartitionStats(ctx, []string{svc}, from, to, 20000, 50)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]AttrPartitionRow{}
	for _, r := range rows {
		by[r.Key] = r
	}
	flow := by["node_red.flow.id"]
	if flow.TracesTotal != 30 || flow.TracesWithKey != 30 || flow.DistinctValues != 3 || flow.TraceValuePairs != 30 {
		t.Errorf("flow id: %+v", flow)
	}
	node := by["node_red.node.id"]
	if node.DistinctValues != 9 || node.TraceValuePairs != 90 {
		t.Errorf("node id (three per run): %+v", node)
	}

	vals, err := s.PartitionValues(ctx, svc, "node_red.flow.id", from, to, 50)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]PartitionValueRow{}
	for _, v := range vals {
		got[v.Value] = v
	}
	if len(got) != 3 || got["orders"].Traces != 10 || got["invoices"].ErrorTraces != 5 || got["shipments"].ErrorTraces != 0 {
		t.Errorf("values: %+v", vals)
	}
}
