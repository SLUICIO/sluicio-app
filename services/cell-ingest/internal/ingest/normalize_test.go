// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package ingest

import "testing"

func span(status string, attrs map[string]string) SpanRow {
	return SpanRow{StatusCode: status, SpanAttributes: attrs}
}

func TestMapHTTP5xxToError(t *testing.T) {
	rows := []SpanRow{
		// 0: the KrakenD case — 500 as attribute, status left Unset.
		span("Unset", map[string]string{"http.response.status_code": "500"}),
		// 1: same but status Ok (KrakenD server spans do this too).
		span("Ok", map[string]string{"http.response.status_code": "503"}),
		// 2: legacy semconv key.
		span("Unset", map[string]string{"http.status_code": "502"}),
		// 3: 4xx is not an error here.
		span("Ok", map[string]string{"http.response.status_code": "404"}),
		// 4: already Error — untouched, no mapped stamp.
		span("Error", map[string]string{"http.response.status_code": "500"}),
		// 5: no HTTP attributes at all.
		span("Unset", map[string]string{"messaging.system": "rabbitmq"}),
		// 6: non-numeric garbage doesn't map (or crash).
		span("Ok", map[string]string{"http.response.status_code": "teapot"}),
	}
	mapped := MapHTTP5xxToError(rows)
	if mapped != 3 {
		t.Fatalf("mapped = %d, want 3", mapped)
	}
	wantStatus := []string{"Error", "Error", "Error", "Ok", "Error", "Unset", "Ok"}
	wantStamp := []bool{true, true, true, false, false, false, false}
	for i, w := range wantStatus {
		if rows[i].StatusCode != w {
			t.Errorf("row %d: status = %q, want %q", i, rows[i].StatusCode, w)
		}
		if got := rows[i].SpanAttributes[MappedStatusAttrKey] == "true"; got != wantStamp[i] {
			t.Errorf("row %d: mapped stamp = %v, want %v", i, got, wantStamp[i])
		}
	}
	if rows[0].StatusMessage == "" {
		t.Error("mapped span should carry an explanatory status message")
	}
}
