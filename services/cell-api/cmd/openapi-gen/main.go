// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// openapi-gen generates the cell-api OpenAPI document FROM the route table, so
// the spec can't drift from the code. It AST-parses the route-registration
// source for every `mux.HandleFunc("<METHOD> <path>", …)` call, then emits an
// OpenAPI 3.1 paths object (methods, path params, tags, security). Go 1.22's
// ServeMux `{param}` pattern syntax is already OpenAPI's, so paths map 1:1.
//
//	go run ./services/cell-api/cmd/openapi-gen \
//	    -src services/cell-api/internal/api/handlers.go \
//	    -out services/cell-api/internal/api/openapi_gen.json
//	# -check exits non-zero if -out is stale (for CI / make openapi-check).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
)

func main() {
	src := flag.String("src", "services/cell-api/internal/api/handlers.go", "route-registration source file")
	out := flag.String("out", "services/cell-api/internal/api/openapi_gen.json", "output OpenAPI json path")
	check := flag.Bool("check", false, "exit non-zero if -out is out of date instead of writing")
	flag.Parse()

	routes, err := extractRoutes(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "openapi-gen:", err)
		os.Exit(1)
	}
	doc := buildDoc(routes)
	rendered, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "openapi-gen: marshal:", err)
		os.Exit(1)
	}
	rendered = append(rendered, '\n')

	if *check {
		existing, _ := os.ReadFile(*out)
		if !bytes.Equal(existing, rendered) {
			fmt.Fprintf(os.Stderr, "openapi-gen: %s is out of date — run `make openapi`\n", *out)
			os.Exit(1)
		}
		fmt.Printf("openapi-gen: %s is up to date (%d routes)\n", *out, len(routes))
		return
	}
	if err := os.WriteFile(*out, rendered, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "openapi-gen: write:", err)
		os.Exit(1)
	}
	fmt.Printf("openapi-gen: wrote %s (%d routes)\n", *out, len(routes))
}

type route struct {
	Method string
	Path   string
}

// extractRoutes parses src and returns every mux.HandleFunc("<METHOD> <path>")
// route in registration order.
func extractRoutes(src string) ([]route, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", src, err)
	}
	var out []route
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "HandleFunc" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pat := strings.Trim(lit.Value, "`\"")
		parts := strings.Fields(pat)
		if len(parts) != 2 { // want "METHOD /path"
			return true
		}
		out = append(out, route{Method: strings.ToUpper(parts[0]), Path: parts[1]})
		return true
	})
	return out, nil
}

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// publicPath reports whether a path is reachable without authentication.
func publicPath(p string) bool {
	return strings.HasPrefix(p, "/api/v1/auth/") ||
		p == "/healthz" ||
		p == "/api/v1/openapi.json" ||
		p == "/api/docs"
}

// tagFor groups a path under a resource tag (the first meaningful segment).
func tagFor(p string) string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	// drop "api","v1"
	i := 0
	for i < len(segs) && (segs[i] == "api" || segs[i] == "v1") {
		i++
	}
	if i >= len(segs) {
		return "misc"
	}
	if segs[i] == "" {
		return "misc"
	}
	return segs[i]
}

func summaryFor(method, p, tag string) string {
	last := p[strings.LastIndex(p, "/")+1:]
	byID := strings.HasPrefix(last, "{")
	noun := strings.TrimSuffix(tag, "s")
	switch method {
	case "GET":
		if byID {
			return "Get " + noun
		}
		return "List " + tag
	case "POST":
		return "Create " + noun
	case "PUT", "PATCH":
		return "Update " + noun
	case "DELETE":
		return "Delete " + noun
	}
	return method + " " + p
}

func buildDoc(routes []route) map[string]any {
	paths := map[string]map[string]any{}
	tagSet := map[string]bool{}

	for _, r := range routes {
		tag := tagFor(r.Path)
		tagSet[tag] = true

		op := map[string]any{
			"summary":     summaryFor(r.Method, r.Path, tag),
			"operationId": operationID(r.Method, r.Path),
			"tags":        []string{tag},
			"responses": map[string]any{
				"200": map[string]any{"description": "OK"},
			},
		}
		if publicPath(r.Path) {
			op["security"] = []any{} // explicitly no auth
		} else {
			(op["responses"].(map[string]any))["401"] = map[string]any{"description": "Unauthenticated"}
		}
		if params := pathParams(r.Path); len(params) > 0 {
			op["parameters"] = params
		}

		if paths[r.Path] == nil {
			paths[r.Path] = map[string]any{}
		}
		paths[r.Path][strings.ToLower(r.Method)] = op
	}

	tags := make([]map[string]any, 0, len(tagSet))
	names := make([]string, 0, len(tagSet))
	for t := range tagSet {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, t := range names {
		tags = append(tags, map[string]any{"name": t})
	}

	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Sluicio API",
			"version":     "v1",
			"description": "Sluicio cell-api. Authenticate with a session cookie (browser) or a Bearer token — a personal access token or a service-account token (see Settings → Service accounts). Generated from the route table; do not edit by hand.",
		},
		"servers": []any{map[string]any{"url": "/"}},
		"security": []any{
			map[string]any{"bearerAuth": []any{}},
			map[string]any{"cookieAuth": []any{}},
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type": "http", "scheme": "bearer",
					"description": "Personal access token (con_pat_…) or service-account token (con_sa_…).",
				},
				"cookieAuth": map[string]any{
					"type": "apiKey", "in": "cookie", "name": "Sluicio-Session",
				},
			},
		},
		"tags":  tags,
		"paths": paths,
	}
}

func pathParams(p string) []any {
	var out []any
	for _, m := range pathParamRe.FindAllStringSubmatch(p, -1) {
		out = append(out, map[string]any{
			"name": m[1], "in": "path", "required": true,
			"schema": map[string]any{"type": "string"},
		})
	}
	return out
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func operationID(method, p string) string {
	clean := nonAlnum.ReplaceAllString(strings.Trim(p, "/"), "_")
	return strings.ToLower(method) + "_" + clean
}
