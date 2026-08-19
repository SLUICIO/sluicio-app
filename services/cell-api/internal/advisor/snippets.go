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
// Three habits every template here follows.
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
//
// It never spells a component's type name inline (issue #16). Collector
// configuration is not version-stable — `otlphttp` was removed in
// v0.146.0 and renamed — so every name comes from collectorversion,
// resolved for the collector the target service actually runs. The
// names we emit today have been stable across the supported range, so
// this changes no output yet. That is the point: the next rename is a
// data change in one table rather than a hunt through format strings,
// and a component we have no record of produces an honest refusal
// instead of YAML that will not start.
package advisor

import (
	"fmt"
	"strings"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectorversion"
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
func snippetDropMetric(name string, t collectorversion.Target) (string, error) {
	filter, err := collectorversion.Name(collectorversion.FilterProcessor, t)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Stop ingesting the "%[1]s" metric. Nothing in Sluicio reads it:
# no alert rule, no dashboard, no chart in the last 30 days.
# Remove this block to start collecting it again — no data is
# deleted, the metric simply stops arriving from here on.
processors:
  %[2]s/sluicio-advisor:
    error_mode: ignore
    metrics:
      metric:
        - 'name == "%[3]s"'

service:
  pipelines:
    metrics:
      processors: [%[2]s/sluicio-advisor]`, name, filter, yamlEsc(name)), nil
}

// snippetLogFloor raises the severity floor for one service's logs.
func snippetLogFloor(service, floor string, t collectorversion.Target) (string, error) {
	filter, err := collectorversion.Name(collectorversion.FilterProcessor, t)
	if err != nil {
		return "", err
	}
	sev := map[string]string{
		"info":  "SEVERITY_NUMBER_INFO",
		"warn":  "SEVERITY_NUMBER_WARN",
		"error": "SEVERITY_NUMBER_ERROR",
	}[floor]
	if sev == "" {
		sev = "SEVERITY_NUMBER_WARN"
	}
	return fmt.Sprintf(`# Stop shipping %[1]s's logs below %[2]s. Everything at
# %[2]s and above keeps flowing, so you can still alert on it —
# only the quieter records nobody has opened are dropped.
processors:
  %[3]s/sluicio-advisor:
    error_mode: ignore
    logs:
      log_record:
        - 'resource.attributes["service.name"] == "%[4]s" and severity_number < %[5]s'

service:
  pipelines:
    logs:
      processors: [%[3]s/sluicio-advisor]`,
		service, strings.ToUpper(floor), filter, yamlEsc(service), sev), nil
}

// snippetDeleteSpanAttr removes one attribute from a service's spans.
//
// The spans themselves keep flowing — this is the distinction that makes
// the suggestion safe on an integration's services, where dropping the
// span would destroy the message record the product exists to keep.
func snippetDeleteSpanAttr(service, key string, t collectorversion.Target) (string, error) {
	transform, err := collectorversion.Name(collectorversion.TransformProcessor, t)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Remove the "%[1]s" attribute from %[2]s's spans.
# The spans themselves keep flowing untouched — message history,
# integration health and error tracking are unaffected. Only this
# one attribute, which nothing in Sluicio reads, stops being stored.
processors:
  %[3]s/sluicio-advisor:
    error_mode: ignore
    trace_statements:
      - context: span
        statements:
          - delete_key(attributes, "%[4]s") where resource.attributes["service.name"] == "%[5]s"

service:
  pipelines:
    traces:
      processors: [%[3]s/sluicio-advisor]`,
		key, service, transform, yamlEsc(key), yamlEsc(service)), nil
}

// snippetDeleteAttrPattern removes a family of keys by regex — the
// header/payload echo case, where a service emits dozens of
// `http.request.header.*` keys that are individually small and
// collectively the largest thing it sends.
func snippetDeleteAttrPattern(service, prefix string, t collectorversion.Target) (string, error) {
	transform, err := collectorversion.Name(collectorversion.TransformProcessor, t)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Remove every "%[1]s*" attribute from %[2]s's spans.
# These are echoed request metadata — Sluicio reads none of them, and
# they are the kind of key that quietly grows as headers are added.
# The spans keep flowing; only these attributes stop being stored.
processors:
  %[3]s/sluicio-advisor:
    error_mode: ignore
    trace_statements:
      - context: span
        statements:
          - delete_matching_keys(attributes, "^%[4]s") where resource.attributes["service.name"] == "%[5]s"

service:
  pipelines:
    traces:
      processors: [%[3]s/sluicio-advisor]`,
		prefix, service, transform, reEsc(prefix), yamlEsc(service)), nil
}

// snippetRedactAttr masks values that look like personal data.
//
// Redaction rather than deletion, deliberately: the attribute is
// presumably there for a reason, and an operator dealing with a
// compliance finding needs the key to keep working while the values stop
// being retained. Hashing preserves "same customer twice" without
// storing who.
func snippetRedactAttr(service, key string, t collectorversion.Target) (string, error) {
	transform, err := collectorversion.Name(collectorversion.TransformProcessor, t)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# The "%[1]s" attribute on %[2]s's spans carries values
# shaped like personal data (email addresses, national ID or IBAN
# patterns). Sluicio stores span attributes for the full retention
# period, so this is worth a look regardless of cost.
#
# This hashes the value instead of deleting the key: correlation still
# works ("the same customer twice"), the personal data stops being
# retained. Swap to delete_key() if the attribute is not needed at all.
processors:
  %[3]s/sluicio-advisor:
    error_mode: ignore
    trace_statements:
      - context: span
        statements:
          - set(attributes["%[4]s"], SHA256(attributes["%[4]s"])) where resource.attributes["service.name"] == "%[5]s" and attributes["%[4]s"] != nil

service:
  pipelines:
    traces:
      processors: [%[3]s/sluicio-advisor]`,
		key, service, transform, yamlEsc(key), yamlEsc(service)), nil
}
