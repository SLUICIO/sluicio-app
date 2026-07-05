// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// cell-mcp is the STDIO transport for Sluicio's MCP server — for local MCP
// clients (Claude Desktop classic, Cursor, …) that spawn a host binary. The
// tool catalogue + protocol live in pkg/mcp; this is just the stdin/stdout
// frame loop. The same core is also served over HTTP by cell-api at
// POST /api/v1/mcp (the remote transport — see docs/mcp.md).
//
// Config (env):
//
//	SLUICIO_BASE_URL   cell-api base, e.g. https://sluicio.example.com (default http://localhost:8081)
//	SLUICIO_TOKEN      Bearer token (a viewer service-account token is ideal)
//
// Client config example (mcpServers):
//
//	{ "mcpServers": { "sluicio": {
//	    "command": "cell-mcp",
//	    "env": { "SLUICIO_BASE_URL": "https://…", "SLUICIO_TOKEN": "con_sa_…" } } } }
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/integration-monitor/integration-monitor/pkg/mcp"
)

func main() {
	base := env("SLUICIO_BASE_URL", "http://localhost:8081")
	auth := ""
	if tok := strings.TrimSpace(os.Getenv("SLUICIO_TOKEN")); tok != "" {
		auth = "Bearer " + tok
	}
	srv := mcp.NewServer(base, auth)

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // tolerate large frames
	w := bufio.NewWriter(os.Stdout)
	for sc.Scan() {
		if resp := srv.HandleMessage(sc.Bytes()); resp != nil {
			w.Write(resp)
			w.WriteByte('\n')
			w.Flush()
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "cell-mcp:", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
