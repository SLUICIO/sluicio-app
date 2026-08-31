// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
			// A realistic runbook, so the palette's sample shows authors
			// (and agents) the shape that's actually useful: what to look
			// at, the usual cause, who to escalate to.
			Runbook: "Check checkout-api's upstream payment provider first — the usual cause is provider latency, not our code. If the provider is healthy, look at the recent deploy list. Escalate to #payments-oncall after 15 minutes.",
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
		// The wordmark is the real one, not a placeholder: the preview and
		// the variable palette are where a partner checks that their brand
		// reaches the email, and showing them "Sluicio" there would be the
		// bug this fixes wearing a different hat. Resolved off Background
		// because branding is a cell-level value — the resolver's context
		// only carries the settings read, not a per-request scope.
		Org:    OrgFacts{Company: "Acme", Environment: "prod", Product: ProductName(context.Background())},
		SentAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// RenderWebhookPreview renders a webhook channel's body against the sample
// firing context: the structural template when one is given, otherwise the
// built-in payload. Same code path delivery uses, so what the editor shows is
// what the receiver gets — a preview rendered by a second implementation
// would agree right up to the case that matters.
func RenderWebhookPreview(ctx context.Context, tmpl string, content NotificationContent, c *AlertContext) (string, error) {
	tmpl = strings.TrimSpace(tmpl)
	if tmpl == "" {
		raw, _ := json.MarshalIndent(c.webhookPayload(content), "", "  ")
		return string(raw), nil
	}
	if _, err := ValidateWebhookTemplate(tmpl); err != nil {
		return "", err
	}
	b := c.bindings(content)
	if TemplateReferencesEmail(tmpl) {
		// Resolve the real ladder, not a placeholder. A preview that shows
		// an empty email.html while delivery posts the org's designed mail
		// is the failure this whole editor exists to prevent: the author
		// concludes the binding is broken and rebuilds something that
		// worked. A zero job scopes the walk to the org/cell default -
		// which rule will fire is not knowable here, so a rule-inline
		// override cannot be previewed.
		b["email"] = c.emailParts(ctx, DeliveryJob{}, content, false,
			c.Alert.Summary, withLink(c.Alert.Summary, c.Org.Product, c.Alert.Link)).bindings()
	}
	rendered, ok := RenderWebhookTemplate(tmpl, b)
	if !ok {
		return "", errors.New("template could not be rendered")
	}
	raw, err := json.MarshalIndent(rendered, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
