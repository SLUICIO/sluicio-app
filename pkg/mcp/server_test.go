// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// callTool drives one tools/call through HandleMessage against a fake
// cell-api and returns the request the tool actually made.
func callTool(t *testing.T, name string, args map[string]any) *http.Request {
	t.Helper()
	var captured *http.Request
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	defer backend.Close()

	s := NewServer(backend.URL, "Bearer test-token")
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	resp := s.HandleMessage(msg)
	if resp == nil {
		t.Fatalf("no response for %s", name)
	}
	var parsed struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Result.IsError {
		t.Fatalf("%s errored: %s", name, parsed.Result.Content[0].Text)
	}
	if captured == nil {
		t.Fatalf("%s made no backend request", name)
	}
	return captured
}

func TestToolCatalogueCoversTheReadSurface(t *testing.T) {
	s := NewServer("http://example", "Bearer x")
	names := map[string]bool{}
	for _, tl := range s.toolList() {
		names[tl["name"].(string)] = true
	}
	for _, want := range []string{
		"sluicio_list_integrations", "sluicio_get_integration",
		"sluicio_list_services", "sluicio_list_systems", "sluicio_get_system",
		"sluicio_system_types", "sluicio_errors", "sluicio_health",
		"sluicio_error_report", "sluicio_alert_instances", "sluicio_digest",
		"sluicio_metric_catalog", "sluicio_metric_series",
		"sluicio_search_traces", "sluicio_get_trace", "sluicio_search_logs",
		"sluicio_usage_report",
	} {
		if !names[want] {
			t.Errorf("catalogue missing %s", want)
		}
	}
}

// The annotations are a promise to the client about whether calling a
// tool is safe, so they must match what the tool actually does. The
// direction that matters is a tool that WRITES while claiming to be
// read-only: a client would then skip confirmation on a real write.
//
// messages/search is the one read that must POST — its body is too big
// for a query string — so it's allowed to claim read-only.
func TestToolAnnotationsMatchWhatToolsDo(t *testing.T) {
	s := NewServer("http://example", "Bearer x")

	claimsReadOnly := map[string]bool{}
	for _, tl := range s.toolList() {
		name := tl["name"].(string)
		ann, ok := tl["annotations"].(map[string]any)
		if !ok {
			t.Fatalf("%s has no annotations", name)
		}
		ro, ok := ann["readOnlyHint"].(bool)
		if !ok {
			t.Fatalf("%s: readOnlyHint missing or not a bool", name)
		}
		claimsReadOnly[name] = ro
		// Nothing in this catalogue destroys anything: reads don't, and a
		// proposal only queues a request for human review.
		if ann["destructiveHint"] != false {
			t.Errorf("%s: destructiveHint should be false", name)
		}
	}

	// At least one writer must exist, or this test passes vacuously
	// forever after someone deletes the propose tool.
	sawWriter := false
	for _, ro := range claimsReadOnly {
		if !ro {
			sawWriter = true
		}
	}
	if !sawWriter {
		t.Error("no write-annotated tool in the catalogue — did the propose tool lose its annotations?")
	}

	for _, tl := range s.toolList() {
		name := tl["name"].(string)
		var method, path string
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method, path = r.Method, r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		}))
		srv := NewServer(backend.URL, "Bearer x")
		// Supply every required arg generically so the call reaches the
		// backend instead of failing validation.
		args := map[string]any{
			"id": "00000000-0000-0000-0000-000000000000", "trace_id": "abc",
			"metric": "m", "name": "m",
			"rule_id":   "00000000-0000-0000-0000-000000000000",
			"rationale": "observed 40 firings in 24h, all auto-resolved within 2m",
			"threshold": 8,
		}
		msg, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args},
		})
		_ = srv.HandleMessage(msg)
		backend.Close()

		if method == "" {
			continue // arg validation rejected it; nothing reached the wire
		}
		writes := method != http.MethodGet && path != "/api/v1/messages/search"
		if writes && claimsReadOnly[name] {
			t.Errorf("%s used %s %s but claims readOnlyHint=true — a client would skip confirmation on a real write",
				name, method, path)
		}
		if !claimsReadOnly[name] && !writes {
			t.Errorf("%s claims to write but only issued %s %s — the annotation over-warns", name, method, path)
		}
	}
}

// The propose tool must reach the proposals endpoint with the target
// kind and the agent's rationale, and must NOT send `before` — cell-api
// snapshots that itself, because an agent-supplied before would let a
// caller defeat the drift check protecting a human's concurrent edit.
func TestProposeCheckTuningRequestShape(t *testing.T) {
	var gotPath string
	var body map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	defer backend.Close()

	s := NewServer(backend.URL, "Bearer x")
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "sluicio_propose_check_tuning", "arguments": map[string]any{
			"rule_id":   "11111111-2222-3333-4444-555555555555",
			"rationale": "fired 40 times in 24h; every instance auto-resolved within 2 minutes",
			"threshold": 8,
			"severity":  "warning",
		}},
	})
	_ = s.HandleMessage(msg)

	if gotPath != "/api/v1/proposals" {
		t.Fatalf("posted to %q, want /api/v1/proposals", gotPath)
	}
	if body["target_kind"] != "alert_rule" {
		t.Errorf("target_kind = %v, want alert_rule", body["target_kind"])
	}
	if body["rationale"] == "" || body["rationale"] == nil {
		t.Error("rationale must be forwarded — it's what the reviewer reads")
	}
	changes, _ := body["changes"].([]any)
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes (threshold, severity), got %d", len(changes))
	}
	for _, c := range changes {
		m := c.(map[string]any)
		if _, hasBefore := m["before"]; hasBefore {
			t.Errorf("change %v carries `before` — cell-api must snapshot it, or the drift guard can be defeated", m["field"])
		}
		if m["after"] == nil {
			t.Errorf("change %v has no after value", m["field"])
		}
	}
}

// Omitting every tunable is a no-op proposal; it must be refused rather
// than filing an empty diff for a human to puzzle over.
func TestProposeCheckTuningRequiresAChange(t *testing.T) {
	s := NewServer("http://example", "Bearer x")
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "sluicio_propose_check_tuning", "arguments": map[string]any{
			"rule_id": "11111111-2222-3333-4444-555555555555", "rationale": "because",
		}},
	})
	var parsed struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(s.HandleMessage(msg), &parsed)
	if !parsed.Result.IsError {
		t.Error("a proposal with no changes must be refused")
	}
}

func TestSearchLogsRequestShape(t *testing.T) {
	r := callTool(t, "sluicio_search_logs", map[string]any{
		"query": "timeout", "min_severity": float64(17), "service": "orders-api",
		"window": "48h", "limit": float64(50),
		"attrs": []any{map[string]any{"key": "http.route", "op": "eq", "value": "/checkout"}},
	})
	if r.URL.Path != "/api/v1/logs" {
		t.Fatalf("path = %s", r.URL.Path)
	}
	q := r.URL.Query()
	for k, want := range map[string]string{
		"q": "timeout", "min_severity": "17", "service": "orders-api",
		"range": "48h", "limit": "50",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	var attr map[string]string
	if err := json.Unmarshal([]byte(q.Get("attr")), &attr); err != nil || attr["key"] != "http.route" {
		t.Errorf("attr param mangled: %q (%v)", q.Get("attr"), err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("auth not forwarded: %q", got)
	}
}

func TestMetricSeriesRequestShape(t *testing.T) {
	r := callTool(t, "sluicio_metric_series", map[string]any{
		"metric": "servicebus.queue.deadletter_messages", "service": "asb-namespace",
	})
	if r.URL.Path != "/api/v1/metric-series" {
		t.Fatalf("path = %s", r.URL.Path)
	}
	q := r.URL.Query()
	if q.Get("metric") != "servicebus.queue.deadletter_messages" || q.Get("service") != "asb-namespace" {
		t.Errorf("query = %v", q)
	}
}

func TestGetIntegrationAndAlertInstances(t *testing.T) {
	r := callTool(t, "sluicio_get_integration", map[string]any{"id": "abc-123"})
	if r.URL.Path != "/api/v1/integrations/abc-123" {
		t.Fatalf("path = %s", r.URL.Path)
	}
	r = callTool(t, "sluicio_alert_instances", map[string]any{"limit": float64(25)})
	if r.URL.Path != "/api/v1/alert-instances" || r.URL.Query().Get("limit") != "25" {
		t.Fatalf("got %s?%s", r.URL.Path, r.URL.RawQuery)
	}
}

func TestUsageReportRequestShape(t *testing.T) {
	r := callTool(t, "sluicio_usage_report", map[string]any{"window": "7d"})
	if r.URL.Path != "/api/v1/reports/usage" {
		t.Fatalf("path = %s", r.URL.Path)
	}
	if got := r.URL.Query().Get("range"); got != "7d" {
		t.Errorf("range = %q, want 7d", got)
	}
	// Default window when unspecified mirrors the admin UI (24h).
	r = callTool(t, "sluicio_usage_report", nil)
	if got := r.URL.Query().Get("range"); got != "24h" {
		t.Errorf("default range = %q, want 24h", got)
	}
}

func TestRequiredArgsRejected(t *testing.T) {
	s := NewServer("http://example.invalid", "Bearer x")
	for tool, args := range map[string]map[string]any{
		"sluicio_metric_series":   {},
		"sluicio_get_integration": {},
	} {
		msg, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": args},
		})
		var parsed struct {
			Result struct {
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal(s.HandleMessage(msg), &parsed); err != nil {
			t.Fatal(err)
		}
		if !parsed.Result.IsError {
			t.Errorf("%s without required args should error", tool)
		}
	}
}
