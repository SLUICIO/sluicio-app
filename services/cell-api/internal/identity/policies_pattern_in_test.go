// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package identity

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Two of three integrations, by name, in one policy: the "is one of"
// pattern. The names are matched exactly, so a name with a space in it
// is one value, not two.
func TestAPatternPolicyGrantsTheIntegrationsItNames(t *testing.T) {
	invoices, orders, shipments := uuid.New(), uuid.New(), uuid.New()
	var s Store
	s.SetEntityListers(func(context.Context, uuid.UUID) ([]namedEntity, error) {
		return []namedEntity{
			{ID: invoices, Name: "Invoice dispatch"},
			{ID: orders, Name: "Order processing"},
			{ID: shipments, Name: "Shipment events"},
		}, nil
	}, nil)

	acc, err := s.composeAccess(context.Background(), uuid.New(), []AccessPolicy{{
		Kind:       PolicyIntegration,
		Conditions: &PolicyExpr{Match: MatchIn, Values: []string{"Invoice dispatch", "Shipment events"}},
	}}, expandToShared, nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	for id, name := range map[uuid.UUID]string{invoices: "Invoice dispatch", shipments: "Shipment events"} {
		if _, ok := acc.Integrations[id]; !ok {
			t.Errorf("%s is named in the policy but not granted", name)
		}
	}
	if _, ok := acc.Integrations[orders]; ok {
		t.Error("Order processing is not named in the policy but was granted")
	}
	if len(acc.Services) != 0 {
		t.Errorf("a pattern grant of integrations granted their services: %v", acc.Services)
	}
}
