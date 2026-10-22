//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// #21 against a real ClickHouse: per-day volumes read from part metadata.
//
//	CAPACITY_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./pkg/clickhouse/ -run TestCapacityLive -v

package clickhouse

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestCapacityLive(t *testing.T) {
	addr := os.Getenv("CAPACITY_PROBE_CH")
	if addr == "" {
		t.Skip("set CAPACITY_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	before, err := MeasureSignals(ctx, conn, now)
	if err != nil {
		t.Fatal(err)
	}

	// Two rows today, one yesterday, for a probe service.
	svc := fmt.Sprintf("capacity-probe-%d", now.UnixNano())
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, StatusCode, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	_ = batch.Append(now, "t1"+svc, "s1", "op", svc, "Ok", "capacity-probe")
	_ = batch.Append(now, "t2"+svc, "s2", "op", svc, "Ok", "capacity-probe")
	_ = batch.Append(now.AddDate(0, 0, -1), "t3"+svc, "s3", "op", svc, "Ok", "capacity-probe")
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}

	after, err := MeasureSignals(ctx, conn, now)
	if err != nil {
		t.Fatal(err)
	}
	tr := after["traces"]
	t.Logf("traces: %+v", tr)
	if tr.Today.Day != now.Format("2006-01-02") || tr.LastFullDay.Day != now.AddDate(0, 0, -1).Format("2006-01-02") {
		t.Errorf("days: today %s, last full %s", tr.Today.Day, tr.LastFullDay.Day)
	}
	if got := tr.Today.Rows - before["traces"].Today.Rows; got != 2 {
		t.Errorf("today grew by %d rows, want 2", got)
	}
	if got := tr.LastFullDay.Rows - before["traces"].LastFullDay.Rows; got != 1 {
		t.Errorf("yesterday grew by %d rows, want 1", got)
	}
	if tr.Today.Bytes == 0 || tr.DaysHeld < 2 || tr.OldestDay == "" || tr.NewestDay < tr.LastFullDay.Day {
		t.Errorf("held: %+v", tr)
	}
	for _, s := range []string{"traces", "logs", "metrics"} {
		if _, ok := after[s]; !ok {
			t.Errorf("signal %s missing", s)
		}
	}

	disks, err := MeasureDisks(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) == 0 || disks[0].TotalBytes == 0 || disks[0].FreeBytes > disks[0].TotalBytes {
		t.Errorf("disks: %+v", disks)
	}
}
