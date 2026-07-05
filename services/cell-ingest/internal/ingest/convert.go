// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// ConvertRequest flattens an ExportTraceServiceRequest's ResourceSpans
// into a slice of SpanRows ready to be inserted into ClickHouse.
func ConvertRequest(resourceSpans []*tracepb.ResourceSpans) []SpanRow {
	var rows []SpanRow
	for _, rs := range resourceSpans {
		resourceAttrs := attributesToMap(rs.GetResource().GetAttributes())
		serviceName := resolveServiceName(resourceAttrs)
		serviceNamespace := resourceAttrs["service.namespace"]
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				rows = append(rows, convertSpan(span, resourceAttrs, serviceName, serviceNamespace))
			}
		}
	}
	return rows
}

func convertSpan(span *tracepb.Span, resourceAttrs map[string]string, serviceName, serviceNamespace string) SpanRow {
	start := time.Unix(0, int64(span.GetStartTimeUnixNano())).UTC()
	end := time.Unix(0, int64(span.GetEndTimeUnixNano())).UTC()
	var duration uint64
	if end.After(start) {
		duration = uint64(end.Sub(start).Nanoseconds())
	}
	status := span.GetStatus()
	return SpanRow{
		Timestamp:          start,
		TraceID:            hex.EncodeToString(span.GetTraceId()),
		SpanID:             hex.EncodeToString(span.GetSpanId()),
		ParentSpanID:       hex.EncodeToString(span.GetParentSpanId()),
		SpanName:           span.GetName(),
		SpanKind:           spanKindString(span.GetKind()),
		ServiceName:        serviceName,
		ServiceNamespace:   serviceNamespace,
		ResourceAttributes: resourceAttrs,
		SpanAttributes:     attributesToMap(span.GetAttributes()),
		DurationNs:         duration,
		StatusCode:         statusCodeString(status.GetCode()),
		StatusMessage:      status.GetMessage(),
	}
}

func attributesToMap(attrs []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, kv := range attrs {
		if kv == nil {
			continue
		}
		out[kv.GetKey()] = anyValueToString(kv.GetValue())
	}
	return out
}

// UnknownService is the identity given to telemetry that arrives without a
// service.name resource attribute — the OTel spec's own default. Collector
// receivers scraping third-party systems (RabbitMQ, Postgres, node-exporter,
// SNMP, …) frequently omit service.name; without this fallback their rows
// carry an empty ServiceName and get silently dropped by the catalog's
// discovery filter (which skips empty service names), so the source never
// registers as a service. Applied at ingest write time (all three signals),
// so the name is
// stored consistently and the source's metrics/logs/traces stay queryable
// under it, not just discoverable.
const UnknownService = "unknown_service"

// resolveServiceName returns the service.name resource attribute, or
// UnknownService when it's absent/empty.
func resolveServiceName(resourceAttrs map[string]string) string {
	if s := resourceAttrs["service.name"]; s != "" {
		return s
	}
	return UnknownService
}

func anyValueToString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'f', -1, 64)
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue)
	case *commonpb.AnyValue_BytesValue:
		return hex.EncodeToString(x.BytesValue)
	case *commonpb.AnyValue_ArrayValue, *commonpb.AnyValue_KvlistValue:
		// Marshal complex shapes as JSON so they remain searchable.
		b, err := json.Marshal(anyValueToInterface(v))
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return ""
	}
}

func anyValueToInterface(v *commonpb.AnyValue) any {
	if v == nil {
		return nil
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return x.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return x.DoubleValue
	case *commonpb.AnyValue_BoolValue:
		return x.BoolValue
	case *commonpb.AnyValue_BytesValue:
		return hex.EncodeToString(x.BytesValue)
	case *commonpb.AnyValue_ArrayValue:
		out := make([]any, 0, len(x.ArrayValue.GetValues()))
		for _, item := range x.ArrayValue.GetValues() {
			out = append(out, anyValueToInterface(item))
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		out := map[string]any{}
		for _, kv := range x.KvlistValue.GetValues() {
			out[kv.GetKey()] = anyValueToInterface(kv.GetValue())
		}
		return out
	}
	return nil
}

func spanKindString(k tracepb.Span_SpanKind) string {
	switch k {
	case tracepb.Span_SPAN_KIND_INTERNAL:
		return "Internal"
	case tracepb.Span_SPAN_KIND_SERVER:
		return "Server"
	case tracepb.Span_SPAN_KIND_CLIENT:
		return "Client"
	case tracepb.Span_SPAN_KIND_PRODUCER:
		return "Producer"
	case tracepb.Span_SPAN_KIND_CONSUMER:
		return "Consumer"
	default:
		return "Unspecified"
	}
}

func statusCodeString(c tracepb.Status_StatusCode) string {
	switch c {
	case tracepb.Status_STATUS_CODE_OK:
		return "Ok"
	case tracepb.Status_STATUS_CODE_ERROR:
		return "Error"
	default:
		return "Unset"
	}
}
