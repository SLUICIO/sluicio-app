// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Synthetic OTLP logs + metrics, emitted alongside the traces so the
// ServiceDetail Logs and Metrics sections have data during local
// development. Keyed to the same demoServices as the traces so the two
// line up in the UI.
//
// Points are spread over the last few minutes (seedMetricLookback) so a
// single one-shot `make seed-traces` already produces a multi-bucket
// chart, not just one dot.

package main

import (
	"bytes"
	"fmt"
	"hash/fnv"
	mrand "math/rand"
	"net/http"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// seedMetricLookback / seedMetricPoints spread each metric's data
	// points over a recent window so a one-shot run charts a real line.
	// The cadence (lookback/points = 15s) is finer than the chart's
	// bucket width so every bucket holds a point — important for the
	// counter, whose per-bucket increase needs a value in each bucket.
	seedMetricLookback = 15 * time.Minute
	seedMetricPoints   = 60
	// seedLogsPerService is how many log records each service emits per
	// batch, weighted toward INFO with a sprinkle of WARN/ERROR.
	seedLogsPerService = 8
)

// Pools backing the deterministic-per-service resource attributes.
var (
	cloudRegions = []string{"eu-north-1", "eu-west-1", "us-east-1"}
	k8sClusters  = []string{"cell-eu-1", "cell-eu-2"}
)

// svcHash is a stable hash of a service name. Deriving the resource
// attributes from it (rather than from rng) keeps a service's resource
// identity constant across every data point and across repeated seed
// runs — so cardinality doesn't drift and logs/metrics for one service
// share byte-identical resource attributes.
func svcHash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// resourceAttrsFor builds OTLP semantic-convention resource attributes
// for a demo service: service identity, telemetry SDK, host/OS/process,
// Kubernetes, and cloud. service.name/namespace match the trace seeder
// so all three signals collapse onto one service; the rest populate the
// "Resource" group in the Logs attribute filter so it has realistic
// keys to bite on (service.version, k8s.pod.name, host.name, …).
func resourceAttrsFor(svc serviceSpec) []*commonpb.KeyValue {
	h := svcHash(svc.name)
	pick := func(pool []string) string { return pool[int(h)%len(pool)] }
	return []*commonpb.KeyValue{
		// Service identity.
		stringAttr("service.name", svc.name),
		stringAttr("service.namespace", svc.namespace),
		stringAttr("service.version", fmt.Sprintf("1.%d.%d", h%6, h%19)),
		stringAttr("service.instance.id", fmt.Sprintf("i-%08x", h)),
		stringAttr("deployment.environment", svc.namespace),
		// Telemetry SDK.
		stringAttr("telemetry.sdk.name", "opentelemetry"),
		stringAttr("telemetry.sdk.language", "go"),
		stringAttr("telemetry.sdk.version", "1.31.0"),
		// Host / OS / process.
		stringAttr("host.name", fmt.Sprintf("ip-10-%d-%d-%d", h%256, (h>>8)%256, (h>>16)%256)),
		stringAttr("host.arch", "amd64"),
		stringAttr("os.type", "linux"),
		intAttr("process.pid", int64(1000+h%30000)),
		stringAttr("process.runtime.name", "go"),
		// Kubernetes.
		stringAttr("k8s.cluster.name", pick(k8sClusters)),
		stringAttr("k8s.namespace.name", svc.namespace),
		stringAttr("k8s.deployment.name", svc.name),
		stringAttr("k8s.pod.name", fmt.Sprintf("%s-%05x-%04x", svc.name, h&0xfffff, (h>>7)&0xffff)),
		// Cloud.
		stringAttr("cloud.provider", "aws"),
		stringAttr("cloud.region", pick(cloudRegions)),
	}
}

// metricAttrsFor returns the OTLP data-point attributes (dimensions)
// for a metric on a service. They're constant per (service, metric) so
// each stays a single coherent stream: a cumulative counter split by a
// varying dimension would render as overlapping curves, and the read
// layer aggregates per service anyway.
func metricAttrsFor(svc serviceSpec, metricName string) []*commonpb.KeyValue {
	switch metricName {
	case "queue.depth":
		return []*commonpb.KeyValue{
			stringAttr("messaging.system", "kafka"),
			stringAttr("messaging.destination.name", svc.name+".inbound"),
		}
	case "messages.processed":
		return []*commonpb.KeyValue{
			stringAttr("messaging.system", "kafka"),
			stringAttr("messaging.destination.name", svc.name+".events"),
			stringAttr("messaging.operation", "process"),
		}
	case "request.duration":
		return []*commonpb.KeyValue{
			stringAttr("http.request.method", "POST"),
			stringAttr("http.route", "/v1/process"),
			intAttr("http.response.status_code", 200),
			stringAttr("server.address", svc.name+".svc.cluster.local"),
		}
	}
	return nil
}

// --- metrics -----------------------------------------------------------

// buildMetricsRequest emits, for every demo service, three metrics that
// exercise all three of the type-aware aggregation paths the read layer
// implements: a gauge (avg), a monotonic counter (increase), and a
// histogram (mean). Returns the request and the total data-point count
// for the send log line.
func buildMetricsRequest(rng *mrand.Rand) (*colmetricspb.ExportMetricsServiceRequest, int) {
	req := &colmetricspb.ExportMetricsServiceRequest{}
	now := time.Now().UTC()
	start := now.Add(-seedMetricLookback)
	step := seedMetricLookback / seedMetricPoints
	points := 0

	for _, svc := range demoServices {
		scope := &commonpb.InstrumentationScope{Name: "seed-metrics", Version: "0.1.0"}

		// Gauge: queue depth fluctuating around a per-service baseline.
		gaugeDPs := make([]*metricspb.NumberDataPoint, 0, seedMetricPoints)
		// Counter: cumulative messages processed, monotonically rising.
		counterDPs := make([]*metricspb.NumberDataPoint, 0, seedMetricPoints)
		// Histogram: request duration, one aggregated point per bucket.
		histDPs := make([]*metricspb.HistogramDataPoint, 0, seedMetricPoints)

		// OTLP data-point attributes (dimensions). Constant across the
		// stream so each metric stays one coherent series.
		gaugeAttrs := metricAttrsFor(svc, "queue.depth")
		counterAttrs := metricAttrsFor(svc, "messages.processed")
		histAttrs := metricAttrsFor(svc, "request.duration")

		// Deterministic per-service rate so repeated seed runs form one
		// coherent cumulative curve — a counter is a single monotonic
		// stream, and overlapping random series would render as noise.
		ratePerSec := int64(1 + len(svc.name)%5) // 1..5 msg/s, stable per service
		for i := 0; i < seedMetricPoints; i++ {
			ts := start.Add(time.Duration(i) * step)
			tsNano := uint64(ts.UnixNano())
			startNano := uint64(start.UnixNano())

			gaugeDPs = append(gaugeDPs, &metricspb.NumberDataPoint{
				TimeUnixNano: tsNano,
				Attributes:   gaugeAttrs,
				Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(rng.Intn(200))},
			})

			counterDPs = append(counterDPs, &metricspb.NumberDataPoint{
				StartTimeUnixNano: startNano,
				TimeUnixNano:      tsNano,
				Attributes:        counterAttrs,
				// Cumulative value derived from wall-clock so every run
				// reports the same curve for a given timestamp.
				Value: &metricspb.NumberDataPoint_AsInt{AsInt: ts.Unix() * ratePerSec},
			})

			count := uint64(10 + rng.Intn(90))
			meanMs := 20 + rng.Float64()*480
			histDPs = append(histDPs, &metricspb.HistogramDataPoint{
				StartTimeUnixNano: startNano,
				TimeUnixNano:      tsNano,
				Attributes:        histAttrs,
				Count:             count,
				Sum:               proto.Float64(meanMs * float64(count)),
			})
			points += 3
		}

		metrics := []*metricspb.Metric{
			{
				Name: "queue.depth",
				Unit: "{messages}",
				Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: gaugeDPs}},
			},
			{
				Name: "messages.processed",
				Unit: "{messages}",
				Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
					AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
					IsMonotonic:            true,
					DataPoints:             counterDPs,
				}},
			},
			{
				Name: "request.duration",
				Unit: "ms",
				Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
					AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
					DataPoints:             histDPs,
				}},
			},
		}

		req.ResourceMetrics = append(req.ResourceMetrics, &metricspb.ResourceMetrics{
			Resource:     &resourcepb.Resource{Attributes: resourceAttrsFor(svc)},
			ScopeMetrics: []*metricspb.ScopeMetrics{{Scope: scope, Metrics: metrics}},
		})
	}
	return req, points
}

// --- logs --------------------------------------------------------------

// logTemplate is one possible log line: an OTLP severity number, its
// text, and a body format string that takes the service name.
type logTemplate struct {
	severity int32
	text     string
	body     string
}

// Weighted toward INFO; a few WARN/ERROR so the severity filter has
// something to bite on. OTLP severity numbers: DEBUG=5, INFO=9,
// WARN=13, ERROR=17.
var logTemplates = []logTemplate{
	{9, "INFO", "%s started processing batch"},
	{9, "INFO", "%s committed offset successfully"},
	{9, "INFO", "%s handled request in %dms"},
	{5, "DEBUG", "%s cache lookup hit"},
	{13, "WARN", "%s retrying after transient error (attempt 2)"},
	{13, "WARN", "%s queue depth above soft limit"},
	{17, "ERROR", "%s failed to deliver message: downstream timeout"},
}

// Synthetic attribute value pools for the log records. Chosen so the
// Logs attribute filters can be exercised: team has "abc", environment
// has "test", group values embed "aaa" for a contains match.
var (
	logTeams    = []string{"abc", "payments", "fulfillment", "platform"}
	logEnvs     = []string{"dev", "test", "prod"}
	logGroups   = []string{"group-aaa-1", "group-bbb-2", "zzz-aaa", "ccc"}
	httpMethods = []string{"GET", "POST", "PUT", "DELETE"}
	urlPaths    = []string{"/v1/orders", "/v1/payments", "/v1/files", "/healthz"}
)

// statusForSeverity picks a plausible HTTP status code for a log's
// severity, so http.response.status_code correlates with severity the
// way it would in a real service (errors 5xx, warnings 4xx, else 2xx).
func statusForSeverity(sev int32, rng *mrand.Rand) int64 {
	switch {
	case sev >= 17: // ERROR / FATAL
		return []int64{500, 502, 503}[rng.Intn(3)]
	case sev >= 13: // WARN
		return []int64{408, 425, 429}[rng.Intn(3)]
	default:
		return []int64{200, 201, 204}[rng.Intn(3)]
	}
}

// buildLogsRequest emits a handful of log records per demo service over
// the recent window. Returns the request and the total record count.
func buildLogsRequest(rng *mrand.Rand) (*collogspb.ExportLogsServiceRequest, int) {
	req := &collogspb.ExportLogsServiceRequest{}
	now := time.Now().UTC()
	records := 0

	for _, svc := range demoServices {
		scope := &commonpb.InstrumentationScope{Name: "seed-logs", Version: "0.1.0"}
		recs := make([]*logspb.LogRecord, 0, seedLogsPerService)
		for i := 0; i < seedLogsPerService; i++ {
			tmpl := pickLogTemplate(rng)
			// Spread records across the last 10 minutes.
			ts := now.Add(-time.Duration(rng.Intn(600)) * time.Second)
			tsNano := uint64(ts.UnixNano())
			body := tmpl.body
			if strings_ContainsPercentD(body) {
				body = fmt.Sprintf(tmpl.body, svc.name, 5+rng.Intn(995))
			} else {
				body = fmt.Sprintf(tmpl.body, svc.name)
			}
			// Varied attributes so the Logs attribute filters have
			// something to bite on. Two flavours: domain keys the demo
			// filters target (team / environment / group / retry.count),
			// and OTLP HTTP semantic conventions (http.* / url.path) that
			// populate the popover's "HTTP" group.
			attrs := []*commonpb.KeyValue{
				stringAttr("log.source", "seed"),
				stringAttr("team", logTeams[rng.Intn(len(logTeams))]),
				stringAttr("environment", logEnvs[rng.Intn(len(logEnvs))]),
				stringAttr("group", logGroups[rng.Intn(len(logGroups))]),
				intAttr("retry.count", int64(rng.Intn(5))),
				stringAttr("http.request.method", httpMethods[rng.Intn(len(httpMethods))]),
				intAttr("http.response.status_code", statusForSeverity(tmpl.severity, rng)),
				stringAttr("url.path", urlPaths[rng.Intn(len(urlPaths))]),
				stringAttr("thread.name", fmt.Sprintf("worker-%d", rng.Intn(8))),
			}
			// Errors carry OTLP exception.* semantic-convention attributes.
			if tmpl.severity >= 17 {
				attrs = append(attrs,
					stringAttr("exception.type", "DownstreamTimeoutError"),
					stringAttr("exception.message", "downstream call exceeded deadline"),
				)
			}
			recs = append(recs, &logspb.LogRecord{
				TimeUnixNano:         tsNano,
				ObservedTimeUnixNano: tsNano,
				SeverityNumber:       logspb.SeverityNumber(tmpl.severity),
				SeverityText:         tmpl.text,
				// Trace context so logs correlate to a trace/span in the UI.
				TraceId:    randomID(16),
				SpanId:     randomID(8),
				Body:       &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: body}},
				Attributes: attrs,
			})
			records++
		}
		req.ResourceLogs = append(req.ResourceLogs, &logspb.ResourceLogs{
			Resource:  &resourcepb.Resource{Attributes: resourceAttrsFor(svc)},
			ScopeLogs: []*logspb.ScopeLogs{{Scope: scope, LogRecords: recs}},
		})
	}
	return req, records
}

// pickLogTemplate weights the selection toward INFO/DEBUG so WARN and
// ERROR stay a minority — closer to real log volume.
func pickLogTemplate(rng *mrand.Rand) logTemplate {
	// 70% info/debug, 30% warn/error.
	if rng.Float64() < 0.70 {
		infoish := logTemplates[:4]
		return infoish[rng.Intn(len(infoish))]
	}
	warnish := logTemplates[4:]
	return warnish[rng.Intn(len(warnish))]
}

// strings_ContainsPercentD reports whether the body format wants a
// numeric arg (a "%d" verb), so we can pick the right fmt.Sprintf call.
func strings_ContainsPercentD(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '%' && s[i+1] == 'd' {
			return true
		}
	}
	return false
}

// --- shared POST -------------------------------------------------------

// postProto marshals and POSTs any OTLP Export*ServiceRequest to an
// OTLP/HTTP endpoint, mirroring post() but generic over the message.
func postProto(url string, msg proto.Message) error {
	body, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	setIngestKey(httpReq)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return nil
}
