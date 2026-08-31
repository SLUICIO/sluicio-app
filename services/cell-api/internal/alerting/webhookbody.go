// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Custom webhook bodies: shaping the payload for a receiver that
// dictates its own schema.
//
// # Why this is not a Liquid template
//
// Email and Slack bodies are Liquid, and they produce PROSE. A webhook
// body produces a DATA STRUCTURE, and the difference decides the design.
//
// A text template that emits JSON breaks the moment a value contains a
// quote or a newline, and alert summaries contain both: integration
// names, error messages, attribute values. The receiver answers 400 and
// the alert is lost. Nothing on the way there says so — the failure
// surfaces as an alert that did not arrive, which is the worst time to
// start debugging and the hardest thing to notice.
//
// So the template is not text with holes in it. It is a JSON DOCUMENT,
// parsed as JSON before anything is substituted, and the substitution
// happens in the parsed tree. Encoding is the encoder's job. Producing
// invalid JSON is not merely discouraged here; it is unrepresentable.
//
// Two more properties fall out of that:
//
//   - A malformed template is rejected when it is SAVED, not when an
//     alert fires. It has to parse as JSON to be stored at all.
//   - A misspelled variable is rejected too. Paths are checked against
//     the same reflected schema the template palette is built from, so
//     `$alert.sumary` is an error at save time rather than an empty
//     string in production.
//
// # The substitution rules
//
// A string that is EXACTLY a reference — "$alert.severity" — is replaced
// by the value at that path with its type intact, so a number stays a
// number and a missing path becomes null.
//
// A string that CONTAINS references — "Alert: $alert.summary" — is
// interpolated as text. Missing paths render empty, matching how the
// prose templates behave.
//
// Object keys are never substituted. A receiver's schema is fixed; only
// the values it carries vary.

package alerting

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// FormatTemplate is the webhook config.format that selects a custom body.
const FormatTemplate = "template"

// MaxWebhookTemplateBytes bounds a stored template.
//
// Generous, because a receiver's schema can be verbose, and small enough
// that a paste accident is caught here rather than in the delivery
// worker's memory.
const MaxWebhookTemplateBytes = 32 * 1024

// refPattern matches a variable reference: $path or ${path}, where a path
// is dot-separated identifiers. The braced form exists so a reference can
// abut text that would otherwise extend the path — "${alert.state}s".
var refPattern = regexp.MustCompile(`\$\{([a-zA-Z_][\w.]*)\}|\$([a-zA-Z_][\w.]*)`)

// refPath pulls the path out of either form.
func refPath(m []string) string {
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

// TemplateReferencesEmail reports whether a body template mentions any
// email.* binding, so delivery only pays for rendering the email when the
// template actually asks for it - resolving the template ladder reads the
// settings store, and most webhook bodies never mention it.
//
// Scans the raw text rather than the parsed document: a template that no
// longer parses has already fallen back to the canonical payload, and
// answering "no" there costs nothing.
func TemplateReferencesEmail(tmpl string) bool {
	for _, m := range refPattern.FindAllStringSubmatch(tmpl, -1) {
		if p := refPath(m); p == "email" || strings.HasPrefix(p, "email.") {
			return true
		}
	}
	return false
}

// ValidateWebhookTemplate parses a template and checks every reference
// against the known variable paths.
//
// Returns the paths it found, so a caller can report them. An unknown
// path is an error rather than a warning: the whole point of validating
// here is that the author finds out now, and a warning somebody scrolls
// past is the same as no check at all.
func ValidateWebhookTemplate(tmpl string) ([]string, error) {
	trimmed := strings.TrimSpace(tmpl)
	if trimmed == "" {
		return nil, fmt.Errorf("body template is empty")
	}
	if len(tmpl) > MaxWebhookTemplateBytes {
		return nil, fmt.Errorf("body template is over %d KB", MaxWebhookTemplateBytes/1024)
	}
	var doc any
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return nil, fmt.Errorf("body template must be valid JSON: %v", err)
	}
	// The root has to be an object. Told here rather than discovered at
	// delivery time, where a non-object root would fall back to the
	// canonical payload and the author would be left wondering why their
	// template appeared to be ignored.
	if _, ok := doc.(map[string]any); !ok {
		return nil, fmt.Errorf("body template must be a JSON object at the top level")
	}
	known := knownTemplatePaths()
	var found []string
	var bad []string
	walkTemplateStrings(doc, func(s string) {
		for _, m := range refPattern.FindAllStringSubmatch(s, -1) {
			p := refPath(m)
			found = append(found, p)
			if !known[p] {
				bad = append(bad, p)
			}
		}
	})
	if len(bad) > 0 {
		return found, fmt.Errorf("unknown variable %s — see the variable list beside the editor", strings.Join(uniqueStrings(bad), ", "))
	}
	return uniqueStrings(found), nil
}

// RenderWebhookTemplate substitutes bindings into a validated template.
//
// Returns the decoded document, which the caller hands to the JSON
// encoder like any other payload. An unparseable template yields ok=false
// and the caller falls back to the canonical payload: a receiver that
// gets the wrong shape can at least be told something happened, which
// beats silence.
func RenderWebhookTemplate(tmpl string, bindings map[string]any) (map[string]any, bool) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(tmpl)), &doc); err != nil {
		return nil, false
	}
	out, ok := substitute(doc, bindings).(map[string]any)
	return out, ok
}

// substitute walks the parsed template, replacing references in string
// values. Keys are left alone; see the file comment.
func substitute(node any, b map[string]any) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			out[k] = substitute(child, b)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = substitute(child, b)
		}
		return out
	case string:
		return substituteString(v, b)
	default:
		return node
	}
}

func substituteString(s string, b map[string]any) any {
	// Whole-value reference: keep the value's own type. "$check.value"
	// should send 4.2, not "4.2", to a receiver whose schema says number.
	if m := refPattern.FindStringSubmatch(s); m != nil && m[0] == s {
		return lookupPath(b, refPath(m))
	}
	// Otherwise interpolate as text.
	return refPattern.ReplaceAllStringFunc(s, func(match string) string {
		m := refPattern.FindStringSubmatch(match)
		return stringify(lookupPath(b, refPath(m)))
	})
}

// lookupPath walks a dotted path through the bindings. A missing or
// non-traversable path is nil, which encodes as JSON null in a
// whole-value position and as "" when interpolated.
func lookupPath(b map[string]any, path string) any {
	var cur any = b
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[seg]
		if !ok {
			return nil
		}
	}
	return cur
}

func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

func walkTemplateStrings(node any, fn func(string)) {
	switch v := node.(type) {
	case map[string]any:
		for _, child := range v {
			walkTemplateStrings(child, fn)
		}
	case []any:
		for _, child := range v {
			walkTemplateStrings(child, fn)
		}
	case string:
		fn(v)
	}
}

// knownTemplatePaths is the set the palette advertises, so validation and
// the UI cannot disagree about what exists.
func knownTemplatePaths() map[string]bool {
	out := map[string]bool{}
	for _, v := range TemplateContextSchema() {
		out[v.Path] = true
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
