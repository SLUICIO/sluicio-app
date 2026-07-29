// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The remote (HTTP) transport for Sluicio's MCP server, mounted on cell-api at
// /api/v1/mcp. Because it lives on cell-api it rides the existing reverse
// proxy + TLS + Bearer auth — no separate endpoint, port, or service. The
// shared core (pkg/mcp) re-dispatches each tool to cell-api over loopback,
// forwarding the caller's Authorization header, so every tool reuses the exact
// REST + auth + RBAC (a viewer token ⇒ read-only, policy-filtered). The same
// core also runs as the stdio binary services/cell-mcp.
//
// This is the spec's Streamable HTTP transport (issue #8, WS1), which is what
// hosted connectors — claude.ai, ChatGPT, connector directories — speak. Three
// choices are worth stating, because each is a "MAY" in the spec that we are
// answering deliberately rather than by omission.
//
// It is STATELESS: no session is created, so no Mcp-Session-Id is issued.
// Every request carries its own Bearer token and every tool dispatch is
// independent, so there is nothing a session would hold. The spec allows this
// explicitly, and it is the reason the endpoint survives a cell restart
// mid-conversation and needs no session reaper. A client that sends a session
// id anyway is not punished for it — the header is simply ignored.
//
// GET and DELETE answer 405. GET would open a stream for server-initiated
// messages and we have none to send: alerts reach agents through webhooks and
// event subscriptions (issue #4), which survive a disconnect, where a held SSE
// stream would not. DELETE terminates a session, and there are none. The spec
// names 405 as the correct answer in both cases; we add a body saying why, so
// a connector author debugging at 2am reads an explanation instead of guessing.
//
// POST answers JSON by default and SSE only when the client will take nothing
// else. Both are legal; JSON is one round trip and no framing, and a
// single-request-single-response exchange gains nothing from a stream.

package api

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/mcp"
)

// mcpProtocolHeader is the revision marker a client sends after the
// initialize handshake (spec 2025-06-18 §Protocol Version Header).
const mcpProtocolHeader = "MCP-Protocol-Version"

// mcpEndpoint: POST /api/v1/mcp — one JSON-RPC message in, one response out
// (notifications get 202 + empty body). Authed by the normal middleware, so the
// caller already holds a valid token; we forward it to the loopback dispatch.
func (h *Handlers) mcpEndpoint(w http.ResponseWriter, r *http.Request) {
	if !h.mcpRequestAllowed(w, r) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "read body failed")
		return
	}
	srv := mcp.NewServer(h.SelfBaseURL, r.Header.Get("Authorization"))
	// Mark the loopback calls this session makes, so writes an agent
	// performs are attributable in the audit log. Same process, so the
	// secret never leaves it.
	srv.ViaToken = h.ViaToken
	resp := srv.HandleMessage(body)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted) // notification — no reply
		return
	}
	if mcpWantsEventStream(r) {
		writeMCPEventStream(w, resp)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp)
}

// mcpStreamEndpoint: GET /api/v1/mcp — we offer no server-initiated
// messages, so there is no stream to open. See the file comment.
func (h *Handlers) mcpStreamEndpoint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "POST")
	httpserver.WriteError(w, http.StatusMethodNotAllowed,
		"this MCP endpoint does not offer a server-initiated event stream — POST your JSON-RPC requests instead. "+
			"For push, subscribe to events (Developers → Event subscriptions) or an alert webhook; those survive a disconnect, a held stream does not.")
}

// mcpDeleteEndpoint: DELETE /api/v1/mcp — the endpoint is stateless, so
// there is no session to terminate. Closing the connection is enough.
func (h *Handlers) mcpDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "POST")
	httpserver.WriteError(w, http.StatusMethodNotAllowed,
		"this MCP endpoint is stateless and issues no session id, so there is no session to delete — just stop sending requests.")
}

// mcpRequestAllowed applies the two transport-level checks the spec asks
// for, answering the request itself when either fails.
func (h *Handlers) mcpRequestAllowed(w http.ResponseWriter, r *http.Request) bool {
	if v := strings.TrimSpace(r.Header.Get(mcpProtocolHeader)); v != "" && !mcp.SupportsProtocol(v) {
		httpserver.WriteError(w, http.StatusBadRequest,
			"unsupported "+mcpProtocolHeader+": "+v+" — this cell speaks "+strings.Join(mcp.SupportedProtocols, ", "))
		return false
	}
	if !mcpOriginAllowed(r) {
		// Deliberately vague to the caller, precise in the log: an
		// attacker's page learns nothing, an operator sees the origin.
		h.Logger.Warn("mcp: cross-origin request without a bearer token refused",
			"origin", r.Header.Get("Origin"), "path", r.URL.Path)
		httpserver.WriteError(w, http.StatusForbidden,
			"cross-origin MCP requests must authenticate with a Bearer token")
		return false
	}
	return true
}

// mcpOriginAllowed is the DNS-rebinding guard the spec requires.
//
// The attack it stops is specific: a page the user happens to have open
// makes a cross-origin POST to this endpoint, the browser attaches the
// user's session cookie automatically, and the tool call runs with their
// rights — including the one tool that writes. The page never sees the
// response, but the side effect has already happened.
//
// A flat Origin allowlist would be the obvious guard and the wrong one:
// legitimate browser-based MCP clients carry their own origin, and we
// cannot enumerate them. The distinction that actually matters is not
// WHICH origin sent the request but WHAT authenticated it. Ambient
// credentials — a cookie the browser attached without the page's
// involvement — are the entire attack; a Bearer token cannot be obtained
// that way. So: same-origin or no Origin at all passes as before, and a
// cross-origin caller must prove itself with a token.
//
// The comparison is by HOSTNAME, port excluded, for two reasons. It has
// to be: nginx forwards `Host: $host`, which drops the port, so a
// browser Origin of `https://cell.example:8443` would never equal the
// Host cell-api sees, and the app would refuse itself behind its own
// proxy. It also should be: cookies are not port-scoped, so a port is
// not a boundary for the credential this guard exists to protect.
func mcpOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true // not a browser-initiated request
	}
	if u, err := url.Parse(origin); err == nil && u.Hostname() != "" &&
		strings.EqualFold(u.Hostname(), hostnameOf(r.Host)) {
		return true // the app talking to itself
	}
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ")
}

// hostnameOf strips a port from a Host header value, leaving IPv6
// literals intact.
func hostnameOf(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.Trim(host, "[]")
}

// mcpWantsEventStream reports whether the client will accept ONLY an
// event stream. A client that also lists application/json gets JSON: the
// exchange is one request and one response, so a stream buys nothing and
// costs an extra frame plus a connection held open through every proxy
// in the path.
func mcpWantsEventStream(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if !strings.Contains(accept, "text/event-stream") {
		return false
	}
	return !strings.Contains(accept, "application/json")
}

// writeMCPEventStream frames one JSON-RPC response as a single SSE
// message and closes the stream, which the spec asks for once the
// response to a request has been delivered.
func writeMCPEventStream(w http.ResponseWriter, resp []byte) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// nginx buffers proxied responses by default, which would hold the
	// event until the stream closed — turning a stream into a slow
	// non-stream. The quickstart and the Helm chart both front cell-api
	// with nginx, so this is not hypothetical.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// No `id:` field: that is the resumability marker, and a client that
	// sees one is entitled to reconnect with Last-Event-ID and expect
	// replay. We keep no history, so promising it would be worse than
	// not offering it.
	_, _ = w.Write([]byte("event: message\ndata: "))
	_, _ = w.Write(resp)
	_, _ = w.Write([]byte("\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
