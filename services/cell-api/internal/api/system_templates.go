// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Built-in monitoring templates. A template is a per-kind bundle of metric
// health checks; "apply" creates them on a service via the normal alert-rule
// engine, so they drive the service's health, show in the health-checks list,
// and can be tuned like any check. The optional channel_ids route notification
// channels onto the created checks so they actually alert.
//
// One catalog covers two cases: system kinds (brokers — RabbitMQ, Artemis —
// also flagged as "systems") and service-type kinds (OTel Collector, .NET
// service). DetectPrefixes let us auto-suggest a kind from a service's emitted
// metrics. Health-check evaluation aggregates the raw metric value (no
// counter-rate), so templates use gauge/UpDownCounter metrics (peak via max),
// never cumulative counters.
//
// Grounding: RabbitMQ (OTel rabbitmqreceiver + Prometheus plugin) and the .NET
// service template are grounded in metrics this stack emits. Artemis and OTel
// Collector are best-effort against standard exporter names — tune after apply.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
)

// systemCheck is one health check in a template. Signal "" / "metric" builds a
// metric rule (Metric/Agg/Op/Threshold/Attrs); Signal "log" builds a log rule
// (MinSeverity/BodyContains/LogThreshold) for failure modes metrics can't see
// — e.g. a collector logging "Exporting failed" without moving a watched
// counter.
type systemCheck struct {
	Name        string
	Description string
	Signal      string // "" | "metric" | "log"
	// metric-signal fields
	Metric    string
	Agg       alerting.Aggregation
	Op        alerting.Operator
	Threshold float64
	Attrs     []alerting.AttrFilter
	// SplitBy evaluates per distinct value of this metric attribute and
	// the firing enumerates each breaching value ("DLQ depth > 0 split
	// by queue" names WHICH queue backed up).
	SplitBy string
	// FireOnNoData makes the ABSENCE of the metric a firing condition,
	// which is the only way to write a heartbeat: a process that dies
	// stops emitting, so a threshold on what it emits has nothing left to
	// breach. Guarded in the engine by "has this rule ever seen a
	// reading", so it cannot fire on a metric that never existed.
	FireOnNoData bool
	// log-signal fields
	MinSeverity  int32  // OTLP severity floor (error≈17); 0 = any
	BodyContains string // case-insensitive substring; "" = no text filter
	LogThreshold int    // matches over the window that fire; default 1
	// trace-signal fields. Signal "trace_error" fires on >= TraceThreshold
	// failed traces (Attrs narrow which error spans count); "trace_latency"
	// fires when p95 span latency >= ThresholdMs; "trace_volume" fires when
	// the distinct trace count drops BELOW TraceThreshold (dead-man).
	TraceThreshold int // trace_error / trace_volume; default 1
	ThresholdMs    int // trace_latency
	WindowSeconds  int // trailing window for trace checks; default 300
	// shared
	Severity alerting.Severity
	Unit     string
	Display  bool // surface latest reading as a value tile (metric checks only)
}

// monitoringTemplate is a per-kind starter bundle. System=true marks the broker
// kinds that also appear in the Systems view. DetectPrefixes are metric-name
// prefixes that auto-identify the kind from emitted telemetry.
type monitoringTemplate struct {
	Kind           string
	Label          string
	System         bool
	DetectPrefixes []string
	// DetectSpanAttrs identifies a kind from the SPAN ATTRIBUTE keys a
	// service emits, for runtimes that have no metrics of their own to
	// be recognised by.
	//
	// Node-RED is the case that forced it: its OpenTelemetry integration
	// is a tracing integration, and the only metrics it exports are HTTP
	// request metrics from http in nodes, under generic semantic-
	// convention names that every HTTP service emits. There is no
	// Node-RED metric name to match, and there never will be - but every
	// span it emits carries node_red.* attributes.
	//
	// Matched the same way as DetectPrefixes: prefix, one hit is enough.
	// A template may declare either or both; both are ORed.
	DetectSpanAttrs []string
	Checks          []systemCheck
	// Runbook is the type-level "what to do about this kind of thing".
	// Empty on the built-ins for now — they're code-defined, and writing
	// generic advice that reads as authoritative would be worse than
	// none. Custom and overridden types carry the org's own.
	Runbook string
}

var monitoringTemplates = []monitoringTemplate{
	{
		Kind: "rabbitmq", Label: "RabbitMQ", System: true,
		DetectPrefixes: []string{"rabbitmq"},
		Checks: []systemCheck{
			{Name: "RabbitMQ memory alarm", Description: "Broker hit the memory watermark — publishers are blocked.", Metric: "rabbitmq_alarms_memory_used_watermark", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityCritical},
			{Name: "RabbitMQ disk alarm", Description: "Free disk dropped below the watermark.", Metric: "rabbitmq_alarms_free_disk_space_watermark", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityCritical},
			{Name: "RabbitMQ file-descriptor alarm", Description: "File-descriptor usage hit the limit.", Metric: "rabbitmq_alarms_file_descriptor_limit", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityCritical},
			{Name: "RabbitMQ queue backlog", Description: "Ready (undelivered) messages are building up.", Metric: "rabbitmq.message.current", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 5000, Attrs: []alerting.AttrFilter{{Key: "state", Op: "eq", Value: "ready"}}, Severity: alerting.SeverityWarning, Unit: "msgs", Display: true},
			{Name: "RabbitMQ no consumers", Description: "A queue has no consumers attached.", Metric: "rabbitmq.consumer.count", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityWarning, Unit: "consumers", Display: true},
			{Name: "RabbitMQ low free disk", Description: "Node free disk is running low (< 2 GiB).", Metric: "rabbitmq.node.disk_free", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 2147483648, Severity: alerting.SeverityWarning, Unit: "bytes", Display: true},
		},
	},
	{
		// Best-effort — verify metric names against your Artemis Prometheus
		// exporter and tune thresholds after applying.
		Kind: "artemis", Label: "ActiveMQ Artemis", System: true,
		DetectPrefixes: []string{"artemis"},
		Checks: []systemCheck{
			{Name: "Artemis queue backlog", Description: "Messages building up on an address/queue.", Metric: "artemis_message_count", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 5000, Severity: alerting.SeverityWarning, Unit: "msgs", Display: true},
			{Name: "Artemis no consumers", Description: "An address/queue has no consumers.", Metric: "artemis_consumer_count", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityWarning, Unit: "consumers", Display: true},
			{Name: "Artemis address memory", Description: "Address memory usage is high.", Metric: "artemis_address_memory_usage", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning, Unit: "bytes", Display: true},
		},
	},
	{
		// Grounded in the Integrio azureservicebusreceiver's documented
		// metrics (github.com/Integrio/azureservicebusreceiver — an OTel
		// Collector receiver scraping Azure APIs; all gauges, attributes
		// queue / topic / subscription). Backlog thresholds mirror the
		// RabbitMQ defaults — tune to your traffic.
		Kind: "azure-servicebus", Label: "Azure Service Bus", System: true,
		DetectPrefixes: []string{"servicebus."},
		Checks: []systemCheck{
			{Name: "Service Bus dead-lettered messages", Description: "Messages are landing in a queue's dead-letter sub-queue — processing is failing.", Metric: "servicebus.queue.deadletter_messages", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0, SplitBy: "queue", Severity: alerting.SeverityWarning, Unit: "msgs", Display: true},
			{Name: "Service Bus dead-lettered messages (subscriptions)", Description: "Messages are landing in a topic subscription's dead-letter sub-queue.", Metric: "servicebus.topic.subscription.deadletter_messages", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0, SplitBy: "subscription", Severity: alerting.SeverityWarning, Unit: "msgs"},
			{Name: "Service Bus queue backlog", Description: "Active (undelivered) messages are building up on a queue.", Metric: "servicebus.queue.active_messages", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 5000, SplitBy: "queue", Severity: alerting.SeverityWarning, Unit: "msgs", Display: true},
			{Name: "Service Bus subscription backlog", Description: "Active messages are building up on a topic subscription.", Metric: "servicebus.topic.subscription.active_messages", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 5000, SplitBy: "subscription", Severity: alerting.SeverityWarning, Unit: "msgs"},
		},
	},
	{
		// Grounded in what krakend-otel actually emits (verified against
		// KrakenD 2.13): krakend.* histograms drive detection; the checks
		// use trace signals since the gateway's failures live in spans.
		// NOTE: KrakenD reports 5xx only as the http.response.status_code
		// attribute (span status stays OK), so the failed-trace check needs
		// the cell setting "Treat HTTP 5xx as errors" (Settings → System)
		// to see them — the check description says so.
		Kind: "krakend", Label: "KrakenD API Gateway", System: true,
		DetectPrefixes: []string{"krakend."},
		Checks: []systemCheck{
			{Name: "KrakenD 5xx responses", Description: "The gateway returned server errors. Requires the 'Treat HTTP 5xx as errors' system setting — KrakenD records 5xx only as a span attribute, not as span status.", Signal: "trace_error", TraceThreshold: 1, WindowSeconds: 300, Attrs: []alerting.AttrFilter{{Key: "http.response.status_code", Op: "gte", Value: "500"}}, Severity: alerting.SeverityWarning},
			{Name: "KrakenD response time", Description: "p95 gateway latency is high — tune the threshold to your traffic.", Signal: "trace_latency", ThresholdMs: 2000, WindowSeconds: 300, Severity: alerting.SeverityWarning, Unit: "ms"},
			{Name: "KrakenD gateway silent", Description: "No traces at all in 15 minutes — the gateway (or its telemetry pipeline) is down. Disable if this gateway legitimately idles.", Signal: "trace_volume", TraceThreshold: 1, WindowSeconds: 900, Severity: alerting.SeverityWarning},
			// Transport-level failures — a different class than 5xx: the
			// backend didn't answer at all. krakend-otel emits these
			// counters only once such events occur (verified live).
			{Name: "KrakenD backend unreachable", Description: "Requests to a backend failed at the transport level (connection refused/reset) — the backend is down or unreachable, not merely erroring.", Metric: "http.client.request.failed.count", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning, Unit: "requests", Display: true},
			{Name: "KrakenD backend timeouts", Description: "Backend calls exceeded the gateway's timeout budget — names the cause behind 500s and latency alerts.", Metric: "http.client.request.timedout.count", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning, Unit: "requests"},
		},
	},
	{
		// Best-effort — WSO2 API-M's native OTel support is TRACING only
		// ([apim.open_telemetry] in deployment.toml, OTLP remote tracer), so
		// the core checks are trace signals like KrakenD's. Whether gateway
		// faults set span status ERROR varies by version/handler — if 5xx
		// only shows as a status-code attribute, enable "Treat HTTP 5xx as
		// errors" (Settings → System). The JVM heap check needs JVM metrics
		// (OTel Java agent's stable-semconv jvm.memory.used); detection
		// prefixes cover the jmx_exporter path (org_wso2_* / wso2_*) since
		// jvm.* is too generic to claim.
		Kind: "wso2-apim", Label: "WSO2 API Manager", System: true,
		DetectPrefixes: []string{"wso2", "org_wso2"},
		Checks: []systemCheck{
			{Name: "WSO2 API-M failed API invocations", Description: "API invocations through the gateway are failing. If faults surface only as a 5xx status-code attribute (not span status), enable the 'Treat HTTP 5xx as errors' system setting.", Signal: "trace_error", TraceThreshold: 1, WindowSeconds: 300, Severity: alerting.SeverityWarning},
			{Name: "WSO2 API-M response time", Description: "p95 gateway latency is high — tune the threshold to your traffic.", Signal: "trace_latency", ThresholdMs: 2000, WindowSeconds: 300, Severity: alerting.SeverityWarning, Unit: "ms"},
			{Name: "WSO2 API-M gateway silent", Description: "No traces at all in 15 minutes — the gateway (or its telemetry pipeline) is down. Disable if this gateway legitimately idles.", Signal: "trace_volume", TraceThreshold: 1, WindowSeconds: 900, Severity: alerting.SeverityWarning},
			{Name: "WSO2 API-M error logs spiking", Description: "Error-level entries in wso2carbon.log are spiking — tune the threshold to your baseline.", Signal: alerting.SignalLog, MinSeverity: 17, LogThreshold: 10, Severity: alerting.SeverityWarning},
			{Name: "WSO2 API-M JVM heap high", Description: "JVM heap usage is high (> 1.5 GiB, assumes a 2 GiB max heap) — tune to your -Xmx. Needs JVM metrics (e.g. the OTel Java agent).", Metric: "jvm.memory.used", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 1610612736, Attrs: []alerting.AttrFilter{{Key: "jvm.memory.type", Op: "eq", Value: "heap"}}, Severity: alerting.SeverityWarning, Unit: "bytes", Display: true},
		},
	},
	{
		// Grounded in the OTel Collector kafkametrics receiver's documented
		// metrics (all gauges; attributes group/topic/partition). Lag and
		// backlog thresholds are starting points — tune to your traffic.
		Kind: "kafka", Label: "Apache Kafka", System: true,
		DetectPrefixes: []string{"kafka."},
		Checks: []systemCheck{
			{Name: "Kafka consumer lag", Description: "A consumer group is falling behind the head of the log — messages are piling up faster than they're consumed.", Metric: "kafka.consumer_group.lag_sum", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 1000, SplitBy: "group", Severity: alerting.SeverityWarning, Unit: "msgs", Display: true},
			{Name: "Kafka consumer group empty", Description: "A known consumer group has no members attached — nothing is consuming.", Metric: "kafka.consumer_group.members", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, SplitBy: "group", Severity: alerting.SeverityWarning, Unit: "consumers"},
			{Name: "Kafka partition without in-sync replicas", Description: "A partition has zero in-sync replicas — it is unavailable for reliable writes.", Metric: "kafka.partition.replicas_in_sync", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityCritical, Unit: "replicas"},
			{Name: "Kafka brokers visible", Description: "Broker count as seen by the metrics receiver — raise the threshold to your cluster size so a lost broker fires.", Metric: "kafka.brokers", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityWarning, Unit: "brokers", Display: true},
		},
	},
	{
		// Grounded in the Confluent Cloud Metrics API export endpoint
		// (api.telemetry.confluent.cloud/v2/metrics/cloud/export) scraped by
		// the collector's prometheus receiver — names arrive underscored
		// (confluent_kafka_server_*). cluster_load_percent is a 0–1 ratio.
		Kind: "confluent-kafka", Label: "Confluent Kafka", System: true,
		DetectPrefixes: []string{"confluent_kafka_", "confluent.kafka."},
		Checks: []systemCheck{
			{Name: "Confluent consumer lag", Description: "A consumer group is falling behind — lag in offsets between group members and the latest offset.", Metric: "confluent_kafka_server_consumer_lag_offsets", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 1000, SplitBy: "consumer_group_id", Severity: alerting.SeverityWarning, Unit: "offsets", Display: true},
			{Name: "Confluent cluster load high", Description: "Cluster load is above 80% — throttling and latency follow. (Dedicated clusters report this; ratio 0–1.)", Metric: "confluent_kafka_server_cluster_load_percent", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0.8, Severity: alerting.SeverityWarning, Display: true},
			{Name: "Confluent hot partition (ingress)", Description: "A partition is receiving disproportionate produce traffic — a hot key or skewed partitioner.", Metric: "confluent_kafka_server_hot_partition_ingress", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning},
		},
	},
	{
		// Best-effort against prometheus-nats-exporter's gnatsd_varz_* names
		// (the most common NATS metrics path; nats-surveyor uses nats_core_*
		// instead — adjust the metric names if you run surveyor).
		// slow_consumers is a monotonic total, so the check uses the delta.
		Kind: "nats", Label: "NATS", System: true,
		DetectPrefixes: []string{"gnatsd_", "nats_"},
		Checks: []systemCheck{
			{Name: "NATS slow consumers", Description: "The server disconnected slow consumers — subscribers can't keep up and messages to them were dropped.", Metric: "gnatsd_varz_slow_consumers", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning, Unit: "consumers", Display: true},
			{Name: "NATS no client connections", Description: "No clients are connected to the server.", Metric: "gnatsd_varz_connections", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityWarning, Unit: "connections", Display: true},
			{Name: "NATS memory high", Description: "Server resident memory is high (> 1 GiB) — tune to your deployment.", Metric: "gnatsd_varz_mem", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 1073741824, Severity: alerting.SeverityWarning, Unit: "bytes"},
		},
	},
	{
		// Best-effort — Debezium exposes these via JMX (OTel jmx receiver or
		// prometheus jmx_exporter with Debezium's documented config); exported
		// names vary by setup, so verify against what your pipeline emits.
		// Connected is 0/1 per connector; MilliSecondsBehindSource is CDC lag.
		Kind: "debezium", Label: "Debezium", System: true,
		DetectPrefixes: []string{"debezium"},
		Checks: []systemCheck{
			{Name: "Debezium disconnected", Description: "A connector lost its database connection — change capture has stopped.", Metric: "debezium_metrics_Connected", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityCritical},
			{Name: "Debezium replication lag", Description: "Change capture is running more than 60s behind the source database.", Metric: "debezium_metrics_MilliSecondsBehindSource", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 60000, Severity: alerting.SeverityWarning, Unit: "ms", Display: true},
			{Name: "Debezium queue nearly full", Description: "The connector's internal event queue is nearly out of capacity — events aren't reaching Kafka fast enough.", Metric: "debezium_metrics_QueueRemainingCapacity", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 100, Severity: alerting.SeverityWarning, Unit: "events"},
		},
	},
	{
		// Grounded in a live paperless-ngx 3.0.4: traces from OpenTelemetry
		// auto-instrumentation, metrics scraped from
		// hansmi/prometheus-paperless-exporter. Both verified end to end
		// against this cell.
		//
		// Detection needs the exporter. Auto-instrumentation alone emits only
		// process.*/system.*/flower.*, which are far too generic to claim a
		// kind from; paperless_ is unambiguous.
		//
		// Two collector-side prerequisites, or half these checks are dead:
		//   1. paperless_task_status must have its `id` label aggregated away
		//      (metricstransform, aggregate_labels [status], sum). Raw, it is
		//      one series per task id - unbounded, and not a count of
		//      anything. Do NOT use paperless_task_status_info instead: it is
		//      a label registry the exporter Set(1)s per status string it has
		//      seen, so every series reads 1 forever.
		//   2. The Celery worker must not recycle its pool children.
		//      Paperless hardcodes worker_max_tasks_per_child = 1, so every
		//      child is killed the moment its task returns - while
		//      BatchSpanProcessor still holds that task's spans. They die
		//      with the child, silently, and every trace check below goes
		//      permanently quiet while ingest looks fine. Paperless does not
		//      expose the setting, so run the worker with --pool solo (no
		//      children to recycle) until it is fixed upstream in
		//      opentelemetry-python. The metric checks are unaffected -
		//      which is why both kinds are here.
		Kind: "paperless-ngx", Label: "Paperless-ngx", System: true,
		DetectPrefixes: []string{"paperless_"},
		Checks: []systemCheck{
			// Ingest outcomes. The trace check names the failing document;
			// the metric check survives when traces don't.
			{Name: "Document ingest failed", Description: "A document failed to consume - parser error, unreadable file, or a plugin raising. The trace names the document and the stage.", Signal: "trace_error", TraceThreshold: 1, WindowSeconds: 900, Severity: alerting.SeverityWarning},
			{Name: "New ingest failures", Description: "Failed consume tasks recorded by paperless in the window. Overlaps the trace check on purpose: this one still fires if the worker's telemetry pipeline is down.", Metric: "paperless_task_status", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, Attrs: []alerting.AttrFilter{{Key: "status", Op: "eq", Value: "failure"}}, Severity: alerting.SeverityWarning, Unit: "tasks"},
			{Name: "Slow document ingest", Description: "p95 consume time is high. OCR dominates this - raise the threshold if you routinely ingest large scanned PDFs.", Signal: "trace_latency", ThresholdMs: 60000, WindowSeconds: 900, Severity: alerting.SeverityWarning, Unit: "ms"},

			// Backlog. Neither traces nor the SDK can see these: a document
			// waiting in the queue has produced no span yet.
			{Name: "Ingest backlog", Description: "Consume tasks are queued and not draining - the worker is wedged, or intake is outrunning OCR.", Metric: "paperless_task_status", Agg: alerting.AggLast, Op: alerting.OpGT, Threshold: 25, Attrs: []alerting.AttrFilter{{Key: "status", Op: "eq", Value: "pending"}}, Severity: alerting.SeverityWarning, Unit: "tasks", Display: true},
			{Name: "Documents unfiled", Description: "Documents consumed successfully but still sitting in the inbox. A business backlog, not a fault - tune to your filing habits or disable.", Metric: "paperless_statistics_documents_inbox_count", Agg: alerting.AggLast, Op: alerting.OpGT, Threshold: 100, Severity: alerting.SeverityWarning, Unit: "documents", Display: true},

			// Component liveness. 1 = healthy; the exporter reports these
			// from paperless' own status endpoint.
			{Name: "Celery worker unhealthy", Description: "Paperless reports its task worker as down - nothing will consume, and no trace will be emitted to tell you so.", Metric: "paperless_status_celery_status", Agg: alerting.AggLast, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityCritical},
			{Name: "Redis unhealthy", Description: "The broker paperless queues through is unreachable.", Metric: "paperless_status_redis_status", Agg: alerting.AggLast, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityCritical},
			{Name: "Database unhealthy", Description: "Paperless cannot reach its database.", Metric: "paperless_status_database_status", Agg: alerting.AggLast, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityCritical},
			{Name: "Unapplied migrations", Description: "The image was upgraded but migrations have not run - a half-upgraded instance.", Metric: "paperless_status_database_unapplied_migrations", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning},

			// Capacity and quality decay.
			{Name: "Low document storage", Description: "Less than 2 GiB free where documents are stored. Paperless fails ingest hard when this runs out.", Metric: "paperless_status_storage_available_bytes", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 2147483648, Severity: alerting.SeverityWarning, Unit: "bytes", Display: true},
			{Name: "Classifier stale", Description: "The document classifier has not retrained in over a week - auto-tagging quality drifts silently, with no failure anywhere.", Metric: "paperless_status_classifier_last_trained_timestamp_seconds", Agg: alerting.AggAge, Op: alerting.OpGT, Threshold: 604800, Severity: alerting.SeverityWarning, Unit: "seconds"},
		},
	},
	{
		// Grounded in the OTel Collector's k8s_cluster receiver (cluster
		// objects, from the Kubernetes API) and kubelet_stats receiver (node
		// and volume resource usage, from the kubelet). Metric names and
		// their enabled-by-default state verified against both receivers'
		// documentation.
		//
		// # One type for every distribution
		//
		// k0s, k3s, RKE2, MicroK8s, vanilla kubeadm, EKS, AKS, GKE and
		// OpenShift are all conformant Kubernetes, and every check below
		// reads the Kubernetes API or the kubelet - interfaces the
		// conformance tests guarantee. So they need no separate types, and
		// adding one per distribution would be a dozen identical templates
		// to keep in step.
		//
		// What DOES differ between them is the control plane, which is
		// exactly why nothing here touches it. etcd, apiserver and scheduler
		// metrics are absent on a managed cluster (the provider runs the
		// control plane and does not expose them) and absent again on k3s
		// backed by kine, which replaces etcd with SQLite or a SQL database.
		// A check on etcd_* would be permanently silent on most clusters
		// while looking armed, which is the worst way for a check to be
		// wrong.
		//
		// # Detection
		//
		// Only the dotted k8s.* namespace, which is what the two receivers
		// above emit. kube-state-metrics scraped through the prometheus
		// receiver arrives as kube_* with entirely different metric names,
		// so detecting it here would name the kind correctly and then apply
		// a set of checks that can never fire. An org on that path is better
		// served by a custom type carrying its own names.
		//
		// Note k8s.node.condition is disabled by default, but the receiver
		// still emits k8s.node.condition_ready: node_conditions_to_report
		// defaults to ["Ready"] and each reported condition becomes its own
		// metric. Values are 1 true, 0 false, -1 unknown, so "< 1" catches a
		// node that is NotReady and one the control plane has lost track of.
		Kind: "kubernetes", Label: "Kubernetes", System: true,
		DetectPrefixes: []string{"k8s."},
		Checks: []systemCheck{
			{Name: "Node not ready", Description: "A node is NotReady or unreachable - its pods are being evicted or are already gone. Needs the k8s_cluster receiver.", Metric: "k8s.node.condition_ready", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, SplitBy: "k8s.node.name", Severity: alerting.SeverityCritical},
			{Name: "Deployment has no available replicas", Description: "Every replica of a deployment is unavailable - that workload is down, not degraded. Raise the threshold to your replica count to catch partial loss too.", Metric: "k8s.deployment.available", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, SplitBy: "k8s.deployment.name", Severity: alerting.SeverityCritical, Unit: "replicas"},
			{Name: "Container restart loop", Description: "Containers restarted repeatedly in the window - CrashLoopBackOff, an OOM kill, or a failing probe. Counts restarts that happened, not the lifetime total.", Metric: "k8s.container.restarts", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 3, SplitBy: "k8s.container.name", Severity: alerting.SeverityWarning, Unit: "restarts", Display: true},
			{Name: "Pod failed", Description: "A pod is in Failed or Unknown phase. Phase is an enum (1 Pending, 2 Running, 3 Succeeded, 4 Failed, 5 Unknown), so the threshold is the enum value, not a count.", Metric: "k8s.pod.phase", Agg: alerting.AggMax, Op: alerting.OpGTE, Threshold: 4, SplitBy: "k8s.pod.name", Severity: alerting.SeverityWarning},
			{Name: "Job failing", Description: "A Job has failed pods. Worth watching on a cluster that runs scheduled integration work - a nightly export that dies leaves nothing else behind to notice it.", Metric: "k8s.job.failed_pods", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0, SplitBy: "k8s.job.name", Severity: alerting.SeverityWarning, Unit: "pods", Display: true},
			{Name: "DaemonSet not running anywhere", Description: "A DaemonSet is ready on no node at all. Often the log or metric agent itself, which is how a cluster goes quiet without anything reporting that it did.", Metric: "k8s.daemonset.ready_nodes", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, SplitBy: "k8s.daemonset.name", Severity: alerting.SeverityWarning, Unit: "nodes"},
			{Name: "Node disk nearly full", Description: "Less than 2 GiB free on a node filesystem - the kubelet starts evicting pods under disk pressure. Tune to your node size. Needs the kubelet_stats receiver.", Metric: "k8s.node.filesystem.available", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 2147483648, SplitBy: "k8s.node.name", Severity: alerting.SeverityWarning, Unit: "bytes", Display: true},
		},
	},
	{
		// Best-effort — nothing was emitting otelcol_* yet. Tune thresholds
		// after applying. Gauges use max (peak); dropped/failed spans use the
		// counter delta (increase).
		Kind: "otel-collector", Label: "OpenTelemetry Collector", System: false,
		DetectPrefixes: []string{"otelcol"},
		Checks: []systemCheck{
			{Name: "Collector exporter queue backlog", Description: "Export queue is filling — the collector can't ship telemetry fast enough.", Metric: "otelcol_exporter_queue_size", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 5000, Severity: alerting.SeverityWarning, Unit: "items", Display: true},
			{Name: "Collector memory high", Description: "Collector resident memory is high.", Metric: "otelcol_process_memory_rss", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 1073741824, Severity: alerting.SeverityWarning, Unit: "bytes", Display: true},
			{Name: "Collector failing to export", Description: "Spans are failing to export — telemetry is being lost.", Metric: "otelcol_exporter_send_failed_spans", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning, Unit: "spans", Display: true},
			{Name: "Collector dropping spans", Description: "The pipeline is dropping spans.", Metric: "otelcol_processor_dropped_spans", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning, Unit: "spans"},
			{Name: "Collector enqueue failing", Description: "Items couldn't be enqueued for export (queue full / backpressure).", Metric: "otelcol_exporter_enqueue_failed_spans", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning, Unit: "spans"},
			{Name: "Collector refusing spans", Description: "A receiver is refusing incoming spans (overload / bad data).", Metric: "otelcol_receiver_refused_spans", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning, Unit: "spans"},
			// Log check — catches export/config/permission failures the collector
			// logs but that don't move a watched counter.
			{Name: "Collector errors logged", Description: "The collector logged error-level messages (config, auth, permanent-error failures).", Signal: alerting.SignalLog, MinSeverity: 17, LogThreshold: 1, Severity: alerting.SeverityWarning},
		},
	},
	{
		// Grounded in what an Airflow 3 deployment actually emits, read
		// off a cell rather than recalled: every metric name, type and
		// attribute below was measured against a running scheduler.
		//
		// # It detects the SCHEDULER, not "Airflow"
		//
		// Airflow is four processes, and they emit disjoint metrics:
		//
		//	scheduler      airflow.dagrun.*  airflow.executor.*
		//	               airflow.pool.*    airflow.scheduler*  airflow.ti*
		//	dag-processor  airflow.dag_processing.*  airflow.dagbag_size
		//	triggerer      airflow.triggers.*  airflow.triggerer*
		//	apiserver      almost nothing
		//
		// So detection names the scheduler's own families rather than the
		// bare airflow. prefix. Matching that would offer this type to
		// the triggerer and the dag-processor, whose metrics none of
		// these checks mention, and every applied check would sit armed
		// and silent. The cost is that a dag-processor problem - an
		// import error, which stops a DAG running with no failure
		// anywhere - is not covered here. That is a second type, not a
		// check that cannot fire.
		//
		// # Per DAG, and why that is not the same as per service
		//
		// One scheduler runs every DAG, so "this service is failing"
		// names the scheduler and hides which DAG broke. The modern OTel
		// path carries dag_id as a proper metric ATTRIBUTE, so the
		// per-DAG checks split on it. (Airflow also still emits the
		// StatsD-era names with the DAG baked in -
		// airflow.dagrun.orders_export.first_task_scheduling_delay - which
		// no template can enumerate; the attributed metrics are the ones
		// to use.)
		//
		// The split is load-bearing beyond naming the DAG. A check split
		// by an attribute is read as describing one flow rather than the
		// process, so it no longer drags every sibling DAG integration
		// down with it (alerting.DescribesWholeService). The two
		// process-level checks here are deliberately left UNSPLIT for the
		// same reason, so they do reach every DAG: a dead scheduler and a
		// starved pool break all of them, and naming the pool would cost
		// exactly that propagation.
		//
		// # Why no check on dag run duration
		//
		// airflow.dagrun.duration.failed and .success are histograms, and
		// a histogram lands here with Value as the bucket SUM. A
		// threshold on it reads as "the total seconds of failed runs",
		// which is not a duration and not a count. The per-DAG task
		// failure counter answers the same question exactly.
		Kind: "airflow", Label: "Apache Airflow", System: false,
		DetectPrefixes: []string{"airflow.dagrun.", "airflow.executor.", "airflow.pool.", "airflow.scheduler"},
		Checks: []systemCheck{
			{Name: "DAG task failures", Description: "Task instances failed, split by DAG so the firing names which one. This is the per-DAG failure signal; bind it to an integration per DAG to alert on one flow alone.", Metric: "airflow.ti_failures", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, SplitBy: "dag_id", Severity: alerting.SeverityWarning, Unit: "tasks", Display: true},
			{Name: "Operator failures", Description: "Failures counted per operator as well as per DAG - the same events as above seen by the kind of task that raised them, which is what tells a flaky sensor from a broken load step.", Metric: "airflow.operator_failures", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, SplitBy: "dag_id", Severity: alerting.SeverityWarning, Unit: "tasks"},
			{Name: "Scheduler not running", Description: "The scheduler heartbeat stopped. Fires on the ABSENCE of the metric, because a scheduler that dies emits nothing left to threshold - which is why nothing else here would notice. Every DAG is stopped while this holds.", Metric: "airflow.scheduler_heartbeat", Agg: alerting.AggIncrease, Op: alerting.OpLT, Threshold: 1, FireOnNoData: true, Severity: alerting.SeverityCritical, Display: true},
			{Name: "Tasks starving for a pool slot", Description: "Tasks are runnable but have no slot, so they wait without failing - a queue nobody is told about. Left unsplit on purpose: it affects every DAG drawing on that pool, and splitting by pool_name would stop it saying so.", Metric: "airflow.pool.starving_tasks", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 0, Severity: alerting.SeverityWarning, Unit: "tasks", Display: true},
			{Name: "Executor out of slots", Description: "No open executor slots, so nothing new starts however much is queued. Raise the threshold above zero to fire before saturation rather than at it.", Metric: "airflow.executor.open_slots", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityWarning, Unit: "slots", Display: true},
			{Name: "Tasks queued and not starting", Description: "The queue is deep. Normal during a burst, and a stuck executor otherwise - tune to what your executor clears in five minutes.", Metric: "airflow.executor.queued_tasks", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 50, Severity: alerting.SeverityWarning, Unit: "tasks"},
		},
	},
	{
		// Node-RED, and the reason DetectSpanAttrs exists.
		//
		// # It has no metrics to be recognised by
		//
		// node-red-contrib-opentelemetry is a TRACING integration. The
		// only metrics it exports are HTTP request metrics from http in
		// nodes, under generic semantic-convention names that every HTTP
		// service in the estate emits - useless as an identity. So this
		// type is detected from the node_red.* span attributes instead,
		// which every span it produces carries.
		//
		// # Why the checks are runtime-level, unlike Camel's
		//
		// A Node-RED runtime hosts many flows, exactly as a Camel JVM
		// hosts many routes, and the flow is what somebody wants alerted
		// on. Trace-signal rules take attribute FILTERS but no split, so
		// a per-flow trace check is not expressible here.
		//
		// That is the right outcome rather than a gap, because Sluicio
		// already has a better answer: make each flow an INTEGRATION,
		// matched on node_red.flow.id, and its checks are per flow by
		// construction. The candidate machinery proposes exactly that
		// grouping. What belongs at the service level is what is true of
		// the RUNTIME and cannot be attributed to one flow, which is
		// what these four are.
		//
		// # The incomplete-span check earns its place
		//
		// msg.error() alone never marks a trace as failed: it routes to
		// a catch node without firing onComplete, so the failed-trace
		// check under-reports by design. A span left incomplete is the
		// evidence that survives that, and node_red.span.incomplete is
		// set for exactly this case. Confirmed against the Node-RED
		// source while working on the instrumentation upstream.
		Kind: "node-red", Label: "Node-RED", System: false,
		DetectSpanAttrs: []string{"node_red."},
		Checks: []systemCheck{
			{Name: "Flow failed", Description: "A flow execution failed. Alert per flow by making each flow an integration matched on node_red.flow.id - this one covers the runtime as a whole.", Signal: "trace_error", TraceThreshold: 1, WindowSeconds: 900, Severity: alerting.SeverityWarning},
			{Name: "Flow execution did not complete", Description: "A span was left open: the run neither finished nor raised. Calling msg.error() routes to a catch node WITHOUT marking the trace failed, so the failed-flow check above cannot see it and this is the only evidence there is.", Signal: "trace_attribute", TraceThreshold: 1, WindowSeconds: 900, Attrs: []alerting.AttrFilter{{Key: "node_red.span.incomplete", Op: "eq", Value: "true"}}, Severity: alerting.SeverityWarning},
			{Name: "Slow flows", Description: "p95 flow duration is high across the runtime. Tune per flow by scoping it to an integration - a file poll and an HTTP call do not share a threshold.", Signal: "trace_latency", ThresholdMs: 30000, WindowSeconds: 900, Severity: alerting.SeverityWarning, Unit: "ms"},
			{Name: "Runtime went quiet", Description: "The runtime produced fewer than one flow execution in an hour. Node-RED fails silently well - the editor stays green while nothing runs. Disable this on a runtime that only runs on a schedule, where quiet is the normal state.", Signal: "trace_volume", TraceThreshold: 1, WindowSeconds: 3600, Severity: alerting.SeverityWarning},
		},
	},
	{
		// Grounded in camel-micrometer's meter and tag names, taken from
		// MicrometerConstants in the Camel source rather than recalled.
		//
		// # Why every check splits by routeId
		//
		// A Camel application is the archetypal shared runtime: one JVM
		// hosting many routes, which are many integrations as far as
		// anyone watching is concerned. A check that says "this service
		// is failing" names the JVM and hides which of its twenty routes
		// broke, so each check below splits on routeId and the firing
		// enumerates the routes that breached.
		//
		// # Metric names depend on how the metrics leave Camel
		//
		// Micrometer keeps the dotted names below when it exports over
		// OTLP, and converts them for Prometheus: dots become
		// underscores and counters gain a _total suffix, so
		// camel.exchanges.failed arrives as camel_exchanges_failed_total.
		// Sluicio stores metric names verbatim and matches them exactly,
		// so one set of checks cannot serve both.
		//
		// These are written for the dotted form, which is what the OTLP
		// path this product asks for produces. Detection covers the
		// underscored prefix too, deliberately: a Prometheus-scraped
		// Camel is still Camel and should be recognised as such, and
		// somebody who has to retune metric names is better off than
		// somebody whose runtime went unrecognised. The docs page says
		// which names to substitute. Same trade as the NATS type.
		//
		// # Traces beside metrics
		//
		// camel-opentelemetry emits a span per exchange, so the trace
		// checks name the failing exchange and its endpoint, which no
		// counter can. The metric checks stay because they survive when
		// the tracing pipeline is the thing that broke - the same
		// reasoning as the Paperless-ngx type.
		Kind: "camel", Label: "Apache Camel", System: false,
		DetectPrefixes: []string{"camel.", "camel_"},
		Checks: []systemCheck{
			{Name: "Route failing", Description: "Exchanges failed on a route without being handled by an error handler. The split names which route, not just which JVM.", Metric: "camel.exchanges.failed", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, SplitBy: "routeId", Severity: alerting.SeverityWarning, Unit: "exchanges", Display: true},
			{Name: "Route dead-lettering", Description: "Failures are being caught and handled - a dead-letter channel doing its job, which is a silent failure if nobody reads that queue. Raise or disable this if dead-lettering is your designed path.", Metric: "camel.exchanges.failures.handled", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 10, SplitBy: "routeId", Severity: alerting.SeverityWarning, Unit: "exchanges"},
			{Name: "Route backlog", Description: "Exchanges are piling up in flight on a route - it is wedged, or intake is outrunning it. Tune to the concurrency the route is built for.", Metric: "camel.exchanges.inflight", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 100, SplitBy: "routeId", Severity: alerting.SeverityWarning, Unit: "exchanges", Display: true},
			{Name: "Routes stopped", Description: "Running routes dropped to zero. Raise the threshold to your route count so losing ONE route fires, rather than only the context going down entirely.", Metric: "camel.routes.running", Agg: alerting.AggMin, Op: alerting.OpLT, Threshold: 1, Severity: alerting.SeverityCritical, Unit: "routes", Display: true},
			{Name: "Broker redelivering to a route", Description: "An external broker is redelivering messages to this route - the consumer keeps rejecting them, which ends in a dead-letter queue rather than a visible error.", Metric: "camel.exchanges.external.redeliveries", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 0, SplitBy: "routeId", Severity: alerting.SeverityWarning, Unit: "redeliveries"},
			{Name: "Exchange failed", Description: "A failed exchange, from the trace rather than the counter: it names the endpoint and the step, which the metric cannot. Needs camel-opentelemetry.", Signal: "trace_error", TraceThreshold: 1, WindowSeconds: 900, Severity: alerting.SeverityWarning},
			{Name: "Slow exchanges", Description: "p95 exchange duration is high. Tune to the route's own shape - a file poll and an HTTP call do not belong to the same threshold.", Signal: "trace_latency", ThresholdMs: 30000, WindowSeconds: 900, Severity: alerting.SeverityWarning, Unit: "ms"},
		},
	},
	{
		// Grounded in the .NET OTel metrics this stack emits. Thread-pool /
		// Kestrel queues are UpDownCounters (max = peak); exceptions is a
		// monotonic counter (increase = thrown in the window).
		Kind: "dotnet-service", Label: ".NET service", System: false,
		DetectPrefixes: []string{"process.runtime.dotnet", "kestrel.", "aspnetcore."},
		Checks: []systemCheck{
			{Name: ".NET thread-pool backlog", Description: "Work is queuing on the thread pool — the app can't keep up.", Metric: "process.runtime.dotnet.thread_pool.queue.length", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 200, Severity: alerting.SeverityWarning, Unit: "items", Display: true},
			{Name: "Kestrel connections queued", Description: "Incoming connections are queuing — the web host is saturated.", Metric: "kestrel.queued_connections", Agg: alerting.AggMax, Op: alerting.OpGT, Threshold: 50, Severity: alerting.SeverityWarning, Unit: "connections", Display: true},
			{Name: ".NET exception rate", Description: "Exceptions thrown are spiking (includes handled) — tune to your baseline.", Metric: "process.runtime.dotnet.exceptions.count", Agg: alerting.AggIncrease, Op: alerting.OpGT, Threshold: 100, Severity: alerting.SeverityWarning, Unit: "exceptions", Display: true},
			// Log check — error-level logs spiking (catches failures that don't
			// surface as exceptions, e.g. logged errors from middleware/jobs).
			{Name: ".NET error logs spiking", Description: "Error-level logs are spiking — tune the threshold to your baseline.", Signal: alerting.SignalLog, MinSeverity: 17, LogThreshold: 25, Severity: alerting.SeverityWarning},
		},
	},
}

func parseChannelIDList(ids []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, s := range ids {
		if id, err := uuid.Parse(strings.TrimSpace(s)); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// createTemplateChecks applies a template's checks to a service: creates the
// missing ones, re-routes channels onto existing same-named checks (when any
// channels were given), and leaves the rest. Idempotent by check name.
func (h *Handlers) createTemplateChecks(r *http.Request, orgID uuid.UUID, service string, checks []systemCheck, channels []uuid.UUID) (created, updated, skipped int, err error) {
	existing, lerr := h.Alerts.ListRules(r.Context(), orgID)
	if lerr != nil {
		h.Logger.Warn("apply template: list rules failed", "err", lerr)
	}
	existingByName := make(map[string]alerting.AlertRule)
	for _, e := range existing {
		if e.ServiceName == service {
			existingByName[e.Name] = e
		}
	}
	for _, c := range checks {
		if ex, ok := existingByName[c.Name]; ok {
			if len(channels) > 0 {
				ex.ChannelIDs = channels
				if _, uerr := h.Alerts.UpdateRule(r.Context(), orgID, ex); uerr != nil {
					return created, updated, skipped, uerr
				}
				updated++
			} else {
				skipped++
			}
			continue
		}
		rule := alerting.AlertRule{
			OrganizationID: orgID,
			ServiceName:    service,
			Name:           c.Name,
			Description:    c.Description,
			Severity:       c.Severity,
			EvalSeconds:    60,
			Enabled:        true,
			Source:         alerting.SourceTelemetry,
			Unit:           c.Unit,
			ResolveMode:    alerting.ResolveAuto,
			ChannelIDs:     channels,
		}
		traceWindow := c.WindowSeconds
		if traceWindow < 60 {
			traceWindow = 300
		}
		traceThreshold := c.TraceThreshold
		if traceThreshold < 1 {
			traceThreshold = 1
		}
		switch c.Signal {
		case alerting.SignalLog:
			th := c.LogThreshold
			if th < 1 {
				th = 1
			}
			rule.Signal = alerting.SignalLog
			rule.LogSpec = &alerting.LogRuleSpec{
				MinSeverity:   c.MinSeverity,
				BodyContains:  c.BodyContains,
				Threshold:     th,
				WindowSeconds: 300,
				Comparison:    alerting.LogComparisonAtLeast,
			}
		case "trace_error":
			rule.Signal = alerting.SignalTraceError
			rule.TraceErrorSpec = &alerting.TraceErrorRuleSpec{
				Kind:          alerting.TraceErrorSpecKind,
				Threshold:     traceThreshold,
				WindowSeconds: traceWindow,
				Attrs:         c.Attrs,
			}
		case "trace_latency":
			rule.Signal = alerting.SignalTraceError
			rule.TraceLatencySpec = &alerting.TraceLatencyRuleSpec{
				Kind:          alerting.TraceLatencySpecKind,
				ThresholdMs:   c.ThresholdMs,
				WindowSeconds: traceWindow,
				Aggregation:   "p95",
			}
		case "trace_volume":
			rule.Signal = alerting.SignalTraceError
			rule.TraceVolumeSpec = &alerting.TraceVolumeRuleSpec{
				Kind:          alerting.TraceVolumeSpecKind,
				Threshold:     traceThreshold,
				WindowSeconds: traceWindow,
			}
		case "trace_attribute":
			rule.Signal = alerting.SignalTraceError
			rule.TraceAttributeSpec = &alerting.TraceAttributeRuleSpec{
				Kind:          alerting.TraceAttributeSpecKind,
				Threshold:     traceThreshold,
				WindowSeconds: traceWindow,
				Attrs:         c.Attrs,
			}
		default:
			rule.Signal = alerting.SignalMetric
			rule.DisplayOnService = c.Display
			rule.Spec = alerting.MetricRuleSpec{
				MetricName:   c.Metric,
				Aggregation:  c.Agg,
				Operator:     c.Op,
				Threshold:    c.Threshold,
				ForWindow:    "5m",
				Attrs:        c.Attrs,
				SplitBy:      c.SplitBy,
				FireOnNoData: c.FireOnNoData,
			}
		}
		if _, cerr := h.Alerts.CreateRule(r.Context(), rule); cerr != nil {
			return created, updated, skipped, cerr
		}
		created++
	}
	return created, updated, skipped, nil
}

// applySystemTemplate: POST /api/v1/services/{name}/system/apply-template  (writer+)
// Applies the template for the service's flagged system_kind.
func (h *Handlers) applySystemTemplate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	var body struct {
		ChannelIDs []string `json:"channel_ids"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	channels := parseChannelIDList(body.ChannelIDs)

	orgID := middleware.OrgID(r)
	cat, ok, err := h.Catalog.GetService(r.Context(), orgID, name)
	if err != nil {
		h.Logger.Error("apply template: get service failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !ok || !cat.IsSystem {
		httpserver.WriteError(w, http.StatusBadRequest, "service is not flagged as a system")
		return
	}
	tmpl, has, terr := h.templateByKind(r.Context(), orgID, cat.SystemKind)
	if terr != nil {
		h.Logger.Error("apply template: catalog failed", "err", terr, "kind", cat.SystemKind)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !has || len(tmpl.Checks) == 0 {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{
			"kind": cat.SystemKind, "created": 0, "updated": 0, "skipped": 0,
			"message": "no template for this system kind",
		})
		return
	}
	created, updated, skipped, err := h.createTemplateChecks(r, orgID, name, tmpl.Checks, channels)
	if err != nil {
		h.Logger.Error("apply template: failed", "err", err, "service", name, "kind", cat.SystemKind)
		httpserver.WriteError(w, http.StatusInternalServerError, "failed to apply template")
		return
	}
	h.recordAudit(r, "service_template.applied", "service", name, map[string]any{"kind": cat.SystemKind})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"kind": cat.SystemKind, "created": created, "updated": updated, "skipped": skipped,
	})
}

// applyTemplate: POST /api/v1/services/{name}/apply-template  (writer+)
// Applies a built-in kind OR a custom template (template_id) to a service —
// not limited to flagged systems. Body: { kind?, template_id?, channel_ids? }.
func (h *Handlers) applyTemplate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	var body struct {
		Kind       string   `json:"kind"`
		TemplateID string   `json:"template_id"`
		ChannelIDs []string `json:"channel_ids"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	orgID := middleware.OrgID(r)

	var checks []systemCheck
	var label string
	if id := strings.TrimSpace(body.TemplateID); id != "" {
		tid, err := uuid.Parse(id)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid template_id")
			return
		}
		t, ok, err := h.Templates.Get(r.Context(), orgID, tid)
		if err != nil {
			h.Logger.Error("apply template: get custom failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		if !ok {
			httpserver.WriteError(w, http.StatusNotFound, "template not found")
			return
		}
		for _, c := range t.Checks {
			checks = append(checks, customCheckToSystemCheck(c))
		}
		label = t.Name
	} else {
		kind := strings.ToLower(strings.TrimSpace(body.Kind))
		tmpl, has, terr := h.templateByKind(r.Context(), orgID, kind)
		if terr != nil {
			h.Logger.Error("apply template: catalog failed", "err", terr, "kind", kind)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		if !has || len(tmpl.Checks) == 0 {
			httpserver.WriteError(w, http.StatusBadRequest, "unknown template kind")
			return
		}
		checks = tmpl.Checks
		label = kind
	}
	if len(checks) == 0 {
		httpserver.WriteError(w, http.StatusBadRequest, "template has no checks")
		return
	}

	channels := parseChannelIDList(body.ChannelIDs)
	created, updated, skipped, err := h.createTemplateChecks(r, orgID, name, checks, channels)
	if err != nil {
		h.Logger.Error("apply template: failed", "err", err, "service", name, "template", label)
		httpserver.WriteError(w, http.StatusInternalServerError, "failed to apply template")
		return
	}
	h.recordAudit(r, "service_template.applied", "service", name, map[string]any{"template": label})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"kind": label, "created": created, "updated": updated, "skipped": skipped,
	})
}

// templateSuggestions: GET /api/v1/services/{name}/template-suggestions  (read)
// Auto-detects likely template kinds from the service's emitted metric names.
func (h *Handlers) templateSuggestions(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	tr := ParseRange(r, 24*time.Hour)
	matched, err := h.detectTemplates(r.Context(), middleware.OrgID(r), name, tr.From, tr.To)
	if err != nil {
		h.Logger.Error("template suggestions: metric names failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Load the service's existing checks once to flag templates already applied
	// (all their checks present) — the UI offers Remove rather than a no-op
	// re-apply.
	haveNames := map[string]bool{}
	if rules, rerr := h.Alerts.ListRules(r.Context(), middleware.OrgID(r)); rerr == nil {
		for _, rl := range rules {
			if rl.ServiceName == name {
				haveNames[rl.Name] = true
			}
		}
	}
	type suggestion struct {
		Kind       string `json:"kind"`
		Label      string `json:"label"`
		System     bool   `json:"system"`
		CheckCount int    `json:"check_count"`
		Applied    bool   `json:"applied"`
	}
	out := []suggestion{}
	for _, t := range matched {
		applied := len(t.Checks) > 0
		for _, c := range t.Checks {
			if !haveNames[c.Name] {
				applied = false
				break
			}
		}
		out = append(out, suggestion{Kind: t.Kind, Label: t.Label, System: t.System, CheckCount: len(t.Checks), Applied: applied})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

// detectTemplates returns the org's effective templates (built-ins + custom)
// whose detection prefixes match the service's emitted metric names in
// [from,to]. Shared by the per-service suggestions endpoint and the digest.
func (h *Handlers) detectTemplates(ctx context.Context, orgID uuid.UUID, serviceName string, from, to time.Time) ([]monitoringTemplate, error) {
	rows, err := h.Store.MetricNames(ctx, serviceName, nil, from, to)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, m := range rows {
		names = append(names, m.MetricName)
	}
	tmpls, err := h.effectiveTemplates(ctx, orgID)
	if err != nil {
		return nil, err
	}
	// Span attribute keys, fetched only when some template actually asks
	// for them. A cell whose types all detect on metric names pays
	// nothing, which keeps this endpoint at the one query it was.
	var attrKeys []string
	wantAttrs := false
	for _, t := range tmpls {
		if len(t.DetectSpanAttrs) > 0 && len(t.Checks) > 0 {
			wantAttrs = true
			break
		}
	}
	if wantAttrs {
		rows, aerr := h.Store.DistinctAttributeKeysScoped(ctx, []string{serviceName}, from, to, 0, nil)
		if aerr != nil {
			// Degraded, not fatal: metric-detected kinds are still worth
			// suggesting, and a failure here must not empty the list.
			h.Logger.Warn("template suggestions: span attribute keys failed", "err", aerr, "service", serviceName)
		} else {
			attrKeys = make([]string, 0, len(rows))
			for _, r := range rows {
				attrKeys = append(attrKeys, r.Key)
			}
		}
	}
	var out []monitoringTemplate
	for _, t := range tmpls {
		if len(t.Checks) == 0 {
			continue
		}
		if len(t.DetectPrefixes) == 0 && len(t.DetectSpanAttrs) == 0 {
			continue
		}
		if metricsMatchPrefixes(names, t.DetectPrefixes) ||
			metricsMatchPrefixes(attrKeys, t.DetectSpanAttrs) {
			out = append(out, t)
		}
	}
	return out, nil
}

// templateChecksFor resolves a built-in kind OR a custom template id to its
// checks (in the built-in systemCheck shape). ok=false means unknown.
func (h *Handlers) templateChecksFor(ctx context.Context, orgID uuid.UUID, kind, templateID string) ([]systemCheck, bool, error) {
	if id := strings.TrimSpace(templateID); id != "" {
		tid, err := uuid.Parse(id)
		if err != nil {
			return nil, false, err
		}
		t, ok, err := h.Templates.Get(ctx, orgID, tid)
		if err != nil || !ok {
			return nil, false, err
		}
		cs := make([]systemCheck, 0, len(t.Checks))
		for _, c := range t.Checks {
			cs = append(cs, customCheckToSystemCheck(c))
		}
		return cs, true, nil
	}
	tmpl, has, err := h.templateByKind(ctx, orgID, strings.ToLower(strings.TrimSpace(kind)))
	if err != nil {
		return nil, false, err
	}
	if !has {
		return nil, false, nil
	}
	return tmpl.Checks, true, nil
}

// removeTemplate: POST /api/v1/services/{name}/remove-template  (writer+)
// Deletes a template's checks from the service (matched by check name) — so a
// user can remove an applied template or switch to a different one rather than
// re-applying (which would just no-op on the already-present checks).
func (h *Handlers) removeTemplate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	var body struct {
		Kind       string `json:"kind"`
		TemplateID string `json:"template_id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	orgID := middleware.OrgID(r)
	checks, ok, err := h.templateChecksFor(r.Context(), orgID, body.Kind, body.TemplateID)
	if err != nil {
		h.Logger.Error("remove template: resolve failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !ok || len(checks) == 0 {
		httpserver.WriteError(w, http.StatusBadRequest, "unknown template")
		return
	}
	names := make(map[string]bool, len(checks))
	for _, c := range checks {
		names[c.Name] = true
	}
	rules, err := h.Alerts.ListRules(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("remove template: list rules failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	removed := 0
	for _, rule := range rules {
		if rule.ServiceName == name && names[rule.Name] {
			if derr := h.Alerts.DeleteRule(r.Context(), orgID, rule.ID); derr != nil {
				h.Logger.Error("remove template: delete rule failed", "err", derr, "rule", rule.ID)
				continue
			}
			removed++
		}
	}
	if removed > 0 {
		h.recordAudit(r, "service_template.removed", "service", name,
			map[string]any{"kind": body.Kind, "template_id": body.TemplateID, "removed": removed})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

func metricsMatchPrefixes(names, prefixes []string) bool {
	for _, n := range names {
		for _, p := range prefixes {
			if strings.HasPrefix(n, p) {
				return true
			}
		}
	}
	return false
}
