// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
	"time"
)

// The session cookie is Secure wherever the browser reached the instance
// over HTTPS, and not on a plain-HTTP self-hosted install, which could
// not send a Secure cookie back.
func TestTheSessionCookieIsSecureOverHTTPS(t *testing.T) {
	plain := httptest.NewRequest("POST", "http://cell.example/api/v1/auth/login", nil)
	proxied := httptest.NewRequest("POST", "http://cell-api:8081/api/v1/auth/login", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	direct := httptest.NewRequest("POST", "https://cell.example/api/v1/auth/login", nil)
	direct.TLS = &tls.ConnectionState{}

	exp := time.Now().Add(time.Hour)
	if sessionCookie(plain, "s", exp).Secure {
		t.Error("a plain-HTTP request got a Secure cookie it cannot send back")
	}
	if !sessionCookie(proxied, "s", exp).Secure {
		t.Error("behind a TLS-terminating proxy the cookie is not Secure")
	}
	if !sessionCookie(direct, "s", exp).Secure {
		t.Error("over direct TLS the cookie is not Secure")
	}
}
