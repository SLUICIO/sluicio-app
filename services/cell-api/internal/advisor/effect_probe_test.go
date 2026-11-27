//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The effect query against a live ClickHouse (build tag `liveprobe`), for
// the query shape: a metric that arrives steadily measures about the same
// rate on both sides of a moment, and an unknown one measures nothing.
//
//	ADVISOR_PROBE_CH=localhost:9000 ADVISOR_PROBE_CH_USER=… ADVISOR_PROBE_CH_PASS=… \
//	ADVISOR_PROBE_ORG=<org id> ADVISOR_PROBE_METRIC=<a steady metric> \
//	go test -tags liveprobe ./services/cell-api/internal/advisor/ -run TestMeasureMetricProbe -v

package advisor

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
)

func TestMeasureMetricProbe(t *testing.T) {
	addr, metric := os.Getenv("ADVISOR_PROBE_CH"), os.Getenv("ADVISOR_PROBE_METRIC")
	org, err := uuid.Parse(os.Getenv("ADVISOR_PROBE_ORG"))
	if addr == "" || metric == "" || err != nil {
		t.Skip("set ADVISOR_PROBE_CH, ADVISOR_PROBE_ORG and ADVISOR_PROBE_METRIC")
	}
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{Database: "telemetry", Username: os.Getenv("ADVISOR_PROBE_CH_USER"), Password: os.Getenv("ADVISOR_PROBE_CH_PASS")},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	applied := now.Add(-time.Hour)

	e, err := MeasureMetric(ctx, conn, org, metric, applied, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: before %d rows (%.0f/h), after %d rows (%.0f/h)", metric, e.Before.Rows, e.Before.PerHour, e.After.Rows, e.After.PerHour)
	if !e.Ready || e.Before.Rows == 0 || e.After.Rows == 0 || e.ChangePct == nil {
		t.Fatalf("a steady metric measured nothing: %+v", e)
	}
	if *e.ChangePct < -50 || *e.ChangePct > 100 {
		t.Errorf("a steady metric changed by %.0f%%", *e.ChangePct)
	}

	none, err := MeasureMetric(ctx, conn, org, "no.such.metric.sluicio.probe", applied, now)
	if err != nil || none.Before.Rows != 0 || none.After.Rows != 0 || none.ChangePct != nil {
		t.Fatalf("unknown metric: %+v %v", none, err)
	}

	early, _ := MeasureMetric(ctx, conn, org, metric, now.Add(-5*time.Minute), now)
	if early.Ready {
		t.Error("measured five minutes after applying")
	}
}
