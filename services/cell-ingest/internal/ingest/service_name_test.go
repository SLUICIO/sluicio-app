// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import "testing"

// resolveServiceName is the fallback that keeps a source which never set a
// service.name resource attribute (common for Collector receivers scraping
// third-party systems) from being dropped by the catalog's discovery filter,
// which skips rows whose ServiceName is empty. It must return the declared
// name verbatim when present,
// and "unknown_service" only when it's genuinely absent/empty.
func TestResolveServiceName(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]string
		want  string
	}{
		{"declared name is kept", map[string]string{"service.name": "orders-api"}, "orders-api"},
		{"empty value falls back", map[string]string{"service.name": ""}, UnknownService},
		{"missing key falls back", map[string]string{"service.namespace": "prod"}, UnknownService},
		{"nil map falls back", nil, UnknownService},
		{"whitespace name is preserved (matches the != '' filter)", map[string]string{"service.name": " "}, " "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveServiceName(tc.attrs); got != tc.want {
				t.Fatalf("resolveServiceName(%v) = %q, want %q", tc.attrs, got, tc.want)
			}
		})
	}
}
