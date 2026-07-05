// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/store"
)

// The metric explorer endpoints back the Metrics page's search →
// filter → sparkline table flow. They sit alongside the simpler
// /metric-names + /metric-series browse endpoints in
// handlers_otlp_metrics.go, mirroring the Logs page's /log-fields and
// /log-attributes/{key}/values shape so the same filter UI can drive
// both.

const (
	// metricSparkTargetPoints is how many buckets a catalog sparkline
	// aims for; the bucket width is derived from the window so the spark
	// stays a readable trend regardless of range.
	metricSparkTargetPoints = 40
	metricSparkMinStep      = 15
)

// metricCatalog: GET /api/v1/metric-catalog
//
// The explorer table: every metric matching the optional name substring
// (`q`), OTLP type (`type`), and attribute filters (repeated `attr`
// JSON), each with a type-aware headline value, a sparkline, and a
// distinct-series count. Rule counts + thresholds are joined from the
// alert engine.
func (h *Handlers) metricCatalog(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)
	attrs, err := parseAttrFilters(r)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	step := int(tr.To.Sub(tr.From).Seconds()) / metricSparkTargetPoints
	if step < metricSparkMinStep {
		step = metricSparkMinStep
	}

	// `integration=<name>` scopes to that integration's services.
	serviceIn, integGroups, _ := h.integrationFilter(r.Context(), r, func() ([]string, error) {
		return h.Store.DistinctMetricServices(r.Context(), tr.From, tr.To)
	})
	// G5: intersect with the caller's group-policy service allowlist.
	pf := h.resolveServiceFilter(r, strings.TrimSpace(r.URL.Query().Get("service")), serviceIn)
	if pf.Blocked {
		httpserver.WriteError(w, http.StatusNotFound, "service not found")
		return
	}
	if pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, MetricCatalogRichResponse{
			Window: tr.Window(), StepSeconds: step, Metrics: []MetricCatalogEntry{},
		})
		return
	}

	// Cap how many metrics we materialize (values + sparklines) so a broad
	// or empty query stays cheap; the explorer narrows by refining search.
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, err := h.Store.MetricCatalog(r.Context(), store.MetricCatalogParams{
		Service:     pf.Service,
		ServiceIn:   pf.ServiceIn,
		NameQuery:   strings.TrimSpace(r.URL.Query().Get("q")),
		MetricType:  normalizeMetricType(r.URL.Query().Get("type")),
		From:        tr.From,
		To:          tr.To,
		StepSeconds: step,
		Attrs:       attrs,
		AttrGroups:  integGroups,
		Limit:       limit,
	})
	if err != nil {
		h.Logger.Error("metric catalog failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// Rule summaries (count + tightest threshold + severity) per metric,
	// from the alert engine — drive the rule badge, sparkline threshold
	// line, and breach tint. Non-fatal: a failure just omits rule data.
	summaries, err := h.Alerts.MetricRuleSummaries(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("metric rule summaries failed", "err", err)
		summaries = nil
	}

	entries := make([]MetricCatalogEntry, 0, len(rows))
	var totalSeries uint64
	var totalRules int
	for _, m := range rows {
		totalSeries += m.SeriesCount
		spark := m.Spark
		if spark == nil {
			spark = []float64{}
		}
		e := MetricCatalogEntry{
			Name:        m.MetricName,
			Type:        m.MetricType,
			Unit:        m.Unit,
			Aggregation: m.Aggregation,
			Value:       m.Value,
			Spark:       spark,
			SeriesCount: m.SeriesCount,
			PointCount:  m.PointCount,
			LastSeen:    m.LastSeen,
		}
		if sum, ok := summaries[m.MetricName]; ok {
			e.RuleCount = sum.Count
			e.Severity = string(sum.Severity)
			th := sum.Threshold
			e.Threshold = &th
			totalRules += sum.Count
		}
		entries = append(entries, e)
	}

	httpserver.WriteJSON(w, http.StatusOK, MetricCatalogRichResponse{
		Window:      tr.Window(),
		StepSeconds: step,
		TotalSeries: totalSeries,
		RuleCount:   totalRules,
		Metrics:     entries,
	})
}

// metricFields: GET /api/v1/metric-fields — the attribute keys seen on
// recent metrics, each tagged numeric, for the explorer's filter key
// picker (mirrors /log-fields).
func (h *Handlers) metricFields(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)
	metric := strings.TrimSpace(r.URL.Query().Get("metric"))
	// G5: restrict the attribute-key sample to the caller's visible services.
	pf := h.resolveServiceFilterSignal(r, "", nil, identity.SignalMetrics)
	if pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, LogFieldsResponse{Window: tr.Window(), Fields: []LogFieldEntry{}})
		return
	}
	keys, err := h.Store.MetricAttributeKeys(r.Context(), metric, pf.ServiceIn, tr.From, tr.To, logFieldsSampleLimit)
	if err != nil {
		h.Logger.Error("metric attribute keys failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	fields := make([]LogFieldEntry, 0, len(keys))
	for _, k := range keys {
		t := "string"
		if k.Numeric {
			t = "number"
		}
		fields = append(fields, LogFieldEntry{Key: k.Key, Type: t, UseCount: k.UseCount, Cardinality: k.Cardinality})
	}
	httpserver.WriteJSON(w, http.StatusOK, LogFieldsResponse{
		Window: tr.Window(),
		Fields: fields,
	})
}

// metricAttributeValues: GET /api/v1/metric-attributes/{key}/values —
// the top-N values for one attribute key (mirrors the logs equivalent).
func (h *Handlers) metricAttributeValues(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.PathValue("key"))
	if !attrKeyRe.MatchString(key) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid attribute key")
		return
	}
	tr := ParseRange(r, time.Hour)
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	// G5: restrict the value scan to the caller's visible services.
	pf := h.resolveServiceFilterSignal(r, "", nil, identity.SignalMetrics)
	if pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, LogAttrValuesResponse{Key: key, Window: tr.Window(), Values: []LogAttrValue{}})
		return
	}
	rows, err := h.Store.MetricAttributeValues(r.Context(), key, strings.TrimSpace(r.URL.Query().Get("metric")), strings.TrimSpace(r.URL.Query().Get("q")), pf.ServiceIn, tr.From, tr.To, limit)
	if err != nil {
		h.Logger.Error("metric attribute values failed", "err", err, "key", key)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	values := make([]LogAttrValue, 0, len(rows))
	for _, v := range rows {
		values = append(values, LogAttrValue{Value: v.Value, Events: v.Events})
	}
	httpserver.WriteJSON(w, http.StatusOK, LogAttrValuesResponse{
		Key:    key,
		Window: tr.Window(),
		Values: values,
	})
}

// normalizeMetricType maps the UI's type-toggle values to the OTLP
// MetricType stored in ClickHouse. "counter" is the UI label for a
// monotonic sum; "all"/"" means no filter.
func normalizeMetricType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "counter", "sum":
		return "sum"
	case "gauge":
		return "gauge"
	case "histogram":
		return "histogram"
	default:
		return ""
	}
}
