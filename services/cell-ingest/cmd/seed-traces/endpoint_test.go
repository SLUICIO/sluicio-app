// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The seeder defaults to localhost:4318 — the developer's own cell —
// which is right for the common case and wrong in the one that costs an
// afternoon: a second cell running on another port, seeded into the
// wrong place with a success message either way. SLUICIO_INGEST_URL
// exists to make redirecting it a one-liner, so the cases where it
// silently does nothing are the ones worth pinning.

package main

import "testing"

func TestIngestBaseResolution(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"unset falls back to the dev cell", "", defaultIngestBase},
		{"whitespace is not a value", "   ", defaultIngestBase},
		{"a plain base is taken as-is", "http://localhost:4319", "http://localhost:4319"},
		{"a trailing slash does not double up", "http://localhost:4319/", "http://localhost:4319"},
		// Every OTLP doc shows the full signal URL, so that is what
		// people paste. Treating it literally would derive
		// ".../v1/traces/v1/logs" and fail on two of three signals.
		{"a pasted traces URL is trimmed back", "http://localhost:4319/v1/traces", "http://localhost:4319"},
		{"a pasted logs URL is trimmed back", "http://localhost:4319/v1/logs", "http://localhost:4319"},
		{"a pasted metrics URL is trimmed back", "http://localhost:4319/v1/metrics", "http://localhost:4319"},
		{"a real host survives", "https://cell.example.com", "https://cell.example.com"},
		// A gateway path that merely CONTAINS v1 must not be mangled.
		{"an unrelated path is left alone", "https://gw.example.com/otlp", "https://gw.example.com/otlp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ingestBase(tc.raw); got != tc.want {
				t.Errorf("ingestBase(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestDerivedEndpointsAreDistinctSignalPaths(t *testing.T) {
	// The three endpoints are derived from one base, so a mistake here
	// would send every signal to the same path — which cell-ingest would
	// reject in a way that looks like a broken cell rather than a broken
	// seeder.
	base := ingestBase("http://localhost:4319/v1/traces")
	seen := map[string]bool{}
	for _, sig := range []string{"/v1/traces", "/v1/logs", "/v1/metrics"} {
		u := base + sig
		if seen[u] {
			t.Fatalf("duplicate endpoint derived: %s", u)
		}
		seen[u] = true
	}
	if len(seen) != 3 {
		t.Fatalf("derived %d endpoints, want 3", len(seen))
	}
}
