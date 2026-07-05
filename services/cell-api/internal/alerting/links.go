// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"os"
	"strings"
)

// PublicBaseURL is the public origin Sluicio is reached at, used to build
// deep links in notifications so a recipient can click straight to the
// alert. It's configured per-hosting via SLUICIO_APP_URL (the same env the
// password-reset links use). Empty when unset — callers then omit the link
// rather than emit a broken relative URL.
func PublicBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("SLUICIO_APP_URL")), "/")
}

// Link joins the configured public base URL with an in-app path (e.g.
// "/alerts"). Returns "" when no base URL is configured.
func Link(path string) string {
	base := PublicBaseURL()
	if base == "" {
		return ""
	}
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// withLink appends a "View in Sluicio: <url>" footer to a notification body
// when a deep link is available, leaving the body unchanged otherwise.
func withLink(body, link string) string {
	if link == "" {
		return body
	}
	return body + "\n\nView in Sluicio: " + link
}
