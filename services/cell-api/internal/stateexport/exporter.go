// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package stateexport pushes the cell's own judgement out as OTLP
// metrics (issue #36).
//
// Sluicio decides whether an integration is healthy, and until now that
// decision never left the cell. In a shop that runs Dynatrace for the
// whole estate and Sluicio as the master for integrations, the estate
// tool had no way to show an integration as red without reimplementing
// the reasoning that made it red. This exports the conclusion instead.
//
// What travels:
//
//	sluicio.integration.state     gauge, int   -1 quiet, 0 ok, 1 errors, 2 unhealthy
//	sluicio.integration.messages  sum, delta   messages since the previous export
//	sluicio.system.state          gauge, int   the same scale
//
// The numbering is deliberate: `>= 1` is the alert condition, and quiet
// sits below ok so an integration with nothing in its window does not
// page anybody at three in the morning for being asleep.
package stateexport

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/cellhealth"
)

// Metric names and the state scale. Exported because an operator
// configuring the receiving end needs them, and because the test that
// pins them is the contract.
const (
	MetricIntegrationState    = "sluicio.integration.state"
	MetricIntegrationMessages = "sluicio.integration.messages"
	MetricSystemState         = "sluicio.system.state"

	StateQuiet     int64 = -1
	StateOK        int64 = 0
	StateErrors    int64 = 1
	StateUnhealthy int64 = 2
)

// StateValue maps the product's status vocabulary onto the scale.
// An unknown status reads as quiet rather than ok: inventing health for
// a word we do not recognise is the one answer that could silence a
// real alert.
func StateValue(status string) int64 {
	switch status {
	case "ok":
		return StateOK
	case "errors":
		return StateErrors
	case "unhealthy":
		return StateUnhealthy
	default:
		return StateQuiet
	}
}

// Entity is one integration or system to report. It mirrors the source's
// row without binding this package to the api package.
type Entity struct {
	ID       uuid.UUID
	Name     string
	Slug     string
	Kind     string
	Status   string
	Messages uint64
}

// Source is where the states come from. The api package implements it,
// because that is where the product decides what healthy means.
type Source interface {
	IntegrationStates(ctx context.Context, orgID uuid.UUID, healthFrom, healthTo, countFrom, countTo time.Time) ([]Entity, error)
	SystemStates(ctx context.Context, orgID uuid.UUID, healthFrom, healthTo time.Time) ([]Entity, error)
}

// Config is the deployment's half of this: where to push, how often, and
// how far behind the clock to count.
type Config struct {
	// Endpoint is an OTLP/HTTP endpoint. A bare origin gets the
	// conventional /v1/metrics path appended, so both spellings work.
	// Empty disables the exporter entirely.
	Endpoint string
	// Headers travel on every request, for the receiver's own auth.
	Headers map[string]string
	// Interval between exports. Default 60s.
	Interval time.Duration
	// Lag is how far behind now the counting window ends. Telemetry
	// arrives after the fact, so a window that ends at now always
	// undercounts its newest seconds and the series dips at the
	// right-hand edge forever. Default 60s.
	Lag time.Duration
	// HealthWindow is the stretch a state is judged over. Default 1h,
	// which is what the Integrations page defaults to, so the exported
	// state and the page agree.
	HealthWindow time.Duration
	// CellName identifies this cell on the resource, so several cells
	// can report to one receiver without colliding.
	CellName string
	// Environment reads the cell's own environment setting, the one the
	// header shows as "ENV · PRODUCTION". A function rather than a
	// string because an admin can change it while the exporter runs, and
	// the resource attribute should follow what the product says rather
	// than what the process was started with.
	Environment func(context.Context) string
	// SelfIngestURL is this cell's own ingest, when the deployment
	// tells us (SLUICIO_INGEST_URL). Only used to notice that the export
	// has been pointed back here.
	SelfIngestURL string
}

// Exporter runs the loop. Zero value is not usable; see New.
type Exporter struct {
	cfg      Config
	src      Source
	orgID    uuid.UUID
	log      *slog.Logger
	client   *http.Client
	entitled func() bool

	// warnedUnlicensed keeps the licence complaint to once per process.
	// A message repeated every minute is how a log stops being read.
	warnedUnlicensed bool
	// env is the cell's environment as of the last export, for the
	// resource attribute.
	env string
	// nextFrom is where the next counting window starts: the end of the
	// last window that was actually delivered. Counting from there makes
	// the deltas tile rather than overlap or leave gaps, and makes a
	// refused export something the next tick picks up rather than a
	// minute nobody ever counted.
	nextFrom time.Time
}

// New wires an exporter. A zero Interval, Lag or HealthWindow takes the
// documented default.
func New(cfg Config, src Source, orgID uuid.UUID, log *slog.Logger, entitled func() bool) *Exporter {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.Lag < 0 {
		cfg.Lag = 0
	}
	if cfg.Lag == 0 {
		cfg.Lag = time.Minute
	}
	if cfg.HealthWindow <= 0 {
		cfg.HealthWindow = time.Hour
	}
	return &Exporter{
		cfg:      cfg,
		src:      src,
		orgID:    orgID,
		log:      log,
		client:   &http.Client{Timeout: 20 * time.Second},
		entitled: entitled,
	}
}

// Enabled reports whether an endpoint was configured at all.
func (e *Exporter) Enabled() bool { return strings.TrimSpace(e.cfg.Endpoint) != "" }

// Run exports on every tick until ctx is cancelled.
func (e *Exporter) Run(ctx context.Context) {
	if !e.Enabled() {
		return
	}
	e.log.Info("state export started",
		"endpoint", e.cfg.Endpoint, "interval", e.cfg.Interval,
		"lag", e.cfg.Lag, "health_window", e.cfg.HealthWindow)
	// Pointed at ourselves, the cell measures its own reporting and
	// every export produces traffic to export. It still runs: somebody
	// may want exactly that for a smoke test, and refusing to start
	// would be a worse surprise than saying so.
	if PointsAtSelf(e.cfg.Endpoint, e.cfg.SelfIngestURL) {
		e.log.Warn("state export is pointed at this cell's own ingest; it will measure its own reporting",
			"endpoint", e.cfg.Endpoint, "ingest", e.cfg.SelfIngestURL)
	}
	t := time.NewTicker(e.cfg.Interval)
	cellhealth.Register("state-export", e.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.ExportOnce(ctx, time.Now().UTC()); err != nil {
				e.log.Warn("state export failed", "err", err)
			}
			// End of cycle, not start: a loop wedged inside its own
			// body is exactly what this catches.
			cellhealth.Beat("state-export")
		}
	}
}

// ExportOnce gathers the states and posts them. Exported so the loop and
// a test can share one path.
func (e *Exporter) ExportOnce(ctx context.Context, now time.Time) error {
	if !e.Enabled() {
		return nil
	}
	if e.entitled != nil && !e.entitled() {
		if !e.warnedUnlicensed {
			e.warnedUnlicensed = true
			e.log.Warn("state export is configured but not licensed; nothing is being exported",
				"entitlement", "state_export", "endpoint", e.cfg.Endpoint)
		}
		return nil
	}
	e.warnedUnlicensed = false

	countTo := now.Add(-e.cfg.Lag)
	countFrom := e.nextFrom
	if countFrom.IsZero() || countTo.Sub(countFrom) > 10*e.cfg.Interval {
		// First run, or a long outage. Count one interval rather than
		// whatever accumulated: a single delta holding an hour of
		// messages is a spike the receiver cannot tell from real load.
		countFrom = countTo.Add(-e.cfg.Interval)
	}
	if !countTo.After(countFrom) {
		return nil // clock went backwards, or a tick arrived early
	}
	healthFrom, healthTo := now.Add(-e.cfg.HealthWindow), now

	integrations, err := e.src.IntegrationStates(ctx, e.orgID, healthFrom, healthTo, countFrom, countTo)
	if err != nil {
		return fmt.Errorf("integration states: %w", err)
	}
	systems, err := e.src.SystemStates(ctx, e.orgID, healthFrom, healthTo)
	if err != nil {
		return fmt.Errorf("system states: %w", err)
	}
	if len(integrations) == 0 && len(systems) == 0 {
		e.nextFrom = countTo
		return nil
	}

	if e.cfg.Environment != nil {
		e.env = e.cfg.Environment(ctx)
	}
	req := e.buildRequest(integrations, systems, countFrom, countTo)
	if err := e.post(ctx, req); err != nil {
		// The window was never delivered, so the next run starts where
		// this one did. Without this a receiver that was restarting
		// would cost exactly the messages that arrived while it was,
		// and nothing downstream would ever show them missing.
		e.nextFrom = countFrom
		return err
	}
	e.nextFrom = countTo
	return nil
}

// buildRequest renders the OTLP payload.
func (e *Exporter) buildRequest(integrations, systems []Entity, countFrom, countTo time.Time) *colmetricspb.ExportMetricsServiceRequest {
	start := uint64(countFrom.UnixNano())
	end := uint64(countTo.UnixNano())

	intState := make([]*metricspb.NumberDataPoint, 0, len(integrations))
	intMsgs := make([]*metricspb.NumberDataPoint, 0, len(integrations))
	for _, it := range integrations {
		attrs := []*commonpb.KeyValue{
			str("sluicio.integration.id", it.ID.String()),
			str("sluicio.integration.name", it.Name),
		}
		if it.Slug != "" {
			attrs = append(attrs, str("sluicio.integration.slug", it.Slug))
		}
		intState = append(intState, &metricspb.NumberDataPoint{
			Attributes:   attrs,
			TimeUnixNano: end,
			Value:        &metricspb.NumberDataPoint_AsInt{AsInt: StateValue(it.Status)},
		})
		intMsgs = append(intMsgs, &metricspb.NumberDataPoint{
			Attributes:        attrs,
			StartTimeUnixNano: start,
			TimeUnixNano:      end,
			Value:             &metricspb.NumberDataPoint_AsInt{AsInt: int64(it.Messages)},
		})
	}

	sysState := make([]*metricspb.NumberDataPoint, 0, len(systems))
	for _, sy := range systems {
		attrs := []*commonpb.KeyValue{
			str("sluicio.system.id", sy.ID.String()),
			str("sluicio.system.name", sy.Name),
		}
		if sy.Kind != "" {
			attrs = append(attrs, str("sluicio.system.type", sy.Kind))
		}
		sysState = append(sysState, &metricspb.NumberDataPoint{
			Attributes:   attrs,
			TimeUnixNano: end,
			Value:        &metricspb.NumberDataPoint_AsInt{AsInt: StateValue(sy.Status)},
		})
	}

	metrics := make([]*metricspb.Metric, 0, 3)
	if len(intState) > 0 {
		metrics = append(metrics,
			&metricspb.Metric{
				Name:        MetricIntegrationState,
				Description: "-1 quiet, 0 ok, 1 errors, 2 unhealthy",
				Data:        &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: intState}},
			},
			&metricspb.Metric{
				Name:        MetricIntegrationMessages,
				Unit:        "{message}",
				Description: "messages counted since the previous export",
				Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
					// Delta, not cumulative. A cumulative counter resets
					// when the cell restarts and again when retention
					// drops the old rows, and both resets look like a
					// negative rate to whoever is reading.
					AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
					IsMonotonic:            true,
					DataPoints:             intMsgs,
				}},
			})
	}
	if len(sysState) > 0 {
		metrics = append(metrics, &metricspb.Metric{
			Name:        MetricSystemState,
			Description: "-1 quiet, 0 ok, 1 errors, 2 unhealthy",
			Data:        &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: sysState}},
		})
	}

	res := []*commonpb.KeyValue{str("service.name", "sluicio-cell")}
	if e.cfg.CellName != "" {
		res = append(res, str("sluicio.cell.name", e.cfg.CellName))
	}
	if e.env != "" {
		res = append(res, str("deployment.environment", e.env))
	}
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource:     &resourcepb.Resource{Attributes: res},
			ScopeMetrics: []*metricspb.ScopeMetrics{{Scope: &commonpb.InstrumentationScope{Name: "sluicio/state-export"}, Metrics: metrics}},
		}},
	}
}

func (e *Exporter) post(ctx context.Context, msg *colmetricspb.ExportMetricsServiceRequest) error {
	body, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, MetricsURL(e.cfg.Endpoint), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	for k, v := range e.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("receiver answered %s", resp.Status)
	}
	return nil
}

// MetricsURL appends the conventional OTLP path when the endpoint was
// given as a bare origin, so both spellings of the setting work. A
// receiver that wants some other path keeps it: only a URL with no path
// at all is completed.
func MetricsURL(endpoint string) string {
	e := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if e == "" {
		return ""
	}
	rest := e
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if strings.Contains(rest, "/") {
		return e
	}
	return e + "/v1/metrics"
}

// PointsAtSelf reports whether the export endpoint is this cell's own
// ingest. Host and port only: the path differs (/v1/metrics either way)
// and the scheme may, through a proxy.
func PointsAtSelf(endpoint, selfIngest string) bool {
	a, b := hostPort(endpoint), hostPort(selfIngest)
	return a != "" && a == b
}

func hostPort(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

func str(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   k,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
	}
}
