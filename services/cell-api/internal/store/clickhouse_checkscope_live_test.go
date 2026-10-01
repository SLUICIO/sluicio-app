//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Runs the reads a health check bound to an integration makes against a
// real ClickHouse, with two integrations sharing one service: two queues
// of one broker (metrics), and two flows of one runtime (traces). Each
// check must read its own integration's slice, never its sibling's.
//
//	SUBTREE_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestCheckScopeLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestCheckScopeLive(t *testing.T) {
	addr := os.Getenv("SUBTREE_PROBE_CH")
	if addr == "" {
		t.Skip("set SUBTREE_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	s := New(conn)
	run := time.Now().UnixNano()
	now := time.Now().UTC()
	from, to := now.Add(-time.Minute), now.Add(time.Minute)

	// ── metrics: one broker, two queues, one scrape ─────────────────────
	// Both queues report at the SAME timestamp, which is what a collector
	// scrape does. A check that pooled them picked one by tie-break.
	broker := fmt.Sprintf("checkscope-broker-%d", run)
	mb, err := conn.PrepareBatch(ctx, "INSERT INTO metrics (Timestamp, MetricName, MetricType, ServiceName, Value, ResourceAttributes, MetricAttributes, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(-10 * time.Second)
	for _, p := range []struct {
		queue, state string
		v            float64
	}{
		{"invoices", "ready", 873}, {"invoices", "unacknowledged", 10},
		{"shipments", "ready", 6000}, {"shipments", "unacknowledged", 0},
	} {
		if err := mb.Append(at, "queue.depth", "gauge", broker, p.v,
			map[string]string{"rabbitmq.queue.name": p.queue}, map[string]string{"state": p.state}, "checkscope-probe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := mb.Send(); err != nil {
		t.Fatal(err)
	}
	queue := func(name string) [][]LogAttrFilter {
		return [][]LogAttrFilter{{
			{Key: "service.name", Op: AttrOpEq, Value: broker},
			{Key: "rabbitmq.queue.name", Op: AttrOpEq, Value: name},
		}}
	}
	ready := []LogAttrFilter{{Key: "state", Op: AttrOpEq, Value: "ready"}}
	svcs := []string{broker}

	for _, c := range []struct {
		queue string
		want  float64
	}{{"invoices", 873}, {"shipments", 6000}} {
		v, n, err := s.MetricAggregate(ctx, "queue.depth", ready, "last", from, to, svcs, queue(c.queue))
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 || v != c.want {
			t.Errorf("%s ready depth: want %v from 1 sample, got %v from %d", c.queue, c.want, v, n)
		}
		series, err := s.MetricSeriesCount(ctx, "queue.depth", ready, from, to, svcs, queue(c.queue))
		if err != nil {
			t.Fatal(err)
		}
		if series != 1 {
			t.Errorf("%s: the check's filter should leave one series, got %d", c.queue, series)
		}
	}
	if _, n, err := s.MetricAggregate(ctx, "queue.depth", ready, "last", from, to, svcs, nil); err != nil || n != 2 {
		t.Errorf("without conditions the broker's two queues are both read: n=%d err=%v", n, err)
	}
	groups, err := s.MetricAggregateGrouped(ctx, "queue.depth", nil, "last", "state", from, to, svcs, queue("invoices"))
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Label != "ready" || groups[0].Value != 873 {
		t.Errorf("invoices split by state: want ready=873 first of 2, got %+v", groups)
	}

	// ── one counter, its resource map stored in two key orders ──────────
	// Written with literal maps, because a map's stored order is the order
	// its keys arrived in, and the driver's order comes from a Go map.
	if err := conn.Exec(ctx, `
		INSERT INTO metrics (Timestamp, MetricName, MetricType, ServiceName, Value, IsMonotonic, ResourceAttributes, MetricAttributes, OrganizationId) VALUES
		(?, 'queue.published', 'sum', ?, 100, 1, map('rabbitmq.queue.name', 'invoices', 'rabbitmq.vhost.name', '/'), map(), 'checkscope-probe'),
		(?, 'queue.published', 'sum', ?, 160, 1, map('rabbitmq.vhost.name', '/', 'rabbitmq.queue.name', 'invoices'), map(), 'checkscope-probe')`,
		now.Add(-40*time.Second), broker, now.Add(-10*time.Second), broker); err != nil {
		t.Fatal(err)
	}
	if v, _, err := s.MetricAggregate(ctx, "queue.published", nil, "increase", from, to, svcs, queue("invoices")); err != nil || v != 60 {
		t.Errorf("one counter rising 100 -> 160 must increase by 60 whatever its map order; got %v (err %v)", v, err)
	}
	if n, err := s.MetricSeriesCount(ctx, "queue.published", nil, from, to, svcs, queue("invoices")); err != nil || n != 1 {
		t.Errorf("one counter in two map orders is one series; got %d (err %v)", n, err)
	}

	// ── traces: one runtime, two flows ──────────────────────────────────
	runtime := fmt.Sprintf("checkscope-runtime-%d", run)
	tid := func(s string) string { return fmt.Sprintf("%s%020d", s, run) }
	tb, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, ParentSpanId, SpanName, ServiceName, SpanAttributes, StatusCode, DurationNs, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range []struct {
		trace, flow, status string
		ms                  int64
	}{
		{tid("a1"), "billing", "Error", 900},
		{tid("a2"), "billing", "Ok", 700},
		{tid("b1"), "shipping", "Ok", 20},
	} {
		attrs := map[string]string{"flow": sp.flow, "doc.kind": "invoice"}
		if err := tb.Append(now.Add(-5*time.Second), sp.trace, "s", "", "run", runtime, attrs, sp.status,
			uint64(sp.ms*int64(time.Millisecond)), "checkscope-probe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tb.Send(); err != nil {
		t.Fatal(err)
	}
	flow := func(name string) [][]LogAttrFilter {
		return [][]LogAttrFilter{{
			{Key: "service.name", Op: AttrOpEq, Value: runtime},
			{Key: "flow", Op: AttrOpEq, Value: name},
		}}
	}
	rt := []string{runtime}

	if n, err := s.CountErrorTracesForServices(ctx, rt, from, to, nil, flow("shipping")); err != nil || n != 0 {
		t.Errorf("shipping failed nothing; its failed-traces check read %d (err %v)", n, err)
	}
	if n, err := s.CountErrorTracesForServices(ctx, rt, from, to, nil, flow("billing")); err != nil || n != 1 {
		t.Errorf("billing failed once; got %d (err %v)", n, err)
	}
	if n, err := s.ErrorTraceCountSince(ctx, runtime, from, to, nil, flow("shipping")); err != nil || n != 0 {
		t.Errorf("shipping since a clear: want 0, got %d (err %v)", n, err)
	}
	invoice := []LogAttrFilter{{Key: "doc.kind", Op: AttrOpEq, Value: "invoice"}}
	if n, err := s.CountTracesMatchingAttrs(ctx, rt, from, to, invoice, flow("shipping")); err != nil || n != 1 {
		t.Errorf("shipping's traces carrying doc.kind: want 1, got %d (err %v)", n, err)
	}
	ms, n, err := s.LatencyMsForServices(ctx, rt, 1.0, from, to, flow("shipping"))
	if err != nil || n != 1 || ms < 19 || ms > 21 {
		t.Errorf("shipping's max response time: want 20ms from 1 span, got %vms from %d (err %v)", ms, n, err)
	}
}
