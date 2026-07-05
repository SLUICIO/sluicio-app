// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mapexec

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// ValidationResult carries the outcome of validating one document
// (the Map's input or output) against a pinned schema. `Skipped`
// marks the case where no schema was pinned, or where the schema's
// format isn't one we can validate yet (e.g. XSD validation isn't
// shipped in v1 — well-formed XML is the best we do for XML output).
//
// The shape mirrors the frontend's expectations 1:1.
type ValidationResult struct {
	Skipped    bool     `json:"skipped"`
	SkipReason string   `json:"skip_reason,omitempty"`
	Valid      bool     `json:"valid"`
	Errors     []string `json:"errors,omitempty"`
	SchemaName string   `json:"schema_name,omitempty"`
}

// Validate runs the appropriate validator for the given schema's
// format. Currently:
//
//   - json — full JSON Schema validation via santhosh-tekuri/jsonschema
//   - xml  — well-formed XML check (XSD validation pending)
//   - anything else — skipped with a reason
//
// If `schemaContent` is empty, validation is skipped with a reason
// (the schema row exists but has no body — happens for placeholder
// schemas).
func Validate(content string, schemaName, schemaFormat, schemaContent string) ValidationResult {
	res := ValidationResult{SchemaName: schemaName}

	if strings.TrimSpace(schemaContent) == "" {
		res.Skipped = true
		res.SkipReason = "pinned schema has no content; nothing to validate against"
		return res
	}

	switch strings.ToLower(strings.TrimSpace(schemaFormat)) {
	case "json", "avro", "openapi":
		return validateJSON(content, schemaName, schemaContent)
	case "xml", "xslt", "html":
		return validateXMLWellFormed(content, schemaName)
	default:
		res.Skipped = true
		res.SkipReason = fmt.Sprintf("validation not implemented for format %q yet", schemaFormat)
		return res
	}
}

// validateJSON parses the schema body as a JSON Schema and validates
// the document against it. Errors from the validator are flattened
// into a string slice keyed by JSON pointer for UI rendering.
//
// santhosh-tekuri/jsonschema is fully spec-compliant up to
// draft-2020-12; the schema document's `$schema` declaration picks
// the draft.
func validateJSON(content, schemaName, schemaSource string) ValidationResult {
	res := ValidationResult{SchemaName: schemaName}

	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource("schema.json", strings.NewReader(schemaSource)); err != nil {
		res.Errors = []string{fmt.Sprintf("schema body is not valid JSON Schema: %v", err)}
		return res
	}
	sch, err := c.Compile("schema.json")
	if err != nil {
		res.Errors = []string{fmt.Sprintf("schema compile failed: %v", err)}
		return res
	}

	var doc any
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		res.Errors = []string{fmt.Sprintf("document is not valid JSON: %v", err)}
		return res
	}
	if err := sch.Validate(doc); err != nil {
		// jsonschema's error type implements Unwrap()/Causes(); the
		// String() form gives a tree we can split into bullet lines.
		res.Errors = splitValidationErrors(err)
		return res
	}
	res.Valid = true
	return res
}

// splitValidationErrors turns the multi-line tree from jsonschema's
// error type into individual lines that read cleanly in the UI.
// Three cleanups happen here:
//
//   - trim the library's "jsonschema: " prefix it adds at the top
//   - replace the noisy "file:///abs/path/schema.json#/..." resource
//     URL with just "schema#/..." since the UI doesn't need the
//     filesystem path (the compiler internally resolves any URI we
//     hand it to an absolute file:// URL)
//   - drop empty lines so the bullet list is tight
func splitValidationErrors(err error) []string {
	raw := err.Error()
	// Strip the file:// prefix and anything up to schema.json so
	// "file:///long/path/schema.json#/required" → "schema#/required".
	raw = schemaURLNoise.ReplaceAllString(raw, "schema")

	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		t = strings.TrimPrefix(t, "jsonschema: ")
		out = append(out, t)
	}
	if len(out) == 0 {
		out = []string{raw}
	}
	return out
}

// schemaURLNoise matches the file:// URL the jsonschema library uses
// as a resource id when we compile from a string source. We swap it
// for a friendlier "schema" so error pointers read cleanly.
var schemaURLNoise = regexp.MustCompile(`file://[^#\s]*\.json`)

// validateXMLWellFormed parses the document with encoding/xml's
// decoder and reports whether it's syntactically valid XML. Full
// XSD validation needs libxml2 (cgo) which we haven't wired in yet.
// Until we do, "well-formed" is the contract — when XSD lands this
// switches to a real validator without changing the API shape.
func validateXMLWellFormed(content, schemaName string) ValidationResult {
	res := ValidationResult{SchemaName: schemaName}
	dec := xml.NewDecoder(strings.NewReader(content))
	for {
		_, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				res.Valid = true
				return res
			}
			res.Errors = []string{fmt.Sprintf("malformed XML: %v", err)}
			return res
		}
	}
}
