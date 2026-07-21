// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The remote (HTTP) transport for Sluicio's MCP server, mounted on cell-api at
// POST /api/v1/mcp. Because it lives on cell-api it rides the existing reverse
// proxy + TLS + Bearer auth — no separate endpoint, port, or service. The
// shared core (pkg/mcp) re-dispatches each tool to cell-api over loopback,
// forwarding the caller's Authorization header, so every tool reuses the exact
// REST + auth + RBAC (a viewer token ⇒ read-only, policy-filtered). The same
// core also runs as the stdio binary services/cell-mcp.

package api

import (
	"io"
	"net/http"

	"github.com/sluicio/sluicio-app/pkg/mcp"
)

// mcpEndpoint: POST /api/v1/mcp — one JSON-RPC message in, one response out
// (notifications get 202 + empty body). Authed by the normal middleware, so the
// caller already holds a valid token; we forward it to the loopback dispatch.
func (h *Handlers) mcpEndpoint(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	srv := mcp.NewServer(h.SelfBaseURL, r.Header.Get("Authorization"))
	resp := srv.HandleMessage(body)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted) // notification — no reply
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp)
}
