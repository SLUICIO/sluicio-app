// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

func metricsRequest(metrics ...*metricspb.Metric) []*metricspb.ResourceMetrics {
	return []*metricspb.ResourceMetrics{{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{{
			Key:   "service.name",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "airflow-scheduler"}},
		}}},
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}},
	}}
}

// Exponential histograms (Airflow 3.x forces them for every timing
// metric) must land as ordinary "histogram" rows - Value = sum,
// Count = count - so the catalog's sum/count mean and threshold checks
// treat them exactly like explicit-bucket histograms.
func TestConvertMetrics_ExponentialHistogram(t *testing.T) {
	start := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	ts := start.Add(time.Minute)
	m := &metricspb.Metric{
		Name: "airflow.task.duration",
		Unit: "ms",
		Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{
			AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			DataPoints: []*metricspb.ExponentialHistogramDataPoint{
				{
					StartTimeUnixNano: uint64(start.UnixNano()),
					TimeUnixNano:      uint64(ts.UnixNano()),
					Count:             4,
					Sum:               proto.Float64(1250.5),
					Scale:             3,
					Positive:          &metricspb.ExponentialHistogramDataPoint_Buckets{Offset: 70, BucketCounts: []uint64{1, 0, 3}},
					Attributes: []*commonpb.KeyValue{{
						Key:   "dag_id",
						Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "etl"}},
					}},
				},
				// Sum is optional; absent must not drop the point.
				{TimeUnixNano: uint64(ts.UnixNano()), Count: 2},
			},
		}},
	}

	rows, skipped := ConvertMetricsRequest(metricsRequest(m))
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	r := rows[0]
	if r.MetricType != "histogram" || r.MetricName != "airflow.task.duration" || r.Unit != "ms" {
		t.Errorf("type/name/unit = %q/%q/%q", r.MetricType, r.MetricName, r.Unit)
	}
	if r.Value != 1250.5 || r.Count != 4 {
		t.Errorf("Value/Count = %v/%d, want 1250.5/4", r.Value, r.Count)
	}
	if !r.Timestamp.Equal(ts) || !r.StartTimestamp.Equal(start) {
		t.Errorf("timestamps = %v/%v, want %v/%v", r.Timestamp, r.StartTimestamp, ts, start)
	}
	if r.ServiceName != "airflow-scheduler" || r.MetricAttributes["dag_id"] != "etl" {
		t.Errorf("service/attrs = %q/%v", r.ServiceName, r.MetricAttributes)
	}
	if r.IsMonotonic != 0 {
		t.Errorf("IsMonotonic = %d, want 0", r.IsMonotonic)
	}

	if r := rows[1]; r.MetricType != "histogram" || r.Value != 0 || r.Count != 2 {
		t.Errorf("no-sum point: type/Value/Count = %q/%v/%d, want histogram/0/2", r.MetricType, r.Value, r.Count)
	}
}

// Summary points aren't stored, but they must be counted as skipped
// rather than vanishing silently.
func TestConvertMetrics_SummaryCountedAsSkipped(t *testing.T) {
	summary := &metricspb.Metric{
		Name: "rpc.latency",
		Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{
			DataPoints: []*metricspb.SummaryDataPoint{{Count: 1, Sum: 2}, {Count: 3, Sum: 4}},
		}},
	}
	gauge := &metricspb.Metric{
		Name: "queue.depth",
		Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
			DataPoints: []*metricspb.NumberDataPoint{{Value: &metricspb.NumberDataPoint_AsInt{AsInt: 7}}},
		}},
	}
	unset := &metricspb.Metric{Name: "empty"}

	rows, skipped := ConvertMetricsRequest(metricsRequest(summary, gauge, unset))
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2", skipped)
	}
	if len(rows) != 1 || rows[0].MetricName != "queue.depth" || rows[0].Value != 7 {
		t.Fatalf("rows = %+v, want only the gauge", rows)
	}
}
