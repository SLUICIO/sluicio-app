// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Usage report: how much telemetry this cell is storing, broken down per
// service across the three signals (trace spans, metric data points +
// active series, log records) plus the actual on-disk size of each table.
// Org-admin only (gated at route registration) and still visibility-scoped
// (G5) as defence in depth. The per-service rows let the UI roll up by
// service or (client-side) by integration; grand totals are summed here so
// they stay accurate even when the UI shows only a top-N slice.

package api

import (
	"net/http"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
)

// integrationUsage reports how many monitored entities the org has versus the
// licensed cap. A "monitored entity" is an integration flow OR a first-class
// system (broker, database, …) — both are separately monitored (health,
// templates, alerts) and so both count. Used = IntegrationCount + SystemCount;
// the breakdown fields let the UI show "3 integrations + 2 systems".
//
// Limit 0 (and Unlimited) = no cap — the free Community edition, Enterprise
// plans, or any state where the license isn't in force. OverLimit is true once
// the cap is reached (used >= limit), which drives the admin warning. The
// counts are authoritative (from the org's catalogs); the cap is tamper-proof
// (signed into the license). Enforcement is advisory — monitoring never stops.
type integrationUsage struct {
	Used             int  `json:"used"` // integrations + systems, checked against the cap
	IntegrationCount int  `json:"integration_count"`
	SystemCount      int  `json:"system_count"`
	Limit            int  `json:"limit"`
	Unlimited        bool `json:"unlimited"`
	OverLimit        bool `json:"over_limit"`
}

// integrationUsage computes the current usage for the caller's org.
func (h *Handlers) integrationUsage(r *http.Request) integrationUsage {
	orgID := middleware.OrgID(r)
	integrations := 0
	if h.Integrations != nil {
		if rows, err := h.Integrations.List(r.Context(), orgID); err == nil {
			integrations = len(rows)
		}
	}
	systems := 0
	if h.Catalog != nil {
		if rows, err := h.Catalog.ListSystems(r.Context(), orgID); err == nil {
			systems = len(rows)
		}
	}
	used := integrations + systems
	limit := 0
	if h.License != nil {
		limit = h.License.MaxIntegrations()
	}
	u := integrationUsage{
		Used:             used,
		IntegrationCount: integrations,
		SystemCount:      systems,
		Limit:            limit,
		Unlimited:        limit <= 0,
	}
	if !u.Unlimited {
		u.OverLimit = used >= limit
	}
	return u
}

type usageVolumeCounts struct {
	Spans        uint64 `json:"spans"`
	MetricPoints uint64 `json:"metric_points"`
	MetricSeries uint64 `json:"metric_series"`
	Logs         uint64 `json:"logs"`
}

type usageVolumeService struct {
	Service      string `json:"service"`
	Spans        uint64 `json:"spans"`
	MetricPoints uint64 `json:"metric_points"`
	MetricSeries uint64 `json:"metric_series"`
	Logs         uint64 `json:"logs"`
}

// usageStorageSignal is the actual on-disk (compressed) footprint of one
// signal's table, whole-table and all-time. rows lets the UI derive a
// bytes-per-row to estimate per-service size.
type usageStorageSignal struct {
	Bytes uint64 `json:"bytes"`
	Rows  uint64 `json:"rows"`
}

type usageStorage struct {
	Spans        usageStorageSignal `json:"spans"`
	MetricPoints usageStorageSignal `json:"metric_points"`
	Logs         usageStorageSignal `json:"logs"`
}

type usageVolumeResponse struct {
	Window       WindowSummary        `json:"window"`
	Windowed     bool                 `json:"windowed"`
	Totals       usageVolumeCounts    `json:"totals"`
	Storage      usageStorage         `json:"storage"`
	Integrations integrationUsage     `json:"integrations"`
	Services     []usageVolumeService `json:"services"`
}

// usageVolume: GET /api/v1/usage/volume — per-service ingestion counts +
// grand totals across spans, metric data points and logs. By default this
// is the total at rest in the databases; `?windowed=1` bounds it to the
// `range` window instead.
func (h *Handlers) usageVolume(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)
	windowed := r.URL.Query().Get("windowed") == "1" || r.URL.Query().Get("windowed") == "true"
	resp := usageVolumeResponse{
		Window:       tr.Window(),
		Windowed:     windowed,
		Integrations: h.integrationUsage(r),
		Services:     []usageVolumeService{},
	}

	// G5: scope to the caller's visible services.
	pf := h.resolveServiceFilter(r, "", nil)
	if pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, resp)
		return
	}

	rows, err := h.Store.UsageVolume(r.Context(), pf.ServiceIn, tr.From, tr.To, windowed)
	if err != nil {
		h.Logger.Error("usage volume failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	for _, row := range rows {
		resp.Services = append(resp.Services, usageVolumeService{
			Service:      row.Service,
			Spans:        row.Spans,
			MetricPoints: row.MetricPoints,
			MetricSeries: row.MetricSeries,
			Logs:         row.Logs,
		})
		resp.Totals.Spans += row.Spans
		resp.Totals.MetricPoints += row.MetricPoints
		resp.Totals.MetricSeries += row.MetricSeries
		resp.Totals.Logs += row.Logs
	}

	// Actual on-disk size of each table (whole-table, all-time) so the UI
	// can show real GB and an estimated per-service size. Non-fatal: a
	// failure just leaves the storage block zeroed.
	if st, err := h.Store.TableStorage(r.Context()); err != nil {
		h.Logger.Warn("table storage failed", "err", err)
	} else {
		resp.Storage.Spans = usageStorageSignal{Bytes: st["traces"].CompressedBytes, Rows: st["traces"].Rows}
		resp.Storage.MetricPoints = usageStorageSignal{Bytes: st["metrics"].CompressedBytes, Rows: st["metrics"].Rows}
		resp.Storage.Logs = usageStorageSignal{Bytes: st["logs"].CompressedBytes, Rows: st["logs"].Rows}
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}
