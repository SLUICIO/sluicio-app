// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	imclickhouse "github.com/sluicio/sluicio-app/pkg/clickhouse"
)

func sampleCapacity() CellCapacity {
	c := CellCapacity{
		MeasuredAt: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
		ClickHouse: ClickHouseCapacity{
			Disks:          []imclickhouse.Disk{{Name: `de"fault`, Path: "/var/lib/clickhouse/", FreeBytes: 40e9, TotalBytes: 100e9}},
			TelemetryBytes: 12e9,
			ByTable:        map[string]uint64{"traces": 9e9, "logs": 3e9},
		},
		Postgres: PostgresCapacity{DatabaseBytes: 250e6},
		Signals:  map[string]SignalCapacity{},
	}
	c.Signals["traces"] = SignalCapacity{
		SignalVolume: imclickhouse.SignalVolume{
			Rows: 1000, Bytes: 9e9, OldestDay: "2026-10-08", NewestDay: "2026-10-22", DaysHeld: 15,
			LastFullDay: imclickhouse.DayVolume{Day: "2026-10-21", Rows: 70, Bytes: 6e8},
			Today:       imclickhouse.DayVolume{Day: "2026-10-22", Rows: 30, Bytes: 2e8},
		},
		RetentionDays: 14,
	}
	c.Signals["logs"] = SignalCapacity{Error: "volume could not be measured"}
	return c
}

// Capacity is an observation: the report must not grow a verdict. A
// status, threshold or projection field here is the design being undone.
func TestCapacityReportCarriesNoVerdict(t *testing.T) {
	raw, err := json.Marshal(sampleCapacity())
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{`"status"`, `"threshold`, `"days_left`, `"days_until`, `"projected`, `"healthy"`, `"org`} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("capacity report contains %s: %s", banned, raw)
		}
	}
}

func TestCapacityPrometheus(t *testing.T) {
	out := capacityPrometheus(sampleCapacity())
	for _, want := range []string{
		`sluicio_capacity_measured{section="clickhouse"} 1`,
		`sluicio_capacity_measured{section="postgres"} 1`,
		`sluicio_clickhouse_disk_free_bytes{disk="de\"fault"} 4e+10`,
		`sluicio_clickhouse_table_bytes{table="traces"} 9e+09`,
		`sluicio_postgres_database_bytes 2.5e+08`,
		`sluicio_signal_last_full_day_bytes{signal="traces"} 6e+08`,
		`sluicio_signal_retention_seconds{signal="traces"} 1.2096e+06`,
		`sluicio_signal_oldest_timestamp_seconds{signal="traces"} 1.7914176e+09`,
		"# TYPE sluicio_signal_stored_bytes gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// A signal that could not be measured is absent, not zero.
	if strings.Contains(out, `signal="logs"`) {
		t.Errorf("unmeasured signal rendered as a value:\n%s", out)
	}

	// An unmeasured section says so and drops its gauge.
	c := sampleCapacity()
	c.Postgres = PostgresCapacity{Error: "database size could not be read"}
	out = capacityPrometheus(c)
	if !strings.Contains(out, `sluicio_capacity_measured{section="postgres"} 0`) {
		t.Error("postgres not reported as unmeasured")
	}
	if strings.Contains(out, "sluicio_postgres_database_bytes") {
		t.Error("postgres gauge rendered without a measurement")
	}
}
