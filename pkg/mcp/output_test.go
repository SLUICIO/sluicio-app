// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// An output schema is a promise a client is entitled to VALIDATE the
// response against, which makes the failure modes asymmetric:
//
//   - Declaring a schema and not returning structuredContent, or
//     returning something that contradicts it, breaks the call outright.
//   - Over-specifying — marking item fields required, or forbidding
//     extra properties — turns any future cell-api field into a failed
//     agent call. The schema would then make the API less evolvable than
//     it actually is, which is the opposite of the point.
//
// Both directions are pinned here, along with the one claim that can
// only be checked at runtime: that a tool's payload really is an object.

package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// walkSchema visits every subschema, reporting the JSON-ish path.
func walkSchema(t *testing.T, path string, node map[string]any, visit func(path string, node map[string]any)) {
	t.Helper()
	visit(path, node)
	if props, ok := node["properties"].(map[string]any); ok {
		for k, v := range props {
			sub, ok := v.(map[string]any)
			if !ok {
				t.Errorf("%s.%s is not a schema object", path, k)
				continue
			}
			walkSchema(t, path+"."+k, sub, visit)
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		walkSchema(t, path+"[]", items, visit)
	}
}

func TestEveryToolDeclaresAnOutputSchema(t *testing.T) {
	s := NewServer("http://example", "Bearer x")
	for _, tl := range s.toolList() {
		name := tl["name"].(string)
		out, ok := tl["outputSchema"].(map[string]any)
		if !ok {
			t.Errorf("%s has no outputSchema — a client cannot tell 'undeclared' from 'no such field'", name)
			continue
		}
		if out["type"] != "object" {
			t.Errorf("%s: outputSchema type = %v, want object (structuredContent must be an object)", name, out["type"])
		}
		// The top level may require the envelope key; nothing deeper may
		// require anything, and no level may close itself to new fields.
		walkSchema(t, name, out, func(path string, node map[string]any) {
			if node["type"] != "object" {
				return
			}
			if node["additionalProperties"] != true {
				t.Errorf("%s: %s forbids extra properties — one new cell-api field would fail every call", name, path)
			}
			if path == name {
				return
			}
			if _, has := node["required"]; has {
				t.Errorf("%s: %s marks fields required; only the top-level envelope may, since item fields are conditional", name, path)
			}
		})
		// Required names must exist in properties, or the schema is
		// unsatisfiable no matter what the handler returns.
		props, _ := out["properties"].(map[string]any)
		for _, r := range asStrings(out["required"]) {
			if _, ok := props[r]; !ok {
				t.Errorf("%s: required %q is not among the declared properties", name, r)
			}
		}
	}
}

func TestEveryDeclaredFieldIsDescribed(t *testing.T) {
	// The description is what a model actually reads. A typed field with
	// no prose tells it the shape but not the meaning, which is the part
	// it cannot guess (is `status` health, or delivery state?).
	s := NewServer("http://example", "Bearer x")
	for _, tl := range s.toolList() {
		name := tl["name"].(string)
		out, _ := tl["outputSchema"].(map[string]any)
		if out == nil {
			continue
		}
		walkSchema(t, name, out, func(path string, node map[string]any) {
			if path == name || strings.HasSuffix(path, "[]") {
				return // the envelope and the anonymous item wrapper
			}
			if d, _ := node["description"].(string); d == "" {
				t.Errorf("%s: %s has no description", name, path)
			}
		})
	}
}

func asStrings(v any) []string {
	raw, ok := v.([]string)
	if ok {
		return raw
	}
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// A declared output schema obliges the result to carry structuredContent
// — a client that validates will reject the call otherwise. The text
// block must survive too: clients predating structured output read only
// that, and dropping it would silently break them.
func TestResultsCarryBothStructuredAndTextContent(t *testing.T) {
	payload := `{"services":[{"service_name":"orders-api","status":"errors"}],"window":{"from":"a","to":"b"}}`
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer backend.Close()

	s := NewServer(backend.URL, "Bearer x")
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "sluicio_list_services", "arguments": map[string]any{}},
	})
	var parsed struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
			Content           []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(s.HandleMessage(msg), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Result.IsError {
		t.Fatal("call errored")
	}
	if parsed.Result.StructuredContent == nil {
		t.Fatal("no structuredContent, though the tool declares an outputSchema")
	}
	if _, ok := parsed.Result.StructuredContent["services"].([]any); !ok {
		t.Errorf("structuredContent.services is not the array the schema promises: %#v",
			parsed.Result.StructuredContent["services"])
	}
	if len(parsed.Result.Content) == 0 || parsed.Result.Content[0].Text != payload {
		t.Error("the raw payload must still be in a text block for clients that predate structured output")
	}
}

// Every tool's endpoint returns an envelope object today. If one starts
// returning an array — or anything else — the call must fail naming the
// tool, rather than shipping a payload that contradicts the schema and
// failing inside the client.
func TestNonObjectPayloadFailsTheCallLoudly(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"service_name":"orders-api"}]`))
	}))
	defer backend.Close()

	s := NewServer(backend.URL, "Bearer x")
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "sluicio_list_services", "arguments": map[string]any{}},
	})
	var parsed struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	_ = json.Unmarshal(s.HandleMessage(msg), &parsed)
	if !parsed.Result.IsError {
		t.Fatal("a non-object payload must fail the call, not be reported as a success")
	}
	if len(parsed.Result.Content) == 0 ||
		!strings.Contains(parsed.Result.Content[0].Text, "sluicio_list_services") {
		t.Error("the error must name the tool — otherwise it is untraceable from a client log")
	}
}

// Every field the propose tool ADVERTISES must actually be forwarded.
//
// This is the concrete bug it guards: `evaluation_seconds` was offered
// for a while, but the apply path never persisted it — so an agent could
// file a proposal, a human could approve it, and nothing would change.
// A no-op that survives review is worse than a rejected one; the human
// walks away believing they made a change.
func TestProposeSchemaOffersOnlyFieldsItForwards(t *testing.T) {
	s := NewServer("http://example", "Bearer x")
	var schema map[string]any
	for _, tl := range s.toolList() {
		if tl["name"] == "sluicio_propose_check_tuning" {
			schema, _ = tl["inputSchema"].(map[string]any)
		}
	}
	if schema == nil {
		t.Fatal("propose tool missing from the catalogue")
	}
	props, _ := schema["properties"].(map[string]any)

	// Build arguments covering every advertised tunable, typed per the
	// schema so the call reaches the wire.
	args := map[string]any{
		"rule_id":   "11111111-2222-3333-4444-555555555555",
		"rationale": "fired 40 times in 24h; every instance auto-resolved within 2 minutes",
	}
	want := map[string]bool{}
	for name, raw := range props {
		if name == "rule_id" || name == "rationale" {
			continue
		}
		spec, _ := raw.(map[string]any)
		switch spec["type"] {
		case "number", "integer":
			args[name] = 9
		case "boolean":
			args[name] = true
		default:
			args[name] = "warning"
		}
		want[name] = true
	}

	var body map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","state":"pending","changes":[]}`))
	}))
	defer backend.Close()

	srv := NewServer(backend.URL, "Bearer x")
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "sluicio_propose_check_tuning", "arguments": args},
	})
	_ = srv.HandleMessage(msg)

	got := map[string]bool{}
	changes, _ := body["changes"].([]any)
	for _, c := range changes {
		m, _ := c.(map[string]any)
		if f, ok := m["field"].(string); ok {
			got[f] = true
		}
	}
	for f := range want {
		if !got[f] {
			t.Errorf("the schema offers %q but the tool drops it — an agent would file a proposal that changes nothing when approved", f)
		}
	}
	for f := range got {
		if !want[f] {
			t.Errorf("the tool forwards %q without advertising it — no agent will ever set it", f)
		}
	}
}

func TestProtocolNegotiationAnswersHonestly(t *testing.T) {
	s := NewServer("http://example", "Bearer x")
	ask := func(v string) string {
		params := map[string]any{}
		if v != "" {
			params["protocolVersion"] = v
		}
		msg, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": params,
		})
		var parsed struct {
			Result struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"result"`
		}
		if err := json.Unmarshal(s.HandleMessage(msg), &parsed); err != nil {
			t.Fatal(err)
		}
		return parsed.Result.ProtocolVersion
	}

	// A revision we speak: agree on it, so an older client stays happy.
	if got := ask("2024-11-05"); got != "2024-11-05" {
		t.Errorf("asked for 2024-11-05, got %q — a supported revision must be agreed to", got)
	}
	// One we don't: name ours rather than echoing a claim we can't back.
	if got := ask("2099-01-01"); got != LatestProtocol {
		t.Errorf("asked for an unknown revision, got %q, want %q — echoing it would claim support we don't have", got, LatestProtocol)
	}
	if got := ask(""); got != LatestProtocol {
		t.Errorf("no version requested, got %q, want %q", got, LatestProtocol)
	}
	// The revision we advertise must be one we list, or the two drift.
	if !SupportsProtocol(LatestProtocol) {
		t.Error("LatestProtocol is not in SupportedProtocols")
	}
}
