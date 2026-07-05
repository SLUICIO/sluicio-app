// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Command seed-traces sends synthetic OTLP/HTTP traces, logs, and
// metrics to cell-ingest so the UI has data to display during local
// development.
//
//	go run ./services/cell-ingest/cmd/seed-traces
//	go run ./services/cell-ingest/cmd/seed-traces -continuous
//
// The synthetic data models a small integration estate (an order API,
// a payment service, a fulfillment worker, and a partner EDI feed)
// with a realistic mix of successful and failed spans. Each batch also
// emits logs (varied severities) and metrics (a gauge, a monotonic
// counter, and a histogram) for the same services — see
// seed_logs_metrics.go.
package main

import (
	"bytes"
	"crypto/rand"
	"flag"
	"fmt"
	mrand "math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// spanFactory builds the spans for one "unit of work" of a synthetic
// service. Most services emit a single root span per call; multi-span
// factories (e.g. invoice-router) emit several spans sharing a trace
// id, with the appropriate parent-child relationships, so the trace
// view shows a real waterfall.
type spanFactory func(rng *mrand.Rand, svc serviceSpec) []*tracepb.Span

// scenarioFactory builds spans that span MULTIPLE services in a
// single trace — e.g. service A publishes to a queue and service B
// consumes and acts on the message. Each call returns one trace's
// worth of spans grouped by the service they belong to, which the
// request builder turns into one ResourceSpans per service.
type scenarioFactory func(rng *mrand.Rand) []serviceSpans

type serviceSpans struct {
	serviceName      string
	serviceNamespace string
	spans            []*tracepb.Span
}

var demoScenarios = []scenarioFactory{
	orderFlowScenario,
	shipmentFlowScenario,
	ftpFanoutScenario,
}

type serviceSpec struct {
	name      string
	namespace string
	spanNames []string
	errorRate float64
	factory   spanFactory // optional; nil uses defaultSpan
}

var demoServices = []serviceSpec{
	{
		name: "order-api", namespace: "production",
		spanNames: []string{"POST /orders", "GET /orders/{id}", "PATCH /orders/{id}/status", "validate_order"},
		errorRate: 0.05,
	},
	{
		name: "payment-service", namespace: "production",
		spanNames: []string{"process_payment", "refund_payment", "verify_card", "publish_payment_event"},
		errorRate: 0.10,
	},
	{
		name: "fulfillment-worker", namespace: "production",
		spanNames: []string{"consume_payment_event", "reserve_inventory", "create_shipment", "notify_customer"},
		errorRate: 0.04,
	},
	{
		name: "partner-edi", namespace: "production",
		spanNames: []string{"poll_edi_inbox", "translate_edi_to_json", "publish_to_kafka"},
		errorRate: 0.15,
	},
	{
		name: "claims-file-handler", namespace: "production",
		spanNames: []string{"pull_file", "push_file", "process_file"},
		errorRate: 0.08,
		factory:   fileTransferSpan,
	},
	{
		// A deliberately boring service: no errors ever, Internal-kind
		// spans only, so it's classified as a Worker and exercises
		// the "everything is fine" status pill end-to-end. Useful as
		// a reference point when other services are flapping.
		name: "audit-logger", namespace: "production",
		spanNames: []string{"record_audit_event", "flush_buffer", "verify_signature", "rotate_log"},
		errorRate: 0.0,
		factory:   healthyWorkerSpan,
	},
	{
		// A multi-span trace: picks up an invoice from an SFTP partner,
		// transforms the filename, and delivers to a local processing
		// folder. Two spans share the trace id — pickup is the root,
		// deliver is its child — so the trace view shows a real
		// waterfall.
		name: "invoice-router", namespace: "production",
		// spanNames is unused by invoiceRouterTrace (it builds its own
		// pickup/deliver pair) but we keep it for the service list
		// header.
		spanNames: []string{"pickup_file", "deliver_file"},
		errorRate: 0.06,
		factory:   invoiceRouterTrace,
	},
}

func main() {
	endpoint := flag.String("endpoint", "http://localhost:4318/v1/traces", "cell-ingest OTLP traces endpoint")
	logsEndpoint := flag.String("logs-endpoint", "http://localhost:4318/v1/logs", "cell-ingest OTLP logs endpoint")
	metricsEndpoint := flag.String("metrics-endpoint", "http://localhost:4318/v1/metrics", "cell-ingest OTLP metrics endpoint")
	batchSize := flag.Int("batch", 50, "spans per request")
	continuous := flag.Bool("continuous", false, "keep emitting until interrupted")
	interval := flag.Duration("interval", 5*time.Second, "wait between batches in continuous mode")
	flag.Parse()

	rng := mrand.New(mrand.NewSource(time.Now().UnixNano()))

	send := func() {
		req := buildRequest(rng, *batchSize)
		if err := post(*endpoint, req); err != nil {
			fmt.Fprintf(os.Stderr, "seed-traces: trace send failed: %v\n", err)
		} else {
			fmt.Printf("sent %d spans to %s\n", *batchSize, *endpoint)
		}

		// Logs and metrics for the same demo services, so the
		// ServiceDetail Logs / Metrics sections have data too.
		logsReq, logCount := buildLogsRequest(rng)
		if err := postProto(*logsEndpoint, logsReq); err != nil {
			fmt.Fprintf(os.Stderr, "seed-traces: log send failed: %v\n", err)
		} else {
			fmt.Printf("sent %d logs to %s\n", logCount, *logsEndpoint)
		}

		metricsReq, pointCount := buildMetricsRequest(rng)
		if err := postProto(*metricsEndpoint, metricsReq); err != nil {
			fmt.Fprintf(os.Stderr, "seed-traces: metric send failed: %v\n", err)
		} else {
			fmt.Printf("sent %d metric points to %s\n", pointCount, *metricsEndpoint)
		}
	}

	if !*continuous {
		send()
		return
	}
	for {
		send()
		time.Sleep(*interval)
	}
}

func buildRequest(rng *mrand.Rand, batch int) *coltracepb.ExportTraceServiceRequest {
	req := &coltracepb.ExportTraceServiceRequest{}
	// Distribute the batch across services + scenarios. Scenarios
	// contribute several spans per call (across multiple services),
	// so they amortize at roughly the same volume as a single
	// service's per-call output.
	units := len(demoServices) + len(demoScenarios)
	perUnit := batch / units
	if perUnit < 1 {
		perUnit = 1
	}

	for _, svc := range demoServices {
		rs := &tracepb.ResourceSpans{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					stringAttr("service.name", svc.name),
					stringAttr("service.namespace", svc.namespace),
					stringAttr("deployment.environment", "dev"),
				},
			},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Scope: &commonpb.InstrumentationScope{Name: "seed-traces", Version: "0.1.0"},
				Spans: make([]*tracepb.Span, 0, perUnit),
			}},
		}
		for i := 0; i < perUnit; i++ {
			factory := svc.factory
			if factory == nil {
				factory = makeSpan
			}
			rs.ScopeSpans[0].Spans = append(rs.ScopeSpans[0].Spans, factory(rng, svc)...)
		}
		req.ResourceSpans = append(req.ResourceSpans, rs)
	}

	// Scenarios produce spans across multiple services per trace.
	// Accumulate them per service name and emit one ResourceSpans
	// block per service at the end.
	type bucket struct {
		namespace string
		spans     []*tracepb.Span
	}
	buckets := map[string]*bucket{}
	for _, scenario := range demoScenarios {
		for i := 0; i < perUnit; i++ {
			for _, ss := range scenario(rng) {
				b, ok := buckets[ss.serviceName]
				if !ok {
					b = &bucket{namespace: ss.serviceNamespace}
					buckets[ss.serviceName] = b
				}
				b.spans = append(b.spans, ss.spans...)
			}
		}
	}
	for serviceName, b := range buckets {
		req.ResourceSpans = append(req.ResourceSpans, &tracepb.ResourceSpans{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					stringAttr("service.name", serviceName),
					stringAttr("service.namespace", b.namespace),
					stringAttr("deployment.environment", "dev"),
				},
			},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Scope: &commonpb.InstrumentationScope{Name: "seed-traces", Version: "0.1.0"},
				Spans: b.spans,
			}},
		})
	}

	return req
}

func makeSpan(rng *mrand.Rand, svc serviceSpec) []*tracepb.Span {
	now := time.Now().UTC()
	durationMs := 5 + rng.Intn(995) // 5–1000 ms
	start := now.Add(-time.Duration(durationMs) * time.Millisecond)
	end := now

	span := &tracepb.Span{
		TraceId:           randomID(16),
		SpanId:            randomID(8),
		ParentSpanId:      []byte{},
		Name:              svc.spanNames[rng.Intn(len(svc.spanNames))],
		Kind:              pickKind(rng),
		StartTimeUnixNano: uint64(start.UnixNano()),
		EndTimeUnixNano:   uint64(end.UnixNano()),
		Attributes: []*commonpb.KeyValue{
			stringAttr("integration.id", pickIntegration(rng)),
			stringAttr("messaging.system", pickMessagingSystem(rng)),
			stringAttr("customer.id", fmt.Sprintf("cust_%05d", rng.Intn(10000))),
			intAttr("payload.bytes", int64(rng.Intn(50000)+128)),
		},
		Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	if rng.Float64() < svc.errorRate {
		span.Status = &tracepb.Status{
			Code:    tracepb.Status_STATUS_CODE_ERROR,
			Message: pickError(rng),
		}
	}
	return []*tracepb.Span{span}
}

func post(url string, req *coltracepb.ExportTraceServiceRequest) error {
	body, err := proto.Marshal(req)
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

// helpers

func stringAttr(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}
func intAttr(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}
func boolAttr(k string, v bool) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v}}}
}

// ioAttrs builds the four canonical io.* attributes that mark a span
// as a boundary span — one end of an input or output the service
// touches. The cell-api facet classifier matches services by the
// distinct (io.kind, io.role) pairs it sees in this attribute set, so
// every span that actually crosses a system boundary should carry
// these. Internal spans (transforms, rule evaluation, in-process
// work) deliberately do NOT.
//
//	role     "input" | "output"
//	kind     "file" | "queue" | "http" | "db" | "email"
//	system   protocol identifier — "ftp", "sftp", "smb", "kafka",
//	         "azure-servicebus", "rabbitmq", "https", "smtp",
//	         "snowflake", ...
//	endpoint host + path / queue name / URL / table — a human-
//	         readable identifier of the source or target.
func ioAttrs(role, kind, system, endpoint string) []*commonpb.KeyValue {
	return []*commonpb.KeyValue{
		stringAttr("io.role", role),
		stringAttr("io.kind", kind),
		stringAttr("io.system", system),
		stringAttr("io.endpoint", endpoint),
	}
}

func randomID(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func pickKind(rng *mrand.Rand) tracepb.Span_SpanKind {
	kinds := []tracepb.Span_SpanKind{
		tracepb.Span_SPAN_KIND_INTERNAL,
		tracepb.Span_SPAN_KIND_SERVER,
		tracepb.Span_SPAN_KIND_CLIENT,
		tracepb.Span_SPAN_KIND_PRODUCER,
		tracepb.Span_SPAN_KIND_CONSUMER,
	}
	return kinds[rng.Intn(len(kinds))]
}

func pickIntegration(rng *mrand.Rand) string {
	v := []string{"order-sync", "payment-events", "partner-edi-flow", "fulfillment"}
	return v[rng.Intn(len(v))]
}

func pickMessagingSystem(rng *mrand.Rand) string {
	v := []string{"kafka", "amqp", "azure-servicebus", "activemq-artemis"}
	return v[rng.Intn(len(v))]
}

func pickError(rng *mrand.Rand) string {
	v := []string{
		"timeout calling downstream service",
		"connection refused",
		"validation failed: missing customer id",
		"rate limit exceeded",
		"deserialization error",
	}
	return v[rng.Intn(len(v))]
}

// --- file transfer factory ---------------------------------------------
//
// fileTransferSpan generates spans modeled on a service that pulls
// claim files from a partner SFTP server, processes them locally, and
// pushes the results to a downstream system. The span attributes
// follow the file.* and transfer.* conventions our File Transfer
// service type matches on.

const (
	sourceHost      = "partner-sftp.example.com"
	destinationHost = "adjudicator.example.com"
)

var fileExtensions = []string{".xml", ".x12", ".edi", ".csv"}
var fileTransferErrors = []string{
	"sftp connection refused",
	"unable to acquire file lock",
	"checksum mismatch",
	"destination quota exceeded",
	"file disappeared before pickup",
}

func fileTransferSpan(rng *mrand.Rand, svc serviceSpec) []*tracepb.Span {
	now := time.Now().UTC()
	// Transfers are usually slower than RPC calls; sample more
	// generously so the latency widget has a wider distribution.
	durationMs := 50 + rng.Intn(2950) // 50–3000 ms
	start := now.Add(-time.Duration(durationMs) * time.Millisecond)

	spanName := svc.spanNames[rng.Intn(len(svc.spanNames))]
	kind := fileTransferKindFor(spanName)
	protocol := pickFileProtocol(rng)

	fileName := fmt.Sprintf("claim_%s%s",
		now.Format("20060102_150405"),
		fileExtensions[rng.Intn(len(fileExtensions))],
	)
	fileSize := int64(rng.Intn(10*1024*1024) + 1024) // 1 KiB–10 MiB

	attrs := []*commonpb.KeyValue{
		stringAttr("file.name", fileName),
		intAttr("file.size", fileSize),
		stringAttr("integration.id", "claims-intake"),
	}

	switch spanName {
	case "pull_file":
		attrs = append(attrs,
			stringAttr("transfer.direction", "inbound"),
			stringAttr("transfer.protocol", protocol),
			stringAttr("transfer.source.host", sourceHost),
			stringAttr("transfer.source.path", "/outbound/claims/"+fileName),
			stringAttr("transfer.destination.path", "/inbox/"+fileName),
			stringAttr("file.path", "/inbox/"+fileName),
		)
		attrs = append(attrs, ioAttrs("input", "file", protocol, sourceHost+":/outbound/claims/"+fileName)...)
	case "push_file":
		attrs = append(attrs,
			stringAttr("transfer.direction", "outbound"),
			stringAttr("transfer.protocol", protocol),
			stringAttr("transfer.source.path", "/processed/"+fileName),
			stringAttr("transfer.destination.host", destinationHost),
			stringAttr("transfer.destination.path", "/inbound/claims/"+fileName),
			stringAttr("file.path", "/processed/"+fileName),
		)
		attrs = append(attrs, ioAttrs("output", "file", protocol, destinationHost+":/inbound/claims/"+fileName)...)
	default: // process_file — Internal, no io.* (it's local processing)
		attrs = append(attrs,
			stringAttr("file.path", "/processing/"+fileName),
			stringAttr("transfer.direction", "internal"),
		)
	}

	span := &tracepb.Span{
		TraceId:           randomID(16),
		SpanId:            randomID(8),
		ParentSpanId:      []byte{},
		Name:              spanName,
		Kind:              kind,
		StartTimeUnixNano: uint64(start.UnixNano()),
		EndTimeUnixNano:   uint64(now.UnixNano()),
		Attributes:        attrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	if rng.Float64() < svc.errorRate {
		span.Status = &tracepb.Status{
			Code:    tracepb.Status_STATUS_CODE_ERROR,
			Message: fileTransferErrors[rng.Intn(len(fileTransferErrors))],
		}
	}
	return []*tracepb.Span{span}
}

func fileTransferKindFor(spanName string) tracepb.Span_SpanKind {
	switch spanName {
	case "pull_file", "push_file":
		// Both file movements are outbound network calls to a remote
		// file server — Client kind. (Consumer/Producer are reserved
		// for messaging-broker conversations.)
		return tracepb.Span_SPAN_KIND_CLIENT
	default:
		return tracepb.Span_SPAN_KIND_INTERNAL
	}
}

func pickFileProtocol(rng *mrand.Rand) string {
	v := []string{"sftp", "ftp", "local", "smb"}
	return v[rng.Intn(len(v))]
}

// --- healthy worker factory --------------------------------------------
//
// healthyWorkerSpan always emits a successful Internal-kind span. It
// ignores the service's errorRate field on purpose — the whole point
// of this factory is "this service is always healthy, regardless of
// the global seed config". Used by the audit-logger demo service.

var auditActions = []string{"create", "update", "delete", "view"}
var auditResources = []string{"order", "invoice", "claim", "shipment", "user"}

// --- invoice router factory --------------------------------------------
//
// invoiceRouterTrace emits two related spans that share one trace id:
//
//   - pickup_file (root, Consumer kind, "inbound"): reads a file from
//     the partner SFTP source folder.
//   - deliver_file (child of pickup, Producer kind, "outbound"): writes
//     a transformed file into the local destination folder. The target
//     filename is intentionally different from the source — that's a
//     common pattern (X12 → JSON, EDI → CSV, etc.) and seeing it in the
//     trace view makes the rename visible.

const (
	invoiceSourceHost = "partner-sftp.example.com"
	invoiceSourceDir  = "/outbound/invoices"
	invoiceTargetDir  = "/local/processing/invoices"
)

func invoiceRouterTrace(rng *mrand.Rand, svc serviceSpec) []*tracepb.Span {
	now := time.Now().UTC()
	traceID := randomID(16)
	pickupSpanID := randomID(8)
	deliverSpanID := randomID(8)

	// Pickup runs first; deliver follows after a tiny gap.
	pickupDur := time.Duration(50+rng.Intn(450)) * time.Millisecond  // 50–500 ms
	deliverDur := time.Duration(20+rng.Intn(280)) * time.Millisecond // 20–300 ms
	gap := time.Duration(rng.Intn(40)) * time.Millisecond

	deliverEnd := now
	deliverStart := deliverEnd.Add(-deliverDur)
	pickupEnd := deliverStart.Add(-gap)
	pickupStart := pickupEnd.Add(-pickupDur)

	// The transform: INV_20260513_142322_0042.x12 → invoice-20260513-0042.json
	timestamp := now.Format("20060102_150405")
	seq := rng.Intn(10000)
	sourceName := fmt.Sprintf("INV_%s_%04d.x12", timestamp, seq)
	targetName := fmt.Sprintf("invoice-%s-%04d.json", now.Format("20060102"), seq)
	fileSize := int64(rng.Intn(2*1024*1024) + 4*1024) // 4 KiB – 2 MiB

	pickupAttrs := []*commonpb.KeyValue{
		stringAttr("file.name", sourceName),
		stringAttr("file.path", invoiceSourceDir+"/"+sourceName),
		intAttr("file.size", fileSize),
		stringAttr("transfer.direction", "inbound"),
		stringAttr("transfer.protocol", "sftp"),
		stringAttr("transfer.source.host", invoiceSourceHost),
		stringAttr("transfer.source.path", invoiceSourceDir+"/"+sourceName),
		stringAttr("integration.id", "invoice-routing"),
	}
	pickupAttrs = append(pickupAttrs, ioAttrs("input", "file", "sftp", invoiceSourceHost+":"+invoiceSourceDir+"/"+sourceName)...)
	pickup := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            pickupSpanID,
		ParentSpanId:      []byte{},
		Name:              "pickup_file",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(pickupStart.UnixNano()),
		EndTimeUnixNano:   uint64(pickupEnd.UnixNano()),
		Attributes:        pickupAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	deliverAttrs := []*commonpb.KeyValue{
		stringAttr("file.name", targetName),
		stringAttr("file.path", invoiceTargetDir+"/"+targetName),
		intAttr("file.size", fileSize),
		stringAttr("transfer.direction", "outbound"),
		stringAttr("transfer.protocol", "local"),
		stringAttr("transfer.destination.path", invoiceTargetDir+"/"+targetName),
		// Keep the original source filename on the deliver span too,
		// so a search by either name finds this trace.
		stringAttr("transfer.source.file.name", sourceName),
		stringAttr("integration.id", "invoice-routing"),
	}
	deliverAttrs = append(deliverAttrs, ioAttrs("output", "file", "local", invoiceTargetDir+"/"+targetName)...)
	deliver := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            deliverSpanID,
		ParentSpanId:      pickupSpanID,
		Name:              "deliver_file",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(deliverStart.UnixNano()),
		EndTimeUnixNano:   uint64(deliverEnd.UnixNano()),
		Attributes:        deliverAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	// One in errorRate traces fails on either pickup or deliver.
	if rng.Float64() < svc.errorRate {
		target := pickup
		if rng.Intn(2) == 0 {
			target = deliver
		}
		target.Status = &tracepb.Status{
			Code:    tracepb.Status_STATUS_CODE_ERROR,
			Message: fileTransferErrors[rng.Intn(len(fileTransferErrors))],
		}
	}

	return []*tracepb.Span{pickup, deliver}
}

// --- order flow scenario -----------------------------------------------
//
// orderFlowScenario emits a single trace that spans two services
// connected through a queue:
//
//   order-intake (Internal + Producer) ──messaging──▶ order-fulfillment (Consumer + Client)
//
//   process_order_file        Internal   service: order-intake        root
//   └── publish_order         Producer   service: order-intake        child
//       └── consume_order     Consumer   service: order-fulfillment   parent: publish_order
//           └── POST /v1/…    Client     service: order-fulfillment   parent: consume_order
//
// The two services share the trace_id and the consumer's parent is
// the producer span — exactly the shape an OTel context propagation
// across a queue produces. About 5% of traces fail at the HTTP step.

var orderMessagingSystems = []string{"kafka", "azure-servicebus", "rabbitmq"}

func orderFlowScenario(rng *mrand.Rand) []serviceSpans {
	now := time.Now().UTC()
	traceID := randomID(16)
	rootID := randomID(8)
	publishID := randomID(8)
	consumeID := randomID(8)

	msgSystem := orderMessagingSystems[rng.Intn(len(orderMessagingSystems))]
	queueName := "orders.queue"
	orderID := fmt.Sprintf("ord_%08d", rng.Intn(99999999))
	fileName := fmt.Sprintf("order_%s.json", orderID)
	fileSize := int64(1024 + rng.Intn(48*1024))
	customerID := fmt.Sprintf("cust_%05d", rng.Intn(10000))

	// Decide whether this trace fails at the HTTP step (~5%).
	httpStatus := int64(201)
	failHTTP := rng.Float64() < 0.05
	if failHTTP {
		httpStatus = 500
	}

	// Lay out durations so the spans nest in time the way they do in
	// real life: file processing, then publish, then a small queue
	// delay, then consume, then the HTTP call.
	processFileDur := time.Duration(50+rng.Intn(150)) * time.Millisecond
	publishDur := time.Duration(5+rng.Intn(20)) * time.Millisecond
	queueLatency := time.Duration(5+rng.Intn(50)) * time.Millisecond
	consumeDur := time.Duration(5+rng.Intn(15)) * time.Millisecond
	submitDur := time.Duration(50+rng.Intn(250)) * time.Millisecond

	submitEnd := now
	submitStart := submitEnd.Add(-submitDur)
	consumeEnd := submitStart
	consumeStart := consumeEnd.Add(-consumeDur)
	publishEnd := consumeStart.Add(-queueLatency)
	publishStart := publishEnd.Add(-publishDur)
	processEnd := publishStart
	processStart := processEnd.Add(-processFileDur)

	processFileAttrs := []*commonpb.KeyValue{
		stringAttr("file.name", fileName),
		stringAttr("file.path", "/inbox/orders/"+fileName),
		intAttr("file.size", fileSize),
		stringAttr("integration.id", "order-flow"),
		stringAttr("order.id", orderID),
		stringAttr("customer.id", customerID),
	}
	processFileAttrs = append(processFileAttrs, ioAttrs("input", "file", "local", "/inbox/orders/"+fileName)...)
	processFileSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            rootID,
		ParentSpanId:      []byte{},
		Name:              "process_order_file",
		Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(processStart.UnixNano()),
		EndTimeUnixNano:   uint64(processEnd.UnixNano()),
		Attributes:        processFileAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	publishAttrs := []*commonpb.KeyValue{
		stringAttr("messaging.system", msgSystem),
		stringAttr("messaging.destination.name", queueName),
		stringAttr("messaging.operation", "publish"),
		stringAttr("integration.id", "order-flow"),
		stringAttr("order.id", orderID),
	}
	publishAttrs = append(publishAttrs, ioAttrs("output", "queue", msgSystem, queueName)...)
	publishSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            publishID,
		ParentSpanId:      rootID,
		Name:              "publish_order",
		Kind:              tracepb.Span_SPAN_KIND_PRODUCER,
		StartTimeUnixNano: uint64(publishStart.UnixNano()),
		EndTimeUnixNano:   uint64(publishEnd.UnixNano()),
		Attributes:        publishAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	consumeAttrs := []*commonpb.KeyValue{
		stringAttr("messaging.system", msgSystem),
		stringAttr("messaging.destination.name", queueName),
		stringAttr("messaging.operation", "consume"),
		stringAttr("integration.id", "order-flow"),
		stringAttr("order.id", orderID),
	}
	consumeAttrs = append(consumeAttrs, ioAttrs("input", "queue", msgSystem, queueName)...)
	consumeSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            consumeID,
		ParentSpanId:      publishID,
		Name:              "consume_order",
		Kind:              tracepb.Span_SPAN_KIND_CONSUMER,
		StartTimeUnixNano: uint64(consumeStart.UnixNano()),
		EndTimeUnixNano:   uint64(consumeEnd.UnixNano()),
		Attributes:        consumeAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	submitAttrs := []*commonpb.KeyValue{
		stringAttr("http.method", "POST"),
		stringAttr("http.url", "https://orders.example.com/v1/orders"),
		stringAttr("http.route", "/v1/orders"),
		stringAttr("http.request.method", "POST"),
		intAttr("http.status_code", httpStatus),
		intAttr("http.response.status_code", httpStatus),
		stringAttr("net.peer.name", "orders.example.com"),
		stringAttr("integration.id", "order-flow"),
		stringAttr("order.id", orderID),
	}
	submitAttrs = append(submitAttrs, ioAttrs("output", "http", "https", "orders.example.com/v1/orders")...)
	submitSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            randomID(8),
		ParentSpanId:      consumeID,
		Name:              "POST /v1/orders",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(submitStart.UnixNano()),
		EndTimeUnixNano:   uint64(submitEnd.UnixNano()),
		Attributes:        submitAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}
	if failHTTP {
		submitSpan.Status = &tracepb.Status{
			Code:    tracepb.Status_STATUS_CODE_ERROR,
			Message: fmt.Sprintf("HTTP %d from downstream", httpStatus),
		}
	}

	return []serviceSpans{
		{
			serviceName:      "order-intake",
			serviceNamespace: "production",
			spans:            []*tracepb.Span{processFileSpan, publishSpan},
		},
		{
			serviceName:      "order-fulfillment",
			serviceNamespace: "production",
			spans:            []*tracepb.Span{consumeSpan, submitSpan},
		},
	}
}

// --- shipment flow scenario --------------------------------------------
//
// shipmentFlowScenario emits a single trace that fans out to two
// services in parallel and then chains one of them to a fourth via
// a queue. It exists to give the integration / trace flow graph
// something more interesting than a straight pipeline to render.
//
//   shipment-orchestrator (Internal root)
//   ├── call_warehouse (Client) ──HTTP──▶ warehouse-picker     (Server)
//   └── call_carrier   (Client) ──HTTP──▶ carrier-dispatcher   (Server)
//                                          └── publish_tracking (Producer)
//                                              ──messaging──▶ tracking-processor
//                                                              (Consumer + Internal)
//
// Eight spans across four services, one shared trace_id, all
// parent-child relationships intact. Two mutually exclusive failure
// modes are seeded:
//   - About 4% of traces fail at the orchestrator itself before any
//     downstream call is made — the root span is the only span in the
//     trace and no message reaches the warehouse, dispatcher, or
//     tracking-processor services.
//   - About 6% of (the remaining) traces fail at the carrier call —
//     both the orchestrator's Client span and the dispatcher's Server
//     span get the error status and the tracking branch is dropped,
//     which makes the error propagation visible in the flow graph.

var shipmentCarriers = []string{"DHL", "FedEx", "UPS", "PostNord"}
var shipmentMessagingSystemsForFlow = []string{"kafka", "azure-servicebus", "rabbitmq"}

func shipmentFlowScenario(rng *mrand.Rand) []serviceSpans {
	now := time.Now().UTC()
	traceID := randomID(16)

	rootID := randomID(8)
	callWarehouseID := randomID(8)
	handlePickID := randomID(8)
	callCarrierID := randomID(8)
	handleHandoffID := randomID(8)
	publishID := randomID(8)
	consumeID := randomID(8)

	shipmentID := fmt.Sprintf("ship_%08d", rng.Intn(99999999))
	customerID := fmt.Sprintf("cust_%05d", rng.Intn(10000))
	trackingID := fmt.Sprintf("trk_%012d", rng.Intn(999999999999))
	carrier := shipmentCarriers[rng.Intn(len(shipmentCarriers))]
	msgSystem := shipmentMessagingSystemsForFlow[rng.Intn(len(shipmentMessagingSystemsForFlow))]

	// Two independent, mutually exclusive failure modes:
	//   failRoot    — the orchestrator rejects the request before doing
	//                 any work (validation, auth, feature flag, etc.).
	//                 The root span errors and NO downstream services
	//                 are called, so the trace contains only the
	//                 shipment-orchestrator's root span.
	//   failCarrier — the orchestrator dispatches to both downstreams,
	//                 the warehouse call succeeds, the carrier call
	//                 fails, and the tracking branch never publishes.
	failRoot := rng.Float64() < 0.04
	failCarrier := !failRoot && rng.Float64() < 0.06
	httpStatusOK := int64(200)
	httpStatusBad := int64(502)
	carrierStatus := httpStatusOK
	if failCarrier {
		carrierStatus = httpStatusBad
	}

	// Time layout — work backwards from "now" so the trace is recent.
	// The two HTTP calls run in parallel; carrier is the slower one
	// because it goes off-network. The tracking publish/consume
	// chain happens inside the carrier handoff and finishes shortly
	// after.
	processDur := time.Duration(120+rng.Intn(60)) * time.Millisecond
	consumeDur := time.Duration(10+rng.Intn(15)) * time.Millisecond
	queueLatency := time.Duration(15+rng.Intn(40)) * time.Millisecond
	publishDur := time.Duration(15+rng.Intn(30)) * time.Millisecond
	handleHandoffDur := time.Duration(220+rng.Intn(80)) * time.Millisecond
	callCarrierDur := handleHandoffDur + time.Duration(8)*time.Millisecond
	handlePickDur := time.Duration(140+rng.Intn(60)) * time.Millisecond
	callWarehouseDur := handlePickDur + time.Duration(8)*time.Millisecond
	rootDur := callCarrierDur + time.Duration(20)*time.Millisecond

	processEnd := now
	processStart := processEnd.Add(-processDur)
	consumeEnd := processStart
	consumeStart := consumeEnd.Add(-consumeDur)
	publishEnd := consumeStart.Add(-queueLatency)
	publishStart := publishEnd.Add(-publishDur)

	// The handoff ends at publishEnd + a few ms (its publish span is
	// nested inside it).
	handleHandoffEnd := publishEnd.Add(5 * time.Millisecond)
	handleHandoffStart := handleHandoffEnd.Add(-handleHandoffDur)
	callCarrierEnd := handleHandoffEnd.Add(2 * time.Millisecond)
	callCarrierStart := callCarrierEnd.Add(-callCarrierDur)

	// Warehouse runs in parallel with the carrier call (same start),
	// but it's quicker — finishes earlier.
	callWarehouseStart := callCarrierStart
	callWarehouseEnd := callWarehouseStart.Add(callWarehouseDur)
	handlePickStart := callWarehouseStart.Add(2 * time.Millisecond)
	handlePickEnd := handlePickStart.Add(handlePickDur)

	rootEnd := callCarrierEnd.Add(5 * time.Millisecond)
	rootStart := rootEnd.Add(-rootDur)

	// Common attributes that every span in the trace carries so
	// search by shipment.id surfaces the whole trace.
	common := func() []*commonpb.KeyValue {
		return []*commonpb.KeyValue{
			stringAttr("integration.id", "shipment-flow"),
			stringAttr("shipment.id", shipmentID),
			stringAttr("customer.id", customerID),
		}
	}

	rootSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            rootID,
		ParentSpanId:      []byte{},
		Name:              "process_shipment",
		Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(rootStart.UnixNano()),
		EndTimeUnixNano:   uint64(rootEnd.UnixNano()),
		Attributes:        common(),
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}
	if failRoot {
		// Orchestrator rejected the shipment before making any
		// downstream calls — finish quickly and mark the span as
		// errored. Downstream service spans are dropped below.
		rootFailDur := time.Duration(3+rng.Intn(8)) * time.Millisecond
		rootEnd = now
		rootStart = rootEnd.Add(-rootFailDur)
		rootSpan.StartTimeUnixNano = uint64(rootStart.UnixNano())
		rootSpan.EndTimeUnixNano = uint64(rootEnd.UnixNano())
		rootSpan.Status = &tracepb.Status{
			Code:    tracepb.Status_STATUS_CODE_ERROR,
			Message: "shipment validation failed; no downstream calls made",
		}
	}

	callWarehouseAttrs := append(common(),
		stringAttr("http.method", "POST"),
		stringAttr("http.url", "https://warehouse.example.internal/pick"),
		stringAttr("http.route", "/pick"),
		intAttr("http.status_code", httpStatusOK),
		stringAttr("net.peer.name", "warehouse.example.internal"),
	)
	callWarehouseAttrs = append(callWarehouseAttrs, ioAttrs("output", "http", "https", "warehouse.example.internal/pick")...)
	callWarehouseSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            callWarehouseID,
		ParentSpanId:      rootID,
		Name:              "POST /pick",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(callWarehouseStart.UnixNano()),
		EndTimeUnixNano:   uint64(callWarehouseEnd.UnixNano()),
		Attributes:        callWarehouseAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	handlePickAttrs := append(common(),
		stringAttr("http.method", "POST"),
		stringAttr("http.route", "/pick"),
		intAttr("http.status_code", httpStatusOK),
		stringAttr("warehouse.bin", fmt.Sprintf("AISLE-%02d", rng.Intn(40)+1)),
	)
	handlePickAttrs = append(handlePickAttrs, ioAttrs("input", "http", "https", "/pick")...)
	handlePickSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            handlePickID,
		ParentSpanId:      callWarehouseID,
		Name:              "POST /pick",
		Kind:              tracepb.Span_SPAN_KIND_SERVER,
		StartTimeUnixNano: uint64(handlePickStart.UnixNano()),
		EndTimeUnixNano:   uint64(handlePickEnd.UnixNano()),
		Attributes:        handlePickAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	callCarrierAttrs := append(common(),
		stringAttr("http.method", "POST"),
		stringAttr("http.url", "https://dispatcher.example.internal/handoff"),
		stringAttr("http.route", "/handoff"),
		intAttr("http.status_code", carrierStatus),
		stringAttr("net.peer.name", "dispatcher.example.internal"),
		stringAttr("carrier.name", carrier),
	)
	callCarrierAttrs = append(callCarrierAttrs, ioAttrs("output", "http", "https", "dispatcher.example.internal/handoff")...)
	callCarrierSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            callCarrierID,
		ParentSpanId:      rootID,
		Name:              "POST /handoff",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(callCarrierStart.UnixNano()),
		EndTimeUnixNano:   uint64(callCarrierEnd.UnixNano()),
		Attributes:        callCarrierAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}
	if failCarrier {
		callCarrierSpan.Status = &tracepb.Status{
			Code:    tracepb.Status_STATUS_CODE_ERROR,
			Message: fmt.Sprintf("HTTP %d from carrier dispatcher", carrierStatus),
		}
	}

	handleHandoffAttrs := append(common(),
		stringAttr("http.method", "POST"),
		stringAttr("http.route", "/handoff"),
		intAttr("http.status_code", carrierStatus),
		stringAttr("carrier.name", carrier),
		stringAttr("tracking.id", trackingID),
	)
	handleHandoffAttrs = append(handleHandoffAttrs, ioAttrs("input", "http", "https", "/handoff")...)
	handleHandoffSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            handleHandoffID,
		ParentSpanId:      callCarrierID,
		Name:              "POST /handoff",
		Kind:              tracepb.Span_SPAN_KIND_SERVER,
		StartTimeUnixNano: uint64(handleHandoffStart.UnixNano()),
		EndTimeUnixNano:   uint64(handleHandoffEnd.UnixNano()),
		Attributes:        handleHandoffAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}
	if failCarrier {
		handleHandoffSpan.Status = &tracepb.Status{
			Code:    tracepb.Status_STATUS_CODE_ERROR,
			Message: "carrier API rejected the handoff",
		}
	}

	publishTrackingAttrs := append(common(),
		stringAttr("messaging.system", msgSystem),
		stringAttr("messaging.destination.name", "tracking.events"),
		stringAttr("messaging.operation", "publish"),
		stringAttr("tracking.id", trackingID),
		stringAttr("carrier.name", carrier),
	)
	publishTrackingAttrs = append(publishTrackingAttrs, ioAttrs("output", "queue", msgSystem, "tracking.events")...)
	publishSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            publishID,
		ParentSpanId:      handleHandoffID,
		Name:              "publish_tracking_event",
		Kind:              tracepb.Span_SPAN_KIND_PRODUCER,
		StartTimeUnixNano: uint64(publishStart.UnixNano()),
		EndTimeUnixNano:   uint64(publishEnd.UnixNano()),
		Attributes:        publishTrackingAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	consumeTrackingAttrs := append(common(),
		stringAttr("messaging.system", msgSystem),
		stringAttr("messaging.destination.name", "tracking.events"),
		stringAttr("messaging.operation", "consume"),
		stringAttr("tracking.id", trackingID),
	)
	consumeTrackingAttrs = append(consumeTrackingAttrs, ioAttrs("input", "queue", msgSystem, "tracking.events")...)
	consumeSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            consumeID,
		ParentSpanId:      publishID,
		Name:              "consume_tracking_event",
		Kind:              tracepb.Span_SPAN_KIND_CONSUMER,
		StartTimeUnixNano: uint64(consumeStart.UnixNano()),
		EndTimeUnixNano:   uint64(consumeEnd.UnixNano()),
		Attributes:        consumeTrackingAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	processSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            randomID(8),
		ParentSpanId:      consumeID,
		Name:              "store_tracking_record",
		Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(processStart.UnixNano()),
		EndTimeUnixNano:   uint64(processEnd.UnixNano()),
		Attributes: append(common(),
			stringAttr("tracking.id", trackingID),
			stringAttr("carrier.name", carrier),
		),
		Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	// When the orchestrator fails before making any downstream calls,
	// the trace contains only its root span — no client spans, no
	// downstream services. Return early so the flow graph shows the
	// orchestrator failing in isolation with no fan-out edges.
	if failRoot {
		return []serviceSpans{
			{
				serviceName:      "shipment-orchestrator",
				serviceNamespace: "production",
				spans:            []*tracepb.Span{rootSpan},
			},
		}
	}

	// When the carrier handoff fails the dispatcher never publishes
	// the tracking event, so the tracking-processor receives nothing
	// and the queue/consume/process spans don't exist either.
	// Drop the entire downstream branch in that case.
	dispatcherSpans := []*tracepb.Span{handleHandoffSpan}
	if !failCarrier {
		dispatcherSpans = append(dispatcherSpans, publishSpan)
	}

	out := []serviceSpans{
		{
			serviceName:      "shipment-orchestrator",
			serviceNamespace: "production",
			spans:            []*tracepb.Span{rootSpan, callWarehouseSpan, callCarrierSpan},
		},
		{
			serviceName:      "warehouse-picker",
			serviceNamespace: "production",
			spans:            []*tracepb.Span{handlePickSpan},
		},
		{
			serviceName:      "carrier-dispatcher",
			serviceNamespace: "production",
			spans:            dispatcherSpans,
		},
	}
	if !failCarrier {
		out = append(out, serviceSpans{
			serviceName:      "tracking-processor",
			serviceNamespace: "production",
			spans:            []*tracepb.Span{consumeSpan, processSpan},
		})
	}
	return out
}

// --- ftp fan-out scenario ----------------------------------------------
//
// ftpFanoutScenario emits a single trace that models one document
// arriving on an FTP server and fanning out across six services
// through a shared message topic and a downstream Kafka topic:
//
//   document-intake (Consumer + Internal + Producer)
//     pickup_file ──▶ transform_file ──▶ publish_to_topic
//                                            │
//                                            ├──▶ archive-writer
//                                            │      consume_document ──▶ write_archive_file
//                                            │
//                                            ├──▶ kafka-bridge
//                                            │      consume_document ──▶ transform_for_kafka ──▶ publish_to_kafka
//                                            │                                                       │
//                                            │                                                       ▼
//                                            │                                                analytics-processor
//                                            │                                                  consume_normalized_document
//                                            │                                                    ──▶ enrich_document
//                                            │                                                    ──▶ upsert_warehouse_record
//                                            │
//                                            └──▶ notification-dispatcher
//                                                   consume_document ──▶ evaluate_notification_rule
//                                                                            └── POST /send  (Client, only when matched)
//                                                                                  │
//                                                                                  ▼
//                                                                           email-service
//                                                                             POST /send (Server)
//                                                                               ├── render_template
//                                                                               └── send_email_smtp (Client → SMTP)
//
// All spans are STATUS_CODE_OK — this scenario deliberately models a
// "happy path" estate so the UI's healthy-flow visuals get exercised.
// The notification dispatcher always consumes and evaluates the rule,
// but only calls the email-service when document.priority == "high"
// (roughly 1 in 4 traces); when it doesn't match, email-service does
// not appear in the trace at all, so the "sometimes a file produces
// an email" behaviour shows up naturally.

const (
	fanoutFTPSource      = "partner-ftp.example.com"
	fanoutTopic          = "documents.processed"
	fanoutKafkaTopic     = "documents.normalized"
	fanoutArchiveHost    = "archive-fs.example.internal"
	fanoutArchiveDir     = "/archive/documents"
	fanoutEmailRecipient = "ops-alerts@example.com"
	fanoutSMTPHost       = "smtp.example.internal"
)

var (
	fanoutDocumentTypes  = []string{"daily-report", "invoice-batch", "claim-summary", "payment-advice"}
	fanoutMessagingBuses = []string{"azure-servicebus", "rabbitmq", "activemq-artemis"}
)

func ftpFanoutScenario(rng *mrand.Rand) []serviceSpans {
	now := time.Now().UTC()
	traceID := randomID(16)

	// Span IDs — declared up-front so parent/child wiring is obvious.
	pickupID := randomID(8)
	transformIntakeID := randomID(8)
	publishTopicID := randomID(8)
	archiveConsumeID := randomID(8)
	archiveWriteID := randomID(8)
	kafkaConsumeID := randomID(8)
	kafkaTransformID := randomID(8)
	kafkaPublishID := randomID(8)
	analyticsConsumeID := randomID(8)
	analyticsEnrichID := randomID(8)
	analyticsWarehouseID := randomID(8)
	notifyConsumeID := randomID(8)
	notifyEvalID := randomID(8)

	// Document characteristics. Priority distribution: 40% low, 35%
	// normal, 25% high — so roughly one in four traces matches the
	// notification rule and produces an email.
	docType := fanoutDocumentTypes[rng.Intn(len(fanoutDocumentTypes))]
	var priority string
	switch roll := rng.Float64(); {
	case roll < 0.40:
		priority = "low"
	case roll < 0.75:
		priority = "normal"
	default:
		priority = "high"
	}
	matched := priority == "high"

	msgSystem := fanoutMessagingBuses[rng.Intn(len(fanoutMessagingBuses))]
	docID := fmt.Sprintf("doc_%010d", rng.Intn(999999999))
	customerID := fmt.Sprintf("cust_%05d", rng.Intn(10000))

	timestamp := now.Format("20060102_150405")
	sourceName := fmt.Sprintf("%s_%s.csv", docType, timestamp)
	normalizedName := fmt.Sprintf("%s-%s-%s.json", docType, now.Format("20060102"), docID[len(docID)-4:])
	fileSize := int64(rng.Intn(4*1024*1024) + 2*1024) // 2 KiB – 4 MiB

	// Per-span durations.
	pickupDur := time.Duration(180+rng.Intn(220)) * time.Millisecond // 180–400 ms (FTP pull)
	transformIntakeDur := time.Duration(40+rng.Intn(80)) * time.Millisecond
	publishDur := time.Duration(8+rng.Intn(15)) * time.Millisecond
	queueLatency := time.Duration(20+rng.Intn(80)) * time.Millisecond

	consumeDur := time.Duration(5+rng.Intn(15)) * time.Millisecond
	archiveWriteDur := time.Duration(60+rng.Intn(140)) * time.Millisecond
	kafkaTransformDur := time.Duration(30+rng.Intn(60)) * time.Millisecond
	kafkaPublishDur := time.Duration(6+rng.Intn(14)) * time.Millisecond
	notifyEvalDur := time.Duration(2+rng.Intn(8)) * time.Millisecond

	// analytics-processor work downstream of kafka-bridge.
	kafkaToAnalyticsQueueLatency := time.Duration(10+rng.Intn(40)) * time.Millisecond
	analyticsConsumeDur := time.Duration(5+rng.Intn(15)) * time.Millisecond
	analyticsEnrichDur := time.Duration(20+rng.Intn(40)) * time.Millisecond
	analyticsWarehouseDur := time.Duration(40+rng.Intn(120)) * time.Millisecond

	// email-service inner work, sized so render + smtp + a small pad
	// fits inside the Server span, and the Server span fits inside
	// the notification-dispatcher's Client span with some network
	// overhead on either side.
	renderTemplateDur := time.Duration(3+rng.Intn(10)) * time.Millisecond
	sendEmailSMTPDur := time.Duration(40+rng.Intn(180)) * time.Millisecond
	emailServerPad := 4 * time.Millisecond
	emailServerDur := renderTemplateDur + sendEmailSMTPDur + emailServerPad
	emailClientOverhead := 8 * time.Millisecond
	callEmailServiceDur := emailServerDur + emailClientOverhead

	// Lay out time backwards from `now`. The three consumer branches
	// run in parallel after the topic publish completes; whichever
	// branch is longest determines where `now` lands.
	archiveBranchDur := consumeDur + archiveWriteDur
	kafkaBranchDur := consumeDur + kafkaTransformDur + kafkaPublishDur +
		kafkaToAnalyticsQueueLatency + analyticsConsumeDur + analyticsEnrichDur + analyticsWarehouseDur
	notifyBranchDur := consumeDur + notifyEvalDur
	if matched {
		notifyBranchDur += callEmailServiceDur
	}
	maxBranchDur := archiveBranchDur
	if kafkaBranchDur > maxBranchDur {
		maxBranchDur = kafkaBranchDur
	}
	if notifyBranchDur > maxBranchDur {
		maxBranchDur = notifyBranchDur
	}

	consumerStartAnchor := now.Add(-maxBranchDur)
	publishEnd := consumerStartAnchor.Add(-queueLatency)
	publishStart := publishEnd.Add(-publishDur)
	transformIntakeEnd := publishStart
	transformIntakeStart := transformIntakeEnd.Add(-transformIntakeDur)
	pickupEnd := transformIntakeStart
	pickupStart := pickupEnd.Add(-pickupDur)

	// Attributes that every span in the trace carries so a search by
	// document.id, customer.id, or priority surfaces the whole flow.
	common := func() []*commonpb.KeyValue {
		return []*commonpb.KeyValue{
			stringAttr("integration.id", "document-fanout"),
			stringAttr("document.id", docID),
			stringAttr("document.type", docType),
			stringAttr("document.priority", priority),
			stringAttr("customer.id", customerID),
		}
	}

	// --- document-intake -------------------------------------------------
	// pickup_file is an outbound FTP fetch — Client kind, not Consumer.
	// Consumer is for queue/broker receipt; FTP is a network call to a
	// file server.
	pickupAttrs := append(common(),
		stringAttr("file.name", sourceName),
		stringAttr("file.path", "/outbound/"+sourceName),
		intAttr("file.size", fileSize),
		stringAttr("transfer.direction", "inbound"),
		stringAttr("transfer.protocol", "ftp"),
		stringAttr("transfer.source.host", fanoutFTPSource),
		stringAttr("transfer.source.path", "/outbound/"+sourceName),
	)
	pickupAttrs = append(pickupAttrs, ioAttrs("input", "file", "ftp", fanoutFTPSource+":/outbound/"+sourceName)...)
	pickupSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            pickupID,
		ParentSpanId:      []byte{},
		Name:              "pickup_file",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(pickupStart.UnixNano()),
		EndTimeUnixNano:   uint64(pickupEnd.UnixNano()),
		Attributes:        pickupAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	transformIntakeSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            transformIntakeID,
		ParentSpanId:      pickupID,
		Name:              "transform_file",
		Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(transformIntakeStart.UnixNano()),
		EndTimeUnixNano:   uint64(transformIntakeEnd.UnixNano()),
		Attributes: append(common(),
			stringAttr("transform.input.format", "csv"),
			stringAttr("transform.output.format", "json"),
			stringAttr("file.name", normalizedName),
		),
		Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	publishAttrs := append(common(),
		stringAttr("messaging.system", msgSystem),
		stringAttr("messaging.destination.name", fanoutTopic),
		stringAttr("messaging.operation", "publish"),
	)
	publishAttrs = append(publishAttrs, ioAttrs("output", "queue", msgSystem, fanoutTopic)...)
	publishTopicSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            publishTopicID,
		ParentSpanId:      pickupID,
		Name:              "publish_to_topic",
		Kind:              tracepb.Span_SPAN_KIND_PRODUCER,
		StartTimeUnixNano: uint64(publishStart.UnixNano()),
		EndTimeUnixNano:   uint64(publishEnd.UnixNano()),
		Attributes:        publishAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	// --- archive-writer --------------------------------------------------
	archiveConsumeStart := consumerStartAnchor
	archiveConsumeEnd := archiveConsumeStart.Add(consumeDur)
	archiveWriteStart := archiveConsumeEnd
	archiveWriteEnd := archiveWriteStart.Add(archiveWriteDur)

	archiveConsumeAttrs := append(common(),
		stringAttr("messaging.system", msgSystem),
		stringAttr("messaging.destination.name", fanoutTopic),
		stringAttr("messaging.operation", "consume"),
	)
	archiveConsumeAttrs = append(archiveConsumeAttrs, ioAttrs("input", "queue", msgSystem, fanoutTopic)...)
	archiveConsumeSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            archiveConsumeID,
		ParentSpanId:      publishTopicID,
		Name:              "consume_document",
		Kind:              tracepb.Span_SPAN_KIND_CONSUMER,
		StartTimeUnixNano: uint64(archiveConsumeStart.UnixNano()),
		EndTimeUnixNano:   uint64(archiveConsumeEnd.UnixNano()),
		Attributes:        archiveConsumeAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	// SMB write to a remote share is an outbound network call — Client
	// kind, not Producer. (Producer is the messaging-broker role.)
	archiveWriteAttrs := append(common(),
		stringAttr("file.name", normalizedName),
		stringAttr("file.path", fanoutArchiveDir+"/"+normalizedName),
		intAttr("file.size", fileSize),
		stringAttr("transfer.direction", "outbound"),
		stringAttr("transfer.protocol", "smb"),
		stringAttr("transfer.destination.host", fanoutArchiveHost),
		stringAttr("transfer.destination.path", fanoutArchiveDir+"/"+normalizedName),
	)
	archiveWriteAttrs = append(archiveWriteAttrs, ioAttrs("output", "file", "smb", fanoutArchiveHost+":"+fanoutArchiveDir+"/"+normalizedName)...)
	archiveWriteSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            archiveWriteID,
		ParentSpanId:      archiveConsumeID,
		Name:              "write_archive_file",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(archiveWriteStart.UnixNano()),
		EndTimeUnixNano:   uint64(archiveWriteEnd.UnixNano()),
		Attributes:        archiveWriteAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	// --- kafka-bridge ----------------------------------------------------
	kafkaConsumeStart := consumerStartAnchor
	kafkaConsumeEnd := kafkaConsumeStart.Add(consumeDur)
	kafkaTransformStart := kafkaConsumeEnd
	kafkaTransformEnd := kafkaTransformStart.Add(kafkaTransformDur)
	kafkaPublishStart := kafkaTransformEnd
	kafkaPublishEnd := kafkaPublishStart.Add(kafkaPublishDur)

	kafkaConsumeAttrs := append(common(),
		stringAttr("messaging.system", msgSystem),
		stringAttr("messaging.destination.name", fanoutTopic),
		stringAttr("messaging.operation", "consume"),
	)
	kafkaConsumeAttrs = append(kafkaConsumeAttrs, ioAttrs("input", "queue", msgSystem, fanoutTopic)...)
	kafkaConsumeSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            kafkaConsumeID,
		ParentSpanId:      publishTopicID,
		Name:              "consume_document",
		Kind:              tracepb.Span_SPAN_KIND_CONSUMER,
		StartTimeUnixNano: uint64(kafkaConsumeStart.UnixNano()),
		EndTimeUnixNano:   uint64(kafkaConsumeEnd.UnixNano()),
		Attributes:        kafkaConsumeAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	kafkaTransformSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            kafkaTransformID,
		ParentSpanId:      kafkaConsumeID,
		Name:              "transform_for_kafka",
		Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(kafkaTransformStart.UnixNano()),
		EndTimeUnixNano:   uint64(kafkaTransformEnd.UnixNano()),
		Attributes: append(common(),
			stringAttr("transform.input.format", "json"),
			stringAttr("transform.output.format", "avro"),
		),
		Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	kafkaPublishAttrs := append(common(),
		stringAttr("messaging.system", "kafka"),
		stringAttr("messaging.destination.name", fanoutKafkaTopic),
		stringAttr("messaging.operation", "publish"),
		stringAttr("messaging.destination.partition.id", fmt.Sprintf("%d", rng.Intn(6))),
	)
	// Kafka is a stream, not a queue — drives the stream-output facet.
	kafkaPublishAttrs = append(kafkaPublishAttrs, ioAttrs("output", "stream", "kafka", fanoutKafkaTopic)...)
	kafkaPublishSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            kafkaPublishID,
		ParentSpanId:      kafkaConsumeID,
		Name:              "publish_to_kafka",
		Kind:              tracepb.Span_SPAN_KIND_PRODUCER,
		StartTimeUnixNano: uint64(kafkaPublishStart.UnixNano()),
		EndTimeUnixNano:   uint64(kafkaPublishEnd.UnixNano()),
		Attributes:        kafkaPublishAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	// --- analytics-processor (downstream of kafka-bridge) ----------------
	analyticsConsumeStart := kafkaPublishEnd.Add(kafkaToAnalyticsQueueLatency)
	analyticsConsumeEnd := analyticsConsumeStart.Add(analyticsConsumeDur)
	analyticsEnrichStart := analyticsConsumeEnd
	analyticsEnrichEnd := analyticsEnrichStart.Add(analyticsEnrichDur)
	analyticsWarehouseStart := analyticsEnrichEnd
	analyticsWarehouseEnd := analyticsWarehouseStart.Add(analyticsWarehouseDur)

	analyticsConsumeAttrs := append(common(),
		stringAttr("messaging.system", "kafka"),
		stringAttr("messaging.destination.name", fanoutKafkaTopic),
		stringAttr("messaging.operation", "consume"),
		stringAttr("messaging.kafka.consumer.group", "analytics"),
		stringAttr("messaging.consumer.group.name", "analytics"),
		stringAttr("messaging.destination.partition.id", fmt.Sprintf("%d", rng.Intn(6))),
	)
	// Kafka is a stream, not a queue — drives the stream-input facet.
	analyticsConsumeAttrs = append(analyticsConsumeAttrs, ioAttrs("input", "stream", "kafka", fanoutKafkaTopic)...)
	analyticsConsumeSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            analyticsConsumeID,
		ParentSpanId:      kafkaPublishID,
		Name:              "consume_normalized_document",
		Kind:              tracepb.Span_SPAN_KIND_CONSUMER,
		StartTimeUnixNano: uint64(analyticsConsumeStart.UnixNano()),
		EndTimeUnixNano:   uint64(analyticsConsumeEnd.UnixNano()),
		Attributes:        analyticsConsumeAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	analyticsEnrichSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            analyticsEnrichID,
		ParentSpanId:      analyticsConsumeID,
		Name:              "enrich_document",
		Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(analyticsEnrichStart.UnixNano()),
		EndTimeUnixNano:   uint64(analyticsEnrichEnd.UnixNano()),
		Attributes: append(common(),
			stringAttr("enrichment.lookup", "customer-segments"),
		),
		Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	warehouseAttrs := append(common(),
		stringAttr("db.system", "snowflake"),
		stringAttr("db.name", "analytics"),
		stringAttr("db.operation", "MERGE"),
		stringAttr("db.sql.table", "documents"),
		stringAttr("net.peer.name", "warehouse.example.internal"),
	)
	warehouseAttrs = append(warehouseAttrs, ioAttrs("output", "db", "snowflake", "warehouse.example.internal/analytics.documents")...)
	analyticsWarehouseSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            analyticsWarehouseID,
		ParentSpanId:      analyticsConsumeID,
		Name:              "upsert_warehouse_record",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(analyticsWarehouseStart.UnixNano()),
		EndTimeUnixNano:   uint64(analyticsWarehouseEnd.UnixNano()),
		Attributes:        warehouseAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	// --- notification-dispatcher ----------------------------------------
	notifyConsumeStart := consumerStartAnchor
	notifyConsumeEnd := notifyConsumeStart.Add(consumeDur)
	notifyEvalStart := notifyConsumeEnd
	notifyEvalEnd := notifyEvalStart.Add(notifyEvalDur)

	notifyConsumeAttrs := append(common(),
		stringAttr("messaging.system", msgSystem),
		stringAttr("messaging.destination.name", fanoutTopic),
		stringAttr("messaging.operation", "consume"),
	)
	notifyConsumeAttrs = append(notifyConsumeAttrs, ioAttrs("input", "queue", msgSystem, fanoutTopic)...)
	notifyConsumeSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            notifyConsumeID,
		ParentSpanId:      publishTopicID,
		Name:              "consume_document",
		Kind:              tracepb.Span_SPAN_KIND_CONSUMER,
		StartTimeUnixNano: uint64(notifyConsumeStart.UnixNano()),
		EndTimeUnixNano:   uint64(notifyConsumeEnd.UnixNano()),
		Attributes:        notifyConsumeAttrs,
		Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	notifyEvalSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            notifyEvalID,
		ParentSpanId:      notifyConsumeID,
		Name:              "evaluate_notification_rule",
		Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(notifyEvalStart.UnixNano()),
		EndTimeUnixNano:   uint64(notifyEvalEnd.UnixNano()),
		Attributes: append(common(),
			stringAttr("notification.rule", "document.priority == \"high\""),
			boolAttr("notification.rule.matched", matched),
		),
		Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}

	notifySpans := []*tracepb.Span{notifyConsumeSpan, notifyEvalSpan}
	// email-service spans only exist when the rule matched and the
	// dispatcher actually called the service.
	var emailServiceSpans []*tracepb.Span

	if matched {
		// Client span on the dispatcher: HTTP call to email-service.
		callEmailStart := notifyEvalEnd
		callEmailEnd := callEmailStart.Add(callEmailServiceDur)
		callEmailID := randomID(8)
		callEmailAttrs := append(common(),
			stringAttr("http.method", "POST"),
			stringAttr("http.url", "https://email-service.example.internal/send"),
			stringAttr("http.route", "/send"),
			intAttr("http.status_code", 202),
			stringAttr("net.peer.name", "email-service.example.internal"),
			stringAttr("peer.service", "email-service"),
			boolAttr("notification.rule.matched", true),
		)
		callEmailAttrs = append(callEmailAttrs, ioAttrs("output", "http", "https", "email-service.example.internal/send")...)
		callEmailSpan := &tracepb.Span{
			TraceId:           traceID,
			SpanId:            callEmailID,
			ParentSpanId:      notifyConsumeID,
			Name:              "POST /send",
			Kind:              tracepb.Span_SPAN_KIND_CLIENT,
			StartTimeUnixNano: uint64(callEmailStart.UnixNano()),
			EndTimeUnixNano:   uint64(callEmailEnd.UnixNano()),
			Attributes:        callEmailAttrs,
			Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
		}
		notifySpans = append(notifySpans, callEmailSpan)

		// --- email-service ---------------------------------------------
		// Server span sits inside the Client call, with a small network
		// pad on each side. render_template then send_email_smtp run
		// sequentially inside the Server span.
		halfClientOverhead := emailClientOverhead / 2
		emailServerStart := callEmailStart.Add(halfClientOverhead)
		emailServerEnd := emailServerStart.Add(emailServerDur)
		halfServerPad := emailServerPad / 2
		renderStart := emailServerStart.Add(halfServerPad)
		renderEnd := renderStart.Add(renderTemplateDur)
		smtpStart := renderEnd
		smtpEnd := smtpStart.Add(sendEmailSMTPDur)

		emailServerID := randomID(8)
		emailRenderID := randomID(8)
		emailSMTPID := randomID(8)

		emailServerAttrs := append(common(),
			stringAttr("http.method", "POST"),
			stringAttr("http.route", "/send"),
			intAttr("http.status_code", 202),
			stringAttr("email.template", "high-priority-document"),
			stringAttr("email.to", fanoutEmailRecipient),
			stringAttr("email.subject", fmt.Sprintf("High-priority %s received: %s", docType, docID)),
		)
		emailServerAttrs = append(emailServerAttrs, ioAttrs("input", "http", "https", "/send")...)
		emailServerSpan := &tracepb.Span{
			TraceId:           traceID,
			SpanId:            emailServerID,
			ParentSpanId:      callEmailID,
			Name:              "POST /send",
			Kind:              tracepb.Span_SPAN_KIND_SERVER,
			StartTimeUnixNano: uint64(emailServerStart.UnixNano()),
			EndTimeUnixNano:   uint64(emailServerEnd.UnixNano()),
			Attributes:        emailServerAttrs,
			Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
		}

		emailRenderSpan := &tracepb.Span{
			TraceId:           traceID,
			SpanId:            emailRenderID,
			ParentSpanId:      emailServerID,
			Name:              "render_template",
			Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
			StartTimeUnixNano: uint64(renderStart.UnixNano()),
			EndTimeUnixNano:   uint64(renderEnd.UnixNano()),
			Attributes: append(common(),
				stringAttr("email.template", "high-priority-document"),
				stringAttr("email.template.engine", "mustache"),
			),
			Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
		}

		smtpAttrs := append(common(),
			stringAttr("email.system", "smtp"),
			stringAttr("net.peer.name", fanoutSMTPHost),
			intAttr("net.peer.port", 587),
			stringAttr("email.to", fanoutEmailRecipient),
			stringAttr("email.from", "noreply@example.com"),
			stringAttr("email.subject", fmt.Sprintf("High-priority %s received: %s", docType, docID)),
		)
		smtpAttrs = append(smtpAttrs, ioAttrs("output", "email", "smtp", fanoutSMTPHost+":587")...)
		emailSMTPSpan := &tracepb.Span{
			TraceId:           traceID,
			SpanId:            emailSMTPID,
			ParentSpanId:      emailServerID,
			Name:              "send_email_smtp",
			Kind:              tracepb.Span_SPAN_KIND_CLIENT,
			StartTimeUnixNano: uint64(smtpStart.UnixNano()),
			EndTimeUnixNano:   uint64(smtpEnd.UnixNano()),
			Attributes:        smtpAttrs,
			Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
		}

		emailServiceSpans = []*tracepb.Span{emailServerSpan, emailRenderSpan, emailSMTPSpan}
	}

	out := []serviceSpans{
		{
			serviceName:      "document-intake",
			serviceNamespace: "production",
			spans:            []*tracepb.Span{pickupSpan, transformIntakeSpan, publishTopicSpan},
		},
		{
			serviceName:      "archive-writer",
			serviceNamespace: "production",
			spans:            []*tracepb.Span{archiveConsumeSpan, archiveWriteSpan},
		},
		{
			serviceName:      "kafka-bridge",
			serviceNamespace: "production",
			spans:            []*tracepb.Span{kafkaConsumeSpan, kafkaTransformSpan, kafkaPublishSpan},
		},
		{
			serviceName:      "analytics-processor",
			serviceNamespace: "production",
			spans:            []*tracepb.Span{analyticsConsumeSpan, analyticsEnrichSpan, analyticsWarehouseSpan},
		},
		{
			serviceName:      "notification-dispatcher",
			serviceNamespace: "production",
			spans:            notifySpans,
		},
	}
	if len(emailServiceSpans) > 0 {
		out = append(out, serviceSpans{
			serviceName:      "email-service",
			serviceNamespace: "production",
			spans:            emailServiceSpans,
		})
	}
	return out
}

func healthyWorkerSpan(rng *mrand.Rand, svc serviceSpec) []*tracepb.Span {
	now := time.Now().UTC()
	durationMs := 1 + rng.Intn(40) // 1–40 ms; these are quick local writes
	start := now.Add(-time.Duration(durationMs) * time.Millisecond)

	return []*tracepb.Span{{
		TraceId:           randomID(16),
		SpanId:            randomID(8),
		ParentSpanId:      []byte{},
		Name:              svc.spanNames[rng.Intn(len(svc.spanNames))],
		Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(start.UnixNano()),
		EndTimeUnixNano:   uint64(now.UnixNano()),
		Attributes: []*commonpb.KeyValue{
			stringAttr("audit.action", auditActions[rng.Intn(len(auditActions))]),
			stringAttr("audit.resource.type", auditResources[rng.Intn(len(auditResources))]),
			stringAttr("audit.actor.id", fmt.Sprintf("user_%05d", rng.Intn(1000))),
		},
		Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}}
}

// setIngestKey attaches the org ingest key from SLUICIO_INGEST_KEY as a
// Bearer token. Empty env = no header — fine for local dev where
// INGEST_ALLOW_ANONYMOUS is set, required for any cell with ingest auth
// on (the public demo cell must never allow anonymous ingest).
func setIngestKey(req *http.Request) {
	if k := strings.TrimSpace(os.Getenv("SLUICIO_INGEST_KEY")); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
}
