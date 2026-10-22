// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ItemRequest.Validate is the gate every dashboard write passes through,
// and its default arm rejects anything it does not recognise. That is
// the right default — but it means adding an entity kind anywhere else
// (migration, store, frontend) without adding it HERE produces a
// complete-looking feature that 400s on save.
//
// That is not hypothetical: system_entity shipped through the migration,
// the store and the whole editor UI before this arm was written, and the
// only symptom was `unknown entityKind "system_entity"` on the first
// save. So this file pins the accepted set, and pins that each kind
// rejects the other kinds' target fields — the same discipline the
// chk_dashboard_item_shape constraint enforces in Postgres.

package dashboards

import "testing"

const someUUID = "11111111-2222-3333-4444-555555555555"

func TestValidateAcceptsEveryKind(t *testing.T) {
	cases := map[string]ItemRequest{
		"integration": {EntityKind: EntityIntegration, IntegrationID: someUUID},
		// Empty kind means "integration" for back-compat with payloads
		// written before the field existed.
		"defaulted integration": {IntegrationID: someUUID},
		"system":                {EntityKind: EntitySystem, SystemName: "svc-a", WidgetType: WidgetSystemHealth},
		"system entity":         {EntityKind: EntitySystemEntity, SystemID: someUUID, WidgetType: WidgetSystemHealth},
	}
	for name, req := range cases {
		if err := req.Validate(); err != nil {
			t.Errorf("%s: want accepted, got %v", name, err)
		}
	}
}

func TestValidateRejectsMalformedItems(t *testing.T) {
	cases := map[string]ItemRequest{
		"unknown kind":                {EntityKind: "fleet", IntegrationID: someUUID},
		"system entity without id":    {EntityKind: EntitySystemEntity},
		"system entity with bad id":   {EntityKind: EntitySystemEntity, SystemID: "not-a-uuid"},
		"system without name":         {EntityKind: EntitySystem},
		"integration without id":      {EntityKind: EntityIntegration},
		"integration with bad widget": {EntityKind: EntityIntegration, IntegrationID: someUUID, WidgetType: "nope"},
		// system_health is display-only: a system item always uses it and
		// an integration item may never pick it.
		"integration with system widget": {EntityKind: EntityIntegration, IntegrationID: someUUID, WidgetType: WidgetSystemHealth},
	}
	for name, req := range cases {
		err := req.Validate()
		if err == nil {
			t.Errorf("%s: want rejected, got nil", name)
			continue
		}
		if !IsValidationError(err) {
			t.Errorf("%s: want a validation error (mapped to 400), got %T", name, err)
		}
	}
}

func TestValidateKeepsTargetFieldsApart(t *testing.T) {
	// Exactly one target column is populated per kind. Letting a second
	// through would trip chk_dashboard_item_shape in Postgres, turning a
	// caller's mistake into a 500 instead of a 400 naming the field.
	cases := map[string]ItemRequest{
		"system entity carrying a name": {
			EntityKind: EntitySystemEntity, SystemID: someUUID, SystemName: "svc-a",
		},
		"system entity carrying an integration": {
			EntityKind: EntitySystemEntity, SystemID: someUUID, IntegrationID: someUUID,
		},
		"system carrying an id": {
			EntityKind: EntitySystem, SystemName: "svc-a", SystemID: someUUID,
		},
		"integration carrying a system id": {
			EntityKind: EntityIntegration, IntegrationID: someUUID, SystemID: someUUID,
		},
	}
	for name, req := range cases {
		if err := req.Validate(); err == nil {
			t.Errorf("%s: want rejected, got nil", name)
		}
	}
}

func TestIncludeRuleAndEnvironmentsValidation(t *testing.T) {
	ok := CreateRequest{Name: "Sweden prod", Environments: []string{"production", "production", ""},
		IncludeRule: &IncludeRule{Kind: "metadata", Key: "country", Value: " Sweden "}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid request refused: %v", err)
	}
	if ok.IncludeRule.Value != "Sweden" {
		t.Errorf("rule value not trimmed: %q", ok.IncludeRule.Value)
	}
	envs, _ := cleanEnvironments(ok.Environments)
	if len(envs) != 2 || envs[0] != "production" || envs[1] != "" {
		t.Errorf("environments: %q", envs)
	}
	for name, r := range map[string]*IncludeRule{
		"metadata without key":   {Kind: "metadata", Value: "Sweden"},
		"metadata without value": {Kind: "metadata", Key: "country"},
		"tag without value":      {Kind: "tag"},
		"unknown kind":           {Kind: "region", Value: "x"},
	} {
		if err := (CreateRequest{Name: "x", IncludeRule: r}).Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = string(rune('a' + i))
	}
	if err := (CreateRequest{Name: "x", Environments: many}).Validate(); err == nil {
		t.Error("21 environments accepted")
	}
}
