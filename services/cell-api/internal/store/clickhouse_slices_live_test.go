//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Runs SliceTraceStats against a real ClickHouse: a UNION ALL of
// aggregates with argMaxIf and a subtree lookup in each branch is exactly
// the kind of statement that reads right and does not run. Writes an
// Airflow-shaped set of traces under a service name of its own, so no
// real service's reads see them.
//
//	SUBTREE_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestSliceTraceStatsLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestSliceTraceStatsLive(t *testing.T) {
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
	svc := fmt.Sprintf("sliceprobe-scheduler-%d", run)
	now := time.Now().UTC()

	// One scheduler, three DAG runs. Only the root carries airflow.dag_id,
	// as in Airflow; the failing worker span is reached through
	// include_descendants or not at all.
	//
	//   gl1     gl_posting     worker fails now
	//   gl2     gl_posting     worker failed 30s ago, before the ack
	//   orders  orders_export  all ok
	type span struct {
		trace, id, parent, dag, status string
		at                             time.Time
	}
	tid := func(s string) string { return fmt.Sprintf("%s%020d", s, run) }
	spans := []span{
		{tid("gl1"), "r", "", "gl_posting", "Ok", now.Add(-5 * time.Second)},
		{tid("gl1"), "t", "r", "", "Ok", now.Add(-4 * time.Second)},
		{tid("gl1"), "w", "t", "", "Error", now.Add(-3 * time.Second)},
		{tid("gl2"), "r", "", "gl_posting", "Ok", now.Add(-32 * time.Second)},
		{tid("gl2"), "w", "r", "", "Error", now.Add(-30 * time.Second)},
		{tid("orders"), "r", "", "orders_export", "Ok", now.Add(-10 * time.Second)},
		{tid("orders"), "w", "r", "", "Ok", now.Add(-9 * time.Second)},
	}
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, ParentSpanId, SpanName, ServiceName, SpanAttributes, StatusCode, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range spans {
		attrs := map[string]string{}
		if s.dag != "" {
			attrs["airflow.dag_id"] = s.dag
		}
		if err := batch.Append(s.at, s.trace, s.id, s.parent, s.id, svc, attrs, s.status, "slice-probe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}

	dag := func(id string) [][]LogAttrFilter {
		return [][]LogAttrFilter{{
			{Key: "service.name", Op: AttrOpEq, Value: svc},
			{Key: "airflow.dag_id", Op: AttrOpEq, Value: id, Descendants: true},
		}}
	}
	slices := []IntegrationSlice{
		{Key: "gl", Services: []string{svc}, Groups: dag("gl_posting")},
		{Key: "orders", Services: []string{svc}, Groups: dag("orders_export")},
		{Key: "scheduler", Services: []string{svc}, Groups: [][]LogAttrFilter{{{Key: "service.name", Op: AttrOpEq, Value: svc}}}},
	}
	s := New(conn)
	q := SliceQuery{
		From:       now.Add(-time.Minute),
		To:         now.Add(time.Minute),
		AckedUntil: map[string]time.Time{svc: now.Add(-20 * time.Second)},
	}
	got, err := s.SliceTraceStats(ctx, slices, q)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range got {
		t.Logf("%-9s %+v", k, v)
	}

	gl := got["gl"]
	if gl.Traces != 2 || gl.ErrorTraces != 2 || gl.OpenErrorTraces != 1 {
		t.Errorf("gl_posting: want 2 traces, 2 errored, 1 open; got %+v", gl)
	}
	if gl.LastErrorService != svc || gl.SampleTraceID != tid("gl1") {
		t.Errorf("gl_posting: newest open error should be gl1 on the scheduler, got %+v", gl)
	}
	if o := got["orders"]; o.Traces != 1 || o.ErrorTraces != 0 || o.OpenErrorTraces != 0 || !o.LastErrorAt.IsZero() {
		t.Errorf("orders_export shares the scheduler and failed nothing; got %+v", o)
	}
	if w := got["scheduler"]; w.Traces != 3 || w.ErrorTraces != 2 || w.OpenErrorTraces != 1 {
		t.Errorf("the whole scheduler: want 3 traces, 2 errored, 1 open; got %+v", w)
	}

	// Errors only reads the same open errors without counting traffic.
	q.ErrorsOnly = true
	got, err = s.SliceTraceStats(ctx, slices, q)
	if err != nil {
		t.Fatal(err)
	}
	if gl := got["gl"]; gl.Traces != 0 || gl.OpenErrorTraces != 1 {
		t.Errorf("errors only, gl_posting: want 0 traffic, 1 open; got %+v", gl)
	}
	if o := got["orders"]; o.OpenErrorTraces != 0 {
		t.Errorf("errors only, orders_export: got %+v", o)
	}
}
