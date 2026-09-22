// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The payload IS the contract here: a receiver somewhere else reads
// these names, this scale and this temporality, and none of it is
// checked by anything else in the product.

package stateexport

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

type fakeSource struct {
	integrations []Entity
	systems      []Entity
	calls        int
	countFrom    time.Time
	countTo      time.Time
	healthFrom   time.Time
	err          error
}

func (f *fakeSource) IntegrationStates(_ context.Context, _ uuid.UUID, healthFrom, _, countFrom, countTo time.Time) ([]Entity, error) {
	f.calls++
	f.countFrom, f.countTo, f.healthFrom = countFrom, countTo, healthFrom
	return f.integrations, f.err
}

func (f *fakeSource) SystemStates(context.Context, uuid.UUID, time.Time, time.Time) ([]Entity, error) {
	return f.systems, nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestExporter(t *testing.T, src Source, cfg Config) (*Exporter, *[]*colmetricspb.ExportMetricsServiceRequest, *[]http.Header) {
	t.Helper()
	var got []*colmetricspb.ExportMetricsServiceRequest
	var heads []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		msg := &colmetricspb.ExportMetricsServiceRequest{}
		if err := proto.Unmarshal(body, msg); err != nil {
			t.Errorf("receiver could not parse the payload: %v", err)
		}
		got = append(got, msg)
		heads = append(heads, r.Header.Clone())
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	cfg.Endpoint = srv.URL + "/v1/metrics"
	return New(cfg, src, uuid.New(), quietLogger(), nil), &got, &heads
}

func metricsIn(t *testing.T, req *colmetricspb.ExportMetricsServiceRequest) map[string]*metricspb.Metric {
	t.Helper()
	out := map[string]*metricspb.Metric{}
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				out[m.Name] = m
			}
		}
	}
	return out
}

func TestStateScale(t *testing.T) {
	for status, want := range map[string]int64{
		"ok": 0, "errors": 1, "unhealthy": 2, "quiet": -1,
	} {
		if got := StateValue(status); got != want {
			t.Errorf("%s: got %d, want %d", status, got, want)
		}
	}
	// The one that matters for an alert rule: an unrecognised word must
	// not read as healthy, or a renamed status silences every alarm.
	if StateValue("something-new") != StateQuiet {
		t.Errorf("an unknown status should read as quiet, got %d", StateValue("something-new"))
	}
	if StateOK >= StateErrors || StateErrors >= StateUnhealthy || StateQuiet >= StateOK {
		t.Error("the scale must be ordered quiet < ok < errors < unhealthy for a threshold to mean anything")
	}
}

func TestPayloadCarriesTheThreeMetrics(t *testing.T) {
	src := &fakeSource{
		integrations: []Entity{{ID: uuid.New(), Name: "ERP Adapter", Slug: "erp-adapter", Status: "errors", Messages: 42}},
		systems:      []Entity{{ID: uuid.New(), Name: "Prod broker", Kind: "rabbitmq", Status: "unhealthy"}},
	}
	e, got, heads := newTestExporter(t, src, Config{
		Headers:     map[string]string{"Authorization": "Api-Token abc"},
		CellName:    "cell-eu-1",
		Environment: func(context.Context) string { return "prod" },
	})
	if err := e.ExportOnce(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("expected one export, got %d", len(*got))
	}
	ms := metricsIn(t, (*got)[0])
	for _, name := range []string{MetricIntegrationState, MetricIntegrationMessages, MetricSystemState} {
		if ms[name] == nil {
			t.Errorf("missing %s", name)
		}
	}

	state := ms[MetricIntegrationState].GetGauge().DataPoints[0]
	if state.GetAsInt() != StateErrors {
		t.Errorf("integration state: got %d, want %d", state.GetAsInt(), StateErrors)
	}
	sys := ms[MetricSystemState].GetGauge().DataPoints[0]
	if sys.GetAsInt() != StateUnhealthy {
		t.Errorf("system state: got %d, want %d", sys.GetAsInt(), StateUnhealthy)
	}

	sum := ms[MetricIntegrationMessages].GetSum()
	if sum.AggregationTemporality != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
		t.Errorf("messages must be a DELTA sum, got %v", sum.AggregationTemporality)
	}
	if !sum.IsMonotonic {
		t.Error("messages counts up, so the sum is monotonic")
	}
	if sum.DataPoints[0].GetAsInt() != 42 {
		t.Errorf("messages: got %d, want 42", sum.DataPoints[0].GetAsInt())
	}
	if sum.DataPoints[0].StartTimeUnixNano == 0 {
		t.Error("a delta point without a start time is a delta over nothing")
	}

	// Identity, so a receiver can tell two integrations apart and two
	// cells reporting to it apart.
	attrs := map[string]string{}
	for _, kv := range state.Attributes {
		attrs[kv.Key] = kv.Value.GetStringValue()
	}
	for _, want := range []string{"sluicio.integration.id", "sluicio.integration.name", "sluicio.integration.slug"} {
		if attrs[want] == "" {
			t.Errorf("missing attribute %s", want)
		}
	}
	res := map[string]string{}
	for _, kv := range (*got)[0].ResourceMetrics[0].Resource.Attributes {
		res[kv.Key] = kv.Value.GetStringValue()
	}
	if res["sluicio.cell.name"] != "cell-eu-1" || res["deployment.environment"] != "prod" {
		t.Errorf("resource does not identify the cell: %v", res)
	}
	if (*heads)[0].Get("Authorization") != "Api-Token abc" {
		t.Error("the configured header did not travel")
	}
	if ct := (*heads)[0].Get("Content-Type"); ct != "application/x-protobuf" {
		t.Errorf("content type: got %q", ct)
	}
}

// The counting window has to tile: no gap, no overlap, or the receiver's
// sum of the deltas is not the number of messages that happened.
func TestWindowsTile(t *testing.T) {
	src := &fakeSource{integrations: []Entity{{ID: uuid.New(), Name: "a", Status: "ok"}}}
	e, _, _ := newTestExporter(t, src, Config{Interval: time.Minute, Lag: time.Minute})
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if err := e.ExportOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	firstEnd := src.countTo
	// The first run counts one interval, not everything since the epoch.
	if d := firstEnd.Sub(src.countFrom); d != time.Minute {
		t.Errorf("first window: got %s, want 1m", d)
	}
	// The window ends behind the clock, because late spans are the norm.
	if d := now.Sub(firstEnd); d != time.Minute {
		t.Errorf("window should lag the clock by 1m, got %s", d)
	}
	if err := e.ExportOnce(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !src.countFrom.Equal(firstEnd) {
		t.Errorf("second window starts at %s, want %s (the first one's end)", src.countFrom, firstEnd)
	}
}

// A long gap must not arrive as one enormous delta that reads as a spike.
func TestALongOutageDoesNotSendOneHugeDelta(t *testing.T) {
	src := &fakeSource{integrations: []Entity{{ID: uuid.New(), Name: "a", Status: "ok"}}}
	e, _, _ := newTestExporter(t, src, Config{Interval: time.Minute, Lag: time.Minute})
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if err := e.ExportOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := e.ExportOnce(context.Background(), now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if d := src.countTo.Sub(src.countFrom); d > 10*time.Minute {
		t.Errorf("after an outage the window is %s; it should fall back to one interval", d)
	}
}

// A window the receiver refused must be counted again, not lost.
func TestAFailedPostIsRetriedNextTick(t *testing.T) {
	src := &fakeSource{integrations: []Entity{{ID: uuid.New(), Name: "a", Status: "ok"}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	e := New(Config{Endpoint: srv.URL, Interval: time.Minute, Lag: time.Minute}, src, uuid.New(), quietLogger(), nil)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if err := e.ExportOnce(context.Background(), now); err == nil {
		t.Fatal("a 503 from the receiver should be reported")
	}
	failedFrom := src.countFrom
	_ = e.ExportOnce(context.Background(), now.Add(time.Minute))
	if !src.countFrom.Equal(failedFrom) {
		t.Errorf("the refused window was dropped: retried from %s, first tried %s", src.countFrom, failedFrom)
	}
}

func TestHealthWindowIsItsOwn(t *testing.T) {
	src := &fakeSource{integrations: []Entity{{ID: uuid.New(), Name: "a", Status: "ok"}}}
	e, _, _ := newTestExporter(t, src, Config{Interval: time.Minute, Lag: time.Minute, HealthWindow: time.Hour})
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if err := e.ExportOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	// State is judged over the hour, not over the minute that was
	// counted, or every integration that runs hourly reads as quiet.
	if d := now.Sub(src.healthFrom); d != time.Hour {
		t.Errorf("health window: got %s, want 1h", d)
	}
}

func TestUnlicensedExportsNothingAndSaysSoOnce(t *testing.T) {
	src := &fakeSource{integrations: []Entity{{ID: uuid.New(), Name: "a", Status: "ok"}}}
	e, got, _ := newTestExporter(t, src, Config{})
	e.entitled = func() bool { return false }
	for i := 0; i < 3; i++ {
		if err := e.ExportOnce(context.Background(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if len(*got) != 0 {
		t.Errorf("an unlicensed cell exported %d payloads", len(*got))
	}
	if src.calls != 0 {
		t.Errorf("an unlicensed cell still asked ClickHouse %d times", src.calls)
	}
}

func TestDisabledWithoutAnEndpoint(t *testing.T) {
	e := New(Config{}, &fakeSource{}, uuid.New(), quietLogger(), nil)
	if e.Enabled() {
		t.Error("no endpoint should mean no exporter")
	}
	if err := e.ExportOnce(context.Background(), time.Now()); err != nil {
		t.Errorf("a disabled exporter should do nothing quietly, got %v", err)
	}
}

func TestMetricsURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://otlp.example.com":             "https://otlp.example.com/v1/metrics",
		"https://otlp.example.com/":            "https://otlp.example.com/v1/metrics",
		"https://otlp.example.com/v1/metrics":  "https://otlp.example.com/v1/metrics",
		"https://otlp.example.com/custom/path": "https://otlp.example.com/custom/path",
		"http://collector:4318":                "http://collector:4318/v1/metrics",
		"":                                     "",
	} {
		if got := MetricsURL(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// Nothing to say is not an error, and must not move the window on: the
// next export should still cover the messages that arrive meanwhile.
func TestNothingToReport(t *testing.T) {
	e, got, _ := newTestExporter(t, &fakeSource{}, Config{})
	if err := e.ExportOnce(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 0 {
		t.Error("an empty cell should send no payload at all")
	}
}

// Pointed at our own ingest, the cell measures its own reporting: every
// export produces traffic, which is exported, which produces traffic.
func TestPointsAtSelf(t *testing.T) {
	cases := []struct {
		endpoint, self string
		want           bool
	}{
		{"http://localhost:4318/v1/metrics", "http://localhost:4318", true},
		{"https://cell.example.com", "https://cell.example.com/v1/traces", true},
		{"localhost:4318", "http://localhost:4318", true},
		{"https://otlp.dynatrace.com/v1/metrics", "https://cell.example.com", false},
		// A cell that was never told its own ingest cannot be compared,
		// and guessing would warn on every deployment that omits it.
		{"https://otlp.example.com", "", false},
		{"", "https://cell.example.com", false},
	}
	for _, c := range cases {
		if got := PointsAtSelf(c.endpoint, c.self); got != c.want {
			t.Errorf("PointsAtSelf(%q, %q) = %v, want %v", c.endpoint, c.self, got, c.want)
		}
	}
}
