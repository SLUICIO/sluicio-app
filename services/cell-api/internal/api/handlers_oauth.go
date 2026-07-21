// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A minimal OAuth 2.1 authorization server for the remote MCP endpoint. MCP
// clients that only support OAuth connectors (e.g. Claude's remote connector /
// Cowork) need this to "Connect": discovery metadata, Dynamic Client
// Registration, an authorization-code + PKCE flow with a consent screen, and a
// token endpoint. The token endpoint mints a VIEWER-scoped api_token as the
// access token, so the MCP endpoint's existing Bearer auth + RBAC validate it
// unchanged and the grant can only ever be read-only. See docs/mcp.md.

package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/oauth"
)

const oauthAccessTokenDays = 90

// requestBaseURL reconstructs the public origin the client used from the
// request, honouring a reverse proxy's X-Forwarded-Proto (scheme) and
// X-Forwarded-Host (host) and falling back to the request's own TLS state and
// Host. Best-effort — prefer publicBaseURL, which uses the operator-configured
// SLUICIO_APP_URL when set (the only thing that's reliable behind a proxy that
// rewrites Host).
func requestBaseURL(r *http.Request) string {
	scheme := firstForwarded(r, "X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := firstForwarded(r, "X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

// firstForwarded returns the first comma-separated token of a header, trimmed.
// X-Forwarded-* carry a chain (client-facing value first) when several proxies
// are in front, so the first entry is the public-facing one.
func firstForwarded(r *http.Request, key string) string {
	v := r.Header.Get(key)
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// configuredAppURL is the operator-set public base URL (SLUICIO_APP_URL) with
// any trailing slash trimmed, or "" if unset. Same knob that drives alert +
// password-reset links, so one setting makes every outward-facing URL correct.
func configuredAppURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("SLUICIO_APP_URL")), "/")
}

// publicBaseURL is the authoritative outward-facing origin for URLs the browser
// or an IdP must reach back on — notably the SSO redirect_uri. SLUICIO_APP_URL
// wins when set: it's deterministic and REQUIRED behind a customer reverse
// proxy, where Host / X-Forwarded-* can't be trusted to reconstruct the public
// origin. Otherwise it reconstructs from the request (dev, or no proxy).
func publicBaseURL(r *http.Request) string {
	if u := configuredAppURL(); u != "" {
		return u
	}
	return requestBaseURL(r)
}

// ── discovery metadata ─────────────────────────────────────────────────

// GET /.well-known/oauth-protected-resource[/api/v1/mcp] (RFC 9728)
func (h *Handlers) oauthProtectedResource(w http.ResponseWriter, r *http.Request) {
	base := requestBaseURL(r)
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"resource":                 base + "/api/v1/mcp",
		"authorization_servers":    []string{base},
		"scopes_supported":         []string{"mcp:read"},
		"bearer_methods_supported": []string{"header"},
	})
}

// GET /.well-known/oauth-authorization-server (RFC 8414) + openid-configuration alias
func (h *Handlers) oauthASMetadata(w http.ResponseWriter, r *http.Request) {
	base := requestBaseURL(r)
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/api/v1/oauth/authorize",
		"token_endpoint":                        base + "/api/v1/oauth/token",
		"registration_endpoint":                 base + "/api/v1/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mcp:read"},
	})
}

// ── dynamic client registration (RFC 7591) ─────────────────────────────

// POST /api/v1/oauth/register
func (h *Handlers) oauthRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON body")
		return
	}
	var uris []string
	for _, u := range body.RedirectURIs {
		if u = strings.TrimSpace(u); u != "" {
			uris = append(uris, u)
		}
	}
	if len(uris) == 0 {
		oauthErr(w, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect_uri is required")
		return
	}
	id, err := randToken(24)
	if err != nil {
		oauthErr(w, http.StatusInternalServerError, "server_error", "client id generation failed")
		return
	}
	clientID := "mcp_" + id
	if err := h.OAuth.CreateClient(r.Context(), oauth.Client{ClientID: clientID, ClientName: body.ClientName, RedirectURIs: uris}); err != nil {
		h.Logger.Error("oauth client register failed", "err", err)
		oauthErr(w, http.StatusInternalServerError, "server_error", "registration failed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_name":                body.ClientName,
		"redirect_uris":              uris,
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// ── authorization-code flow ────────────────────────────────────────────

type authorizeParams struct {
	ClientID            string
	RedirectURI         string
	State               string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	Email               string // filled for the consent template
	ClientName          string
}

func parseAuthorizeParams(get func(string) string) authorizeParams {
	return authorizeParams{
		ClientID:            get("client_id"),
		RedirectURI:         get("redirect_uri"),
		State:               get("state"),
		Scope:               get("scope"),
		CodeChallenge:       get("code_challenge"),
		CodeChallengeMethod: get("code_challenge_method"),
	}
}

// GET /api/v1/oauth/authorize — validate, then render consent (or a sign-in
// prompt if there's no Sluicio session in this browser).
func (h *Handlers) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	ap := parseAuthorizeParams(r.URL.Query().Get)
	client, ok := h.validateAuthorizeClient(w, r, ap)
	if !ok {
		return
	}
	if r.URL.Query().Get("response_type") != "code" {
		redirectErr(w, r, ap.RedirectURI, ap.State, "unsupported_response_type")
		return
	}
	if ap.CodeChallenge == "" || (ap.CodeChallengeMethod != "" && ap.CodeChallengeMethod != "S256") {
		redirectErr(w, r, ap.RedirectURI, ap.State, "invalid_request")
		return
	}
	p, authed, _ := h.AuthMW.Resolve(r)
	if !authed || p.UserID == nil {
		renderOAuthPage(w, http.StatusOK, signInTmpl, map[string]any{"Base": requestBaseURL(r)})
		return
	}
	ap.Email = p.Email
	ap.ClientName = clientLabel(client)
	renderOAuthPage(w, http.StatusOK, consentTmpl, ap)
}

// POST /api/v1/oauth/authorize — the consent decision. On approve, mint a code
// and 302 back to the client's redirect_uri.
func (h *Handlers) oauthAuthorizeDecision(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderOAuthPage(w, http.StatusBadRequest, errorTmpl, map[string]any{"Message": "Malformed request."})
		return
	}
	ap := parseAuthorizeParams(r.PostFormValue)
	if _, ok := h.validateAuthorizeClient(w, r, ap); !ok {
		return
	}
	if r.PostFormValue("decision") != "approve" {
		redirectErr(w, r, ap.RedirectURI, ap.State, "access_denied")
		return
	}
	p, authed, _ := h.AuthMW.Resolve(r)
	if !authed || p.UserID == nil {
		renderOAuthPage(w, http.StatusOK, signInTmpl, map[string]any{"Base": requestBaseURL(r)})
		return
	}
	code, err := randToken(32)
	if err != nil {
		renderOAuthPage(w, http.StatusInternalServerError, errorTmpl, map[string]any{"Message": "Could not start the grant. Try again."})
		return
	}
	err = h.OAuth.CreateCode(r.Context(), oauth.AuthCode{
		Code:                code,
		ClientID:            ap.ClientID,
		RedirectURI:         ap.RedirectURI,
		CodeChallenge:       ap.CodeChallenge,
		CodeChallengeMethod: "S256",
		UserID:              *p.UserID,
		Scope:               ap.Scope,
		ExpiresAt:           time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		h.Logger.Error("oauth code create failed", "err", err)
		renderOAuthPage(w, http.StatusInternalServerError, errorTmpl, map[string]any{"Message": "Could not start the grant. Try again."})
		return
	}
	u, _ := url.Parse(ap.RedirectURI)
	q := u.Query()
	q.Set("code", code)
	if ap.State != "" {
		q.Set("state", ap.State)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// POST /api/v1/oauth/token — exchange the code (+ PKCE verifier) for a viewer
// access token (a minted api_token).
func (h *Handlers) oauthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}
	if r.PostFormValue("grant_type") != "authorization_code" {
		oauthErr(w, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code is supported")
		return
	}
	code := r.PostFormValue("code")
	verifier := r.PostFormValue("code_verifier")
	if code == "" || verifier == "" {
		oauthErr(w, http.StatusBadRequest, "invalid_request", "code and code_verifier are required")
		return
	}
	ac, err := h.OAuth.ConsumeCode(r.Context(), code)
	if err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid or expired")
		return
	}
	if ac.ClientID != r.PostFormValue("client_id") || ac.RedirectURI != r.PostFormValue("redirect_uri") {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "client_id / redirect_uri mismatch")
		return
	}
	if !verifyPKCE(ac.CodeChallenge, verifier) {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	tok, err := identity.NewToken(identity.TokenKindPersonal)
	if err != nil {
		oauthErr(w, http.StatusInternalServerError, "server_error", "token mint failed")
		return
	}
	expiresAt := expiryFromDays(oauthAccessTokenDays)
	if _, err := h.Identity.CreateAPIToken(r.Context(), "user", ac.UserID, "MCP (OAuth connector)", string(identity.RoleViewer), expiresAt, tok); err != nil {
		h.Logger.Error("oauth access token mint failed", "err", err)
		oauthErr(w, http.StatusInternalServerError, "server_error", "token mint failed")
		return
	}
	h.Logger.Info("oauth access token issued", "user_id", ac.UserID, "client_id", ac.ClientID)
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"access_token": tok.Plaintext,
		"token_type":   "Bearer",
		"expires_in":   oauthAccessTokenDays * 24 * 60 * 60,
		"scope":        "mcp:read",
	})
}

// ── helpers ────────────────────────────────────────────────────────────

// validateAuthorizeClient loads the client and checks the redirect_uri is one
// it registered. On failure it renders a safe error page (never redirects to an
// unvalidated URI) and returns ok=false.
func (h *Handlers) validateAuthorizeClient(w http.ResponseWriter, r *http.Request, ap authorizeParams) (oauth.Client, bool) {
	client, err := h.OAuth.GetClient(r.Context(), ap.ClientID)
	if err != nil {
		renderOAuthPage(w, http.StatusBadRequest, errorTmpl, map[string]any{"Message": "Unknown client. Remove and re-add the connector."})
		return oauth.Client{}, false
	}
	if !slicesContains(client.RedirectURIs, ap.RedirectURI) {
		renderOAuthPage(w, http.StatusBadRequest, errorTmpl, map[string]any{"Message": "redirect_uri is not registered for this client."})
		return oauth.Client{}, false
	}
	return client, true
}

func clientLabel(c oauth.Client) string {
	if strings.TrimSpace(c.ClientName) != "" {
		return c.ClientName
	}
	return "An MCP client"
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func verifyPKCE(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// oauthErr writes an RFC 6749 token/registration error object.
func oauthErr(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// redirectErr bounces an authorization error back to the (already-validated)
// client redirect_uri per RFC 6749 §4.1.2.1.
func redirectErr(w http.ResponseWriter, r *http.Request, redirectURI, state, code string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		renderOAuthPage(w, http.StatusBadRequest, errorTmpl, map[string]any{"Message": "Invalid redirect_uri."})
		return
	}
	q := u.Query()
	q.Set("error", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func renderOAuthPage(w http.ResponseWriter, status int, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = tmpl.Execute(w, data)
}

// ── consent / sign-in / error pages ────────────────────────────────────

const oauthPageCSS = `body{font-family:system-ui,-apple-system,Segoe UI,sans-serif;background:#0b1020;color:#e6e9f0;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}
.card{background:#151b2e;border:1px solid #26304d;border-radius:14px;padding:32px;max-width:430px;width:calc(100% - 32px);box-shadow:0 10px 40px rgba(0,0,0,.4)}
h1{font-size:19px;margin:0 0 10px}p{color:#9aa4bd;font-size:14px;line-height:1.55;margin:8px 0}
.brand{color:#6ea8fe;font-weight:600}.who{color:#e6e9f0;font-weight:500}
.scope{background:#0f1424;border:1px solid #26304d;border-radius:10px;padding:14px;margin:18px 0;font-size:13px;color:#c3cbe0}
.row{display:flex;gap:12px;margin-top:22px}
button,.btn{flex:1;padding:11px;border-radius:9px;border:0;font-size:14px;cursor:pointer;text-align:center;text-decoration:none}
.approve{background:#3b82f6;color:#fff}.deny{background:#26304d;color:#e6e9f0}`

var consentTmpl = template.Must(template.New("consent").Parse(`<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Authorize · Sluicio</title>
<style>` + oauthPageCSS + `</style></head><body><div class="card">
<h1>Authorize access</h1>
<p><span class="brand">{{.ClientName}}</span> wants to connect to your <span class="brand">Sluicio</span> cell.</p>
<div class="scope"><strong>Read-only.</strong> It can view integrations, services, systems, errors and metrics — it cannot make any changes.</div>
<p>Signed in as <span class="who">{{.Email}}</span></p>
<form method="POST" action="/api/v1/oauth/authorize">
<input type="hidden" name="client_id" value="{{.ClientID}}">
<input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
<input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
<input type="hidden" name="code_challenge_method" value="S256">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="scope" value="{{.Scope}}">
<div class="row">
<button class="deny" name="decision" value="deny" type="submit">Deny</button>
<button class="approve" name="decision" value="approve" type="submit">Authorize</button>
</div></form></div></body></html>`))

var signInTmpl = template.Must(template.New("signin").Parse(`<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Sign in · Sluicio</title>
<style>` + oauthPageCSS + `</style></head><body><div class="card">
<h1>Sign in required</h1>
<p>You're not signed in to <span class="brand">Sluicio</span> in this browser. Sign in, then return here and reload to authorize the connector.</p>
<div class="row">
<a class="btn approve" href="{{.Base}}/login" target="_blank" rel="noopener">Open Sluicio sign-in</a>
<a class="btn deny" href="javascript:location.reload()">Reload</a>
</div></div></body></html>`))

var errorTmpl = template.Must(template.New("oautherr").Parse(`<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Error · Sluicio</title>
<style>` + oauthPageCSS + `</style></head><body><div class="card">
<h1>Couldn't authorize</h1><p>{{.Message}}</p></div></body></html>`))
