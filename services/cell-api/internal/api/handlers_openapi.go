// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Serves the API documentation: the generated OpenAPI document at
// /api/v1/openapi.json and a Redoc reference UI at /api/docs. The spec is
// produced from the route table by cmd/openapi-gen (make openapi) and embedded
// here, so it ships with the binary and can't drift from what's registered.
// Both endpoints are public (added to the auth skip-list in main.go).

package api

import (
	_ "embed"
	"net/http"
)

//go:embed openapi_gen.json
var openapiSpecJSON []byte

// openapiSpec serves the embedded OpenAPI 3.1 document.
func (h *Handlers) openapiSpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(openapiSpecJSON)
}

// apiDocs serves a Redoc page that renders the spec. Redoc is loaded from a CDN
// — fine for connected deployments; an air-gapped install can vendor the bundle.
func (h *Handlers) apiDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(redocPage))
}

const redocPage = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8"/>
    <title>Sluicio API</title>
    <meta name="viewport" content="width=device-width, initial-scale=1"/>
    <style>body { margin: 0; }</style>
  </head>
  <body>
    <redoc spec-url="/api/v1/openapi.json"></redoc>
    <script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script>
  </body>
</html>`
