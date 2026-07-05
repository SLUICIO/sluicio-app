// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// ConvertMetricsRequest flattens an ExportMetricsServiceRequest's
// ResourceMetrics into MetricRows — one row per numeric data point.
//
// Only the metric types thresholding needs are stored: gauge, sum, and
// histogram (as count + sum). ExponentialHistogram and Summary points
// are skipped for now; see 0003_metrics.sql for the rationale.
func ConvertMetricsRequest(resourceMetrics []*metricspb.ResourceMetrics) []MetricRow {
	var rows []MetricRow
	for _, rm := range resourceMetrics {
		resourceAttrs := attributesToMap(rm.GetResource().GetAttributes())
		serviceName := resolveServiceName(resourceAttrs)
		serviceNamespace := resourceAttrs["service.namespace"]
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				rows = append(rows, convertMetric(m, resourceAttrs, serviceName, serviceNamespace)...)
			}
		}
	}
	return rows
}

func convertMetric(m *metricspb.Metric, resourceAttrs map[string]string, serviceName, serviceNamespace string) []MetricRow {
	// base returns a row pre-filled with the fields common to every data
	// point of this metric.
	base := func() MetricRow {
		return MetricRow{
			MetricName:         m.GetName(),
			ServiceName:        serviceName,
			ServiceNamespace:   serviceNamespace,
			Unit:               m.GetUnit(),
			ResourceAttributes: resourceAttrs,
		}
	}

	var rows []MetricRow
	switch data := m.GetData().(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range data.Gauge.GetDataPoints() {
			r := base()
			r.MetricType = "gauge"
			r.Timestamp = time.Unix(0, int64(dp.GetTimeUnixNano())).UTC()
			r.StartTimestamp = time.Unix(0, int64(dp.GetStartTimeUnixNano())).UTC()
			r.Value = numberDataPointValue(dp)
			r.MetricAttributes = attributesToMap(dp.GetAttributes())
			rows = append(rows, r)
		}
	case *metricspb.Metric_Sum:
		var monotonic uint8
		if data.Sum.GetIsMonotonic() {
			monotonic = 1
		}
		for _, dp := range data.Sum.GetDataPoints() {
			r := base()
			r.MetricType = "sum"
			r.Timestamp = time.Unix(0, int64(dp.GetTimeUnixNano())).UTC()
			r.StartTimestamp = time.Unix(0, int64(dp.GetStartTimeUnixNano())).UTC()
			r.Value = numberDataPointValue(dp)
			r.IsMonotonic = monotonic
			r.MetricAttributes = attributesToMap(dp.GetAttributes())
			rows = append(rows, r)
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range data.Histogram.GetDataPoints() {
			r := base()
			r.MetricType = "histogram"
			r.Timestamp = time.Unix(0, int64(dp.GetTimeUnixNano())).UTC()
			r.StartTimestamp = time.Unix(0, int64(dp.GetStartTimeUnixNano())).UTC()
			r.Value = dp.GetSum() // bucket sum; Count carries the observation count
			r.Count = dp.GetCount()
			r.MetricAttributes = attributesToMap(dp.GetAttributes())
			rows = append(rows, r)
		}
	default:
		// ExponentialHistogram, Summary, or an unset Data oneof — skipped.
	}
	return rows
}

// numberDataPointValue resolves a NumberDataPoint's value, which is a
// oneof of double or int.
func numberDataPointValue(dp *metricspb.NumberDataPoint) float64 {
	switch v := dp.GetValue().(type) {
	case *metricspb.NumberDataPoint_AsDouble:
		return v.AsDouble
	case *metricspb.NumberDataPoint_AsInt:
		return float64(v.AsInt)
	}
	return 0
}
