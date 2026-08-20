// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Issue #28: an integration grant must stay an integration grant.
//
// The defect this pins was invisible in review and total in effect. A
// policy granting ONE integration was lowered to its member service
// names on the way in, and from that point nothing could tell it apart
// from a grant of the services themselves — so on a runtime hosting
// several flows, a user given one integration could read all of them,
// telemetry included.
//
// The property below is the whole fix in one line: composing an
// integration policy must leave the integration identifiable and must
// NOT put its services in the allowlist unless asked.

package identity

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// expandToShared stands in for the catalog: every integration in these
// tests is carried by the same one service, which is the shape that
// broke — three Node-RED flows on one runtime.
func expandToShared(_ context.Context, _ uuid.UUID, _ uuid.UUID) ([]string, error) {
	return []string{"romaitab-nodered"}, nil
}

func integrationPolicy(id uuid.UUID, grantServices bool) AccessPolicy {
	return AccessPolicy{
		Kind:                PolicyIntegration,
		TargetIntegrationID: &id,
		GrantServices:       grantServices,
	}
}

func TestAnIntegrationGrantDoesNotBecomeAServiceGrant(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	var s Store

	acc, err := s.composeAccess(context.Background(), uuid.New(),
		[]AccessPolicy{integrationPolicy(a, false)}, expandToShared, nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	if _, ok := acc.Integrations[a]; !ok {
		t.Error("the granted integration is not in the resolved set, so nothing downstream can honour it")
	}
	if _, ok := acc.Integrations[b]; ok {
		t.Error("an integration nobody granted appeared in the resolved set")
	}
	// The heart of it. Before the fix this map held "romaitab-nodered",
	// which is what made every sibling integration on that service
	// readable: canSeeIntegration asks "can you see any member
	// service?", and the answer had become yes for all of them.
	if len(acc.Services) != 0 {
		t.Errorf("an integration-only grant put services in the allowlist: %v", acc.Services)
	}
	if acc.HasNoAccess() {
		t.Error("a user granted one integration reports as having no access at all")
	}
}

func TestGrantServicesRestoresTheOldMeaning(t *testing.T) {
	// Policies written before the flag existed are migrated to true,
	// because that was their meaning when somebody authored them.
	// Narrowing them on upgrade would silently remove access.
	var s Store
	acc, err := s.composeAccess(context.Background(), uuid.New(),
		[]AccessPolicy{integrationPolicy(uuid.New(), true)}, expandToShared, nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if _, ok := acc.Services["romaitab-nodered"]; !ok {
		t.Error("grant_services=true did not grant the member service")
	}
	if len(acc.Integrations) != 1 {
		t.Error("grant_services=true lost the integration itself")
	}
}

func TestTwoIntegrationsOnOneServiceStayDistinct(t *testing.T) {
	// The reproduction from the issue, at the level where it went wrong.
	// Both integrations expand to the same single service, so a model
	// keyed on service names cannot separate them however the policies
	// are written.
	a, b := uuid.New(), uuid.New()
	var s Store
	acc, err := s.composeAccess(context.Background(), uuid.New(),
		[]AccessPolicy{integrationPolicy(a, false)}, expandToShared, nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if _, ok := acc.Integrations[b]; ok {
		t.Fatal("granting A also granted B")
	}
	if len(acc.Integrations) != 1 {
		t.Errorf("expected exactly one granted integration, got %d", len(acc.Integrations))
	}
}

func TestNoPoliciesStillMeansNoAccess(t *testing.T) {
	// Deny-by-default is load-bearing and easy to break while adding a
	// new map: an empty Integrations map must not read as a grant.
	var s Store
	acc, err := s.composeAccess(context.Background(), uuid.New(), nil, expandToShared, nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !acc.HasNoAccess() {
		t.Error("a principal with no policies does not report as having no access")
	}
}
