// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"encoding/hex"
	"time"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
)

// ConvertLogsRequest flattens an ExportLogsServiceRequest's
// ResourceLogs into LogRows ready for a ClickHouse batch. It reuses the
// shared attribute conversion helpers in convert.go.
func ConvertLogsRequest(resourceLogs []*logspb.ResourceLogs) []LogRow {
	var rows []LogRow
	for _, rl := range resourceLogs {
		resourceAttrs := attributesToMap(rl.GetResource().GetAttributes())
		serviceName := resolveServiceName(resourceAttrs)
		serviceNamespace := resourceAttrs["service.namespace"]
		for _, sl := range rl.GetScopeLogs() {
			scopeName := sl.GetScope().GetName()
			for _, lr := range sl.GetLogRecords() {
				rows = append(rows, convertLogRecord(lr, resourceAttrs, serviceName, serviceNamespace, scopeName))
			}
		}
	}
	return rows
}

func convertLogRecord(lr *logspb.LogRecord, resourceAttrs map[string]string, serviceName, serviceNamespace, scopeName string) LogRow {
	ts := time.Unix(0, int64(lr.GetTimeUnixNano())).UTC()
	observed := time.Unix(0, int64(lr.GetObservedTimeUnixNano())).UTC()
	// A producer may set only ObservedTimeUnixNano (e.g. when scraping a
	// log file with no embedded timestamp); fall back so the row always
	// has a usable Timestamp for partitioning and ordering.
	if lr.GetTimeUnixNano() == 0 && lr.GetObservedTimeUnixNano() != 0 {
		ts = observed
	}
	return LogRow{
		Timestamp:          ts,
		ObservedTimestamp:  observed,
		TraceID:            hex.EncodeToString(lr.GetTraceId()),
		SpanID:             hex.EncodeToString(lr.GetSpanId()),
		SeverityNumber:     int32(lr.GetSeverityNumber()),
		SeverityText:       lr.GetSeverityText(),
		ServiceName:        serviceName,
		ServiceNamespace:   serviceNamespace,
		ScopeName:          scopeName,
		Body:               anyValueToString(lr.GetBody()),
		ResourceAttributes: resourceAttrs,
		LogAttributes:      attributesToMap(lr.GetAttributes()),
	}
}
