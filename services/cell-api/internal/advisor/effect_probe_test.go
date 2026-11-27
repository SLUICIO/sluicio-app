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
	"strings"
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

	// Logs, with ADVISOR_PROBE_LOG_SERVICE: a service with steady records
	// below WARN.
	if svc := os.Getenv("ADVISOR_PROBE_LOG_SERVICE"); svc != "" {
		l, err := MeasureLogs(ctx, conn, org, svc, 13, applied, now)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s logs below WARN: before %d rows (%.0f/h), after %d rows (%.0f/h)", svc, l.Before.Rows, l.Before.PerHour, l.After.Rows, l.After.PerHour)
		if !l.Ready || l.Before.Rows == 0 || l.After.Rows == 0 {
			t.Fatalf("a steady log stream measured nothing: %+v", l)
		}
	}
}

// A change applied for real: ADVISOR_PROBE_APPLIED is when the collector
// applied it (RFC 3339, the deployment's finished_at), and each metric in
// ADVISOR_PROBE_DROPPED (comma separated) should have dropped. Logs the
// measurement for each; with ADVISOR_PROBE_STOPPED it must also have
// stopped arriving altogether.
func TestMeasureAppliedChangeProbe(t *testing.T) {
	addr, dropped := os.Getenv("ADVISOR_PROBE_CH"), os.Getenv("ADVISOR_PROBE_DROPPED")
	org, err := uuid.Parse(os.Getenv("ADVISOR_PROBE_ORG"))
	applied, terr := time.Parse(time.RFC3339Nano, os.Getenv("ADVISOR_PROBE_APPLIED"))
	if addr == "" || dropped == "" || err != nil || terr != nil {
		t.Skip("set ADVISOR_PROBE_CH, ADVISOR_PROBE_ORG, ADVISOR_PROBE_APPLIED and ADVISOR_PROBE_DROPPED")
	}
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{Database: "telemetry", Username: os.Getenv("ADVISOR_PROBE_CH_USER"), Password: os.Getenv("ADVISOR_PROBE_CH_PASS")},
	})
	if err != nil {
		t.Fatal(err)
	}
	stopped := map[string]bool{}
	for _, m := range strings.Split(os.Getenv("ADVISOR_PROBE_STOPPED"), ",") {
		stopped[strings.TrimSpace(m)] = true
	}
	for _, m := range strings.Split(dropped, ",") {
		m = strings.TrimSpace(m)
		e, err := MeasureMetric(context.Background(), conn, org, m, applied, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if !e.Ready || e.ChangePct == nil {
			t.Fatalf("%s: not measurable yet: %+v", m, e)
		}
		t.Logf("%s: %.0f/h before, %.0f/h after (%s each side), %+.0f%%", m, e.Before.PerHour, e.After.PerHour, e.After.To.Sub(e.After.From).Round(time.Second), *e.ChangePct)
		if *e.ChangePct >= 0 {
			t.Errorf("%s did not drop", m)
		}
		if stopped[m] && e.After.Rows != 0 {
			t.Errorf("%s still arrives: %d rows after", m, e.After.Rows)
		}
	}
}

// Span attributes, with ADVISOR_PROBE_SPAN_SERVICE and
// ADVISOR_PROBE_SPAN_APPLIED (a moment between spans sent before and
// after a change that deleted debug.blob and the http.request.header.*
// family and hashed customer.email): each must be present before and
// gone after.
func TestMeasureSpansProbe(t *testing.T) {
	addr, svc := os.Getenv("ADVISOR_PROBE_CH"), os.Getenv("ADVISOR_PROBE_SPAN_SERVICE")
	org, err := uuid.Parse(os.Getenv("ADVISOR_PROBE_ORG"))
	applied, terr := time.Parse(time.RFC3339, os.Getenv("ADVISOR_PROBE_SPAN_APPLIED"))
	if addr == "" || svc == "" || err != nil || terr != nil {
		t.Skip("set ADVISOR_PROBE_CH, ADVISOR_PROBE_ORG, ADVISOR_PROBE_SPAN_SERVICE and ADVISOR_PROBE_SPAN_APPLIED")
	}
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{Database: "telemetry", Username: os.Getenv("ADVISOR_PROBE_CH_USER"), Password: os.Getenv("ADVISOR_PROBE_CH_PASS")},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := applied.Add(20 * time.Minute)
	for _, c := range []struct {
		key          string
		prefix, hash bool
	}{{"debug.blob", false, false}, {"http.request.header.", true, false}, {"customer.email", false, true}} {
		e, err := MeasureSpans(context.Background(), conn, org, svc, c.key, c.prefix, c.hash, applied, now)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s (prefix %v, hash %v): %d spans before, %d after", c.key, c.prefix, c.hash, e.Before.Rows, e.After.Rows)
		if e.Before.Rows == 0 || e.After.Rows != 0 {
			t.Errorf("%s: before %d, after %d; want some, then none", c.key, e.Before.Rows, e.After.Rows)
		}
	}
}
