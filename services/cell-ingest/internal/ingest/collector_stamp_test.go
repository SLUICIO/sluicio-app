// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// The collector stamp leaves the resource attributes for a column of its
// own in every signal, so it is never part of a series' identity, and
// unstamped data reads as no collector.
func TestCollectorStampHasItsOwnColumn(t *testing.T) {
	stamped := &resourcepb.Resource{Attributes: []*commonpb.KeyValue{str("service.name", "orders-api"), str(CollectorStampKey, "01a21f4c-9d12")}}
	plain := &resourcepb.Resource{Attributes: []*commonpb.KeyValue{str("service.name", "orders-api")}}

	spans := ConvertRequest([]*tracepb.ResourceSpans{
		{Resource: stamped, ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Name: "a"}}}}},
		{Resource: plain, ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Name: "b"}}}}},
	})
	logs := ConvertLogsRequest([]*logspb.ResourceLogs{
		{Resource: stamped, ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{}}}}},
	})
	gauge := &metricspb.Metric{Name: "m", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
		DataPoints: []*metricspb.NumberDataPoint{{Value: &metricspb.NumberDataPoint_AsInt{AsInt: 1}}, {Value: &metricspb.NumberDataPoint_AsInt{AsInt: 2}}},
	}}}
	metrics, _ := ConvertMetricsRequest([]*metricspb.ResourceMetrics{
		{Resource: stamped, ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{gauge}}}},
	})

	if len(spans) != 2 || spans[0].CollectorID != "01a21f4c-9d12" || spans[1].CollectorID != "" {
		t.Fatalf("spans: %+v", spans)
	}
	if len(logs) != 1 || logs[0].CollectorID != "01a21f4c-9d12" {
		t.Fatalf("logs: %+v", logs)
	}
	if len(metrics) != 2 || metrics[0].CollectorID != "01a21f4c-9d12" || metrics[1].CollectorID != "01a21f4c-9d12" {
		t.Fatalf("metrics: %+v", metrics)
	}
	for _, attrs := range []map[string]string{spans[0].ResourceAttributes, logs[0].ResourceAttributes, metrics[0].ResourceAttributes} {
		if _, ok := attrs[CollectorStampKey]; ok {
			t.Fatalf("the stamp stayed in the resource attributes: %v", attrs)
		}
		if attrs["service.name"] != "orders-api" {
			t.Fatalf("the other resource attributes were lost: %v", attrs)
		}
	}
}
