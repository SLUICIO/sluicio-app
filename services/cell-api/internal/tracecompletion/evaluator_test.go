// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tracecompletion

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeAlertStore records the calls fireDelayed makes so tests can assert
// on the notify-vs-suppress decision without a database.
type fakeAlertStore struct {
	active     *AlertInstance // returned by ActiveInstanceByFingerprint
	opened     int
	touched    int
	enqueued   [][]uuid.UUID
	suppressed []uuid.UUID // window ids stamped on the opened instance
}

func (f *fakeAlertStore) OpenInstance(ctx context.Context, ruleID uuid.UUID, fingerprint string, labels map[string]string, summary string) (AlertInstance, error) {
	f.opened++
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
