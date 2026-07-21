// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// These endpoints expose the OTLP metrics a service has pushed in via
// cell-ingest (the `metrics` ClickHouse table). They are distinct from
// the custom-metrics endpoints under /services/{name}/metrics, which
// are the manual-push threshold metrics that drive health — hence the
// separate `metric-names` / `metric-series` paths.

const (
	// metricSeriesTargetPoints is how many buckets we aim to return for
	// a charted series; the bucket width is derived from the window so
	// the chart stays readable regardless of range.
	metricSeriesTargetPoints = 120
	// metricSeriesMinStepSeconds floors the bucket width so a tiny
	// window doesn't produce sub-second buckets.
	metricSeriesMinStepSeconds = 15
)

// listServiceMetricNames: GET /api/v1/services/{name}/metric-names
//
// Returns the catalog of OTLP metrics the service has emitted in the
// window — name, type, unit, point count, last seen — so the UI can
// offer a picker before charting any one series.
func (h *Handlers) listServiceMetricNames(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	if !h.serviceSignalVisible(r, name, identity.SignalMetrics) {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"service_name": name, "metrics": []any{}})
		return
	}
	tr := ParseRange(r, time.Hour)

	// Per-service route — already gated by gateServiceRoute, so no policy
	// allowlist needed here (nil serviceIn).
	rows, err := h.Store.MetricNames(r.Context(), name, nil, tr.From, tr.To)
	if err != nil {
		h.Logger.Error("metric names for service failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, ServiceMetricNamesResponse{
		ServiceName: name,
		Window:      tr.Window(),
		Metrics:     toMetricEntries(rows),
	})
}

// listMetricCatalog: GET /api/v1/metric-names
//
// The global Metrics catalog: every distinct metric across all
// services in the window, with how many services emit each. The
// optional `service` narrows to one service (unused by the global page
// but kept symmetric with the logs endpoint).
func (h *Handlers) listMetricCatalog(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)
	service := strings.TrimSpace(r.URL.Query().Get("service"))

	// G5: constrain to the services the caller's group policies allow.
	pf := h.resolveServiceFilterSignal(r, service, nil, identity.SignalMetrics)
	if pf.Blocked {
		httpserver.WriteError(w, http.StatusNotFound, "service not found")
		return
	}
	if pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, MetricCatalogResponse{Window: tr.Window(), Metrics: []MetricNameEntry{}})
		return
	}

	rows, err := h.Store.MetricNames(r.Context(), pf.Service, pf.ServiceIn, tr.From, tr.To)
	if err != nil {
		h.Logger.Error("metric catalog failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, MetricCatalogResponse{
		Window:  tr.Window(),
		Metrics: toMetricEntries(rows),
	})
}

// listMetricSeries: GET /api/v1/metric-series?metric=<name>
//
// The global Metrics chart: one time-bucketed series per service that
// emits the metric. Optional repeated `service` params narrow to
// specific services.
func (h *Handlers) listMetricSeries(w http.ResponseWriter, r *http.Request) {
	metric := strings.TrimSpace(r.URL.Query().Get("metric"))
	if metric == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "query parameter metric is required")
		return
	}
	tr := ParseRange(r, time.Hour)

	stepSeconds := int(tr.To.Sub(tr.From).Seconds()) / metricSeriesTargetPoints
	if stepSeconds < metricSeriesMinStepSeconds {
		stepSeconds = metricSeriesMinStepSeconds
	}

	if v := r.URL.Query().Get("step"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= metricSeriesMinStepSeconds && n <= 24*3600 {
			stepSeconds = n
		}
	}
	transform := parseMetricTransform(r)

	var serviceFilter []string
	for _, v := range r.URL.Query()["service"] {
		if v = strings.TrimSpace(v); v != "" {
			serviceFilter = append(serviceFilter, v)
		}
	}

	// G5: intersect the requested services with the caller's policy
	// allowlist. A teamless caller short-circuits to an empty chart.
	pf := h.resolveServiceFilterSignal(r, "", serviceFilter, identity.SignalMetrics)
	if pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, MetricSeriesByServiceResponse{
			Metric:      metric,
			StepSeconds: stepSeconds,
			Window:      tr.Window(),
			Series:      []MetricServiceSeries{},
		})
		return
	}

	res, err := h.Store.MetricSeriesByService(r.Context(), metric, tr.From, tr.To, stepSeconds, pf.ServiceIn, transform)
	if err != nil {
		h.Logger.Error("metric series by service failed", "err", err, "metric", metric)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	series := make([]MetricServiceSeries, 0, len(res.Series))
	for _, s := range res.Series {
		points := make([]MetricSeriesPoint, 0, len(s.Points))
		for _, p := range s.Points {
			points = append(points, MetricSeriesPoint{Bucket: p.Bucket, Value: p.Value})
		}
		series = append(series, MetricServiceSeries{ServiceName: s.ServiceName, Points: points})
	}
	httpserver.WriteJSON(w, http.StatusOK, MetricSeriesByServiceResponse{
		Metric:      metric,
		Type:        res.MetricType,
		Unit:        res.Unit,
		Aggregation: res.Aggregation,
		StepSeconds: stepSeconds,
		Window:      tr.Window(),
		Series:      series,
	})
}

// parseMetricTransform reads + whitelists the chart transform query param.
// Unknown / empty → "" (the store's type-based default).
func parseMetricTransform(r *http.Request) string {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("transform"))) {
	case "raw":
		return "raw"
	case "increase":
		return "increase"
	case "rate":
		return "rate"
	default:
		return ""
	}
}

func toMetricEntries(rows []store.MetricNameRow) []MetricNameEntry {
	out := make([]MetricNameEntry, 0, len(rows))
	for _, m := range rows {
		out = append(out, MetricNameEntry{
			Name:         m.MetricName,
			Type:         m.MetricType,
			Unit:         m.Unit,
			PointCount:   m.PointCount,
			ServiceCount: m.ServiceCount,
			LastSeen:     m.LastSeen,
		})
	}
	return out
}

// serviceMetricSeries: GET /api/v1/services/{name}/metric-series?metric=<name>
//
// Returns a single time-bucketed series for one metric, with the
// per-bucket aggregation chosen by the store based on the metric's OTLP
// type. The bucket width is derived from the window (or an explicit
// `step` in seconds, clamped) — never user free text — so the store can
// interpolate it into the SQL safely.
func (h *Handlers) serviceMetricSeries(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	if !h.serviceSignalVisible(r, name, identity.SignalMetrics) {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"service_name": name, "series": []any{}})
		return
	}
	metric := strings.TrimSpace(r.URL.Query().Get("metric"))
	if metric == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "query parameter metric is required")
		return
	}
	tr := ParseRange(r, time.Hour)

	stepSeconds := int(tr.To.Sub(tr.From).Seconds()) / metricSeriesTargetPoints
	if stepSeconds < metricSeriesMinStepSeconds {
		stepSeconds = metricSeriesMinStepSeconds
	}
	if v := r.URL.Query().Get("step"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= metricSeriesMinStepSeconds && n <= 24*3600 {
			stepSeconds = n
		}
	}

	res, err := h.Store.MetricSeries(r.Context(), name, metric, tr.From, tr.To, stepSeconds, parseMetricTransform(r))
	if err != nil {
		h.Logger.Error("metric series failed", "err", err, "service", name, "metric", metric)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	points := make([]MetricSeriesPoint, 0, len(res.Points))
	for _, p := range res.Points {
		points = append(points, MetricSeriesPoint{Bucket: p.Bucket, Value: p.Value})
	}
	httpserver.WriteJSON(w, http.StatusOK, ServiceMetricSeriesResponse{
		ServiceName: name,
		Metric:      metric,
		Type:        res.MetricType,
		Unit:        res.Unit,
		Aggregation: res.Aggregation,
		StepSeconds: stepSeconds,
		Window:      tr.Window(),
		Points:      points,
	})
}
