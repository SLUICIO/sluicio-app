// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package mcp

import (
	"encoding/json"
	"testing"
)

// A collector's answer carries its newest configuration, read from the
// masked route; there is no other route for it to read.
func TestGetCollectorMergesMaskedConfig(t *testing.T) {
	seen, text := callRecording(t, "sluicio_get_collector", map[string]any{"id": "c1"}, map[string]string{
		"/api/v1/collectors/c1":                `{"collector":{"id":"c1"},"history":[{"id":"v1"}]}`,
		"/api/v1/collectors/c1/configs/latest": `{"files":[{"body":"token: ********"}]}`,
	})
	if len(seen) != 2 || seen[1].path != "/api/v1/collectors/c1/configs/latest" {
		t.Fatalf("requests = %+v", seen)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["config"]; !ok {
		t.Fatalf("no config in %s", text)
	}
}

// Without stored versions there is nothing to fetch.
func TestGetCollectorWithoutHistory(t *testing.T) {
	seen, _ := callRecording(t, "sluicio_get_collector", map[string]any{"id": "c1"}, map[string]string{
		"/api/v1/collectors/c1": `{"collector":{"id":"c1"},"history":[]}`,
	})
	if len(seen) != 1 {
		t.Fatalf("requests = %+v", seen)
	}
}

// Deployments are read from the collector's own route, which the team
// boundary already guards.
func TestListCollectorDeployments(t *testing.T) {
	seen, text := callRecording(t, "sluicio_list_collector_deployments", map[string]any{"id": "c1"}, map[string]string{
		"/api/v1/collectors/c1/deployments": `{"deployments":[{"id":"d1","status":"applied"}]}`,
	})
	if len(seen) != 1 || seen[0].path != "/api/v1/collectors/c1/deployments" {
		t.Fatalf("requests = %+v", seen)
	}
	if text != `{"deployments":[{"id":"d1","status":"applied"}]}` {
		t.Fatalf("answer = %s", text)
	}
}
