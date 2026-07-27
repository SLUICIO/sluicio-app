// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The sweep's job is to keep referenced telemetry OUT of the advisor's
// "unused" bucket. Every miss here becomes a suggestion to delete
// something a rule depends on, so these tests are mostly about what the
// sweep must NOT overlook: disabled config, split_by, legacy span
// fields, integration-scoped rules that name no service.

package demand

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/messageviews"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/monitoringtemplates"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tracecompletion"
)

var testOrg = uuid.MustParse("11111111-1111-1111-1111-111111111111")

// recorded drains the writer's buffer into a comparable set. The writer
// needs no ClickHouse for this: Record buffers regardless, and we never
// call Flush.
func recorded(t *testing.T, w *Writer) map[bucket]uint64 {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[bucket]uint64, len(w.buf))
	for k, v := range w.buf {
		out[k] = v
	}
	return out
}

// hasKey reports whether any bucket matches signal+service+key+kind,
// ignoring the day (which is "today" by construction).
func hasKey(got map[bucket]uint64, signal Signal, service, key string, kind ConsumerKind) bool {
	for b := range got {
		if b.signal == signal && b.service == service && b.key == key && b.kind == kind {
			return true
		}
	}
	return false
}

// stubConn satisfies driver.Conn without implementing it: Record only
// checks the field for non-nil and never calls through, so the embedded
// nil interface is never dereferenced.
type stubConn struct{ driver.Conn }

func newTestWriter() *Writer {
	// A non-nil conn is required for Record to buffer; the tests never
	// flush, so the value is never dereferenced.
	w := NewWriter(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	w.conn = stubConn{}
	return w
}

func newTestSweeper(w *Writer) *Sweeper {
	return &Sweeper{
		Writer: w, OrgID: testOrg,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// ── alert rules ──────────────────────────────────────────────────────

type stubRules struct{ rules []alerting.AlertRule }

func (s stubRules) ListRules(context.Context, uuid.UUID) ([]alerting.AlertRule, error) {
	return s.rules, nil
}

type stubCatalog struct{ services []string }

func (s stubCatalog) IntegrationServices(context.Context, uuid.UUID) ([]string, error) {
	return s.services, nil
}

func TestSweepAlertRules_metricKeysAndSplitBy(t *testing.T) {
	w := newTestWriter()
	s := newTestSweeper(w)
	s.Rules = stubRules{rules: []alerting.AlertRule{{
		Signal:      "metric",
		ServiceName: "payments",
		Spec: alerting.MetricRuleSpec{
			MetricName: "queue.depth",
			Attrs:      []alerting.AttrFilter{{Key: "queue_name", Op: "=", Value: "dlq"}},
			SplitBy:    "queue_name",
		},
	}}}
	if err := s.sweepAlertRules(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got := recorded(t, w)
	if !hasKey(got, SignalMetric, "payments", "queue.depth", KindRule) {
		t.Error("metric name not recorded — T1 would call a live metric unused")
	}
	if !hasKey(got, SignalMetric, "payments", "queue_name", KindRule) {
		t.Error("attribute key not recorded — T3 would call a filtered attribute dead weight")
	}
}

func TestSweepAlertRules_disabledRulesStillCount(t *testing.T) {
	// A paused rule's telemetry is still referenced: advising someone to
	// drop it breaks the rule the moment they re-enable it.
	w := newTestWriter()
	s := newTestSweeper(w)
	s.Rules = stubRules{rules: []alerting.AlertRule{{
		Signal: "metric", ServiceName: "payments", Enabled: false,
		Spec: alerting.MetricRuleSpec{MetricName: "paused.metric"},
	}}}
	if err := s.sweepAlertRules(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !hasKey(recorded(t, w), SignalMetric, "payments", "paused.metric", KindRule) {
		t.Error("disabled rule's metric was skipped")
	}
}

func TestSweepAlertRules_integrationScopeSpreadsOverServices(t *testing.T) {
	w := newTestWriter()
	s := newTestSweeper(w)
	integID := uuid.New()
	s.Catalog = stubCatalog{services: []string{"svc-a", "svc-b"}}
	s.Rules = stubRules{rules: []alerting.AlertRule{{
		Signal: "metric", IntegrationID: &integID,
		Spec: alerting.MetricRuleSpec{MetricName: "shared.metric"},
	}}}
	if err := s.sweepAlertRules(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got := recorded(t, w)
	for _, svc := range []string{"svc-a", "svc-b"} {
		if !hasKey(got, SignalMetric, svc, "shared.metric", KindRule) {
			t.Errorf("integration-scoped rule missed member service %q", svc)
		}
	}
}

// ── matchers ─────────────────────────────────────────────────────────

type stubMatchers struct {
	all []integrations.MatcherWithIntegration
}

func (s stubMatchers) AllMatchersWithIntegration(context.Context, uuid.UUID) ([]integrations.MatcherWithIntegration, error) {
	return s.all, nil
}

func TestSweepMatchers_skipsServiceMatchers(t *testing.T) {
	w := newTestWriter()
	s := newTestSweeper(w)
	s.Matchers = stubMatchers{all: []integrations.MatcherWithIntegration{
		{Matcher: integrations.Matcher{Attribute: "service.name", Value: "payments"}},
		{Matcher: integrations.Matcher{Attribute: "messaging.destination", Value: "orders"}},
	}}
	if err := s.sweepMatchers(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got := recorded(t, w)
	if !hasKey(got, SignalTrace, "", "messaging.destination", KindMatcher) {
		t.Error("attribute matcher not recorded")
	}
	// A service matcher names a service, not a telemetry attribute —
	// counting it would make "service.name" look like consumed data.
	if hasKey(got, SignalTrace, "", "service.name", KindMatcher) {
		t.Error("service matcher recorded as attribute demand")
	}
}

// ── completion rules ─────────────────────────────────────────────────

type stubCompletions struct{ rules []tracecompletion.Rule }

func (s stubCompletions) ListAll(context.Context, uuid.UUID) ([]tracecompletion.Rule, error) {
	return s.rules, nil
}

func TestSweepCompletions_legacyClosingSpansCountedOnce(t *testing.T) {
	// EffectiveStages folds ClosingSpanNames into the stage list; reading
	// both fields would double-count the same span name.
	w := newTestWriter()
	s := newTestSweeper(w)
	s.Catalog = stubCatalog{services: []string{"svc-a"}}
	s.Completions = stubCompletions{rules: []tracecompletion.Rule{{
		IntegrationID: uuid.New(),
		Spec: tracecompletion.RuleSpec{
			StartSpanName:    "order.received",
			ClosingSpanNames: []string{"order.delivered"},
		},
	}}}
	if err := s.sweepCompletions(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got := recorded(t, w)
	if !hasKey(got, SignalTrace, "svc-a", "order.received", KindCompletion) {
		t.Error("start span name not recorded")
	}
	for b, hits := range got {
		if b.key == "order.delivered" && hits != 1 {
			t.Errorf("legacy closing span counted %d times, want 1", hits)
		}
	}
}

// ── templates and views ──────────────────────────────────────────────

type stubTemplates struct {
	tpls []monitoringtemplates.Template
}

func (s stubTemplates) List(context.Context, uuid.UUID) ([]monitoringtemplates.Template, error) {
	return s.tpls, nil
}

func TestSweepTemplates_metricAndAttrKeys(t *testing.T) {
	w := newTestWriter()
	s := newTestSweeper(w)
	s.Templates = stubTemplates{tpls: []monitoringtemplates.Template{{
		Checks: []monitoringtemplates.Check{{
			Signal: "metric", Metric: "kafka.lag",
			Attrs:   []monitoringtemplates.AttrFilter{{Key: "topic"}},
			SplitBy: "partition",
		}},
	}}}
	if err := s.sweepTemplates(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got := recorded(t, w)
	for _, want := range []string{"kafka.lag", "topic", "partition"} {
		if !hasKey(got, SignalMetric, "", want, KindTemplate) {
			t.Errorf("template key %q not recorded", want)
		}
	}
}

type stubViews struct{ views []messageviews.View }

func (s stubViews) List(context.Context, uuid.UUID, *uuid.UUID) ([]messageviews.View, error) {
	return s.views, nil
}

func TestSweepViews_onlyPayloadFiltersCarryKeys(t *testing.T) {
	w := newTestWriter()
	s := newTestSweeper(w)
	s.Views = stubViews{views: []messageviews.View{{Filters: []messageviews.Filter{
		{Field: messageviews.FieldPayload, FieldPath: "order.id", Value: "x"},
		{Field: messageviews.FieldStatus, Value: "error"},
	}}}}
	if err := s.sweepViews(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got := recorded(t, w)
	if !hasKey(got, SignalTrace, "", "order.id", KindView) {
		t.Error("payload filter's attribute key not recorded")
	}
	if len(got) != 1 {
		t.Errorf("recorded %d buckets, want 1 — a built-in column was treated as an attribute key", len(got))
	}
}
