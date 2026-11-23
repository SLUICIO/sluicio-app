//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The environment of a metric labelled per data point, against a real
// ClickHouse. A collector that scrapes one cloud account for several
// environments (Azure Monitor, a Service Bus namespace per environment)
// can only put the environment on each data point. Read from the
// resource alone, every such series was "(no environment)": a check
// scoped to production read nothing, and per-environment checks folded
// test into prod. Traces that carry the environment on the resource keep
// it with a cell key set for the metrics.
//
//	ENV_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestEnvironmentOnDataPointsLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	imclickhouse "github.com/sluicio/sluicio-app/pkg/clickhouse"
)

func TestEnvironmentOnDataPointsLive(t *testing.T) {
	addr := os.Getenv("ENV_PROBE_CH")
	if addr == "" {
		t.Skip("set ENV_PROBE_CH")
	}
	t.Cleanup(func() { SetEnvironmentConfig(EnvironmentConfig{}) })
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	s := New(conn)
	run := time.Now().UnixNano()
	now := time.Now().UTC()
	from, to := now.Add(-time.Minute), now.Add(time.Minute)
	at := now.Add(-10 * time.Second)
	broker := fmt.Sprintf("env-probe-sb-%d", run)
	app := fmt.Sprintf("env-probe-app-%d", run)
	const metric = "servicebus.queue.deadletter_messages"

	mb, err := conn.PrepareBatch(ctx, "INSERT INTO metrics (Timestamp, MetricName, MetricType, ServiceName, Value, ResourceAttributes, MetricAttributes, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		res, attrs map[string]string
		v          float64
	}{
		{map[string]string{}, map[string]string{"environment": "prod", "queue": "orders"}, 5},
		{map[string]string{}, map[string]string{"environment": "test", "queue": "orders"}, 700},
		// The resource still wins where it says something.
		{map[string]string{"deployment.environment": "staging"}, map[string]string{"environment": "prod", "queue": "orders"}, 1},
	} {
		if err := mb.Append(at, metric, "gauge", broker, p.v, p.res, p.attrs, "env-probe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := mb.Send(); err != nil {
		t.Fatal(err)
	}
	tb, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, ResourceAttributes, SpanAttributes, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	if err := tb.Append(at, fmt.Sprintf("%032x", run), "0000000000000001", "send", app, map[string]string{"deployment.environment": "prod"}, map[string]string{}, "env-probe"); err != nil {
		t.Fatal(err)
	}
	if err := tb.Send(); err != nil {
		t.Fatal(err)
	}

	// The cell names the key its metrics carry.
	SetEnvironmentConfig(EnvironmentConfig{Key: "environment"})

	sum := func(env string) float64 {
		t.Helper()
		v, n, err := s.MetricAggregate(ctx, metric, nil, "sum", from, to, []string{broker}, WithEnvironment(nil, env))
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return -1
		}
		return v
	}
	if got := sum("prod"); got != 5 {
		t.Errorf("prod reads %v, want the prod data point alone (5)", got)
	}
	if got := sum("test"); got != 700 {
		t.Errorf("test reads %v, want 700", got)
	}
	if got := sum("staging"); got != 1 {
		t.Errorf("staging reads %v, want the point whose resource says staging", got)
	}

	vols, err := s.EnvironmentsIn(ctx, []string{broker, app}, nil, from, to)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]EnvironmentVolume{}
	for _, v := range vols {
		got[v.Environment] = v
	}
	if got["prod"].MetricRows != 1 || got["test"].MetricRows != 1 || got["prod"].Messages != 1 {
		t.Errorf("environments = %+v; want prod with a metric row and the trace, test with a metric row", vols)
	}
	if _, ok := got[""]; ok {
		t.Errorf("a row read as (no environment): %+v", vols)
	}

	// The per-request filter narrows each table by its own attributes.
	filtered := imclickhouse.WithTableFilters(ctx, "env-probe", func(table string) string {
		return "(" + EnvironmentExprIn(PrimaryMapOf(table)) + ") IN ('test')"
	})
	v, n, err := s.MetricAggregate(filtered, metric, nil, "sum", from, to, []string{broker}, nil)
	if err != nil || n != 1 || v != 700 {
		t.Errorf("a request read in test sees %v over %d rows (%v), want 700 over 1", v, n, err)
	}
}
