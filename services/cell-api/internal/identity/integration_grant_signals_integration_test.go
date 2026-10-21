// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// A grant of an integration reaches that integration's own slice of
// metrics and logs, not only its messages. That only holds if the grant
// is resolved per signal: a policy narrowed to messages must not start
// reaching metrics and logs because the integration grant learned to.
package identity_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

func TestIntegrationGrantsResolvePerSignal(t *testing.T) {
	f := newAuthzFixture(t)
	universe := func(context.Context, uuid.UUID) ([]string, error) { return []string{"rabbitmq"}, nil }

	u := f.user("queues@acme", identity.RoleViewer)
	g := f.group("finance", u, identity.RoleViewer)
	invoices := f.integration("invoice-dispatch")
	shipments := f.integration("shipment-events")
	// Every signal for one queue; messages only for the other.
	f.policy(g, identity.AccessPolicyInput{Kind: identity.PolicyIntegration, TargetIntegrationID: invoices.String()})
	f.policy(g, identity.AccessPolicyInput{
		Kind: identity.PolicyIntegration, TargetIntegrationID: shipments.String(),
		Signals: []identity.Signal{identity.SignalMessages},
	})

	sets, err := f.store.ResolveAccessSets(f.ctx, u, f.org, stubExpand, stubExpandSys, universe)
	if err != nil {
		t.Fatalf("ResolveAccessSets: %v", err)
	}
	has := func(sig identity.Signal, id uuid.UUID) bool {
		_, ok := sets.IntegrationsFor(sig)[id]
		return ok
	}
	for _, sig := range []identity.Signal{identity.SignalMetrics, identity.SignalLogs, identity.SignalMessages} {
		if !has(sig, invoices) {
			t.Errorf("%s: the all-signal grant of invoice-dispatch is missing", sig)
		}
	}
	if !has(identity.SignalMessages, shipments) {
		t.Error("messages: the messages-only grant of shipment-events is missing")
	}
	for _, sig := range []identity.Signal{identity.SignalMetrics, identity.SignalLogs} {
		if has(sig, shipments) {
			t.Errorf("%s: a grant narrowed to messages reaches %s", sig, sig)
		}
	}
	// The integration grant is still not a service grant.
	if set, wild := sets.VisibleFor(identity.SignalMetrics); wild || len(set) != 0 {
		t.Errorf("an integration grant put services in the metrics set: %v (wildcard %v)", set, wild)
	}
}
