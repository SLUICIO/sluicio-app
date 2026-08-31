// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The template-variable schema: the UI's palette is REFLECTED from
// AlertContext's JSON tags plus the description table below, so the
// struct stays the single source of truth and the palette cannot drift.
// TestTemplateSchemaComplete fails when a struct field lacks its table
// entry — adding a variable forces documenting it. Variable paths are a
// public contract once teams write templates: additive only, no renames
// (same discipline as the webhook payload).

package alerting

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// ScopeWebhook marks a variable offered only in the webhook body
// template.
const ScopeWebhook = "webhook"

// TemplateVariable is one palette entry.
type TemplateVariable struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Description string `json:"description"`
	// Available says when the variable carries a value ("always", or the
	// scope condition, e.g. "metric-check rules only").
	Available string `json:"available"`
	// Sample is what this path renders as against the preview's sample
	// firing — the difference between "check.value" meaning nothing and
	// meaning "4.2%". Empty when the sample has nothing for the path.
	Sample string `json:"sample,omitempty"`
	// Scope limits where a variable is offered. Empty = everywhere.
	// "webhook" = the webhook body template only: email.* is the rendered
	// email, which inside an email template would be the template asking
	// for its own output.
	Scope string `json:"scope,omitempty"`
}

// varDoc is the hand-maintained half: description + availability per
// path. Paths not in this table fail the completeness test.
type varDoc struct{ Description, Available string }

var templateVariableDocs = map[string]varDoc{
	"alert.state":       {"\"firing\" or \"resolved\"", "always"},
	"alert.severity":    {"info | warning | critical", "always"},
	"alert.summary":     {"the built-in human summary line", "always"},
	"alert.started_at":  {"when the alert started firing (RFC 3339)", "always"},
	"alert.link":        {"deep link into Sluicio", "when a public base URL is configured"},
	"alert.state_emoji": {"Slack colon code for the state (🔴/🟡/🔵 firing by severity, 🟢 resolved)", "always"},

	"rule.name":        {"the alert rule's name", "always"},
	"rule.description": {"the alert rule's description", "when set"},
	"rule.signal":      {"metric | log | trace", "always"},
	"rule.kind":        {"trace-rule kind (trace_error | trace_latency | …)", "trace rules only"},
	"rule.runbook":     {"the rule's runbook — what to do when this fires", "when the rule has one"},

	"check.name":      {"the firing check's name", "metric-check rules only"},
	"check.metric":    {"the metric that breached", "metric-check rules only"},
	"check.value":     {"the value that breached", "metric-check rules only"},
	"check.threshold": {"the threshold it breached", "metric-check rules only"},
	"check.window":    {"the evaluation window", "metric-check rules only"},

	"service.name":           {"the service's name", "service-scoped rules"},
	"service.status":         {"ok | errors | quiet | unhealthy", "service-scoped rules"},
	"service.error_count":    {"error traces in the current window", "service-scoped rules"},
	"service.metadata.<key>": {"a service metadata field (also iterable: {% for kv in service.metadata %})", "service-scoped rules with the metadata block enabled"},

	"integration.name":           {"the integration's name", "integration-scoped rules"},
	"integration.slug":           {"the integration's slug", "integration-scoped rules"},
	"integration.status":         {"ok | errors | quiet | unhealthy", "integration-scoped rules"},
	"integration.services":       {"member service names (iterable)", "integration-scoped rules"},
	"integration.metadata.<key>": {"an integration metadata field (also iterable)", "integration-scoped rules with the metadata block enabled"},

	"org.company":     {"the organization's company name", "when configured"},
	"org.environment": {"the cell's environment label", "when configured"},
	"org.product":     {"what this deployment calls itself — the cell's wordmark, or \"Sluicio\"", "always"},

	"sent_at": {"when this notification was sent (RFC 3339)", "always"},

	// The rule's content toggles, exposed to templates as `include.*`
	// (the built-in email body branches on them). Not AlertContext
	// fields, so they're appended explicitly below rather than reflected.
	"include.check":                {"true when the rule includes the failing-check block", "always"},
	"include.service":              {"true when the rule includes the service block", "always"},
	"include.integration":          {"true when the rule includes the integration block", "always"},
	"include.service_metadata":     {"true when the rule includes service metadata", "always"},
	"include.integration_metadata": {"true when the rule includes integration metadata", "always"},

	// Webhook-only: the rendered email, for a receiver that is itself an
	// email sender. See webhookEmailPaths.
	"email.subject": {"the alert email's subject, from the org/team template ladder", "webhook body templates"},
	"email.text":    {"the alert email's plaintext body", "webhook body templates"},
	"email.html":    {"the alert email's HTML body, exactly as an email channel would send it", "webhook body templates"},
}

// webhookEmailPaths are offered in the webhook body template only. They
// carry the rendered email so a receiver that sends mail (AhaSend,
// Postmark, SES) posts the message the org already designed, rather than
// a second, plainer one hand-written into the webhook body.
var webhookEmailPaths = map[string]string{
	"email.subject": "string",
	"email.text":    "string",
	"email.html":    "string",
}

// includePaths are the non-reflected `include.*` bindings (see above).
var includePaths = []string{
	"include.check",
	"include.integration",
	"include.integration_metadata",
	"include.service",
	"include.service_metadata",
}

// StarterSlackTitle / StarterSlackBody are what the editor offers as a
// starting point for Slack — they reproduce the notifier's built-in line
// so "start from the default" yields today's output, editable. They are
// NOT the runtime default: an empty Slack template still means "use the
// built-in line" (see effectiveSlackTemplate).
const StarterSlackTitle = ``

const StarterSlackBody = `{{ alert.state_emoji }} *[{{ alert.state | upcase }}]* {{ alert.summary }}{% if alert.link %}

<{{ alert.link }}|View in {{ org.product }}>{% endif %}`

// StarterTemplates is what the editors load when someone asks to start
// from the built-in template.
func StarterTemplates() map[string]string {
	return map[string]string{
		"email_subject": DefaultEmailSubject,
		"email_body":    DefaultEmailBody,
		"slack_title":   StarterSlackTitle,
		"slack_body":    StarterSlackBody,
	}
}

// TemplateContextSchema walks AlertContext's JSON shape and returns the
// documented variable list, path-sorted. Metadata maps collapse to a
// single "<prefix>.metadata.<key>" entry.
func TemplateContextSchema() []TemplateVariable {
	paths := map[string]string{} // path -> JSON type
	walkStruct(reflect.TypeOf(AlertContext{}), "", paths)
	for _, p := range includePaths {
		paths[p] = "boolean"
	}
	// Sample values come from the same context the preview renders, so
	// the palette shows exactly what a template would produce.
	samples := SampleAlertContext().bindings(NotificationContent{
		Service: true, Integration: true, ServiceMetadata: true, IntegrationMetadata: true, Check: true,
	})
	out := make([]TemplateVariable, 0, len(paths)+len(webhookEmailPaths))
	for path, typ := range paths {
		doc := templateVariableDocs[path] // zero value when missing — the test catches it
		out = append(out, TemplateVariable{
			Path: path, Type: typ, Description: doc.Description, Available: doc.Available,
			Sample: sampleFor(samples, path),
		})
	}
	// Not reflected from AlertContext: these are rendered output, not
	// context, and they exist only for the webhook body.
	for path, typ := range webhookEmailPaths {
		doc := templateVariableDocs[path]
		out = append(out, TemplateVariable{
			Path: path, Type: typ, Description: doc.Description, Available: doc.Available,
			Scope: ScopeWebhook,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// sampleFor resolves a dotted variable path against the sample bindings.
// The "<key>" segment of a metadata path resolves to the first pair in
// the sample map, so the palette can show a real key=value.
func sampleFor(bindings map[string]any, path string) string {
	var cur any = bindings
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		if seg == "<key>" {
			// The parent resolved to the metadata pair list.
			return ""
		}
		next, ok := m[seg]
		if !ok {
			return ""
		}
		// A metadata list ([{key,value}]) followed by "<key>": show the
		// first pair as "Team → Payments".
		if pairs, isPairs := next.([]map[string]string); isPairs && len(pairs) > 0 {
			return pairs[0]["key"] + " → " + pairs[0]["value"]
		}
		cur = next
	}
	switch v := cur.(type) {
	case string:
		return truncateSample(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case bool:
		return strconv.FormatBool(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return truncateSample(strings.Join(parts, ", "))
	}
	return ""
}

func truncateSample(v string) string {
	const max = 60
	if len(v) > max {
		return v[:max] + "…"
	}
	return v
}

func walkStruct(t reflect.Type, prefix string, paths map[string]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		switch {
		case ft.Kind() == reflect.Struct:
			walkStruct(ft, path, paths)
		case ft.Kind() == reflect.Map:
			paths[path+".<key>"] = "string"
		case ft.Kind() == reflect.Slice:
			paths[path] = "list"
		case ft.Kind() == reflect.Int:
			paths[path] = "number"
		default:
			paths[path] = "string"
		}
	}
}
