// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Customers asked that an alert say which message it is about, which
// integration, and which environment, without anyone writing a template.

func TestRuleFilterOfReadsTheMessageRules(t *testing.T) {
	spec := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	attrs := []AttrFilter{{Key: "order.id", Op: "exists"}, {Key: "documents.failed", Op: "gt", Value: "0"}, {Key: "order.id", Op: "neq", Value: "x"}}

	f, ok := RuleFilterOf(SignalTraceError, TraceErrorSpecKind, spec(TraceErrorRuleSpec{Kind: TraceErrorSpecKind, Attrs: attrs}))
	if !ok || !f.ErrorsOnly || f.Logs {
		t.Fatalf("failed-trace rule: %+v %v", f, ok)
	}
	if got := strings.Join(f.Keys(), ","); got != "order.id,documents.failed" {
		t.Errorf("keys %q, want each once in the order written", got)
	}
	if f, ok := RuleFilterOf(SignalTraceError, TraceAttributeSpecKind, spec(TraceAttributeRuleSpec{Kind: TraceAttributeSpecKind, Attrs: attrs})); !ok || f.ErrorsOnly {
		t.Errorf("trace-attribute rule counts any span, not only failing ones: %+v %v", f, ok)
	}
	if f, ok := RuleFilterOf(SignalLog, "", spec(LogRuleSpec{Attrs: attrs})); !ok || !f.Logs {
		t.Errorf("log rule: %+v %v", f, ok)
	}
	if _, ok := RuleFilterOf(SignalTraceError, TraceErrorSpecKind, spec(TraceErrorRuleSpec{Kind: TraceErrorSpecKind})); ok {
		t.Error("a rule without a filter has no message attributes to name")
	}
	if _, ok := RuleFilterOf(SignalMetric, "", spec(MetricRuleSpec{Attrs: attrs})); ok {
		t.Error("a metric series is not a message")
	}
}

func TestAnAlertNamesItsIntegrationEnvironmentAndMessage(t *testing.T) {
	c := SampleAlertContext()
	// With the integration block switched off the name still goes out.
	content := NotificationContent{Service: true}
	p := c.webhookPayload(content)
	if integ, ok := p["integration"].(IntegrationFacts); !ok || integ.Name != "Order Pipeline" || integ.Metadata != nil {
		t.Errorf("webhook integration: %#v, want the name without metadata", p["integration"])
	}
	if p["environment"] != "production" {
		t.Errorf("webhook environment: %v", p["environment"])
	}
	if m, ok := p["message"].(*MessageFacts); !ok || m.Attributes["order.id"] != "ORD-10432" {
		t.Errorf("webhook message: %#v", p["message"])
	}

	lines := strings.Join(c.factLines(), "\n")
	for _, want := range []string{"Integration: Order Pipeline", "Environment: production", "order.id: ORD-10432", "payment.provider: acme-pay"} {
		if !strings.Contains(lines, want) {
			t.Errorf("plain-text facts lack %q:\n%s", want, lines)
		}
	}

	b := c.bindings(content)
	subject, ok := renderLiquid(DefaultEmailSubject, b)
	if !ok || !strings.Contains(subject, "Order Pipeline") || !strings.Contains(subject, "production") {
		t.Errorf("default subject %q lacks the integration or environment", subject)
	}
	body, ok := renderLiquid(DefaultEmailBody, b)
	if !ok || !strings.Contains(body, "ORD-10432") || !strings.Contains(body, "Integration <b>Order Pipeline</b>") || !strings.Contains(body, "Environment <b>production</b>") {
		t.Error("default email body lacks the message attributes, integration or environment")
	}
}

// A service-bound alert names the integrations its service is in.
func TestAServiceAlertNamesItsIntegrations(t *testing.T) {
	c := SampleAlertContext()
	c.Integration = nil
	c.Service.Integrations = []string{"Order Pipeline", "Returns"}
	if got := strings.Join(c.factLines(), "\n"); !strings.Contains(got, "Integrations: Order Pipeline, Returns") {
		t.Errorf("facts: %s", got)
	}
	if p := c.webhookPayload(NotificationContent{}); p["integrations"] == nil {
		t.Error("webhook lacks the service's integrations")
	}
	body, _ := renderLiquid(DefaultEmailBody, c.bindings(NotificationContent{}))
	if !strings.Contains(body, "Order Pipeline, Returns") {
		t.Error("default email lacks the service's integrations")
	}
}

// A delayed-trace webhook went out with an empty severity, no rule name,
// no environment and only a shortened trace id in the summary, so the
// reader copied the id and searched for it by hand.
func TestATraceCompletionWebhookCarriesTheTrace(t *testing.T) {
	t.Setenv("SLUICIO_APP_URL", "https://cell.example")
	integ := uuid.New()
	job := DeliveryJob{
		State: "firing", Summary: "Trace 2aebb86da164 is delayed", AlertInstanceID: uuid.New(),
		RuleSignal: "trace", RuleKind: "trace_completion", IntegrationID: &integ,
		Labels: map[string]string{
			"rule_kind": "trace_completion", "trace_id": "2aebb86da1640000aaaa", "environment": "prod",
			// What ClaimDueJobs fills in from the rule.
			"severity": "warning", "rule_name": "Send step must complete",
		},
	}
	job.Channel.Kind = ChannelWebhook
	msg := messageFromJob(context.Background(), job, "Sluicio", "", "")
	p := msg.Payload
	if p["severity"] != "warning" || p["environment"] != "prod" {
		t.Errorf("severity %v, environment %v", p["severity"], p["environment"])
	}
	if r, ok := p["rule"].(RuleFacts); !ok || r.Name != "Send step must complete" {
		t.Errorf("rule: %#v", p["rule"])
	}
	m, ok := p["message"].(*MessageFacts)
	want := "https://cell.example/traces/2aebb86da1640000aaaa?integration=" + integ.String()
	if !ok || m.TraceID != "2aebb86da1640000aaaa" || m.Link != want {
		t.Fatalf("message: %#v, want the trace and %s", p["message"], want)
	}
	if !strings.Contains(msg.Body, "Trace: "+want) {
		t.Errorf("plain-text body lacks the trace link:\n%s", msg.Body)
	}
}
