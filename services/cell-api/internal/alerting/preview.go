// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"encoding/json"
	"time"
)

// RenderForPreview renders a notification for the given channel kind against a
// context, without sending — powering the template preview endpoint. For email
// it returns (subject, HTML body); for slack ("", mrkdwn text); for webhook
// ("", pretty JSON); otherwise the plain summary. Previews render the CANDIDATE
// templates carried inline on content (the editors put the text being edited
// there), so the stored ladder isn't consulted — a zero job scopes the walk
// to nothing.
func RenderForPreview(ctx context.Context, kind string, content NotificationContent, c *AlertContext) (subject, body string) {
	switch kind {
	case ChannelEmail:
		b := c.bindings(content)
		subTmpl, bodyTmpl := effectiveEmailTemplate(ctx, DeliveryJob{}, content)
		subject, _ = renderLiquid(subTmpl, b)
		body, _ = renderLiquid(bodyTmpl, b)
		return subject, body
	case ChannelSlack:
		title, bodyT := effectiveSlackTemplate(ctx, DeliveryJob{}, content)
		if bodyT == "" {
			// No candidate template — show the built-in line, exactly what
			// an unconfigured channel posts.
			return "", c.Alert.StateEmoji + " *[" + map[bool]string{true: "FIRING", false: "RESOLVED"}[c.Alert.State == "firing"] + "]* " + c.Alert.Summary
		}
		b := c.bindings(content)
		text, _ := renderLiquid(bodyT, b)
		if title != "" {
			if t, ok := renderLiquid(title, b); ok && t != "" {
				text = "*" + t + "*\n" + text
			}
		}
		return "", text
	case ChannelWebhook:
		raw, _ := json.MarshalIndent(c.webhookPayload(content), "", "  ")
		return "", string(raw)
	default:
		return "", c.Alert.Summary
	}
}

// SampleAlertContext is a representative firing context for previews: a
// critical metric breach on a service that belongs to an integration, with
// metadata on both — so toggling any content block shows real-looking data.
func SampleAlertContext() *AlertContext {
	return &AlertContext{
		Alert: AlertFacts{
			State:      "firing",
			Severity:   "critical",
			Summary:    "error rate 4.2% over 5m exceeded 1% on checkout-api",
			StartedAt:  time.Now().UTC().Add(-3 * time.Minute).Format(time.RFC3339),
			Link:       "https://sluicio.example.com/alerts",
			StateEmoji: ":red_circle:",
		},
		Rule: RuleFacts{
			Name:        "Checkout error rate",
			Description: "Page when checkout-api error rate is sustained.",
			Signal:      "metric",
		},
		Check: &CheckFacts{
			Name:      "Checkout error rate",
			Metric:    "error_rate",
			Value:     "4.2%",
			Threshold: "1%",
			Window:    "5m",
		},
		Service: &ServiceFacts{
			Name:       "checkout-api",
			Status:     "unhealthy",
			ErrorCount: 128,
			Metadata: map[string]string{
				"Team":    "Payments",
				"On-call": "payments-oncall@example.com",
				"Tier":    "1",
			},
		},
		Integration: &IntegrationFacts{
			Name:     "Order Pipeline",
			Slug:     "order-pipeline",
			Status:   "errors",
			Services: []string{"order-gateway", "checkout-api", "order-processor"},
			Metadata: map[string]string{
				"Business Impact": "Revenue-critical",
				"Runbook":         "https://wiki.example.com/order-pipeline",
			},
		},
		Org:    OrgFacts{Company: "Acme", Environment: "prod"},
		SentAt: time.Now().UTC().Format(time.RFC3339),
	}
}
