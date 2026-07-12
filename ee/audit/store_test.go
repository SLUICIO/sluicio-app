// SPDX-License-Identifier: LicenseRef-Sluicio-Enterprise
//
// Copyright (c) ROMA IT AB. All rights reserved.
package audit

import (
	"encoding/json"
	"testing"
	"time"
)

// The chain hash must survive the JSONB round-trip: what Record hashes at
// write time has to equal what Verify re-derives from the stored payload
// (unmarshal into map[string]any, re-marshal with sorted keys). A struct
// in the metadata marshals in field-declaration order, so hashing the raw
// marshal broke verification for every config-import entry.
func TestCanonicalMetadataSurvivesJSONBRoundTrip(t *testing.T) {
	type counts struct {
		// Deliberately NOT alphabetical: declaration order ≠ sorted order.
		Updated int `json:"updated"`
		Created int `json:"created"`
		Skipped int `json:"skipped"`
	}
	cases := []struct {
		name string
		meta map[string]any
	}{
		{"nil", nil},
		{"empty", map[string]any{}},
		{"scalars", map[string]any{"name": "orders", "count": 3}},
		{"nested struct", map[string]any{
			"mode":       "replace",
			"source_org": "default",
			"report":     map[string]any{"tags": counts{Updated: 4, Created: 1, Skipped: 0}},
		}},
		{"struct value", map[string]any{"report": counts{Updated: 2}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			written := canonicalMetadata(tc.meta)

			// Simulate Verify's re-derivation from the JSONB column: the
			// stored bytes come back (key order arbitrary), get unmarshalled
			// into map[string]any and re-marshalled.
			rederived := "{}"
			var m map[string]any
			if err := json.Unmarshal(written, &m); err == nil && len(m) > 0 {
				b, err := json.Marshal(m)
				if err != nil {
					t.Fatalf("re-marshal: %v", err)
				}
				rederived = string(b)
			}
			if string(written) != rederived {
				t.Fatalf("canonical mismatch:\n write:  %s\n verify: %s", written, rederived)
			}

			// And the hash built from each side agrees.
			at := time.Date(2026, 7, 12, 6, 20, 27, 457834000, time.UTC)
			w := chainHash("prev", "org", "actor", "n", "e", "a", "t", "id", string(written), "ip", at)
			v := chainHash("prev", "org", "actor", "n", "e", "a", "t", "id", rederived, "ip", at)
			if w != v {
				t.Fatalf("hash mismatch: %s vs %s", w, v)
			}
		})
	}
}
