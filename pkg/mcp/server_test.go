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
