// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const invoiceIntegID = "11111111-2222-3333-4444-555555555555"

// The integration the trace questions are asked about: its view calls
// the iId attribute "Invoice Id".
var invoiceReplies = map[string]string{
	"/api/v1/integrations": `{"integrations":[{"id":"` + invoiceIntegID + `","name":"Invoices","slug":"invoices"}]}`,
	"/api/v1/integrations/" + invoiceIntegID: `{"integration":{"id":"` + invoiceIntegID + `","name":"Invoices",
		"message_columns":[{"kind":"builtin","key":"service","label":"Service"},{"kind":"attribute","key":"iId","label":"Invoice Id"}]}}`,
	"/api/v1/messages/search": `{"total":2,"results":[
		{"trace_id":"aaa","promoted":{"iId":"123"}},
		{"trace_id":"bbb","promoted":{"iId":"123"}}],
		"message_columns":[{"kind":"attribute","key":"iId","label":"Invoice Id"}]}`,
	"/api/v1/traces/aaa": `{"trace_id":"aaa","spans":[{"span_id":"s1","span_attributes":{"iId":"123"}}]}`,
	"/api/v1/traces/bbb": `{"trace_id":"bbb","spans":[{"span_id":"s2"}]}`,
}

// callToolError runs a tool against a fake cell-api answering from
// invoiceReplies and returns its error text, or "" when it succeeded.
func callToolError(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if reply, ok := invoiceReplies[r.URL.Path]; ok {
			_, _ = w.Write([]byte(reply))
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer backend.Close()
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	var parsed struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(NewServer(backend.URL, "Bearer x").HandleMessage(msg), &parsed); err != nil {
		t.Fatal(err)
	}
	if !parsed.Result.IsError {
		return ""
	}
	return parsed.Result.Content[0].Text
}

func searchBody(t *testing.T, seen []recorded) map[string]any {
	t.Helper()
	for _, r := range seen {
		if r.path == "/api/v1/messages/search" {
			return r.body
		}
	}
	t.Fatal("no message search was made")
	return nil
}

func filtersOf(body map[string]any) []map[string]any {
	var out []map[string]any
	for _, f := range body["filters"].([]any) {
		out = append(out, f.(map[string]any))
	}
	return out
}

// "Invoice Id = 123 in Invoices" is searched as the telemetry says it,
// in the integration's slice, and lookups include messages that went
// through.
func TestSearchTracesByColumnLabelInAnIntegration(t *testing.T) {
	seen, text := callRecording(t, "sluicio_search_traces", map[string]any{
		"integration": "invoices",
		"attributes":  []any{map[string]any{"key": "invoice id", "value": float64(123)}},
	}, invoiceReplies)

	filters := filtersOf(searchBody(t, seen))
	var integ, attr map[string]any
	for _, f := range filters {
		switch f["field"] {
		case "status":
			t.Errorf("a lookup was narrowed to errors: %v", f)
		case "integration":
			integ = f
		case "payload":
			attr = f
		}
	}
	if integ == nil || integ["value"] != "Invoices" {
		t.Errorf("integration filter = %v, want the integration's name", integ)
	}
	if searchBody(t, seen)["payload_links"] != true {
		t.Error("the search did not ask for payload links inline")
	}
	if attr == nil || attr["fieldPath"] != "iId" || attr["op"] != "equals" || attr["value"] != "123" {
		t.Errorf("attribute filter = %v, want iId equals 123", attr)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	res := out["resolved_attributes"].([]any)[0].(map[string]any)
	if res["given"] != "invoice id" || res["key"] != "iId" {
		t.Errorf("resolved_attributes = %v", res)
	}
	first := out["results"].([]any)[0].(map[string]any)
	if cols := first["columns"].(map[string]any); cols["Invoice Id"] != "123" {
		t.Errorf("columns = %v, want the value under its label", cols)
	}
	if _, ok := first["spans"]; ok {
		t.Error("spans attached without include_spans")
	}
}

// An attribute key the telemetry carries is used as it is, and a plain
// search keeps the triage default of errors only.
func TestSearchTracesKeepsKeysAndTheTriageDefault(t *testing.T) {
	seen, _ := callRecording(t, "sluicio_search_traces", map[string]any{"window": "7d"}, invoiceReplies)
	filters := filtersOf(searchBody(t, seen))
	if len(filters) != 1 || filters[0]["field"] != "status" {
		t.Errorf("plain search filters = %v, want errors only", filters)
	}

	seen, _ = callRecording(t, "sluicio_search_traces", map[string]any{
		"attributes": []any{
			map[string]any{"key": "iId", "op": "in", "value": []any{"123", "124"}},
			map[string]any{"key": "partner", "op": "exists"},
		},
	}, invoiceReplies)
	filters = filtersOf(searchBody(t, seen))
	if len(filters) != 2 {
		t.Fatalf("filters = %v, want the two attribute conditions only", filters)
	}
	if filters[0]["fieldPath"] != "iId" || filters[0]["op"] != "in" || filters[0]["value"] != "123,124" {
		t.Errorf("in filter = %v", filters[0])
	}
	if filters[1]["op"] != "exists" || filters[1]["value"] != "" {
		t.Errorf("exists filter = %v", filters[1])
	}
	for _, r := range seen {
		if r.path == "/api/v1/integrations" {
			t.Error("resolved an integration nobody asked for")
		}
	}
}

// include_spans reads each result's trace in the integration's slice.
func TestSearchTracesIncludesSpans(t *testing.T) {
	seen, text := callRecording(t, "sluicio_search_traces", map[string]any{
		"integration":   invoiceIntegID,
		"attributes":    []any{map[string]any{"key": "iId", "value": "123"}},
		"include_spans": true,
	}, invoiceReplies)
	reads := 0
	for _, r := range seen {
		if r.path == "/api/v1/traces/aaa" || r.path == "/api/v1/traces/bbb" {
			reads++
			if r.query != "integration="+invoiceIntegID {
				t.Errorf("trace read %s?%s, want the integration's slice", r.path, r.query)
			}
		}
		if r.path == "/api/v1/integrations" {
			t.Error("an id was looked up in the list")
		}
	}
	if reads != 2 {
		t.Errorf("read %d traces, want 2", reads)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	if out["spans_included"] != float64(2) {
		t.Errorf("spans_included = %v", out["spans_included"])
	}
	first := out["results"].([]any)[0].(map[string]any)
	if spans, _ := first["spans"].([]any); len(spans) != 1 {
		t.Errorf("first result's spans = %v", first["spans"])
	}
}

func TestSearchTracesRejectsBadAttributes(t *testing.T) {
	for name, attrs := range map[string]any{
		"no key":     []any{map[string]any{"value": "1"}},
		"no value":   []any{map[string]any{"key": "iId"}},
		"unknown op": []any{map[string]any{"key": "iId", "op": "like", "value": "1"}},
	} {
		if msg := callToolError(t, "sluicio_search_traces", map[string]any{"attributes": attrs}); msg == "" {
			t.Errorf("%s: accepted", name)
		}
	}
	if msg := callToolError(t, "sluicio_search_traces", map[string]any{"integration": "nope"}); msg == "" {
		t.Error("an unknown integration was accepted")
	}
}

// A trace read in an integration is its slice, with its labels.
func TestGetTraceInAnIntegrationCarriesItsLabels(t *testing.T) {
	seen, text := callRecording(t, "sluicio_get_trace", map[string]any{"trace_id": "aaa", "integration": "Invoices"}, invoiceReplies)
	last := seen[len(seen)-1]
	if last.path != "/api/v1/traces/aaa" || last.query != "integration="+invoiceIntegID {
		t.Errorf("trace read %s?%s", last.path, last.query)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	labels, _ := out["attribute_labels"].(map[string]any)
	if labels["iId"] != "Invoice Id" || labels["service"] != nil {
		t.Errorf("attribute_labels = %v, want attribute columns only", labels)
	}

	seen, _ = callRecording(t, "sluicio_get_trace", map[string]any{"trace_id": "aaa"}, invoiceReplies)
	if len(seen) != 1 || seen[0].query != "" {
		t.Errorf("a plain trace read made %d requests: %v", len(seen), seen)
	}
}

// Within an integration the search asks for each message's completion
// state, so "is invoice 123 complete or stuck, and where" is one call.
func TestSearchTracesAsksForCompletionInAnIntegration(t *testing.T) {
	seen, _ := callRecording(t, "sluicio_search_traces", map[string]any{
		"integration": "invoices",
		"attributes":  []any{map[string]any{"key": "iId", "value": "123"}},
	}, invoiceReplies)
	if body := searchBody(t, seen); body["completion"] != true {
		t.Errorf("search body = %v, want completion requested", body)
	}
	seen, _ = callRecording(t, "sluicio_search_traces", map[string]any{"window": "1h"}, invoiceReplies)
	if body := searchBody(t, seen); body["completion"] != nil {
		t.Errorf("a search outside an integration asked for completion: %v", body)
	}
}
