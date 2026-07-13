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
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/alerting"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
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
	Checks         []systemCheck
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
		default:
			rule.Signal = alerting.SignalMetric
			rule.DisplayOnService = c.Display
			rule.Spec = alerting.MetricRuleSpec{
				MetricName:  c.Metric,
				Aggregation: c.Agg,
				Operator:    c.Op,
				Threshold:   c.Threshold,
				ForWindow:   "5m",
				Attrs:       c.Attrs,
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
	var out []monitoringTemplate
	for _, t := range tmpls {
		if len(t.DetectPrefixes) == 0 || len(t.Checks) == 0 {
			continue
		}
		if metricsMatchPrefixes(names, t.DetectPrefixes) {
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
