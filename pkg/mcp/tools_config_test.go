// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// recorded is one request the fake cell-api saw, with its JSON body.
type recorded struct {
	method, path, query string
	body                map[string]any
}

// callRecording runs one tool against a fake cell-api that answers each
// path from replies (default {}), and returns every request in order and
// the tool's result text.
func callRecording(t *testing.T, name string, args map[string]any, replies map[string]string) ([]recorded, string) {
	t.Helper()
	var seen []recorded
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recorded{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &rec.body)
		}
		seen = append(seen, rec)
		w.Header().Set("Content-Type", "application/json")
		if reply, ok := replies[r.URL.Path]; ok {
			_, _ = w.Write([]byte(reply))
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer backend.Close()
	s := NewServer(backend.URL, "Bearer test-token")
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
	if err := json.Unmarshal(s.HandleMessage(msg), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Result.IsError {
		t.Fatalf("%s errored: %s", name, parsed.Result.Content[0].Text)
	}
	return seen, parsed.Result.Content[0].Text
}

// An integration's answer carries its checks and completion rules, not
// only how many checks are failing.
func TestGetIntegrationCarriesItsChecks(t *testing.T) {
	seen, text := callRecording(t, "sluicio_get_integration", map[string]any{"id": "i-1"}, map[string]string{
		"/api/v1/integrations/i-1":                  `{"integration":{"id":"i-1"}}`,
		"/api/v1/alert-rules":                       `{"rules":[{"id":"r-1"}],"states":{"r-1":{"state":"ok"}}}`,
		"/api/v1/integrations/i-1/completion-rules": `{"rules":[{"id":"c-1"}]}`,
	})
	if len(seen) != 3 || seen[1].query != "integration=i-1" {
		t.Fatalf("requests: %+v", seen)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	if out["health_checks"] == nil || out["completion_rules"] == nil || out["integration"] == nil {
		t.Fatalf("merged answer: %s", text)
	}
}

func TestProposalToolsFileTheRightKind(t *testing.T) {
	seen, _ := callRecording(t, "sluicio_propose_alert_rule", map[string]any{
		"rule": map[string]any{"name": "no traffic"}, "rationale": "silent for 3h on 4 days",
	}, nil)
	if b := seen[0].body; seen[0].path != "/api/v1/proposals" || b["target_kind"] != "new_alert_rule" || b["target_id"] != nil {
		t.Fatalf("new check: %+v", seen[0])
	}

	seen, _ = callRecording(t, "sluicio_propose_trace_completion_rule", map[string]any{
		"integration_id": "i-1", "rule": map[string]any{"name": "every receiver"}, "rationale": "fan-out",
	}, nil)
	if b := seen[0].body; b["target_kind"] != "new_completion_rule" || b["target_id"] != "i-1" {
		t.Fatalf("completion rule: %+v", b)
	}

	seen, _ = callRecording(t, "sluicio_propose_integration_settings", map[string]any{
		"integration_id": "i-1", "notify_unacknowledged_errors": true, "rationale": "errors go unseen",
	}, nil)
	changes := seen[0].body["changes"].([]any)
	if len(changes) != 1 || changes[0].(map[string]any)["field"] != "notify_unacknowledged_errors" {
		t.Fatalf("settings: %+v", seen[0].body)
	}
}

// Adding a source proposes today's list plus the one, in the shape the
// sources route takes, so the reviewer sees exactly one addition.
func TestProposeMetricSourceAppendsOne(t *testing.T) {
	seen, _ := callRecording(t, "sluicio_propose_metric_source", map[string]any{
		"integration_id": "i-1", "origin": "servicebus", "metric_names": []any{"servicebus.topic.subscription.*"},
		"rationale": "the backlog lives on the subscription",
	}, map[string]string{
		"/api/v1/integrations/i-1/sources": `{"sources":[{"id":"s-1","signal":"logs","origin_kind":"service","origin":"runtime","origin_name":"runtime","conditions":[],"metric_names":[]}]}`,
	})
	if len(seen) != 2 || seen[0].method != http.MethodGet || seen[1].method != http.MethodPost {
		t.Fatalf("requests: %+v", seen)
	}
	change := seen[1].body["changes"].([]any)[0].(map[string]any)
	list := change["after"].([]any)
	if change["field"] != "sources" || len(list) != 2 {
		t.Fatalf("sources: %+v", change)
	}
	first, added := list[0].(map[string]any), list[1].(map[string]any)
	if first["id"] != nil || first["origin_name"] != nil || first["origin"] != "runtime" {
		t.Fatalf("the current source in the input shape: %+v", first)
	}
	if added["signal"] != "metrics" || added["origin_kind"] != "service" || added["origin"] != "servicebus" {
		t.Fatalf("the added source: %+v", added)
	}
}

func TestBacktestAndDiscoveryRequests(t *testing.T) {
	seen, _ := callRecording(t, "sluicio_backtest_rule", map[string]any{"rule_id": "r-1", "threshold": float64(12), "days": float64(14)}, nil)
	if b := seen[0].body; seen[0].path != "/api/v1/alert-rules/backtest" || b["rule_id"] != "r-1" || b["threshold"] != float64(12) || b["days"] != float64(14) {
		t.Fatalf("backtest: %+v", seen[0])
	}
	seen, _ = callRecording(t, "sluicio_span_names", map[string]any{"integration_id": "i-1", "service": "erp-loader"}, nil)
	if seen[0].path != "/api/v1/integrations/i-1/span-names" || seen[0].query != "range=24h&service=erp-loader" {
		t.Fatalf("span names: %+v", seen[0])
	}
	seen, _ = callRecording(t, "sluicio_metric_label_values", map[string]any{"key": "subscription", "metric": "servicebus.active"}, nil)
	if seen[0].path != "/api/v1/metric-attributes/subscription/values" || seen[0].query != "metric=servicebus.active&range=24h" {
		t.Fatalf("label values: %+v", seen[0])
	}
}
