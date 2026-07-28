// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Where a change came from (issue #8, WS5).
//
// Once agents can file proposals and act through MCP, "who changed this"
// stops being enough — an admin needs to answer "what did the agents
// do?" separately from "what did people do?". Every audit entry records
// the channel it arrived through.
//
// The value has to be UNFORGEABLE, which is the whole difficulty. A
// plain "X-Sluicio-Via: mcp" header would be settable by any client,
// so a caller could dress its own writes up as an agent's, or hide an
// agent's among its own. In a record that exists for compliance, a field
// that can be lied to is worse than no field: it invites conclusions it
// cannot support.
//
// So the marker is a secret generated once per cell-api process. The
// MCP endpoint runs in that same process and hands the secret to the
// loopback client it builds; a request is agent-originated only if it
// presents that exact value. An outside caller cannot guess it, and it
// dies with the process, so a leaked one is worthless after a restart.
//
// Everything else is classified by how it authenticated: a browser
// session is the UI, a bearer token is direct API use.

package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"

	"github.com/sluicio/sluicio-app/pkg/mcp"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// Channels an entry can be attributed to.
const (
	ViaMCP = "mcp" // through the MCP server — an agent
	ViaAPI = "api" // direct call with an API/service-account token
	ViaUI  = "ui"  // a signed-in browser session
)

// NewViaToken mints the per-process loopback secret. Called once at
// startup; a fresh value each boot is deliberate.
func NewViaToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Without a secret we cannot distinguish loopback traffic. Return
		// empty rather than a guessable fallback: losing the distinction
		// is recoverable, publishing a forgeable one is not.
		return ""
	}
	return hex.EncodeToString(b)
}

// requestVia classifies where a request came from.
//
// The MCP check is a constant-time compare against the process secret,
// and only a non-empty configured token can ever match — an unset token
// must not turn every request into "mcp".
func (h *Handlers) requestVia(r *http.Request) string {
	if h.ViaToken != "" {
		presented := r.Header.Get(mcp.ViaHeader)
		if presented != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(h.ViaToken)) == 1 {
			return ViaMCP
		}
	}
	// A service account cannot hold a browser session, so its calls are
	// API calls by construction.
	if p := middleware.Principal(r); p.Kind == identity.PrincipalServiceAccount {
		return ViaAPI
	}
	if _, err := r.Cookie(middleware.SessionCookieName); err == nil {
		return ViaUI
	}
	if _, err := r.Cookie(middleware.SessionCookieLegacyName); err == nil {
		return ViaUI
	}
	return ViaAPI
}
