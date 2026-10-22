// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Cell capacity (issue #21): is this cell heading for trouble while
// everything still works? A different question from /api/v1/cell-health
// ("is it broken"), asked on a different schedule (weekly, not during an
// incident), so it has its own endpoint: a caller alerting on health does
// not have to ignore half a payload.
//
// Capacity is REPORTED, never judged. Nothing here has a status, a
// threshold or a projection: what counts as "too full" is the operator's
// tolerance, not ours, and a forecast that is wrong often enough costs
// more trust than it earns. A disk that is actually full stops writes,
// which cell-health already reports as a dependency that stopped working.
//
// Aggregate only, never per organization: a cell's operator and a
// tenant's admins can be different parties, and per-org volumes would
// tell the operator what each tenant ingests. (The deployment reads
// per-org figures from /api/v1/deployment/usage, with its own token.)
//
// Cheap enough to scrape: ClickHouse part metadata and disk figures, and
// Postgres's own database size. No row is counted.

package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	imclickhouse "github.com/sluicio/sluicio-app/pkg/clickhouse"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/settings"
)

// CellCapacity is the capacity report. Each section carries its own
// error, so one store that cannot be measured does not hide the others.
type CellCapacity struct {
	MeasuredAt time.Time          `json:"measured_at"`
	ClickHouse ClickHouseCapacity `json:"clickhouse"`
	Postgres   PostgresCapacity   `json:"postgres"`
	// Signals is what each telemetry signal holds and takes in, keyed
	// traces / logs / metrics.
	Signals map[string]SignalCapacity `json:"signals"`
}

type ClickHouseCapacity struct {
	Disks []imclickhouse.Disk `json:"disks"`
	// TelemetryBytes is everything in the telemetry database on disk,
	// signals and the smaller tables kept alongside them.
	TelemetryBytes uint64            `json:"telemetry_bytes"`
	ByTable        map[string]uint64 `json:"by_table,omitempty"`
	Error          string            `json:"error,omitempty"`
}

type PostgresCapacity struct {
	DatabaseBytes uint64 `json:"database_bytes"`
	Error         string `json:"error,omitempty"`
}

// SignalCapacity is one signal's volume next to its configured
// retention. Held beyond the window is visible as OldestDay older than
// RetentionDays allow; the report says what is there, not whether
// that is a problem (a retention change takes effect at the next
// enforcement, and partitions are dropped whole).
type SignalCapacity struct {
	imclickhouse.SignalVolume
	RetentionDays int    `json:"retention_days,omitempty"`
	Error         string `json:"error,omitempty"`
}

// cellCapacity: GET /api/v1/cell-capacity. Same access as cell-health:
// SLUICIO_HEALTH_TOKEN for machines, an operator session for people.
// ?format=prometheus answers in the Prometheus text format.
func (h *Handlers) cellCapacity(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	report := h.measureCapacity(ctx, time.Now())
	if r.URL.Query().Get("format") == "prometheus" {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(capacityPrometheus(report)))
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, report)
}

func (h *Handlers) measureCapacity(ctx context.Context, now time.Time) CellCapacity {
	out := CellCapacity{MeasuredAt: now.UTC(), Signals: map[string]SignalCapacity{}}

	if h.ClickHouseConn == nil {
		out.ClickHouse.Error = "no clickhouse connection"
	} else {
		if disks, err := imclickhouse.MeasureDisks(ctx, h.ClickHouseConn); err != nil {
			out.ClickHouse.Error = "disks could not be measured"
			h.Logger.Warn("cell capacity: disks", "err", err)
		} else {
			out.ClickHouse.Disks = disks
		}
		if u, err := h.storageUsage(ctx); err != nil {
			out.ClickHouse.Error = "storage could not be measured"
			h.Logger.Warn("cell capacity: storage", "err", err)
		} else {
			out.ClickHouse.TelemetryBytes = u.Bytes
			out.ClickHouse.ByTable = u.ByTable
		}
	}

	if h.PGPool == nil {
		out.Postgres.Error = "no postgres pool"
	} else if err := h.PGPool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&out.Postgres.DatabaseBytes); err != nil {
		out.Postgres.Error = "database size could not be read"
		h.Logger.Warn("cell capacity: postgres size", "err", err)
	}

	var retention settings.RetentionPolicy
	haveRetention := false
	if h.Settings != nil {
		if p, err := h.Settings.GetRetention(ctx); err == nil {
			retention, haveRetention = p, true
		} else {
			h.Logger.Warn("cell capacity: retention policy", "err", err)
		}
	}
	retentionDays := func(signal string) int {
		if !haveRetention {
			return 0
		}
		switch settings.TelemetryType(signal) {
		case settings.TelemetryTraces:
			return retention.Traces.Days
		case settings.TelemetryLogs:
			return retention.Logs.Days
		case settings.TelemetryMetrics:
			return retention.Metrics.Days
		}
		return 0
	}

	var volumes map[string]imclickhouse.SignalVolume
	var volErr error
	if h.ClickHouseConn != nil {
		volumes, volErr = imclickhouse.MeasureSignals(ctx, h.ClickHouseConn, now)
		if volErr != nil {
			h.Logger.Warn("cell capacity: signals", "err", volErr)
		}
	}
	for signal := range imclickhouse.SignalTables {
		sc := SignalCapacity{RetentionDays: retentionDays(signal)}
		switch {
		case h.ClickHouseConn == nil:
			sc.Error = "no clickhouse connection"
		case volErr != nil:
			sc.Error = "volume could not be measured"
		default:
			sc.SignalVolume = volumes[signal]
		}
		out.Signals[signal] = sc
	}
	return out
}

// capacityPrometheus renders the report as Prometheus text. Hand-written
// rather than a client library: a dozen gauges do not justify a
// dependency, and nothing here is a counter the process keeps. A section
// that could not be measured is left out and reported through
// sluicio_capacity_measured, so a scrape tells "zero" from "unknown".
func capacityPrometheus(c CellCapacity) string {
	var b strings.Builder
	gauge := func(name, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	}
	val := func(name, labels string, v float64) {
		if labels != "" {
			fmt.Fprintf(&b, "%s{%s} %g\n", name, labels, v)
		} else {
			fmt.Fprintf(&b, "%s %g\n", name, v)
		}
	}
	boolf := func(ok bool) float64 {
		if ok {
			return 1
		}
		return 0
	}

	gauge("sluicio_capacity_measured", "1 when the section could be measured, 0 when it could not.")
	val("sluicio_capacity_measured", `section="clickhouse"`, boolf(c.ClickHouse.Error == ""))
	val("sluicio_capacity_measured", `section="postgres"`, boolf(c.Postgres.Error == ""))

	if len(c.ClickHouse.Disks) > 0 {
		gauge("sluicio_clickhouse_disk_free_bytes", "Free space on a ClickHouse disk, as the filesystem reports it.")
		for _, d := range c.ClickHouse.Disks {
			val("sluicio_clickhouse_disk_free_bytes", `disk="`+promLabel(d.Name)+`"`, float64(d.FreeBytes))
		}
		gauge("sluicio_clickhouse_disk_total_bytes", "Size of a ClickHouse disk.")
		for _, d := range c.ClickHouse.Disks {
			val("sluicio_clickhouse_disk_total_bytes", `disk="`+promLabel(d.Name)+`"`, float64(d.TotalBytes))
		}
		gauge("sluicio_clickhouse_disk_keep_free_bytes", "Space ClickHouse is configured never to use on a disk.")
		for _, d := range c.ClickHouse.Disks {
			val("sluicio_clickhouse_disk_keep_free_bytes", `disk="`+promLabel(d.Name)+`"`, float64(d.KeepFreeBytes))
		}
	}
	if len(c.ClickHouse.ByTable) > 0 {
		gauge("sluicio_clickhouse_table_bytes", "Bytes on disk per table in the telemetry database.")
		tables := make([]string, 0, len(c.ClickHouse.ByTable))
		for t := range c.ClickHouse.ByTable {
			tables = append(tables, t)
		}
		sort.Strings(tables)
		for _, t := range tables {
			val("sluicio_clickhouse_table_bytes", `table="`+promLabel(t)+`"`, float64(c.ClickHouse.ByTable[t]))
		}
	}
	if c.Postgres.Error == "" {
		gauge("sluicio_postgres_database_bytes", "Size of the Postgres database.")
		val("sluicio_postgres_database_bytes", "", float64(c.Postgres.DatabaseBytes))
	}

	signals := make([]string, 0, len(c.Signals))
	for s, sc := range c.Signals {
		if sc.Error == "" {
			signals = append(signals, s)
		}
	}
	sort.Strings(signals)
	perSignal := func(name, help string, get func(SignalCapacity) float64) {
		if len(signals) == 0 {
			return
		}
		gauge(name, help)
		for _, s := range signals {
			val(name, `signal="`+s+`"`, get(c.Signals[s]))
		}
	}
	perSignal("sluicio_signal_stored_bytes", "Bytes on disk held for a signal.", func(s SignalCapacity) float64 { return float64(s.Bytes) })
	perSignal("sluicio_signal_stored_rows", "Rows held for a signal.", func(s SignalCapacity) float64 { return float64(s.Rows) })
	// Durations in seconds, as Prometheus wants base units: how far back
	// a signal reaches is time() - sluicio_signal_oldest_timestamp_seconds.
	perSignal("sluicio_signal_oldest_timestamp_seconds", "Start of the oldest UTC day a signal holds data for, as a Unix time (0 when it holds none).", func(s SignalCapacity) float64 {
		d, err := time.Parse("2006-01-02", s.OldestDay)
		if err != nil {
			return 0
		}
		return float64(d.Unix())
	})
	perSignal("sluicio_signal_retention_seconds", "Configured retention for a signal (0 when unknown).", func(s SignalCapacity) float64 { return float64(s.RetentionDays) * 86400 })
	perSignal("sluicio_signal_last_full_day_bytes", "Bytes stored for the last complete UTC day: the ingest rate.", func(s SignalCapacity) float64 { return float64(s.LastFullDay.Bytes) })
	perSignal("sluicio_signal_last_full_day_rows", "Rows stored for the last complete UTC day.", func(s SignalCapacity) float64 { return float64(s.LastFullDay.Rows) })
	perSignal("sluicio_signal_today_bytes", "Bytes stored so far today (UTC).", func(s SignalCapacity) float64 { return float64(s.Today.Bytes) })
	perSignal("sluicio_signal_today_rows", "Rows stored so far today (UTC).", func(s SignalCapacity) float64 { return float64(s.Today.Rows) })
	return b.String()
}

// promLabel escapes a label value for the text format.
func promLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}
