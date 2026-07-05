// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"encoding/json"
	"sort"
)

// NotificationContent controls which enrichment blocks a rule's
// notifications include, and optionally carries an inline Liquid email
// override. The zero value = no enrichment + org-default email template, so a
// rule created before this feature behaves exactly as it did. Stored per-rule
// as alert_rules.notification_config (JSONB) and carried on DeliveryJob so the
// delivery worker renders without re-fetching the rule.
type NotificationContent struct {
	Service             bool `json:"service"`
	Integration         bool `json:"integration"`
	ServiceMetadata     bool `json:"service_metadata"`
	IntegrationMetadata bool `json:"integration_metadata"`
	Check               bool `json:"check"`
	// Optional inline Liquid email override. Empty → org default email
	// template (SetDefaultEmailTemplateResolver / the built-in default).
	EmailSubject string `json:"email_subject,omitempty"`
	EmailBody    string `json:"email_body,omitempty"`
}

// AlertContext is the documented data object exposed to notification
// templates (Liquid) and assembled into the canonical webhook JSON.
// Sub-objects are nil when out of scope (e.g. Service is nil for an
// integration- or org-scoped rule). It is resolved LIVE at delivery time
// (SetAlertContextResolver) so service/integration state + metadata reflect
// the moment the notification is sent, not when the rule fired.
type AlertContext struct {
	Alert       AlertFacts        `json:"alert"`
	Rule        RuleFacts         `json:"rule"`
	Check       *CheckFacts       `json:"check,omitempty"`
	Service     *ServiceFacts     `json:"service,omitempty"`
	Integration *IntegrationFacts `json:"integration,omitempty"`
	Org         OrgFacts          `json:"org"`
	SentAt      string            `json:"sent_at"`
}

type AlertFacts struct {
	State     string `json:"state"`    // "firing" | "resolved"
	Severity  string `json:"severity"` // "info" | "warning" | "critical"
	Summary   string `json:"summary"`  // built-in human summary
	StartedAt string `json:"started_at,omitempty"`
	Link      string `json:"link,omitempty"` // deep link into Sluicio
}

type RuleFacts struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Signal      string `json:"signal,omitempty"`
	Kind        string `json:"kind,omitempty"`
}

// CheckFacts is "what failed and by how much" — the firing check's metric,
// the value that breached, and the threshold it breached.
type CheckFacts struct {
	Name      string `json:"name,omitempty"`
	Metric    string `json:"metric,omitempty"`
	Value     string `json:"value,omitempty"`
	Threshold string `json:"threshold,omitempty"`
	Window    string `json:"window,omitempty"`
}

type ServiceFacts struct {
	Name       string            `json:"name"`
	Status     string            `json:"status,omitempty"`
	ErrorCount int               `json:"error_count"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type IntegrationFacts struct {
	Name     string            `json:"name"`
	Slug     string            `json:"slug,omitempty"`
	Status   string            `json:"status,omitempty"`
	Services []string          `json:"services,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type OrgFacts struct {
	Company     string `json:"company,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// alertContextResolver, when wired, supplies the heavy parts of an
// AlertContext — Service / Integration / Check — by reading the catalog,
// integrations and metadata stores. delivery.go fills the rest (Alert / Rule /
// Org / link) from the job, so this package stays free of those stores.
var alertContextResolver func(ctx context.Context, job DeliveryJob) (*AlertContext, error)

// SetAlertContextResolver wires the live enrichment provider. The resolver
// returns an AlertContext whose Service/Integration/Check are populated (the
// other fields are ignored — delivery owns them). Injected at startup.
func SetAlertContextResolver(f func(ctx context.Context, job DeliveryJob) (*AlertContext, error)) {
	alertContextResolver = f
}

// resolveEnrichment returns the live Service/Integration/Check for a job, or
// nils when no resolver is wired or it errors — enrichment is best-effort and
// never blocks delivery.
func resolveEnrichment(ctx context.Context, job DeliveryJob) (*ServiceFacts, *IntegrationFacts, *CheckFacts) {
	if alertContextResolver == nil {
		return nil, nil, nil
	}
	c, err := alertContextResolver(ctx, job)
	if err != nil || c == nil {
		return nil, nil, nil
	}
	return c.Service, c.Integration, c.Check
}

// defaultEmailTemplateResolver, when wired, returns the org's default email
// subject + HTML body (Liquid). Empty strings fall back to the built-in
// constants below. Injected so this package stays free of the settings store.
var defaultEmailTemplateResolver func(ctx context.Context) (subject, body string)

// SetDefaultEmailTemplateResolver wires the org-default email template
// provider (persisted under cell-settings).
func SetDefaultEmailTemplateResolver(f func(ctx context.Context) (subject, body string)) {
	defaultEmailTemplateResolver = f
}

// effectiveEmailTemplate resolves the email subject + body to render for a
// job: the rule's inline override wins; else the org default; else the
// built-in. Always returns non-empty templates.
func effectiveEmailTemplate(ctx context.Context, content NotificationContent) (subject, body string) {
	subject, body = content.EmailSubject, content.EmailBody
	if subject == "" || body == "" {
		var dSub, dBody string
		if defaultEmailTemplateResolver != nil {
			dSub, dBody = defaultEmailTemplateResolver(ctx)
		}
		if subject == "" {
			subject = firstNonEmpty(dSub, DefaultEmailSubject)
		}
		if body == "" {
			body = firstNonEmpty(dBody, DefaultEmailBody)
		}
	}
	return subject, body
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// bindings flattens the context to the map Liquid renders against, plus an
// `include` map so the default template can branch on the toggles
// ({% if include.service %}…{% endif %}).
func (c *AlertContext) bindings(content NotificationContent) map[string]any {
	raw, _ := json.Marshal(c)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	// Metadata reaches Liquid as a sorted [{key,value}] list so a template can
	// iterate it cleanly ({% for kv in service.metadata %}{{ kv.key }}…).
	if c.Service != nil {
		if svc, ok := m["service"].(map[string]any); ok {
			svc["metadata"] = metaPairs(c.Service.Metadata)
		}
	}
	if c.Integration != nil {
		if integ, ok := m["integration"].(map[string]any); ok {
			integ["metadata"] = metaPairs(c.Integration.Metadata)
		}
	}
	m["include"] = map[string]any{
		"service":              content.Service,
		"integration":          content.Integration,
		"service_metadata":     content.ServiceMetadata,
		"integration_metadata": content.IntegrationMetadata,
		"check":                content.Check,
	}
	return m
}

// metaPairs turns a metadata map into a key-sorted slice of {key,value} maps
// for Liquid iteration.
func metaPairs(meta map[string]string) []map[string]string {
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]string{"key": k, "value": meta[k]})
	}
	return out
}

// webhookPayload assembles the canonical webhook JSON: the core alert facts
// always, plus the enrichment blocks the rule toggled on (metadata nested
// inside its parent block, gated by its own toggle).
func (c *AlertContext) webhookPayload(content NotificationContent) map[string]any {
	p := map[string]any{
		"state":    c.Alert.State,
		"severity": c.Alert.Severity,
		"summary":  c.Alert.Summary,
		"rule":     c.Rule,
		"link":     c.Alert.Link,
		"sent_at":  c.SentAt,
		"source":   "sluicio",
	}
	if content.Check && c.Check != nil {
		p["check"] = c.Check
	}
	if content.Service && c.Service != nil {
		s := *c.Service
		if !content.ServiceMetadata {
			s.Metadata = nil
		}
		p["service"] = s
	}
	if content.Integration && c.Integration != nil {
		i := *c.Integration
		if !content.IntegrationMetadata {
			i.Metadata = nil
		}
		p["integration"] = i
	}
	return p
}

// DefaultEmailSubject / DefaultEmailBody are the built-in Liquid email
// templates used when neither the rule nor the org override them. The body is
// a self-contained responsive HTML layout; blocks are gated on the rule's
// content toggles so an un-toggled rule still produces a clean email.
const DefaultEmailSubject = `Sluicio{% if org.environment %} {{ org.environment }}{% endif %} — [{{ alert.state | upcase }}] {{ rule.name | default: alert.summary }}{% if org.company %} — {{ org.company }}{% endif %}`

const DefaultEmailBody = `<!doctype html>
<html><body style="margin:0;background:#f1f5f9;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#0f172a;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f1f5f9;padding:24px 0;">
    <tr><td align="center">
      <table role="presentation" width="600" cellpadding="0" cellspacing="0" style="background:#ffffff;border:1px solid #e2e8f0;border-radius:12px;overflow:hidden;">
        <tr><td style="background:{% if alert.state == 'firing' %}{% if alert.severity == 'critical' %}#b91c1c{% elsif alert.severity == 'warning' %}#b45309{% else %}#1d4ed8{% endif %}{% else %}#15803d{% endif %};padding:16px 24px;color:#ffffff;font-size:16px;font-weight:700;">
          {% if alert.state == 'firing' %}● FIRING{% else %}✓ RESOLVED{% endif %} · {{ alert.severity | capitalize }}
        </td></tr>
        <tr><td style="padding:24px;">
          <div style="font-size:18px;font-weight:700;margin-bottom:4px;">{{ rule.name | default: alert.summary }}</div>
          <div style="font-size:14px;color:#475569;white-space:pre-line;">{{ alert.summary }}</div>
          {% if include.check and check %}
          <div style="margin-top:16px;border-top:1px solid #e2e8f0;padding-top:12px;">
            <div style="font-size:11px;text-transform:uppercase;letter-spacing:.05em;color:#64748b;font-weight:600;">Failing check</div>
            <div style="font-size:14px;margin-top:4px;">{{ check.metric }} = <b>{{ check.value }}</b> (threshold {{ check.threshold }}{% if check.window %}, over {{ check.window }}{% endif %})</div>
          </div>
          {% endif %}
          {% if include.service and service %}
          <div style="margin-top:16px;border-top:1px solid #e2e8f0;padding-top:12px;">
            <div style="font-size:11px;text-transform:uppercase;letter-spacing:.05em;color:#64748b;font-weight:600;">Service</div>
            <div style="font-size:14px;margin-top:4px;"><b>{{ service.name }}</b>{% if service.status %} · {{ service.status }}{% endif %} · {{ service.error_count }} errors</div>
            {% if include.service_metadata and service.metadata %}<table style="margin-top:6px;font-size:13px;">{% for kv in service.metadata %}<tr><td style="color:#64748b;padding-right:10px;">{{ kv.key }}</td><td>{{ kv.value }}</td></tr>{% endfor %}</table>{% endif %}
          </div>
          {% endif %}
          {% if include.integration and integration %}
          <div style="margin-top:16px;border-top:1px solid #e2e8f0;padding-top:12px;">
            <div style="font-size:11px;text-transform:uppercase;letter-spacing:.05em;color:#64748b;font-weight:600;">Integration</div>
            <div style="font-size:14px;margin-top:4px;"><b>{{ integration.name }}</b>{% if integration.status %} · {{ integration.status }}{% endif %}</div>
            {% if include.integration_metadata and integration.metadata %}<table style="margin-top:6px;font-size:13px;">{% for kv in integration.metadata %}<tr><td style="color:#64748b;padding-right:10px;">{{ kv.key }}</td><td>{{ kv.value }}</td></tr>{% endfor %}</table>{% endif %}
          </div>
          {% endif %}
          {% if alert.link %}<div style="margin-top:24px;"><a href="{{ alert.link }}" style="display:inline-block;background:#0f172a;color:#ffffff;text-decoration:none;padding:10px 18px;border-radius:8px;font-size:14px;font-weight:600;">View in Sluicio</a></div>{% endif %}
        </td></tr>
        <tr><td style="padding:12px 24px;background:#f8fafc;border-top:1px solid #e2e8f0;font-size:12px;color:#94a3b8;">
          Sluicio{% if org.environment %} · {{ org.environment }}{% endif %}{% if org.company %} · {{ org.company }}{% endif %}
        </td></tr>
      </table>
    </td></tr>
  </table>
</body></html>`
