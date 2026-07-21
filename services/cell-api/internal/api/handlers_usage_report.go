// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The usage report behind Settings → Reports: for each signal, how much
// of what's being ingested is actually USED (watched by an alert rule /
// health check), and what the unused share costs in storage — the nudge
// that gets admins trimming. Estimates use the table's average
// compressed bytes/row from ClickHouse (cell-level, so on multi-org
// cells the per-org figure is an approximation — good enough for a
// "roughly X MB/day" nudge, and labelled as such in the UI).

package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
)

// UsageServiceRow is one service's footprint for logs/traces.
type UsageServiceRow struct {
	ServiceName string `json:"service_name"`
	Rows        uint64 `json:"rows"`
	EstBytes    uint64 `json:"est_bytes"`
	// Covered: at least one alert rule of this signal is bound to the
	// service — its data is "used".
	Covered bool `json:"covered"`
}

// UsageSignalReport is one signal's section of the report.
type UsageSignalReport struct {
	// Metrics section: catalog counts. Logs/traces: service counts.
	Total   int `json:"total"`
	Unused  int `json:"unused"`
	// UnusedRows is the datapoint/row count the unused share produced in
	// the window; EstBytesPerDay/Per30d scale it by the table's average
	// compressed row size.
	UnusedRows     uint64            `json:"unused_rows"`
	EstBytesPerDay uint64            `json:"est_bytes_per_day"`
	EstBytesPer30d uint64            `json:"est_bytes_per_30d"`
	Services       []UsageServiceRow `json:"services,omitempty"`
}

// UsageReportResponse is GET /api/v1/reports/usage.
type UsageReportResponse struct {
	Window  WindowSummary     `json:"window"`
	Metrics UsageSignalReport `json:"metrics"`
	Logs    UsageSignalReport `json:"logs"`
	Traces  UsageSignalReport `json:"traces"`
}

// usageReport: GET /api/v1/reports/usage?range= (admin)
func (h *Handlers) usageReport(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, 24*time.Hour)
	windowDays := tr.To.Sub(tr.From).Hours() / 24
	if windowDays <= 0 {
		windowDays = 1
	}
	orgID := middleware.OrgID(r)

	// What's watched: alert rules per signal.
	rules, err := h.Alerts.ListRules(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("usage report: list rules failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	watchedMetrics := map[string]bool{}
	logServices := map[string]bool{}
	traceServices := map[string]bool{}
	for _, rule := range rules {
		switch rule.Signal {
		case "metric":
			if rule.Spec.MetricName != "" {
				watchedMetrics[rule.Spec.MetricName] = true
			}
		case "log":
			if rule.ServiceName != "" {
				logServices[rule.ServiceName] = true
			}
		case "trace":
			if rule.ServiceName != "" {
				traceServices[rule.ServiceName] = true
			}
		}
	}

	perDay := func(rows uint64, avg float64) uint64 {
		return uint64(float64(rows) * avg / windowDays)
	}

	// Metrics: catalog vs watched names.
	var metricsRep UsageSignalReport
	if names, merr := h.Store.MetricNames(r.Context(), "", nil, tr.From, tr.To); merr != nil {
		h.Logger.Warn("usage report: metric names failed", "err", merr)
	} else {
		avg, _ := h.Store.TableAvgRowBytes(r.Context(), "metrics")
		metricsRep.Total = len(names)
		for _, n := range names {
			if !watchedMetrics[n.MetricName] {
				metricsRep.Unused++
				metricsRep.UnusedRows += n.PointCount
			}
		}
		metricsRep.EstBytesPerDay = perDay(metricsRep.UnusedRows, avg)
		metricsRep.EstBytesPer30d = metricsRep.EstBytesPerDay * 30
	}

	// Logs + traces: per-service rows vs rule coverage.
	signalRep := func(table string, covered map[string]bool) UsageSignalReport {
		rep := UsageSignalReport{}
		rows, rerr := h.Store.RowsByService(r.Context(), table, tr.From, tr.To)
		if rerr != nil {
			h.Logger.Warn("usage report: rows by service failed", "table", table, "err", rerr)
			return rep
		}
		avg, _ := h.Store.TableAvgRowBytes(r.Context(), table)
		rep.Total = len(rows)
		for _, sr := range rows {
			cov := covered[sr.ServiceName]
			if !cov {
				rep.Unused++
				rep.UnusedRows += sr.Rows
			}
			if len(rep.Services) < 500 {
				rep.Services = append(rep.Services, UsageServiceRow{
					ServiceName: sr.ServiceName,
					Rows:        sr.Rows,
					EstBytes:    uint64(float64(sr.Rows) * avg),
					Covered:     cov,
				})
			}
		}
		// Uncovered first (that's what the report is about), then by size.
		sort.SliceStable(rep.Services, func(i, j int) bool {
			if rep.Services[i].Covered != rep.Services[j].Covered {
				return !rep.Services[i].Covered
			}
			return rep.Services[i].Rows > rep.Services[j].Rows
		})
		rep.EstBytesPerDay = perDay(rep.UnusedRows, avg)
		rep.EstBytesPer30d = rep.EstBytesPerDay * 30
		return rep
	}

	httpserver.WriteJSON(w, http.StatusOK, UsageReportResponse{
		Window:  tr.Window(),
		Metrics: metricsRep,
		Logs:    signalRep("logs", logServices),
		Traces:  signalRep("traces", traceServices),
	})
}
