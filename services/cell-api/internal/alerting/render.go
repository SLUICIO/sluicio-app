// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import "github.com/osteele/liquid"

// liquidEngine is the shared Liquid engine for notification templates.
// Stateless + safe for concurrent ParseAndRender, so one instance serves the
// whole delivery worker pool.
var liquidEngine = liquid.NewEngine()

// renderLiquid renders a Liquid template against bindings. Returns
// (rendered, true) on success; ("", false) on a parse/execute error so the
// caller can fall back — a bad template must never block delivery (same
// swallow-and-fallback contract as renderTemplate for Go text/template).
func renderLiquid(tmpl string, bindings map[string]any) (string, bool) {
	if tmpl == "" {
		return "", false
	}
	out, err := liquidEngine.ParseAndRenderString(tmpl, liquid.Bindings(bindings))
	if err != nil {
		return "", false
	}
	return out, true
}

// ValidateLiquid parses tmpl and returns a non-nil error if it isn't valid
// Liquid. The empty string is valid (means "use the default template"). Used
// by the API to reject a malformed inline template at save time.
func ValidateLiquid(tmpl string) error {
	if tmpl == "" {
		return nil
	}
	if _, err := liquidEngine.ParseString(tmpl); err != nil {
		return err
	}
	return nil
}
