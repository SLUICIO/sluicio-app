// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package errornotify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

// A brand-new service's errors are routed through the integrations its
// rules put it in, without waiting for the catalog to learn of it.
func TestIntegrationsOf(t *testing.T) {
	ruled, catalogued := uuid.New(), uuid.New()
	n := &Notifier{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	n.catalogIntegrations = func(context.Context, string) ([]uuid.UUID, error) { return []uuid.UUID{catalogued}, nil }

	n.IntegrationsFor = func(context.Context, string) ([]uuid.UUID, error) { return []uuid.UUID{ruled, uuid.New()}, nil }
	if got := n.integrationsOf(context.Background(), "svc"); len(got) != 2 || got[0] != ruled {
		t.Errorf("the rules decide when they answer: got %v", got)
	}

	for name, rules := range map[string]func(context.Context, string) ([]uuid.UUID, error){
		"failing": func(context.Context, string) ([]uuid.UUID, error) { return nil, errors.New("down") },
		"empty":   func(context.Context, string) ([]uuid.UUID, error) { return nil, nil },
		"unset":   nil,
	} {
		n.IntegrationsFor = rules
		if got := n.integrationsOf(context.Background(), "svc"); len(got) != 1 || got[0] != catalogued {
			t.Errorf("%s rules fall back to the catalog: got %v", name, got)
		}
	}

	n.IntegrationsFor = nil
	n.catalogIntegrations = func(context.Context, string) ([]uuid.UUID, error) { return nil, nil }
	if got := n.integrationsOf(context.Background(), "svc"); got != nil {
		t.Errorf("no integration anywhere: got %v", got)
	}
}

// Notifying about unacknowledged errors is off unless an integration
// turned it on; when the setting cannot be read the batch waits.
func TestOnlyIntegrationsThatAskedAreNotified(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	ctx := context.Background()
	n := &Notifier{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if got, ok := n.notifying(ctx, []uuid.UUID{a, b}); got != nil || !ok {
		t.Errorf("with no setting wired: %v %v, want nobody, settled", got, ok)
	}
	n.NotifyingIntegrations = func(_ context.Context, ids []uuid.UUID) ([]uuid.UUID, error) { return []uuid.UUID{b}, nil }
	if got, ok := n.notifying(ctx, []uuid.UUID{a, b}); len(got) != 1 || got[0] != b || !ok {
		t.Errorf("only b asked: got %v %v", got, ok)
	}
	if got, ok := n.notifying(ctx, nil); got != nil || !ok {
		t.Errorf("a service in no integration: %v %v, want nobody, settled", got, ok)
	}
	n.NotifyingIntegrations = func(context.Context, []uuid.UUID) ([]uuid.UUID, error) { return nil, errors.New("down") }
	if got, ok := n.notifying(ctx, []uuid.UUID{a}); got != nil || ok {
		t.Errorf("setting unreadable: %v %v, want nobody and not settled", got, ok)
	}
}
