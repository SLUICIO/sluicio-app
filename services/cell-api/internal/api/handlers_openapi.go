// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Serves the API documentation, all embedded in the binary so it ships with
// the cell and works air-gapped (no CDN calls):
//
//   - /api/v1/openapi.json — the OpenAPI 3.1 document, generated from the
//     route table by cmd/openapi-gen (make openapi), so it can't drift.
//   - /api/v1/llms.txt     — the same routes as compact markdown, one line per
//     endpoint. The token-frugal format for AIs that read specs; AI *agents*
//     should prefer the MCP endpoint (POST /api/v1/mcp) instead.
//   - /api/docs            — interactive reference (Scalar): renders the spec
//     AND has a built-in client, so a human can paste a Bearer token and try
//     requests live against this cell, same-origin.
//   - /api/docs/scalar.js  — the vendored Scalar bundle (MIT,
//     @scalar/api-reference standalone — version noted in the file header).
//
// All four are public (auth skip-list in main.go): they describe the API,
// they never expose data.

package api

import (
	_ "embed"
	"net/http"
)

//go:embed openapi_gen.json
var openapiSpecJSON []byte

//go:embed llms_gen.txt
var llmsTxt []byte

//go:embed scalar.standalone.js
var scalarJS []byte

// openapiSpec serves the embedded OpenAPI 3.1 document.
func (h *Handlers) openapiSpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(openapiSpecJSON)
}

// llmsSpec serves the compact markdown API summary (llms.txt convention).
func (h *Handlers) llmsSpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(llmsTxt)
}

// scalarAsset serves the vendored Scalar bundle. It only changes with a
// release, so let browsers cache it for a day.
func (h *Handlers) scalarAsset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(scalarJS)
}

// apiDocs serves the interactive Scalar reference over the embedded spec.
func (h *Handlers) apiDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(scalarPage))
}

const scalarPage = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8"/>
    <title>Sluicio API</title>
    <meta name="viewport" content="width=device-width, initial-scale=1"/>
    <style>body { margin: 0; }</style>
  </head>
  <body>
    <div id="app"></div>
    <script src="/api/docs/scalar.js"></script>
    <script>
      Scalar.createApiReference('#app', {
        url: '/api/v1/openapi.json',
        // The try-it client targets this same cell; paste a personal access
        // token or service-account token as the Bearer credential.
        authentication: { preferredSecurityScheme: 'bearerAuth' },
        hideClientButton: false,
      });
    </script>
  </body>
</html>`
