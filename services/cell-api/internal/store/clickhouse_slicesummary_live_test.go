//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A message read inside an integration is that integration's slice: when
// member A also calls C, the row's span and service counts and promoted
// columns leave C out, and the trace view keeps only A's and B's spans.
// Runs the two statements against a real ClickHouse, under service names
// of its own.
//
//	SUBTREE_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestIntegrationSliceOfAMessageLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestIntegrationSliceOfAMessageLive(t *testing.T) {
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
	a, b, c := fmt.Sprintf("sumprobe-a-%d", run), fmt.Sprintf("sumprobe-b-%d", run), fmt.Sprintf("sumprobe-c-%d", run)
	trace := fmt.Sprintf("sumprobe%024d", run)
	now := time.Now().UTC()

	// a calls b and c; only c carries order.id.
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, ParentSpanId, SpanName, ServiceName, SpanAttributes, StatusCode, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		id, parent, svc string
		attrs           map[string]string
	}{
		{"a1", "", a, map[string]string{}},
		{"b1", "a1", b, map[string]string{}},
		{"c1", "a1", c, map[string]string{"order.id": "42"}},
		{"c2", "c1", c, map[string]string{}},
	}
	for i, r := range rows {
		if err := batch.Append(now.Add(time.Duration(i)*time.Millisecond), trace, r.id, r.parent, r.id, r.svc, r.attrs, "Ok", "sumprobe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	s := New(conn)

	search := func(slice bool) SearchTraceRow {
		t.Helper()
		got, err := s.SearchMessages(ctx, MessagesSearchParams{
			From: now.Add(-time.Minute), To: now.Add(time.Minute), Limit: 10,
			ServiceFilter: []string{a, b}, PromotedKeys: []string{"order.id"},
			SliceSummary: slice,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range got {
			if r.TraceID == trace {
				return r
			}
		}
		t.Fatalf("the probe's message is not found (slice=%v)", slice)
		return SearchTraceRow{}
	}
	whole := search(false)
	if whole.TotalSpans != 4 || whole.ServiceCount != 3 {
		t.Errorf("whole trace: %d spans in %d services, want 4 in 3", whole.TotalSpans, whole.ServiceCount)
	}
	inSlice := search(true)
	if inSlice.TotalSpans != 2 || inSlice.ServiceCount != 2 {
		t.Errorf("the slice: %d spans in %d services, want 2 in 2", inSlice.TotalSpans, inSlice.ServiceCount)
	}
	if len(inSlice.Promoted) > 0 && inSlice.Promoted[0] != "" {
		t.Errorf("the slice shows c's order.id: %v", inSlice.Promoted)
	}

	ids, err := s.SliceSpanIDs(ctx, trace, []string{a, b}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids["c1"]; ok || len(ids) != 2 {
		t.Errorf("trace view of the slice: %v, want a1 and b1", ids)
	}
}
