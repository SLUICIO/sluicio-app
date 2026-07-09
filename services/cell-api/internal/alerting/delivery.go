// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
)

// The built-in notifiers register themselves at package init. Adding a
// channel type means implementing Notifier + Register()ing it here —
// no switch to edit. SMTP (email) is the standard/default channel.
func init() {
	Register(emailNotifier{})
	Register(webhookNotifier{})
	Register(slackNotifier{})
	Register(pagerdutyNotifier{})
}

// deliver renders a job into a channel-agnostic Message and hands it to
// the notifier registered for the channel's kind. A non-2xx / transport
// error propagates so the worker can retry. Returns the rendered
// Message too so the caller can record exactly what was sent (delivery
// history).
func deliver(ctx context.Context, client *http.Client, job DeliveryJob) (Message, error) {
	env, company := DeploymentContext(ctx)
	msg := messageFromJob(ctx, job, env, company)
	n, ok := notifierFor(job.Channel.Kind)
	if !ok {
		return msg, fmt.Errorf("no notifier registered for channel kind %q", job.Channel.Kind)
	}
	return msg, n.Send(ctx, client, msg)
}

// deploymentContext, when wired, returns the cell's environment label and
// the org/company name. They're woven into every notification's subject +
// body (and exposed as {{.environment}} / {{.company}} to custom templates)
// so a recipient instantly sees which deployment and company an alert is
// from. Injected at startup (SetDeploymentContextResolver) so this package
// stays free of the settings + identity stores.
var deploymentContext func(ctx context.Context) (env, company string)

// SetDeploymentContextResolver wires the environment + company provider.
func SetDeploymentContextResolver(f func(ctx context.Context) (env, company string)) {
	deploymentContext = f
}

// DeploymentContext returns the wired environment + company, or two empty
// strings when no resolver is set (then the decorations are simply omitted).
func DeploymentContext(ctx context.Context) (env, company string) {
	if deploymentContext == nil {
		return "", ""
	}
	return deploymentContext(ctx)
}

// notifSubject builds the standard notification title:
//
//	Sluicio {env} - {core} - {company}
//
// with the env / company segments omitted when unknown. core is the
// state-prefixed one-line summary (e.g. "[FIRING] …").
func NotifSubject(env, company, core string) string {
	head := "Sluicio"
	if env = strings.TrimSpace(env); env != "" {
		head += " " + env
	}
	s := head + " - " + core
	if company = strings.TrimSpace(company); company != "" {
		s += " - " + company
	}
	return s
}

// contextHeader is the leading body line carrying the deployment context
// ("Environment: prod · Company: Acme"); empty when neither is known.
func ContextHeader(env, company string) string {
	parts := make([]string, 0, 2)
	if env = strings.TrimSpace(env); env != "" {
		parts = append(parts, "Environment: "+env)
	}
	if company = strings.TrimSpace(company); company != "" {
		parts = append(parts, "Company: "+company)
	}
	return strings.Join(parts, " · ")
}

// messageFromJob builds the rendered Message for a job. When the owning
// rule has a title/body template, it's rendered against the firing
// context; otherwise we fall back to the built-in summary so behaviour
// is unchanged for rules without a custom template. A malformed
// template also falls back (renderTemplate returns ok=false) — a bad
// template must never block delivery.
func messageFromJob(ctx context.Context, job DeliveryJob, env, company string) Message {
	// Deep link straight to where the recipient can act on this alert, so
	// they can click through from the notification. Empty when no public
	// base URL is configured.
	link := ""
	if job.AlertInstanceID != uuid.Nil {
		link = Link(alertLinkPath(job))
	}

	// Legacy flat map for the back-compat Go text/template path.
	data := templateData(job)
	if link != "" {
		data["link"] = link
	}
	data["environment"] = env
	data["company"] = company

	// Rich context: core facts from the job + live enrichment (service /
	// integration / metadata) resolved at delivery time.
	actx := contextFromJob(job, env, company, link)
	if svc, integ, chk := resolveEnrichment(ctx, job); true {
		actx.Service, actx.Integration = svc, integ
		if chk != nil {
			actx.Check = chk
		}
	}
	content := job.Content
	legacyTmpl := job.BodyTemplate != "" || job.TitleTemplate != ""

	// Plaintext body + subject — the back-compat default, and the plaintext
	// fallback part of an HTML email.
	body := job.Summary
	if job.BodyTemplate != "" {
		if rendered, ok := renderTemplate(job.BodyTemplate, data); ok {
			body = rendered
		}
	} else if header := ContextHeader(env, company); header != "" {
		body = header + "\n\n" + body
	}
	subject := NotifSubject(env, company, stateSubject(job.State, job.Summary))
	if job.TitleTemplate != "" {
		if rendered, ok := renderTemplate(job.TitleTemplate, data); ok {
			subject = rendered
		}
	}
	if link != "" && !strings.Contains(body, link) {
		body = withLink(body, link)
	}

	msg := Message{
		State:    job.State,
		Severity: Severity(job.Labels["severity"]),
		Subject:  subject,
		Body:     body,
		Labels:   labelsWithLink(job.Labels, link),
		Config:   job.Channel.Config,
	}

	// Channel-kind enrichment: HTML Liquid email + canonical webhook JSON.
	switch job.Channel.Kind {
	case ChannelEmail:
		// Render the Liquid HTML email unless the rule carries a legacy Go
		// text/template (whose author chose plaintext). A bad template falls
		// back to the plaintext body via renderLiquid's ok=false.
		if !legacyTmpl {
			b := actx.bindings(content)
			subTmpl, bodyTmpl := effectiveEmailTemplate(ctx, content)
			if s, ok := renderLiquid(subTmpl, b); ok {
				msg.Subject = s
			}
			if h, ok := renderLiquid(bodyTmpl, b); ok {
				msg.BodyHTML = h
			}
		}
	case ChannelWebhook:
		msg.Payload = actx.webhookPayload(content)
	}
	return msg
}

// contextFromJob assembles the core AlertContext from the firing job (its
// denormalised labels + state/summary) plus the deployment context + deep
// link. The heavy Service/Integration/Check enrichment is overlaid by the
// resolver in messageFromJob; the Check basics (metric/value/threshold) come
// from the fire-time labels so "what failed and by how much" is always present.
func contextFromJob(job DeliveryJob, env, company, link string) *AlertContext {
	c := &AlertContext{
		Alert: AlertFacts{
			State:     job.State,
			Severity:  job.Labels["severity"],
			Summary:   job.Summary,
			StartedAt: job.Labels["started_at"],
			Link:      link,
		},
		Rule: RuleFacts{
			Name:   job.Labels["rule_name"],
			Signal: job.RuleSignal,
			Kind:   job.RuleKind,
		},
		Org:    OrgFacts{Company: company, Environment: env},
		SentAt: time.Now().UTC().Format(time.RFC3339),
	}
	if m := job.Labels["metric"]; m != "" || job.Labels["value"] != "" || job.Labels["threshold"] != "" {
		c.Check = &CheckFacts{
			Name:      job.Labels["rule_name"],
			Metric:    job.Labels["metric"],
			Value:     job.Labels["value"],
			Threshold: job.Labels["threshold"],
			Window:    job.Labels["window"],
		}
	}
	return c
}

// labelsWithLink returns job labels with the deep link added (a copy when a
// link is present, the original otherwise).
func labelsWithLink(labels map[string]string, link string) map[string]string {
	if link == "" {
		return labels
	}
	out := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		out[k] = v
	}
	out["link"] = link
	return out
}

// alertLinkPath picks the in-app destination for a firing alert's deep
// link. A failed-trace rule ("N errors on …") points at the Errors page
// for its scope — the integration's Errors view when bound to one, else
// the global errors overview — so a recipient lands where they can review
// and acknowledge the errors. Every other rule points at the alert itself
// on the Alerts page.
func alertLinkPath(job DeliveryJob) string {
	// Every destination carries ?instance=<alert-instance-id>: the target
	// page resolves it, pulses the matching row, and scrolls it into view
	// (frontend lib/useInstanceHighlight) — so the recipient lands on the
	// exact alert that paged them, not just the right page.
	instance := "instance=" + job.AlertInstanceID.String()
	if job.RuleSignal == SignalTraceError && job.RuleKind == TraceErrorSpecKind {
		if job.IntegrationID != nil {
			return "/integrations/" + job.IntegrationID.String() + "/errors?" + instance
		}
		return "/stuck?" + instance
	}
	return "/alerts?" + instance
}

// templateData is the variable map exposed to a rule's title/body
// templates: every denormalised label (rule_name, metric, value,
// threshold, severity, …) plus the firing state and the built-in
// summary, so a template can reference {{.summary}} as a base and
// decorate around it.
func templateData(job DeliveryJob) map[string]string {
	data := make(map[string]string, len(job.Labels)+2)
	for k, v := range job.Labels {
		data[k] = v
	}
	data["state"] = job.State
	data["summary"] = job.Summary
	return data
}

// renderTemplate executes a Go text/template against data. Returns
// (rendered, true) on success; (\"\", false) on a parse or execution
// error so the caller can fall back. Missingkey=zero keeps an
// unknown {{.foo}} as an empty string rather than erroring — a typo
// in a user template degrades gracefully instead of dropping the
// notification.
func renderTemplate(tmpl string, data map[string]string) (string, bool) {
	t, err := template.New("notif").Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return "", false
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", false
	}
	return buf.String(), true
}

// ValidateTemplate parses tmpl and returns a non-nil error if it isn't
// a syntactically valid Go text/template. The empty string is valid
// (means "no template"). Used by the API to reject a malformed
// title/body template at create/update time, rather than silently
// falling back at delivery.
func ValidateTemplate(tmpl string) error {
	if tmpl == "" {
		return nil
	}
	_, err := template.New("notif").Option("missingkey=zero").Parse(tmpl)
	return err
}

// stateSubject prefixes a one-line body with the firing/resolved state
// so a glance at an inbox / title conveys what happened. Used as the
// default subject when a rule has no custom title template.
func stateSubject(state, body string) string {
	prefix := "RESOLVED"
	if state == "firing" {
		prefix = "FIRING"
	}
	return fmt.Sprintf("[%s] %s", prefix, firstLine(body))
}

// firstLine returns the first line of s, so a multi-line body still
// yields a sensible single-line subject.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// DeliverTest sends a one-off sample notification to a channel so an
// operator can confirm the destination works before wiring it to a
// rule. Bypasses the job queue; uses a short timeout. Any delivery
// error (bad SMTP creds, unreachable webhook, …) is returned so the
// caller can surface it.
func DeliverTest(ctx context.Context, ch NotificationChannel) error {
	job := DeliveryJob{
		Channel: ch,
		State:   "firing",
		Summary: fmt.Sprintf("Test notification from Sluicio (channel %q)", ch.Name),
		Labels:  map[string]string{"severity": "info", "source": "test"},
	}
	client := &http.Client{Timeout: 15 * time.Second}
	_, err := deliver(ctx, client, job)
	return err
}

// ── email (SMTP) — the standard/default channel ──────────────────────

// systemMailDefaults, when wired, returns the org's system SMTP settings as
// channel-config keys (smtp_host/smtp_port/from/username/password). Email
// channels inherit these unless they set their own. Injected at startup
// (SetSystemMailResolver) so this package stays free of the settings store.
var systemMailDefaults func(ctx context.Context) map[string]string

// SetSystemMailResolver wires the system SMTP provider used as the default
// transport for email channels that don't carry their own server config.
func SetSystemMailResolver(f func(ctx context.Context) map[string]string) {
	systemMailDefaults = f
}

// effectiveMailConfig overlays a channel's email config on top of the
// system SMTP defaults: every key the channel sets (non-empty) wins; the
// rest fall back to system settings. With no resolver wired, the channel
// config is used as-is (legacy self-contained behaviour).
func effectiveMailConfig(ctx context.Context, channel map[string]string) map[string]string {
	if systemMailDefaults == nil {
		return channel
	}
	merged := systemMailDefaults(ctx)
	if merged == nil {
		merged = map[string]string{}
	}
	for k, v := range channel {
		if strings.TrimSpace(v) != "" {
			merged[k] = v
		}
	}
	return merged
}

type emailNotifier struct{}

func (emailNotifier) Kind() string { return ChannelEmail }

// Send delivers a plain-text email over SMTP. Config keys: to (required,
// comma-separated recipients); smtp_host, smtp_port (default 587), from,
// username + password are OPTIONAL — when omitted they fall back to the
// org's system SMTP settings (Settings → System email), so a channel can
// just say "send to these addresses". A channel that sets its own keys
// overrides the system defaults per key. net/smtp.SendMail upgrades to
// STARTTLS when the server advertises it; implicit-TLS (465) isn't
// supported.
func (emailNotifier) Send(ctx context.Context, _ *http.Client, msg Message) error {
	cfg := effectiveMailConfig(ctx, msg.Config)
	host := strings.TrimSpace(cfg["smtp_host"])
	if host == "" {
		return fmt.Errorf("email channel has no SMTP host — set one on the channel or configure Settings → System email")
	}
	port := strings.TrimSpace(cfg["smtp_port"])
	if port == "" {
		port = "587"
	}
	from := strings.TrimSpace(cfg["from"])
	to := splitRecipients(cfg["to"])
	if from == "" || len(to) == 0 {
		return fmt.Errorf("email channel needs a from address (channel or system) and at least one to recipient")
	}
	var auth smtp.Auth
	if user := strings.TrimSpace(cfg["username"]); user != "" {
		auth = smtp.PlainAuth("", user, cfg["password"], host)
	}
	// multipart/alternative (plaintext + HTML) when an HTML body was rendered;
	// plain text/plain otherwise (legacy + fallback).
	var raw []byte
	if strings.TrimSpace(msg.BodyHTML) != "" {
		raw = emailMessageMultipart(from, to, msg.Subject, msg.Body, msg.BodyHTML)
	} else {
		raw = emailMessage(from, to, msg.Subject, msg.Body)
	}
	if err := smtp.SendMail(net.JoinHostPort(host, port), auth, from, to, raw); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// emailMessage builds a minimal RFC 822 text/plain message.
func emailMessage(from string, to []string, subject, body string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	return b.Bytes()
}

// emailMessageMultipart builds a multipart/alternative RFC 822 message with a
// text/plain part (fallback for non-HTML clients) and a text/html part.
func emailMessageMultipart(from string, to []string, subject, text, html string) []byte {
	const boundary = "----=_sluicio_alt_b0undary_2f8a"
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(text)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	b.WriteString(html)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

// splitRecipients parses a comma-separated recipient list.
func splitRecipients(raw string) []string {
	out := make([]string, 0)
	for _, p := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ── webhook (generic JSON) ───────────────────────────────────────────

type webhookNotifier struct{}

func (webhookNotifier) Kind() string { return ChannelWebhook }

func (webhookNotifier) Send(ctx context.Context, client *http.Client, msg Message) error {
	// The canonical, content-toggled payload when one was assembled
	// (messageFromJob for webhook channels); else the legacy fixed shape so
	// untouched rules / one-off sends are unchanged.
	if msg.Payload != nil {
		return postJSON(ctx, client, msg.Config["url"], msg.Payload)
	}
	return postJSON(ctx, client, msg.Config["url"], map[string]any{
		"state":   msg.State,
		"subject": msg.Subject,
		"summary": msg.Body,
		"labels":  msg.Labels,
		"sent_at": time.Now().UTC().Format(time.RFC3339),
		"source":  "sluicio",
	})
}

// ── slack (incoming webhook) ─────────────────────────────────────────

type slackNotifier struct{}

func (slackNotifier) Kind() string { return ChannelSlack }

func (slackNotifier) Send(ctx context.Context, client *http.Client, msg Message) error {
	icon := ":large_green_circle:"
	prefix := "RESOLVED"
	if msg.State == "firing" {
		prefix = "FIRING"
		switch msg.Severity {
		case SeverityCritical:
			icon = ":red_circle:"
		case SeverityWarning:
			icon = ":large_yellow_circle:"
		default:
			icon = ":large_blue_circle:"
		}
	}
	return postJSON(ctx, client, msg.Config["url"],
		map[string]any{"text": fmt.Sprintf("%s *[%s]* %s", icon, prefix, msg.Body)})
}

// ── pagerduty (Events API v2) ────────────────────────────────────────

type pagerdutyNotifier struct{}

func (pagerdutyNotifier) Kind() string { return ChannelPagerDuty }

func (pagerdutyNotifier) Send(ctx context.Context, client *http.Client, msg Message) error {
	action := "resolve"
	if msg.State == "firing" {
		action = "trigger"
	}
	sev := "warning"
	switch msg.Severity {
	case SeverityCritical:
		sev = "critical"
	case SeverityInfo:
		sev = "info"
	}
	dedup := msg.Labels["rule_id"]
	if dedup == "" {
		dedup = msg.Body
	}
	return postJSON(ctx, client, "https://events.pagerduty.com/v2/enqueue", map[string]any{
		"routing_key":  msg.Config["routing_key"],
		"event_action": action,
		"dedup_key":    dedup,
		"payload": map[string]any{
			"summary":   msg.Body,
			"severity":  sev,
			"source":    "sluicio",
			"component": msg.Labels["metric"],
		},
	})
}

// ── shared HTTP POST ─────────────────────────────────────────────────

func postJSON(ctx context.Context, client *http.Client, url string, body any) error {
	if url == "" {
		return fmt.Errorf("channel has no destination URL")
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("delivery returned %s: %s", resp.Status, string(snippet))
	}
	return nil
}
