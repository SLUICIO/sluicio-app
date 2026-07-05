// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/alerting"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/catalog"
)

// buildUnhealthyView is the grouping core: attribute failing checks + open
// errors to the integration/system they belong to, roll up a status, and route
// anything unattributable to "other". This exercises every attribution path.
func TestBuildUnhealthyView(t *testing.T) {
	int2 := uuid.MustParse("00000000-0000-0000-0000-0000000000e2")
	sysID := uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	ref := func(id, name, slug string) IntegrationRef { return IntegrationRef{ID: id, Name: name, Slug: slug} }

	summaries := []ServiceSummary{
		{ServiceName: "order-api", Status: "unhealthy", Integrations: []IntegrationRef{ref("int-1", "Order Sync", "order-sync")}},
		{ServiceName: "billing-api", Status: "errors", Integrations: []IntegrationRef{ref(int2.String(), "Billing", "billing")}},
		{ServiceName: "reports-api", Status: "errors", Integrations: []IntegrationRef{ref("int-3", "Reports", "reports")}},
		{ServiceName: "rmq-exporter", Status: "unhealthy"}, // system member, no integration
	}
	systems := []catalog.System{
		{ID: sysID, Name: "RabbitMQ prod", TypeKey: "rabbitmq", Members: []string{"rmq-exporter"}},
	}
	checks := []FailingCheck{
		{RuleName: "HTTP 5xx rate", Severity: alerting.SeverityCritical, Summary: "12% 5xx", StartedAt: now, TargetKind: "service", ServiceName: "order-api"},
		{RuleName: "Consumer lag", Severity: alerting.SeverityWarning, Summary: "lag 4k", StartedAt: now, TargetKind: "service", ServiceName: "rmq-exporter"},
		{RuleName: "SLA breach", Severity: alerting.SeverityCritical, Summary: "p99 up", StartedAt: now, TargetKind: "integration", IntegrationID: &int2, IntegrationName: "Billing"},
		{RuleName: "Ingest down", Severity: alerting.SeverityCritical, StartedAt: now, TargetKind: "global"},
	}
	openErrors := []OpenServiceError{
		{ServiceName: "billing-api", ErrorTraces: 87, LastErrorAt: now, SampleTrace: "abc"},
		{ServiceName: "reports-api", ErrorTraces: 3, LastErrorAt: now},
		{ServiceName: "lonely-api", ErrorTraces: 1, LastErrorAt: now}, // no integration/system
	}

	got := buildUnhealthyView(WindowSummary{}, checks, summaries, openErrors, systems)

	byID := map[string]UnhealthyEntity{}
	for _, e := range got.Integrations {
		byID[e.ID] = e
	}

	// INT001 — unhealthy from a service-bound check, no errors.
	i1, ok := byID["int-1"]
	if !ok || i1.Status != "unhealthy" || len(i1.FailingChecks) != 1 || len(i1.ErrorServices) != 0 {
		t.Fatalf("int-1: %+v", i1)
	}
	if i1.FailingChecks[0].OnService != "order-api" || i1.Name != "Order Sync" {
		t.Fatalf("int-1 detail: %+v", i1)
	}

	// INT002 — integration-bound check (unhealthy) AND a member error service.
	i2, ok := byID[int2.String()]
	if !ok || i2.Status != "unhealthy" || len(i2.FailingChecks) != 1 || len(i2.ErrorServices) != 1 {
		t.Fatalf("int-2: %+v", i2)
	}
	if i2.ErrorServices[0].ServiceName != "billing-api" || i2.ErrorServices[0].ErrorTraces != 87 {
		t.Fatalf("int-2 error service: %+v", i2.ErrorServices)
	}

	// INT003 — only an error service, no check → status "errors".
	i3, ok := byID["int-3"]
	if !ok || i3.Status != "errors" || len(i3.FailingChecks) != 0 || len(i3.ErrorServices) != 1 {
		t.Fatalf("int-3: %+v", i3)
	}

	// System — unhealthy from the check on its member service.
	if len(got.Systems) != 1 {
		t.Fatalf("systems: %+v", got.Systems)
	}
	sy := got.Systems[0]
	if sy.ID != sysID.String() || sy.Status != "unhealthy" || sy.TypeKey != "rabbitmq" || len(sy.FailingChecks) != 1 {
		t.Fatalf("system: %+v", sy)
	}
	if sy.FailingChecks[0].OnService != "rmq-exporter" {
		t.Fatalf("system check: %+v", sy.FailingChecks)
	}

	// Other — the global check and the orphan error service, nothing else.
	if len(got.Other.FailingChecks) != 1 || got.Other.FailingChecks[0].RuleName != "Ingest down" {
		t.Fatalf("other checks: %+v", got.Other.FailingChecks)
	}
	if len(got.Other.ErrorServices) != 1 || got.Other.ErrorServices[0].ServiceName != "lonely-api" {
		t.Fatalf("other errors: %+v", got.Other.ErrorServices)
	}

	// Ordering: unhealthy before errors.
	if got.Integrations[len(got.Integrations)-1].Status != "errors" {
		t.Fatalf("expected errors-status integrations last: %+v", got.Integrations)
	}

	want := map[string]int{"integrations_unhealthy": 2, "integrations_errors": 1, "systems_unhealthy": 1, "systems_errors": 0}
	for k, v := range want {
		if got.Counts[k] != v {
			t.Fatalf("counts[%s] = %d, want %d (%+v)", k, got.Counts[k], v, got.Counts)
		}
	}
}

// A check on a service that maps to BOTH an integration and a system should
// surface under each — the same failure explains both entities.
func TestBuildUnhealthyView_SharedService(t *testing.T) {
	sysID := uuid.MustParse("00000000-0000-0000-0000-0000000000b2")
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	summaries := []ServiceSummary{
		{ServiceName: "gateway", Status: "unhealthy", Integrations: []IntegrationRef{{ID: "int-x", Name: "Gateway Flow"}}},
	}
	systems := []catalog.System{{ID: sysID, Name: "API GW", TypeKey: "gateway", Members: []string{"gateway"}}}
	checks := []FailingCheck{{RuleName: "5xx", Severity: alerting.SeverityCritical, StartedAt: now, TargetKind: "service", ServiceName: "gateway"}}

	got := buildUnhealthyView(WindowSummary{}, checks, summaries, nil, systems)
	if len(got.Integrations) != 1 || len(got.Systems) != 1 {
		t.Fatalf("expected the check under both entity kinds: %d ints, %d systems", len(got.Integrations), len(got.Systems))
	}
	if len(got.Integrations[0].FailingChecks) != 1 || len(got.Systems[0].FailingChecks) != 1 {
		t.Fatalf("expected 1 check on each: %+v / %+v", got.Integrations[0], got.Systems[0])
	}
	if len(got.Other.FailingChecks) != 0 {
		t.Fatalf("attributed check should not fall through to other: %+v", got.Other.FailingChecks)
	}
}
