// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package web serves the Sluicio web app: the built bundle, with a
// fallback to index.html for the app's own routes, and /api/ passed
// through to cell-api, so one port serves the whole UI.
//
// It replaces the nginx the frontend image used to run. That image
// carried an operating system - Alpine, with busybox and its GPL - to
// serve static files and proxy one path. This needs neither: it runs on
// an empty image, so what the frontend image delivers is the bundle, this
// program and nothing else (issue #41).
package web

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Config is what the server needs.
type Config struct {
	// Root is the built bundle (index.html, assets/).
	Root string
	// APIUpstream is cell-api's base URL; /api/ is proxied to it with the
	// path unchanged, since cell-api mounts its routes under /api/v1.
	APIUpstream *url.URL
}

// New returns the server's handler.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", apiProxy(cfg.APIUpstream))
	mux.Handle("/", static(cfg.Root))
	return mux
}

func apiProxy(upstream *url.URL) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.SetXForwarded()
			// Keep the Host the browser used: cell-api builds absolute
			// links (OAuth callbacks, the MCP endpoint) from it, as it did
			// behind nginx's "proxy_set_header Host $host".
			r.Out.Host = r.In.Host
			r.Out.Header.Set("X-Real-IP", clientIP(r.In))
		},
		// MCP streams server-sent events through /api; flush as written.
		FlushInterval: -1,
		Transport: &http.Transport{
			Proxy:                 nil,
			ResponseHeaderTimeout: 60 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   32,
		},
	}
}

func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

// static serves the bundle. /assets/ are the fingerprinted files the
// build emits, cached for a year and never rewritten; anything else that
// is not a file is one of the app's routes, answered with index.html,
// which is never cached so a new release is picked up at once.
func static(root string) http.Handler {
	files := http.FileServer(http.Dir(root))
	index := filepath.Join(root, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			files.ServeHTTP(w, r)
			return
		}
		if clean != "/" {
			if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(clean))); err == nil && !st.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}
