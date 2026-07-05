// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"testing"
)

func urlReq(host string, headers map[string]string) *http.Request {
	r := &http.Request{Host: host, Header: http.Header{}}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestRequestBaseURL(t *testing.T) {
	// No proxy headers: scheme from TLS (nil → http), host from r.Host.
	if got := requestBaseURL(urlReq("localhost:8081", nil)); got != "http://localhost:8081" {
		t.Fatalf("plain: got %q", got)
	}
	// Reverse proxy: X-Forwarded-Proto + X-Forwarded-Host override r.Host, so
	// the public origin is reconstructed even when Host is the backend.
	got := requestBaseURL(urlReq("cell-api:8081", map[string]string{
		"X-Forwarded-Proto": "https",
		"X-Forwarded-Host":  "sluicio.acme.com",
	}))
	if got != "https://sluicio.acme.com" {
		t.Fatalf("proxied: got %q", got)
	}
	// Chained proxies: the first token of each header is the client-facing one.
	got = requestBaseURL(urlReq("backend:8081", map[string]string{
		"X-Forwarded-Proto": "https, http",
		"X-Forwarded-Host":  "sluicio.acme.com, internal.lan",
	}))
	if got != "https://sluicio.acme.com" {
		t.Fatalf("chained: got %q", got)
	}
}

func TestPublicBaseURL_PrefersConfigured(t *testing.T) {
	// SLUICIO_APP_URL wins over the request (the whole point behind a proxy),
	// and a trailing slash is trimmed so callback paths don't double up.
	t.Setenv("SLUICIO_APP_URL", "https://sluicio.acme.com/")
	got := publicBaseURL(urlReq("cell-api:8081", map[string]string{"X-Forwarded-Host": "internal.lan"}))
	if got != "https://sluicio.acme.com" {
		t.Fatalf("configured: got %q", got)
	}
}

func TestPublicBaseURL_FallsBackToRequest(t *testing.T) {
	t.Setenv("SLUICIO_APP_URL", "") // unset ⇒ reconstruct from the request
	got := publicBaseURL(urlReq("localhost:5173", map[string]string{"X-Forwarded-Proto": "http"}))
	if got != "http://localhost:5173" {
		t.Fatalf("fallback: got %q", got)
	}
}

// The redirect_uri must be identical at /start and /callback and match the IdP's
// allowed list — this locks the exact string in the configured case.
func TestSSOCallbackURL(t *testing.T) {
	t.Setenv("SLUICIO_APP_URL", "https://sluicio.acme.com")
	if got := ssoCallbackURL(urlReq("anything", nil)); got != "https://sluicio.acme.com/api/v1/auth/sso/callback" {
		t.Fatalf("configured: got %q", got)
	}
	t.Setenv("SLUICIO_APP_URL", "http://localhost:5173")
	if got := ssoCallbackURL(urlReq("cell-api:8081", nil)); got != "http://localhost:5173/api/v1/auth/sso/callback" {
		t.Fatalf("dev: got %q", got)
	}
}
