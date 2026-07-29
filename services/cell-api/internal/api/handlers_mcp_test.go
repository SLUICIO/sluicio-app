// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The MCP transport is where a remote connector meets this cell, and its
// failure modes are the unhelpful kind: a connector that cannot complete
// a handshake reports "could not connect" and nothing else. So the
// contract is pinned here — what each method answers, how a response is
// framed, and the one security property the transport itself owns.
//
// That property is worth stating plainly. A page in the user's browser
// can POST here cross-origin, and the browser will attach the user's
// session cookie without the page's involvement. Every tool would then
// run with that user's rights, including the one that writes. The guard
// is not an origin allowlist — it is refusing to act on ambient
// credentials, which is what TestCrossOriginCookieCallIsRefused pins.

package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mcpHandlers() *Handlers {
	return &Handlers{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// mcpPost drives one message through the POST handler.
func mcpPost(h *Handlers, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://cell.example/api/v1/mcp", strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.mcpEndpoint(w, r)
	return w
}

const initMsg = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`

func TestGetAndDeleteExplainThemselves(t *testing.T) {
	// A bare 405 sends a connector author to the spec to work out which
	// of several "MAY"s we declined. The body says which, and why.
	h := mcpHandlers()
	for _, tc := range []struct {
		name    string
		call    func(w http.ResponseWriter, r *http.Request)
		method  string
		mustSay string
	}{
		{"GET", h.mcpStreamEndpoint, http.MethodGet, "event stream"},
		{"DELETE", h.mcpDeleteEndpoint, http.MethodDelete, "stateless"},
	} {
		r := httptest.NewRequest(tc.method, "http://cell.example/api/v1/mcp", nil)
		w := httptest.NewRecorder()
		tc.call(w, r)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", tc.name, w.Code)
		}
		if got := w.Header().Get("Allow"); !strings.Contains(got, "POST") {
			t.Errorf("%s: Allow = %q, must name the method that does work", tc.name, got)
		}
		if !strings.Contains(w.Body.String(), tc.mustSay) {
			t.Errorf("%s body does not explain the refusal: %s", tc.name, w.Body.String())
		}
	}
}

func TestUnsupportedProtocolVersionIsRejected(t *testing.T) {
	h := mcpHandlers()
	// A revision we don't speak must fail at the door. Proceeding would
	// let a client use features we never implemented and blame the cell
	// for the results.
	w := mcpPost(h, initMsg, map[string]string{"MCP-Protocol-Version": "2099-01-01"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("unknown protocol version = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "2025-06-18") {
		t.Error("the refusal must list what this cell does speak, or the client cannot recover")
	}
	// A revision we do speak passes through.
	if w := mcpPost(h, initMsg, map[string]string{"MCP-Protocol-Version": "2024-11-05"}); w.Code != http.StatusOK {
		t.Errorf("supported protocol version = %d, want 200", w.Code)
	}
	// Absent is fine — the header only appears after the handshake.
	if w := mcpPost(h, initMsg, nil); w.Code != http.StatusOK {
		t.Errorf("no protocol header = %d, want 200", w.Code)
	}
}

func TestCrossOriginCookieCallIsRefused(t *testing.T) {
	h := mcpHandlers()

	// The attack: a page the user has open POSTs here, the browser
	// attaches their session cookie, and a tool runs as them.
	w := mcpPost(h, initMsg, map[string]string{
		"Origin": "https://evil.example",
		"Cookie": "sluicio_session=whatever",
	})
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin cookie-only call = %d, want 403 — this is the DNS-rebinding case", w.Code)
	}

	// A real remote client holds a token, which a page cannot obtain by
	// being open in the same browser. Origin alone must not refuse it:
	// browser-based MCP clients are legitimate and unenumerable.
	if w := mcpPost(h, initMsg, map[string]string{
		"Origin": "https://claude.ai", "Authorization": "Bearer con_sa_test",
	}); w.Code != http.StatusOK {
		t.Errorf("cross-origin bearer call = %d, want 200 — an origin allowlist would lock out hosted connectors", w.Code)
	}

	// The app talking to itself, and a non-browser caller with no Origin
	// at all, both behave as before.
	if w := mcpPost(h, initMsg, map[string]string{"Origin": "https://cell.example"}); w.Code != http.StatusOK {
		t.Errorf("same-origin call = %d, want 200", w.Code)
	}

	// Behind the bundled nginx, `proxy_set_header Host $host` strips the
	// port, so the browser's Origin carries one and the Host cell-api
	// sees does not. Comparing them verbatim would make the app refuse
	// itself through its own proxy — and cookies aren't port-scoped, so
	// the port is not a boundary worth enforcing here anyway.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(initMsg))
	r.Host = "cell.example" // what nginx forwards
	r.Header.Set("Origin", "https://cell.example:8443")
	w = httptest.NewRecorder()
	h.mcpEndpoint(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("same-host-different-port call = %d, want 200 — this is the app behind its own reverse proxy", w.Code)
	}
	if w := mcpPost(h, initMsg, nil); w.Code != http.StatusOK {
		t.Errorf("call with no Origin = %d, want 200", w.Code)
	}
}

func TestResponseFramingFollowsAccept(t *testing.T) {
	h := mcpHandlers()

	// The common case: a client that takes either gets JSON, because the
	// exchange is one request and one response.
	w := mcpPost(h, initMsg, map[string]string{"Accept": "application/json, text/event-stream"})
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json when the client accepts both", ct)
	}

	// A client that will take nothing but a stream gets one.
	w = mcpPost(h, initMsg, map[string]string{"Accept": "text/event-stream"})
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "event: message\ndata: ") || !strings.HasSuffix(body, "\n\n") {
		t.Errorf("SSE framing is wrong; a client will not parse this:\n%q", body)
	}
	// The payload must survive framing intact, and carry no `id:` line —
	// that marker promises replay on reconnect, which we cannot do.
	if strings.Contains(body, "\nid:") {
		t.Error("an SSE id promises Last-Event-ID resumability this endpoint does not implement")
	}
	var parsed struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	data := strings.TrimSuffix(strings.TrimPrefix(body, "event: message\ndata: "), "\n\n")
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("SSE data is not the JSON-RPC response: %v", err)
	}
	if parsed.Result.ProtocolVersion == "" {
		t.Error("the handshake result did not survive SSE framing")
	}
	// nginx fronts cell-api in both the quickstart and the Helm chart,
	// and buffers proxied responses by default.
	if w.Header().Get("X-Accel-Buffering") != "no" {
		t.Error("without X-Accel-Buffering the event sits in nginx's buffer until the stream closes")
	}
}

func TestNotificationGetsNoBody(t *testing.T) {
	// A JSON-RPC notification has no id and expects no reply. Answering
	// one would leave a client waiting on a correlation that never comes.
	h := mcpHandlers()
	w := mcpPost(h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	if w.Code != http.StatusAccepted {
		t.Errorf("notification = %d, want 202", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("notification answered with a body: %s", w.Body.String())
	}
}
