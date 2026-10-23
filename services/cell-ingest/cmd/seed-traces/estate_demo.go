// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The demo estate (-estate demo): what the public demo cell runs. Two
// integrations, small enough to understand at a glance, where the full
// estate above models everything the product has ever needed seeded.
//
//	Order sync (1 to 1)
//	  webshop-orders ──orders.created──▶ erp-order-sync ──HTTP──▶ ERP
//
//	Invoice distribution (A to B to C and D)
//	  invoice-intake ──invoices.received──▶ invoice-router
//	                                          ├──invoices.erp────▶ erp-invoice-adapter ──HTTP──▶ ERP
//	                                          └──invoices.archive─▶ invoice-archiver ──▶ blob storage
//
// One trace per message, every service in it, parent-child intact. Most
// traffic is production and some is test, so the environment picker has
// something to pick (#40); a whole trace runs in one environment.
//
// Failures are seeded where each flow would really fail, each with an
// exception event (#42):
//   - Order sync: the ERP answers 503 now and then.
//   - Invoice distribution: a malformed invoice fails at the router, and
//     neither branch receives it; separately the ERP rejects an invoice,
//     which fails the ERP branch while the archive branch still succeeds.

package main

import (
	"fmt"
	mrand "math/rand"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// demoEstateServices are the six services, for the logs and metrics.
var demoEstateServices = []serviceSpec{
	{name: "webshop-orders", namespace: "production", environment: "production"},
	{name: "erp-order-sync", namespace: "production", environment: "production"},
	{name: "invoice-intake", namespace: "production", environment: "production"},
	{name: "invoice-router", namespace: "production", environment: "production"},
	{name: "erp-invoice-adapter", namespace: "production", environment: "production"},
	{name: "invoice-archiver", namespace: "production", environment: "production"},
}

var demoEstateScenarios = []scenarioFactory{
	orderSyncScenario,
	invoiceDistributionScenario,
}

// useDemoEstate switches the seeder to the demo estate: its services and
// its two flows, and no standalone single-service spans.
func useDemoEstate() {
	demoServices = demoEstateServices
	demoScenarios = demoEstateScenarios
	standaloneSpans = false
}

// demoEnvironment picks the environment one trace runs in.
func demoEnvironment(rng *mrand.Rand) string {
	if rng.Float64() < 0.15 {
		return "test"
	}
	return "production"
}

// spanAt builds one span of a demo trace.
func spanAt(traceID, id, parent []byte, name string, kind tracepb.Span_SpanKind, start, end time.Time, attrs []*commonpb.KeyValue) *tracepb.Span {
	if parent == nil {
		parent = []byte{}
	}
	return &tracepb.Span{
		TraceId: traceID, SpanId: id, ParentSpanId: parent,
		Name: name, Kind: kind,
		StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(end.UnixNano()),
		Attributes: attrs,
		Status:     &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
	}
}

// fail marks a span failed, with the exception that failed it.
func fail(s *tracepb.Span, at time.Time, excType, msg, stack string) {
	s.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: msg}
	s.Events = append(s.Events, &tracepb.Span_Event{
		TimeUnixNano: uint64(at.UnixNano()),
		Name:         "exception",
		Attributes: []*commonpb.KeyValue{
			stringAttr("exception.type", excType),
			stringAttr("exception.message", msg),
			stringAttr("exception.stacktrace", stack),
		},
	})
}

func ms(rng *mrand.Rand, lo, spread int) time.Duration {
	return time.Duration(lo+rng.Intn(spread)) * time.Millisecond
}

// orderSyncScenario: an order placed in the webshop is published and
// synced into the ERP by one consumer.
func orderSyncScenario(rng *mrand.Rand) []serviceSpans {
	env := demoEnvironment(rng)
	traceID := randomID(16)
	orderID := fmt.Sprintf("ORD-%06d", rng.Intn(999999))
	receiveID, publishID, consumeID := randomID(8), randomID(8), randomID(8)

	// Laid out backwards from now so the spans nest in time.
	callEnd := time.Now().UTC()
	callStart := callEnd.Add(-ms(rng, 60, 240))
	consumeEnd := callEnd.Add(ms(rng, 1, 4))
	consumeStart := callStart.Add(-ms(rng, 2, 8))
	publishEnd := consumeStart.Add(-ms(rng, 5, 60))
	publishStart := publishEnd.Add(-ms(rng, 2, 10))
	receiveEnd := publishEnd.Add(ms(rng, 1, 3))
	receiveStart := publishStart.Add(-ms(rng, 10, 40))

	receive := spanAt(traceID, receiveID, nil, "POST /orders", tracepb.Span_SPAN_KIND_SERVER, receiveStart, receiveEnd,
		append([]*commonpb.KeyValue{
			stringAttr("http.request.method", "POST"),
			stringAttr("http.route", "/orders"),
			intAttr("http.response.status_code", 202),
			stringAttr("order.id", orderID),
		}, ioAttrs("input", "http", "https", "shop.example.com/orders")...))
	publish := spanAt(traceID, publishID, receiveID, "orders.created publish", tracepb.Span_SPAN_KIND_PRODUCER, publishStart, publishEnd,
		append([]*commonpb.KeyValue{
			stringAttr("messaging.system", "rabbitmq"),
			stringAttr("messaging.destination.name", "orders.created"),
			stringAttr("messaging.operation.type", "publish"),
			stringAttr("order.id", orderID),
		}, ioAttrs("output", "queue", "rabbitmq", "orders.created")...))
	consume := spanAt(traceID, consumeID, publishID, "orders.created process", tracepb.Span_SPAN_KIND_CONSUMER, consumeStart, consumeEnd,
		append([]*commonpb.KeyValue{
			stringAttr("messaging.system", "rabbitmq"),
			stringAttr("messaging.destination.name", "orders.created"),
			stringAttr("messaging.operation.type", "process"),
			stringAttr("order.id", orderID),
		}, ioAttrs("input", "queue", "rabbitmq", "orders.created")...))
	status := int64(201)
	call := spanAt(traceID, randomID(8), consumeID, "POST /api/salesorders", tracepb.Span_SPAN_KIND_CLIENT, callStart, callEnd,
		append([]*commonpb.KeyValue{
			stringAttr("http.request.method", "POST"),
			stringAttr("url.full", "https://erp.example.com/api/salesorders"),
			stringAttr("server.address", "erp.example.com"),
			stringAttr("order.id", orderID),
		}, ioAttrs("output", "http", "https", "erp.example.com/api/salesorders")...))
	if rng.Float64() < 0.04 {
		status = 503
		msg := fmt.Sprintf("ERP returned 503 Service Unavailable for order %s", orderID)
		fail(call, callEnd, "ErpUnavailableException", msg,
			"ErpUnavailableException: "+msg+"\n   at ErpOrderSync.ErpClient.CreateSalesOrder(Order order) in /src/ErpClient.cs:line 57\n   at ErpOrderSync.OrderConsumer.Handle(OrderCreated message) in /src/OrderConsumer.cs:line 31")
		consume.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: msg}
	}
	call.Attributes = append(call.Attributes, intAttr("http.response.status_code", status))

	return []serviceSpans{
		{serviceName: "webshop-orders", serviceNamespace: "production", environment: env, spans: []*tracepb.Span{receive, publish}},
		{serviceName: "erp-order-sync", serviceNamespace: "production", environment: env, spans: []*tracepb.Span{consume, call}},
	}
}

// invoiceDistributionScenario: a supplier invoice arrives as a file, is
// routed, and fans out to the ERP and to the archive.
func invoiceDistributionScenario(rng *mrand.Rand) []serviceSpans {
	env := demoEnvironment(rng)
	traceID := randomID(16)
	invoiceID := fmt.Sprintf("INV-%06d", rng.Intn(999999))
	fileName := fmt.Sprintf("%s.xml", invoiceID)
	now := time.Now().UTC()

	// A: pick up the file, publish it.
	pickupID, publishID := randomID(8), randomID(8)
	pickupStart := now.Add(-ms(rng, 900, 600))
	publishStart := pickupStart.Add(ms(rng, 40, 120))
	publishEnd := publishStart.Add(ms(rng, 2, 10))
	pickupEnd := publishEnd.Add(ms(rng, 1, 3))
	pickup := spanAt(traceID, pickupID, nil, "pickup invoice", tracepb.Span_SPAN_KIND_INTERNAL, pickupStart, pickupEnd,
		append([]*commonpb.KeyValue{
			stringAttr("file.name", fileName),
			intAttr("file.size", int64(4096+rng.Intn(60000))),
			stringAttr("invoice.id", invoiceID),
		}, ioAttrs("input", "file", "sftp", "sftp.supplier.example.com/outbound/"+fileName)...))
	publishA := spanAt(traceID, publishID, pickupID, "invoices.received publish", tracepb.Span_SPAN_KIND_PRODUCER, publishStart, publishEnd,
		append([]*commonpb.KeyValue{
			stringAttr("messaging.system", "kafka"),
			stringAttr("messaging.destination.name", "invoices.received"),
			stringAttr("invoice.id", invoiceID),
		}, ioAttrs("output", "queue", "kafka", "invoices.received")...))
	intake := serviceSpans{serviceName: "invoice-intake", serviceNamespace: "production", environment: env, spans: []*tracepb.Span{pickup, publishA}}

	// B: consume, validate and transform, publish to both branches.
	routeID := randomID(8)
	routeStart := publishEnd.Add(ms(rng, 10, 80))
	transformStart := routeStart.Add(ms(rng, 2, 5))
	transformEnd := transformStart.Add(ms(rng, 20, 80))
	routeConsume := spanAt(traceID, routeID, publishID, "invoices.received process", tracepb.Span_SPAN_KIND_CONSUMER, routeStart, transformEnd,
		append([]*commonpb.KeyValue{
			stringAttr("messaging.system", "kafka"),
			stringAttr("messaging.destination.name", "invoices.received"),
			stringAttr("invoice.id", invoiceID),
		}, ioAttrs("input", "queue", "kafka", "invoices.received")...))
	transform := spanAt(traceID, randomID(8), routeID, "validate and transform", tracepb.Span_SPAN_KIND_INTERNAL, transformStart, transformEnd,
		[]*commonpb.KeyValue{stringAttr("invoice.id", invoiceID), stringAttr("invoice.format", "UBL 2.1")})
	router := serviceSpans{serviceName: "invoice-router", serviceNamespace: "production", environment: env, spans: []*tracepb.Span{routeConsume, transform}}

	// A malformed invoice stops at the router: no branch receives it.
	if rng.Float64() < 0.03 {
		msg := fmt.Sprintf("Invoice %s is not valid UBL: element 'cbc:IssueDate' is missing", invoiceID)
		fail(transform, transformEnd, "InvoiceValidationException", msg,
			"InvoiceValidationException: "+msg+"\n   at InvoiceRouter.UblValidator.Validate(XDocument doc) in /src/UblValidator.cs:line 112\n   at InvoiceRouter.RouteHandler.Handle(InvoiceReceived message) in /src/RouteHandler.cs:line 44")
		routeConsume.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: msg}
		return []serviceSpans{intake, router}
	}

	branch := func(queue string, at time.Time) (*tracepb.Span, []byte) {
		id := randomID(8)
		end := at.Add(ms(rng, 2, 8))
		s := spanAt(traceID, id, routeID, queue+" publish", tracepb.Span_SPAN_KIND_PRODUCER, at, end,
			append([]*commonpb.KeyValue{
				stringAttr("messaging.system", "kafka"),
				stringAttr("messaging.destination.name", queue),
				stringAttr("invoice.id", invoiceID),
			}, ioAttrs("output", "queue", "kafka", queue)...))
		return s, id
	}
	toERP, toERPID := branch("invoices.erp", transformEnd)
	toArchive, toArchiveID := branch("invoices.archive", transformEnd.Add(ms(rng, 1, 3)))
	routeConsume.EndTimeUnixNano = toArchive.EndTimeUnixNano
	router.spans = append(router.spans, toERP, toArchive)

	// C: post the invoice to the ERP.
	cConsumeID := randomID(8)
	cStart := time.Unix(0, int64(toERP.EndTimeUnixNano)).Add(ms(rng, 10, 60))
	postStart := cStart.Add(ms(rng, 2, 6))
	postEnd := postStart.Add(ms(rng, 80, 400))
	cConsume := spanAt(traceID, cConsumeID, toERPID, "invoices.erp process", tracepb.Span_SPAN_KIND_CONSUMER, cStart, postEnd.Add(ms(rng, 1, 3)),
		append([]*commonpb.KeyValue{
			stringAttr("messaging.system", "kafka"),
			stringAttr("messaging.destination.name", "invoices.erp"),
			stringAttr("invoice.id", invoiceID),
		}, ioAttrs("input", "queue", "kafka", "invoices.erp")...))
	post := spanAt(traceID, randomID(8), cConsumeID, "POST /api/supplierinvoices", tracepb.Span_SPAN_KIND_CLIENT, postStart, postEnd,
		append([]*commonpb.KeyValue{
			stringAttr("http.request.method", "POST"),
			stringAttr("url.full", "https://erp.example.com/api/supplierinvoices"),
			stringAttr("server.address", "erp.example.com"),
			stringAttr("invoice.id", invoiceID),
		}, ioAttrs("output", "http", "https", "erp.example.com/api/supplierinvoices")...))
	status := int64(201)
	if rng.Float64() < 0.05 {
		status = 422
		msg := fmt.Sprintf("ERP rejected invoice %s: supplier is not registered", invoiceID)
		fail(post, postEnd, "ErpRejectedException", msg,
			"ErpRejectedException: "+msg+"\n   at ErpInvoiceAdapter.ErpClient.PostSupplierInvoice(Invoice invoice) in /src/ErpClient.cs:line 73\n   at ErpInvoiceAdapter.InvoiceConsumer.Handle(InvoiceForErp message) in /src/InvoiceConsumer.cs:line 28")
		cConsume.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: msg}
	}
	post.Attributes = append(post.Attributes, intAttr("http.response.status_code", status))

	// D: write the invoice to the archive.
	dConsumeID := randomID(8)
	dStart := time.Unix(0, int64(toArchive.EndTimeUnixNano)).Add(ms(rng, 10, 60))
	writeStart := dStart.Add(ms(rng, 2, 6))
	writeEnd := writeStart.Add(ms(rng, 15, 60))
	dConsume := spanAt(traceID, dConsumeID, toArchiveID, "invoices.archive process", tracepb.Span_SPAN_KIND_CONSUMER, dStart, writeEnd.Add(ms(rng, 1, 3)),
		append([]*commonpb.KeyValue{
			stringAttr("messaging.system", "kafka"),
			stringAttr("messaging.destination.name", "invoices.archive"),
			stringAttr("invoice.id", invoiceID),
		}, ioAttrs("input", "queue", "kafka", "invoices.archive")...))
	write := spanAt(traceID, randomID(8), dConsumeID, "write archive blob", tracepb.Span_SPAN_KIND_CLIENT, writeStart, writeEnd,
		append([]*commonpb.KeyValue{
			stringAttr("file.name", fileName),
			stringAttr("invoice.id", invoiceID),
		}, ioAttrs("output", "file", "azure-blob", "archive/invoices/"+fileName)...))

	return []serviceSpans{
		intake,
		router,
		{serviceName: "erp-invoice-adapter", serviceNamespace: "production", environment: env, spans: []*tracepb.Span{cConsume, post}},
		{serviceName: "invoice-archiver", serviceNamespace: "production", environment: env, spans: []*tracepb.Span{dConsume, write}},
	}
}
