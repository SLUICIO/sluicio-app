// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The rule column of an integration's Metrics tab. It counted every rule
// on the metric name in the cell, so on a broker with one integration
// per queue, a brand-new "Invoice dispatch" integration showed "1 rule"
// on rabbitmq.message.current although the only rule was bound to
// "Shipment events". These pin the count to the rules that are actually
// evaluated over the integration.

package alerting

import (
	"testing"

	"github.com/google/uuid"
)

func metricRule(metric string, threshold float64, sev Severity) AlertRule {
	return AlertRule{Signal: SignalMetric, Severity: sev, Spec: MetricRuleSpec{MetricName: metric, Threshold: threshold}}
}

func TestEvaluatesForIntegration(t *testing.T) {
	mine, sibling, sys := uuid.New(), uuid.New(), uuid.New()
	cases := []struct {
		name string
		rule AlertRule
		want bool
	}{
		{"bound to this integration", AlertRule{IntegrationID: &mine}, true},
		{"bound to a sibling integration", AlertRule{IntegrationID: &sibling}, false},
		{"global", AlertRule{}, true},
		// Reads the whole broker, not this queue's slice of it.
		{"bound to a member service", AlertRule{ServiceName: "rabbitmq"}, false},
		{"bound to a system", AlertRule{SystemID: &sys}, false},
		// System wins the precedence, so the rule reads the system.
		{"system and this integration", AlertRule{SystemID: &sys, IntegrationID: &mine}, false},
	}
	for _, c := range cases {
		if got := EvaluatesForIntegration(c.rule, mine); got != c.want {
			t.Errorf("%s: EvaluatesForIntegration = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSummarizeMetricRulesScopedToIntegration(t *testing.T) {
	shipments, invoices := uuid.New(), uuid.New()
	const metric = "rabbitmq.message.current"

	onShipments := metricRule(metric, 100, SeverityWarning)
	onShipments.IntegrationID = &shipments
	rules := []AlertRule{onShipments}

	// Cell-wide (the global Metrics page) still counts it.
	if got := SummarizeMetricRules(rules, nil)[metric].Count; got != 1 {
		t.Fatalf("cell-wide count = %d, want 1", got)
	}
	// The integration it is bound to counts it.
	keepFor := func(id uuid.UUID) func(AlertRule) bool {
		return func(r AlertRule) bool { return EvaluatesForIntegration(r, id) }
	}
	if got := SummarizeMetricRules(rules, keepFor(shipments))[metric].Count; got != 1 {
		t.Errorf("Shipment events count = %d, want 1", got)
	}
	// The sibling integration on the same broker does not: the bug.
	if sum, ok := SummarizeMetricRules(rules, keepFor(invoices))[metric]; ok {
		t.Errorf("Invoice dispatch shows %d rule(s) on %s, want none", sum.Count, metric)
	}

	// A global rule is evaluated over every integration, so both count it,
	// and its threshold is the one drawn on the sibling's sparkline.
	global := metricRule(metric, 500, SeverityCritical)
	rules = append(rules, global)
	if got := SummarizeMetricRules(rules, keepFor(shipments))[metric].Count; got != 2 {
		t.Errorf("Shipment events count with a global rule = %d, want 2", got)
	}
	sum := SummarizeMetricRules(rules, keepFor(invoices))[metric]
	if sum.Count != 1 || sum.Threshold != 500 || sum.Severity != SeverityCritical {
		t.Errorf("Invoice dispatch summary = %+v, want the global rule alone", sum)
	}
}
