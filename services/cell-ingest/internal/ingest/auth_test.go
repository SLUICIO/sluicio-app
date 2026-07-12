// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package ingest

import (
	"net/http/httptest"
	"testing"
)

// Both documented ways of presenting an ingest key must work:
// "Authorization: Bearer <key>" (OTLP-idiomatic) and the
// X-Sluicio-Ingest-Key fallback. Bearer wins when both are present.
func TestExtractIngestKey(t *testing.T) {
	cases := []struct {
		name   string
		bearer string
		xkey   string
		want   string
	}{
		{"none", "", "", ""},
		{"bearer only", "Bearer slk_abc", "", "slk_abc"},
		{"bearer case-insensitive", "bearer slk_abc", "", "slk_abc"},
		{"x-header only", "", "slk_xyz", "slk_xyz"},
		{"x-header trimmed", "", "  slk_xyz  ", "slk_xyz"},
		{"bearer wins over x-header", "Bearer slk_abc", "slk_xyz", "slk_abc"},
		{"basic auth is not an ingest key", "Basic dXNlcjpwYXNz", "slk_xyz", "slk_xyz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/traces", nil)
			if tc.bearer != "" {
				r.Header.Set("Authorization", tc.bearer)
			}
			if tc.xkey != "" {
				r.Header.Set("X-Sluicio-Ingest-Key", tc.xkey)
			}
			if got := extractIngestKey(r); got != tc.want {
				t.Fatalf("extractIngestKey = %q, want %q", got, tc.want)
			}
		})
	}
}
