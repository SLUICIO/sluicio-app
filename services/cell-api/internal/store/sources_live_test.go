//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// An integration's metric and log sources (#43) against a real
// ClickHouse: a broker that is not a member, serving two integrations'
// queues. The integration reads its members plus its own queue of the
// broker, never the other queue, and a log source is read beside the
// one-trace gate its broker's logs could never pass.
//
//	SOURCES_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestSourcesLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestSourcesLive(t *testing.T) {
	addr := os.Getenv("SOURCES_PROBE_CH")
	if addr == "" {
		t.Skip("set SOURCES_PROBE_CH")
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
	broker := fmt.Sprintf("sources-broker-%d", run)
	a, b := fmt.Sprintf("sources-a-%d", run), fmt.Sprintf("sources-b-%d", run)
	at := now.Add(-10 * time.Second)

	mb, err := conn.PrepareBatch(ctx, "INSERT INTO metrics (Timestamp, MetricName, MetricType, ServiceName, Value, ResourceAttributes, MetricAttributes, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		svc, metric, queue string
		v                  float64
	}{
		{broker, "rabbitmq.queue.depth", "orders", 40},
		{broker, "rabbitmq.queue.depth", "payments", 9000},
		{broker, "rabbitmq.node.memory", "", 5000}, // not a queue metric
		{a, "app.inflight", "", 3},
	} {
		attrs := map[string]string{}
		if p.queue != "" {
			attrs["rabbitmq.queue.name"] = p.queue
		}
		if err := mb.Append(at, p.metric, "gauge", p.svc, p.v, map[string]string{}, attrs, "sources-probe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := mb.Send(); err != nil {
		t.Fatal(err)
	}

	m0, m1 := 0, 1
	members := [][]LogAttrFilter{
		{{Key: "service.name", Op: AttrOpEq, Value: a, RequireAllGroups: true, Member: &m0}, {Key: "service.name", Op: AttrOpMatches, Value: "^(?:" + a + "|" + b + ")$", RequireAllGroups: true, Member: &m0}},
		{{Key: "service.name", Op: AttrOpEq, Value: b, RequireAllGroups: true, Member: &m1}, {Key: "service.name", Op: AttrOpMatches, Value: "^(?:" + a + "|" + b + ")$", RequireAllGroups: true, Member: &m1}},
	}
	source := []LogAttrFilter{
		{Key: "service.name", Op: AttrOpMatches, Value: "^(?:" + broker + ")$", Source: true},
		{Key: "rabbitmq.queue.name", Op: AttrOpEq, Value: "orders", Source: true},
		{Key: MetricNameAttrKey, Op: AttrOpMatches, Value: `^(?:rabbitmq\.queue\..*)$`, Source: true},
	}
	groups := append(append([][]LogAttrFilter{}, members...), source)
	svcs := []string{a, b, broker}

	for _, c := range []struct {
		metric string
		want   float64
		n      uint64
	}{
		{"rabbitmq.queue.depth", 40, 1}, // orders only, never payments
		{"rabbitmq.node.memory", 0, 0},  // outside the source's metric names
		{"app.inflight", 3, 1},          // the members still read
	} {
		v, n, err := s.MetricAggregate(ctx, c.metric, nil, "last", from, to, svcs, groups)
		if err != nil {
			t.Fatal(err)
		}
		if n != c.n || v != c.want {
			t.Errorf("%s: want %v from %d samples, got %v from %d", c.metric, c.want, c.n, v, n)
		}
	}

	// Logs: the members are combined as one trace through every member,
	// a gate the broker's logs (no trace) could never pass.
	lb, err := conn.PrepareBatch(ctx, "INSERT INTO logs (Timestamp, TraceId, SpanId, ServiceName, Body, LogAttributes, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []struct{ svc, queue, body string }{
		{broker, "orders", "consumer cancelled on orders"},
		{broker, "payments", "consumer cancelled on payments"},
	} {
		if err := lb.Append(at, "", "", l.svc, l.body, map[string]string{"rabbitmq.queue.name": l.queue}, "sources-probe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := lb.Send(); err != nil {
		t.Fatal(err)
	}
	logSource := source[:2]
	n, err := s.CountLogs(ctx, LogQueryParams{ServiceIn: svcs, From: from, To: to, AttrGroups: append(append([][]LogAttrFilter{}, members...), logSource)})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("the broker's orders log, beside the gate: want 1, got %d", n)
	}
}
