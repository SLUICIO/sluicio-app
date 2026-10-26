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

// A brand-new service's errors are routed through the integration its
// rules put it in, without waiting for the catalog to learn of it.
func TestIntegrationOf(t *testing.T) {
	ruled, catalogued := uuid.New(), uuid.New()
	n := &Notifier{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	n.catalogIntegrations = func(context.Context, string) ([]uuid.UUID, error) { return []uuid.UUID{catalogued}, nil }

	n.IntegrationsFor = func(context.Context, string) ([]uuid.UUID, error) { return []uuid.UUID{ruled, uuid.New()}, nil }
	if got := n.integrationOf(context.Background(), "svc"); got == nil || *got != ruled {
		t.Errorf("the rules decide when they answer: got %v", got)
	}

	for name, rules := range map[string]func(context.Context, string) ([]uuid.UUID, error){
		"failing": func(context.Context, string) ([]uuid.UUID, error) { return nil, errors.New("down") },
		"empty":   func(context.Context, string) ([]uuid.UUID, error) { return nil, nil },
		"unset":   nil,
	} {
		n.IntegrationsFor = rules
		if got := n.integrationOf(context.Background(), "svc"); got == nil || *got != catalogued {
			t.Errorf("%s rules fall back to the catalog: got %v", name, got)
		}
	}

	n.IntegrationsFor = nil
	n.catalogIntegrations = func(context.Context, string) ([]uuid.UUID, error) { return nil, nil }
	if got := n.integrationOf(context.Background(), "svc"); got != nil {
		t.Errorf("no integration anywhere routes to the default: got %v", got)
	}
}
