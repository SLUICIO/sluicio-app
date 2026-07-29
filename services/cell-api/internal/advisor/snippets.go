// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Collector snippets — the actual deliverable of a telemetry
// suggestion. A finding without one is a complaint; the snippet is what
// turns it into a change someone can make in a minute.
//
// One dialect only: OTel Collector processors (design §8.5). It is the
// surface that is always the operator's to change, it matches the
// existing Trim panels so the two produce recognisably the same YAML,
// and it does not multiply into per-SDK templates we cannot keep
// current.
//
// Two habits every template here follows.
//
// It says what it does at the top, in a comment, in the operator's
// terms. Someone pastes this into a config they own and will read again
// in a year; "dropped because Sluicio said so" is not a maintainable
// reason to leave in a file.
//
// It never emits a whole-span drop. Sampling or dropping the spans of a
// service that belongs to an integration would break the completeness
// promise the product is sold on, and a snippet is the one artefact that
// escapes our guardrails the moment it is copied — so the guardrail has
// to live in what we are willing to WRITE, not only in what we are
// willing to suggest.
package advisor

import (
	"fmt"
	"strings"
)

// yamlEsc escapes a value for a double-quoted YAML/OTTL string.
func yamlEsc(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}

// reEsc escapes a literal for use inside an OTTL IsMatch regex.
func reEsc(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`.^$*+?()[]{}|\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// snippetDropMetric drops one metric name before it reaches Sluicio.
func snippetDropMetric(name string) string {
	return fmt.Sprintf(`# Stop ingesting the "%s" metric. Nothing in Sluicio reads it:
# no alert rule, no dashboard, no chart in the last 30 days.
# Remove this block to start collecting it again — no data is
# deleted, the metric simply stops arriving from here on.
processors:
  filter/sluicio-advisor:
    error_mode: ignore
    metrics:
      metric:
        - 'name == "%s"'

service:
  pipelines:
    metrics:
      processors: [filter/sluicio-advisor]`, name, yamlEsc(name))
}

// snippetLogFloor raises the severity floor for one service's logs.
func snippetLogFloor(service, floor string) string {
	sev := map[string]string{
		"info":  "SEVERITY_NUMBER_INFO",
		"warn":  "SEVERITY_NUMBER_WARN",
		"error": "SEVERITY_NUMBER_ERROR",
	}[floor]
	if sev == "" {
		sev = "SEVERITY_NUMBER_WARN"
	}
	return fmt.Sprintf(`# Stop shipping %s's logs below %s. Everything at
# %s and above keeps flowing, so you can still alert on it —
# only the quieter records nobody has opened are dropped.
processors:
  filter/sluicio-advisor:
    error_mode: ignore
    logs:
      log_record:
        - 'resource.attributes["service.name"] == "%s" and severity_number < %s'

service:
  pipelines:
    logs:
      processors: [filter/sluicio-advisor]`,
		service, strings.ToUpper(floor), strings.ToUpper(floor), yamlEsc(service), sev)
}

// snippetDeleteSpanAttr removes one attribute from a service's spans.
//
// The spans themselves keep flowing — this is the distinction that makes
// the suggestion safe on an integration's services, where dropping the
// span would destroy the message record the product exists to keep.
func snippetDeleteSpanAttr(service, key string) string {
	return fmt.Sprintf(`# Remove the "%s" attribute from %s's spans.
# The spans themselves keep flowing untouched — message history,
# integration health and error tracking are unaffected. Only this
# one attribute, which nothing in Sluicio reads, stops being stored.
processors:
  transform/sluicio-advisor:
    error_mode: ignore
    trace_statements:
      - context: span
        statements:
          - delete_key(attributes, "%s") where resource.attributes["service.name"] == "%s"

service:
  pipelines:
    traces:
      processors: [transform/sluicio-advisor]`,
		key, service, yamlEsc(key), yamlEsc(service))
}

// snippetDeleteAttrPattern removes a family of keys by regex — the
// header/payload echo case, where a service emits dozens of
// `http.request.header.*` keys that are individually small and
// collectively the largest thing it sends.
func snippetDeleteAttrPattern(service, prefix string) string {
	return fmt.Sprintf(`# Remove every "%s*" attribute from %s's spans.
# These are echoed request metadata — Sluicio reads none of them, and
# they are the kind of key that quietly grows as headers are added.
# The spans keep flowing; only these attributes stop being stored.
processors:
  transform/sluicio-advisor:
    error_mode: ignore
    trace_statements:
      - context: span
        statements:
          - delete_matching_keys(attributes, "^%s") where resource.attributes["service.name"] == "%s"

service:
  pipelines:
    traces:
      processors: [transform/sluicio-advisor]`,
		prefix, service, reEsc(prefix), yamlEsc(service))
}

// snippetRedactAttr masks values that look like personal data.
//
// Redaction rather than deletion, deliberately: the attribute is
// presumably there for a reason, and an operator dealing with a
// compliance finding needs the key to keep working while the values stop
// being retained. Hashing preserves "same customer twice" without
// storing who.
func snippetRedactAttr(service, key string) string {
	return fmt.Sprintf(`# The "%s" attribute on %s's spans carries values
# shaped like personal data (email addresses, national ID or IBAN
# patterns). Sluicio stores span attributes for the full retention
# period, so this is worth a look regardless of cost.
#
# This hashes the value instead of deleting the key: correlation still
# works ("the same customer twice"), the personal data stops being
# retained. Swap to delete_key() if the attribute is not needed at all.
processors:
  transform/sluicio-advisor:
    error_mode: ignore
    trace_statements:
      - context: span
        statements:
          - set(attributes["%s"], SHA256(attributes["%s"])) where resource.attributes["service.name"] == "%s" and attributes["%s"] != nil

service:
  pipelines:
    traces:
      processors: [transform/sluicio-advisor]`,
		key, service, yamlEsc(key), yamlEsc(key), yamlEsc(service), yamlEsc(key))
}
