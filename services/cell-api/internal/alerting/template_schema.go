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
	"strings"
)

// TemplateVariable is one palette entry.
type TemplateVariable struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Description string `json:"description"`
	// Available says when the variable carries a value ("always", or the
	// scope condition, e.g. "metric-check rules only").
	Available string `json:"available"`
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

	"sent_at": {"when this notification was sent (RFC 3339)", "always"},
}

// TemplateContextSchema walks AlertContext's JSON shape and returns the
// documented variable list, path-sorted. Metadata maps collapse to a
// single "<prefix>.metadata.<key>" entry.
func TemplateContextSchema() []TemplateVariable {
	paths := map[string]string{} // path -> JSON type
	walkStruct(reflect.TypeOf(AlertContext{}), "", paths)
	out := make([]TemplateVariable, 0, len(paths))
	for path, typ := range paths {
		doc := templateVariableDocs[path] // zero value when missing — the test catches it
		out = append(out, TemplateVariable{Path: path, Type: typ, Description: doc.Description, Available: doc.Available})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
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
