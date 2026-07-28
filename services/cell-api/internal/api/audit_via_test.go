// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Provenance in the audit log is only worth recording if it cannot be
// forged. These tests pin the two ways that could go wrong: a caller
// claiming to be an agent, and a misconfigured cell labelling everything
// as one.

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sluicio/sluicio-app/pkg/mcp"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
)

func TestViaTokenIsUnguessable(t *testing.T) {
	a, b := NewViaToken(), NewViaToken()
	if a == "" || b == "" {
		t.Fatal("NewViaToken returned empty")
	}
	if a == b {
		t.Error("two tokens are identical — the value must be random per process")
	}
	if len(a) < 32 {
		t.Errorf("token is %d chars; too short to resist guessing", len(a))
	}
}

func TestOutsiderCannotClaimMCP(t *testing.T) {
	// The attack this defends against: a client sets the header itself to
	// make its writes look like an agent's, or to launder them. Anything
	// other than the exact process secret must not be believed.
	h := &Handlers{ViaToken: "the-real-process-secret"}
	for _, presented := range []string{
		"mcp",
		"true",
		"the-real-process-secre", // near miss
		"THE-REAL-PROCESS-SECRET",
		"",
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", nil)
		if presented != "" {
			r.Header.Set(mcp.ViaHeader, presented)
		}
		if got := h.requestVia(r); got == ViaMCP {
			t.Errorf("header %q was accepted as MCP provenance", presented)
		}
	}
}

func TestLoopbackWithTheSecretIsMCP(t *testing.T) {
	h := &Handlers{ViaToken: "the-real-process-secret"}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/proposals", nil)
	r.Header.Set(mcp.ViaHeader, "the-real-process-secret")
	if got := h.requestVia(r); got != ViaMCP {
		t.Errorf("requestVia = %q, want %q for a genuine loopback call", got, ViaMCP)
	}
}

func TestUnsetTokenNeverLabelsAnythingMCP(t *testing.T) {
	// If the secret failed to generate, the safe failure is losing the
	// distinction — NOT treating every caller, or every empty header, as
	// an agent. A blanket "mcp" label would be a false record.
	h := &Handlers{ViaToken: ""}
	for _, presented := range []string{"", "mcp", "anything"} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", nil)
		r.Header.Set(mcp.ViaHeader, presented)
		if got := h.requestVia(r); got == ViaMCP {
			t.Errorf("with no configured token, header %q yielded MCP", presented)
		}
	}
}

func TestBrowserSessionIsUI(t *testing.T) {
	h := &Handlers{ViaToken: "secret"}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", nil)
	r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "x"})
	if got := h.requestVia(r); got != ViaUI {
		t.Errorf("requestVia = %q, want %q for a session cookie", got, ViaUI)
	}
}

func TestBearerCallWithoutSessionIsAPI(t *testing.T) {
	h := &Handlers{ViaToken: "secret"}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", nil)
	r.Header.Set("Authorization", "Bearer con_sa_whatever")
	if got := h.requestVia(r); got != ViaAPI {
		t.Errorf("requestVia = %q, want %q", got, ViaAPI)
	}
}

func TestWithViaDoesNotMutateTheCallersMap(t *testing.T) {
	// The same metadata map is usually handed to the domain-event emitter
	// a line earlier. Writing through would silently add `via` to a
	// published event payload — changing a contract by accident.
	h := &Handlers{ViaToken: "secret"}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", nil)
	original := map[string]any{"name": "checkout latency"}

	out := h.withVia(r, original)

	if _, leaked := original["via"]; leaked {
		t.Error("withVia mutated the caller's map — the event payload would inherit `via`")
	}
	if out["via"] == nil {
		t.Error("withVia did not stamp the channel")
	}
	if out["name"] != "checkout latency" {
		t.Error("withVia dropped existing metadata")
	}
}
