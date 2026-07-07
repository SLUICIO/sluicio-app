// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"testing"

	"github.com/google/uuid"
)

// Covers is the whole decision "does this window silence this rule" —
// worth pinning: a false positive silences real alerts, a false negative
// pages during announced maintenance.
func TestMaintenanceWindowCovers(t *testing.T) {
	integA, integB := uuid.New(), uuid.New()
	teamX, teamY := uuid.New(), uuid.New()

	ruleOnIntegA := AlertRule{IntegrationID: &integA}
	ruleOnSvc := AlertRule{ServiceName: "payments-api"}
	ruleOfTeamX := AlertRule{GroupID: &teamX}
	ruleOrgWide := AlertRule{}

	cases := []struct {
		name string
		w    MaintenanceWindow
		rule AlertRule
		want bool
	}{
		{"all_org covers unbound rule", MaintenanceWindow{ScopeKind: "all_org"}, ruleOrgWide, true},
		{"all_org covers integration rule", MaintenanceWindow{ScopeKind: "all_org"}, ruleOnIntegA, true},

		{"entities: matching integration", MaintenanceWindow{ScopeKind: "entities",
			IntegrationIDs: set(integA), ServiceNames: map[string]struct{}{}}, ruleOnIntegA, true},
		{"entities: other integration", MaintenanceWindow{ScopeKind: "entities",
			IntegrationIDs: set(integB), ServiceNames: map[string]struct{}{}}, ruleOnIntegA, false},
		{"entities: matching service (incl. system expansion)", MaintenanceWindow{ScopeKind: "entities",
			IntegrationIDs: set(), ServiceNames: names("payments-api")}, ruleOnSvc, true},
		{"entities: never covers unbound org-wide rules", MaintenanceWindow{ScopeKind: "entities",
			IntegrationIDs: set(integA), ServiceNames: names("payments-api")}, ruleOrgWide, false},

		{"group: own team's rule", MaintenanceWindow{ScopeKind: "group", GroupID: &teamX}, ruleOfTeamX, true},
		{"group: other team's rule", MaintenanceWindow{ScopeKind: "group", GroupID: &teamY}, ruleOfTeamX, false},
		{"group: org-wide rules stay loud", MaintenanceWindow{ScopeKind: "group", GroupID: &teamX}, ruleOrgWide, false},

		{"unknown kind fails toward delivery", MaintenanceWindow{ScopeKind: "everything"}, ruleOnIntegA, false},
	}
	for _, tc := range cases {
		if got := tc.w.Covers(tc.rule); got != tc.want {
			t.Errorf("%s: Covers = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func set(ids ...uuid.UUID) map[uuid.UUID]struct{} {
	m := map[uuid.UUID]struct{}{}
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func names(ns ...string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, n := range ns {
		m[n] = struct{}{}
	}
	return m
}
