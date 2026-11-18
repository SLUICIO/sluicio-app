// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tracecompletion

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/envctx"
)

// fakeAlertStore records the calls fireDelayed makes so tests can assert
// on the notify-vs-suppress decision without a database.
type fakeAlertStore struct {
	active     *AlertInstance // returned by ActiveInstanceByFingerprint
	opened     int
	touched    int
	enqueued   [][]uuid.UUID
	suppressed []uuid.UUID // window ids stamped on the opened instance
	labels     map[string]string
	summary    string
}

func (f *fakeAlertStore) OpenInstance(ctx context.Context, ruleID uuid.UUID, fingerprint string, labels map[string]string, summary string) (AlertInstance, error) {
	f.opened++
	f.labels, f.summary = labels, summary
	return AlertInstance{ID: uuid.New()}, nil
}

func (f *fakeAlertStore) TouchInstance(ctx context.Context, id uuid.UUID) error {
	f.touched++
	return nil
}

func (f *fakeAlertStore) EnqueueJobs(ctx context.Context, instanceID uuid.UUID, channelIDs []uuid.UUID) error {
	f.enqueued = append(f.enqueued, channelIDs)
	return nil
}

func (f *fakeAlertStore) ActiveInstanceByFingerprint(ctx context.Context, ruleID uuid.UUID, fingerprint string) (*AlertInstance, error) {
	return f.active, nil
}

func (f *fakeAlertStore) ResolveInstance(ctx context.Context, id uuid.UUID, summary string) error {
	return nil
}

func (f *fakeAlertStore) SuppressingWindowForIntegration(ctx context.Context, orgID, integrationID uuid.UUID) (*uuid.UUID, error) {
	return nil, nil
}

func (f *fakeAlertStore) MarkInstanceSuppressed(ctx context.Context, instanceID, windowID uuid.UUID) error {
	f.suppressed = append(f.suppressed, windowID)
	return nil
}

func testEvaluator(alerts *fakeAlertStore) *Evaluator {
	return &Evaluator{
		alerts: alerts,
		orgID:  uuid.New(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func testRuleAndDelay() (Rule, TraceClassification) {
	rule := Rule{
		ID:            uuid.New(),
		IntegrationID: uuid.New(),
		Name:          "orders",
		ChannelIDs:    []uuid.UUID{uuid.New()},
	}
	c := TraceClassification{
		TraceID:          "abc123",
		State:            StateDelayed,
		DelayedStage:     1,
		DelayedStageName: "order-shipped",
		StartedAt:        time.Now().Add(-time.Minute),
	}
	return rule, c
}

func TestFireDelayedNotifiesWithoutWindow(t *testing.T) {
	alerts := &fakeAlertStore{}
	e := testEvaluator(alerts)
	rule, c := testRuleAndDelay()

	if err := e.fireDelayed(context.Background(), rule, c, nil); err != nil {
		t.Fatalf("fireDelayed: %v", err)
	}
	if alerts.opened != 1 {
		t.Fatalf("opened = %d, want 1", alerts.opened)
	}
	if len(alerts.enqueued) != 1 || len(alerts.enqueued[0]) != 1 {
		t.Fatalf("enqueued = %v, want one job set with the rule's channel", alerts.enqueued)
	}
	if len(alerts.suppressed) != 0 {
		t.Fatalf("suppressed = %v, want none", alerts.suppressed)
	}
}

func TestFireDelayedSuppressedByWindow(t *testing.T) {
	alerts := &fakeAlertStore{}
	e := testEvaluator(alerts)
	rule, c := testRuleAndDelay()
	window := uuid.New()

	if err := e.fireDelayed(context.Background(), rule, c, &window); err != nil {
		t.Fatalf("fireDelayed: %v", err)
	}
	// The instance still opens (windows mute delivery, not recording) but
	// is stamped suppressed and no jobs are enqueued.
	if alerts.opened != 1 {
		t.Fatalf("opened = %d, want 1", alerts.opened)
	}
	if len(alerts.enqueued) != 0 {
		t.Fatalf("enqueued = %v, want none during a maintenance window", alerts.enqueued)
	}
	if len(alerts.suppressed) != 1 || alerts.suppressed[0] != window {
		t.Fatalf("suppressed = %v, want [%s]", alerts.suppressed, window)
	}
}

func TestFireDelayedSticky(t *testing.T) {
	alerts := &fakeAlertStore{active: &AlertInstance{ID: uuid.New()}}
	e := testEvaluator(alerts)
	rule, c := testRuleAndDelay()
	window := uuid.New()

	// An already-open (trace, stage) firing is only touched — no new
	// instance, no jobs, no suppression stamp, window or not.
	if err := e.fireDelayed(context.Background(), rule, c, &window); err != nil {
		t.Fatalf("fireDelayed: %v", err)
	}
	if alerts.touched != 1 {
		t.Fatalf("touched = %d, want 1", alerts.touched)
	}
	if alerts.opened != 0 || len(alerts.enqueued) != 0 || len(alerts.suppressed) != 0 {
		t.Fatalf("open/enqueue/suppress = %d/%v/%v, want none on sticky touch",
			alerts.opened, alerts.enqueued, alerts.suppressed)
	}
}

// profileResolver stands in for notifyprofiles.Store: explicit channels
// win, otherwise the integration's profile.
type profileResolver struct {
	profile []uuid.UUID
	integ   *uuid.UUID
}

func (p *profileResolver) Resolve(_ context.Context, _ uuid.UUID, explicit []uuid.UUID, integrationID, _ *uuid.UUID) ([]uuid.UUID, error) {
	p.integ = integrationID
	if len(explicit) > 0 {
		return explicit, nil
	}
	return p.profile, nil
}

// A rule with no channels of its own used to send its first page
// nowhere; it now goes to its integration's notification profile, like
// every other check.
func TestFireDelayedRoutesThroughTheProfile(t *testing.T) {
	alerts := &fakeAlertStore{}
	e := testEvaluator(alerts)
	viaProfile := uuid.New()
	res := &profileResolver{profile: []uuid.UUID{viaProfile}}
	e.SetChannelResolver(res)
	rule, c := testRuleAndDelay()
	rule.ChannelIDs = []uuid.UUID{}

	if err := e.fireDelayed(context.Background(), rule, c, nil); err != nil {
		t.Fatalf("fireDelayed: %v", err)
	}
	if len(alerts.enqueued) != 1 || len(alerts.enqueued[0]) != 1 || alerts.enqueued[0][0] != viaProfile {
		t.Fatalf("enqueued = %v, want the profile's channel", alerts.enqueued)
	}
	if res.integ == nil || *res.integ != rule.IntegrationID {
		t.Fatalf("resolved for integration %v, want %s", res.integ, rule.IntegrationID)
	}

	// The rule's own channels still win over the profile.
	alerts.enqueued = nil
	own := uuid.New()
	rule.ChannelIDs = []uuid.UUID{own}
	c.TraceID = "def456"
	if err := e.fireDelayed(context.Background(), rule, c, nil); err != nil {
		t.Fatalf("fireDelayed: %v", err)
	}
	if len(alerts.enqueued) != 1 || alerts.enqueued[0][0] != own {
		t.Fatalf("enqueued = %v, want the rule's own channel", alerts.enqueued)
	}
}

// envRecorder is a resolver that records the environment it routed for.
type envRecorder struct {
	env    string
	hasEnv bool
}

func (r *envRecorder) Resolve(ctx context.Context, _ uuid.UUID, explicit []uuid.UUID, _, _ *uuid.UUID) ([]uuid.UUID, error) {
	r.env, r.hasEnv = envctx.From(ctx)
	return explicit, nil
}

// A firing carries what every other check's firing does (severity, rule
// name) and the environment of its own trace, which also picks the
// notification profile, as it does for a per-environment check.
func TestFireDelayedNamesSeverityRuleAndEnvironment(t *testing.T) {
	alerts := &fakeAlertStore{}
	e := testEvaluator(alerts)
	res := &envRecorder{}
	e.SetChannelResolver(res)
	rule, c := testRuleAndDelay()
	rule.Severity = "warning"
	c.Environment = "prod"

	if err := e.fireDelayed(context.Background(), rule, c, nil); err != nil {
		t.Fatalf("fireDelayed: %v", err)
	}
	l := alerts.labels
	if l["severity"] != "warning" || l["rule_name"] != "orders" || l[EnvironmentLabel] != "prod" || l["trace_id"] != "abc123" {
		t.Errorf("labels: %v", l)
	}
	if !strings.HasSuffix(alerts.summary, " [prod]") || !strings.Contains(alerts.summary, `of "orders"`) {
		t.Errorf("summary: %q", alerts.summary)
	}
	if !res.hasEnv || res.env != "prod" {
		t.Errorf("routed for environment %q (%v), want prod", res.env, res.hasEnv)
	}

	// A trace whose spans named no environment is not labelled with one.
	alerts.labels = nil
	c.TraceID, c.Environment = "def456", ""
	if err := e.fireDelayed(context.Background(), rule, c, nil); err != nil {
		t.Fatalf("fireDelayed: %v", err)
	}
	if _, has := alerts.labels[EnvironmentLabel]; has {
		t.Errorf("labels: %v, want no environment", alerts.labels)
	}
}
