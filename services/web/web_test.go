// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "assets"), 0o755))
	must(os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>app"), 0o644))
	must(os.WriteFile(filepath.Join(root, "assets", "app-abc123.js"), []byte("js"), 0o644))
	must(os.WriteFile(filepath.Join(root, "third-party.json"), []byte(`{"components":[]}`), 0o644))
	return root
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestTheBundleIsServedLikeTheNginxItReplaces(t *testing.T) {
	up, _ := url.Parse("http://127.0.0.1:1")
	h := New(Config{Root: bundle(t), APIUpstream: up})

	// The app's own routes get index.html, uncached, so a release is
	// picked up on the next load.
	for _, p := range []string{"/", "/integrations/abc/metrics", "/third-party-licences"} {
		rec := get(h, p)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "app") || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: want index.html uncached, got %d %q %q", p, rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
		}
	}
	// Fingerprinted assets are cached for good; a missing one is a 404,
	// not index.html (which would be served as JavaScript).
	if rec := get(h, "/assets/app-abc123.js"); rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := get(h, "/assets/gone.js"); rec.Code != 404 {
		t.Errorf("missing asset: want 404, got %d", rec.Code)
	}
	// Other real files are served as themselves.
	if rec := get(h, "/third-party.json"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "components") {
		t.Errorf("third-party.json: %d %q", rec.Code, rec.Body.String())
	}
	// No escaping the bundle.
	if rec := get(h, "/../../etc/passwd"); strings.Contains(rec.Body.String(), "root:") {
		t.Error("path traversal reached outside the bundle")
	}
}

func TestTheAPIIsPassedThroughWithItsPathAndHost(t *testing.T) {
	var seen *http.Request
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer api.Close()
	up, _ := url.Parse(api.URL)
	front := httptest.NewServer(New(Config{Root: bundle(t), APIUpstream: up}))
	defer front.Close()

	req, _ := http.NewRequest(http.MethodGet, front.URL+"/api/v1/me?x=1", nil)
	req.Host = "sluicio.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != `{"ok":true}` {
		t.Fatalf("proxied response: %d %s", resp.StatusCode, body)
	}
	if seen.URL.Path != "/api/v1/me" || seen.URL.RawQuery != "x=1" {
		t.Errorf("path not passed through unchanged: %s?%s", seen.URL.Path, seen.URL.RawQuery)
	}
	if seen.Host != "sluicio.example.com" {
		t.Errorf("cell-api must see the browser's host, got %q", seen.Host)
	}
	if seen.Header.Get("X-Forwarded-Host") != "sluicio.example.com" || seen.Header.Get("X-Real-IP") == "" {
		t.Errorf("forwarding headers missing: %v", seen.Header)
	}
}
