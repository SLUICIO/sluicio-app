// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package mcp is the transport-agnostic core of Sluicio's MCP server: the
// curated tool catalogue + JSON-RPC 2.0 message handling. It is a thin
// client over the cell-api REST surface (/api/v1), authenticated with a
// Sluicio Bearer token.
//
// The catalogue is read-only with one deliberate exception: an agent may
// FILE a proposal (a reviewable change request) but never apply one. Every
// tool is annotated so a client knows which is which — see
// readOnlyAnnotations / proposeAnnotations. Two transports embed it:
//
//   - services/cell-mcp (stdio)         — local, for Claude Desktop classic etc.
//   - cell-api  POST /api/v1/mcp (HTTP) — remote, served on the same URL as the
//     app behind the existing reverse proxy + Bearer auth + RBAC.
//
// HandleMessage processes one JSON-RPC message and returns the response bytes
// (nil for notifications), so both transports just frame messages and call it.
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultProtocol = "2024-11-05"
	serverName      = "sluicio-mcp"
	serverVersion   = "0.3.0"
)

// Server holds the connection config for one MCP session. BaseURL is the
// cell-api base (e.g. https://host or http://127.0.0.1:8081); Auth is the full
// Authorization header value (e.g. "Bearer con_sa_…").
type Server struct {
	BaseURL string
	Auth    string
	HTTP    *http.Client
	tools   []tool
}

// NewServer builds a Server for the given cell-api base URL + Authorization
// header value.
func NewServer(baseURL, auth string) *Server {
	s := &Server{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Auth:    strings.TrimSpace(auth),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
	s.tools = buildTools(s)
	return s
}

// ── JSON-RPC 2.0 ───────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// HandleMessage processes one JSON-RPC message and returns the marshalled
// response, or nil for a notification (no id) / unparseable frame.
func (s *Server) HandleMessage(raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil
	}
	isNotification := len(req.ID) == 0
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = s.initialize(req.Params)
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": s.toolList()}
	case "tools/call":
		resp.Result = s.callTool(req.Params)
	default:
		if isNotification {
			return nil
		}
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	if isNotification {
		return nil
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	return out
}

func (s *Server) initialize(params json.RawMessage) map[string]any {
	proto := defaultProtocol
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(params, &p) == nil && p.ProtocolVersion != "" {
		proto = p.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": proto,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
		"instructions":    "Access to a Sluicio monitoring cell. Report on integration/service/system health, errors, alerts, logs, metrics, and traces/messages — everything is filtered by the token's RBAC scope. You cannot change any monitoring configuration directly. You MAY propose a tuning change to an existing alert rule (sluicio_propose_check_tuning); that files a reviewable request which a human approves or rejects, and only their approval applies it. Propose only when you can cite what you observed, and say so in the rationale.",
	}
}

// ── tools ──────────────────────────────────────────────────────────────

type tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Call        func(args map[string]any) (string, error)
	// Annotations overrides the read-only set. Non-nil only for tools
	// that write — inheriting "safe to call" would be a lie to the
	// client, and the guard test fails any writer that forgets.
	Annotations map[string]any
}

// readOnlyAnnotations are attached to every tool in the catalogue.
//
// The whole surface is read-only by construction: each tool is a GET
// against cell-api, and the single POST (messages/search) is a search
// with a body too complex for a query string, not a mutation. Declaring
// that lets a client show the catalogue as safe and skip per-call
// confirmation prompts — the difference between an agent that can
// answer a question in one turn and one that asks permission 17 times.
//
// If a tool that MUTATES is ever added — the proposal primitive in the
// agent design is the obvious candidate — it must carry its own
// annotations rather than inherit these.
// TestToolsAdvertiseReadOnlyAndAreReadOnly guards that by failing any
// tool that reaches cell-api by a writing verb while claiming to be
// read-only.
var readOnlyAnnotations = map[string]any{
	"readOnlyHint": true,
	// No environment change, so "destructive" is vacuous — state it
	// anyway; the spec's default for destructiveHint is true.
	"destructiveHint": false,
	// Repeating a read costs nothing beyond the query.
	"idempotentHint": true,
	// Closed world: everything is scoped to this cell and the caller's
	// RBAC, never the open internet.
	"openWorldHint": false,
}

func (s *Server) toolList() []map[string]any {
	out := make([]map[string]any, len(s.tools))
	for i, t := range s.tools {
		ann := t.Annotations
		if ann == nil {
			ann = readOnlyAnnotations
		}
		out[i] = map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.Schema,
			"annotations": ann,
		}
	}
	return out
}

// proposeAnnotations describe a tool that FILES a change request. It
// writes — a row lands in the proposals table — so readOnlyHint is
// false and a client is right to confirm before calling it.
//
// destructiveHint is false all the same: a proposal changes no
// monitoring config on its own. It sits in a queue until a human with
// the rights to make that change approves it, and that is the whole
// point of the primitive. idempotentHint is false because each call
// files a new proposal (superseding any earlier pending one for the
// same target), not because repeats are dangerous.
var proposeAnnotations = map[string]any{
	"readOnlyHint":    false,
	"destructiveHint": false,
	"idempotentHint":  false,
	"openWorldHint":   false,
}

func (s *Server) callTool(params json.RawMessage) map[string]any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolError("invalid tools/call params")
	}
	for _, t := range s.tools {
		if t.Name == p.Name {
			text, err := t.Call(p.Arguments)
			if err != nil {
				return toolError(err.Error())
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
		}
	}
	return toolError("unknown tool: " + p.Name)
}

func toolError(msg string) map[string]any {
	return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": msg}}}
}

func objSchema(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// get performs an authenticated GET against the cell-api and returns the body.
func (s *Server) get(path string, query url.Values) (string, error) {
	if s.Auth == "" {
		return "", fmt.Errorf("no Sluicio token configured")
	}
	u := s.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	return s.do(req)
}

// post performs an authenticated POST of a JSON body and returns the response.
func (s *Server) post(path string, query url.Values, body any) (string, error) {
	if s.Auth == "" {
		return "", fmt.Errorf("no Sluicio token configured")
	}
	u := s.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.do(req)
}

// do sets the auth + accept headers, executes the request, and returns the body
// (mapping 401 and other 4xx/5xx to errors).
func (s *Server) do(req *http.Request) (string, error) {
	req.Header.Set("Authorization", s.Auth)
	req.Header.Set("Accept", "application/json")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("401 unauthorized — check the token")
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("cell-api %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

func argStr(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// rangeArg builds a ?range= query from the window arg, falling back to def when
// unspecified. A generous default (24h) matters for low-frequency integrations:
// a 1h window misses sporadic traffic and reads to the model as "no traffic".
func rangeArg(args map[string]any, def string) url.Values {
	w := argStr(args, "window")
	if w == "" {
		w = def
	}
	return url.Values{"range": {w}}
}

func argBool(args map[string]any, key string, def bool) bool {
	if args != nil {
		if v, ok := args[key].(bool); ok {
			return v
		}
	}
	return def
}

func argInt(args map[string]any, key string, def int) int {
	if args != nil {
		switch v := args[key].(type) {
		case float64:
			return int(v)
		case int:
			return v
		}
	}
	return def
}

// buildTools is the curated, read-only tool catalogue.
func buildTools(s *Server) []tool {
	return []tool{
		// Start here. One call that answers "what am I looking at, and is
		// anything wrong?" — listed first because an agent reads the
		// catalogue top-down and this is the cheapest useful opening move.
		{Name: "sluicio_cell_brief", Description: "START HERE for orientation. One call returning the cell's shape and current state: the organisation and environment name, counts of integrations / systems / services (with how many are unhealthy, erroring or quiet), everything firing RIGHT NOW worst-first — each with its runbook when the rule has one — services carrying traffic that no alert rule watches, and how many agent proposals await review. Deliberately compact: it is an orientation call, not a data dump, so long lists are capped and counted. Follow up with sluicio_health for why something is unhealthy, or sluicio_search_traces / sluicio_search_logs to investigate. Everything is filtered to what your token may see.", Schema: objSchema(map[string]any{
			"window": strProp("Time window the counts cover, e.g. 1h, 24h, 7d. Default 24h."),
		}),
			Call: func(a map[string]any) (string, error) { return s.get("/api/v1/cell-brief", rangeArg(a, "24h")) }},

		{Name: "sluicio_list_integrations", Description: "List the org's integrations with their rolled-up health status (ok/errors/unhealthy/quiet) and traffic/error counts.", Schema: objSchema(nil),
			Call: func(a map[string]any) (string, error) { return s.get("/api/v1/integrations", nil) }},
		{Name: "sluicio_list_services", Description: "List discovered services with their TRAFFIC (trace + error counts), last-seen, and health over a time window (default 24h). This is the source of truth for whether a service has traffic — if a service shows zero, widen `window` (e.g. 7d) before concluding it has none, since low-frequency integrations may be quiet within a short window.", Schema: objSchema(map[string]any{"window": strProp("Time window, e.g. 1h, 24h, 7d. Default 24h.")}),
			Call: func(a map[string]any) (string, error) { return s.get("/api/v1/services", rangeArg(a, "24h")) }},
		{Name: "sluicio_list_systems", Description: "List systems (RabbitMQ, Kafka, etc.) — entities spanning member services — with rolled-up health.", Schema: objSchema(nil),
			Call: func(a map[string]any) (string, error) { return s.get("/api/v1/systems", nil) }},
		{Name: "sluicio_get_system", Description: "Get one system by id, including its member services and their health.", Schema: objSchema(map[string]any{"id": strProp("The system id (uuid) from sluicio_list_systems.")}, "id"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "id")
				if id == "" {
					return "", fmt.Errorf("id is required")
				}
				return s.get("/api/v1/systems/"+url.PathEscape(id), nil)
			}},
		{Name: "sluicio_system_types", Description: "List the system-types catalog (built-in + custom): detection prefixes and starter health checks per type.", Schema: objSchema(nil),
			Call: func(a map[string]any) (string, error) { return s.get("/api/v1/system-types", nil) }},
		{Name: "sluicio_errors", Description: "The 'in trouble' feed — integrations and systems with failing health checks + open errors, for triage.", Schema: objSchema(map[string]any{"window": strProp("Time window, e.g. 1h, 24h, 7d. Default 24h.")}),
			Call: func(a map[string]any) (string, error) { return s.get("/api/v1/errors", rangeArg(a, "24h")) }},
		{Name: "sluicio_health", Description: "What's unhealthy and WHY. Integrations and systems that are unhealthy or in error right now, each GROUPED with the failing health checks (rule, severity, since when) and error services that explain it — e.g. 'INT002 is unhealthy because HTTP 5xx rate is critical on order-api'. Use this over sluicio_errors when you want the reason per entity, not a flat list. Current-state: firing checks aren't windowed; the window scopes only the error/traffic portion.", Schema: objSchema(map[string]any{"window": strProp("Time window for the error/traffic portion, e.g. 1h, 24h, 7d. Default 24h.")}),
			Call: func(a map[string]any) (string, error) { return s.get("/api/v1/unhealthy", rangeArg(a, "24h")) }},
		{Name: "sluicio_error_report", Description: "Errors-since-<time> triage. Everything YOU'RE ALLOWED TO SEE (access-scoped to your token) that is erroring or unhealthy since a given time — default the last 24h, i.e. 'since yesterday' — each grouped with the failing health check(s) (rule, severity, since when) that make it unhealthy, plus the error services. This is the tool for questions like 'give me all errors since yesterday and the health check causing the unhealthy state'. Same current-state semantics as sluicio_health: firing checks reflect NOW; `since` scopes the error/traffic portion.", Schema: objSchema(map[string]any{"since": strProp("How far back to look — e.g. '24h' (since yesterday, the default), '2d', '7d', or an absolute 'from/to' ISO range like '2026-07-01T00:00:00Z/2026-07-02T00:00:00Z'.")}),
			Call: func(a map[string]any) (string, error) {
				since := argStr(a, "since")
				if since == "" {
					since = argStr(a, "window")
				}
				if since == "" {
					since = "24h"
				}
				return s.get("/api/v1/unhealthy", url.Values{"range": {since}})
			}},
		{Name: "sluicio_digest", Description: "The since-last-visit digest: new services, detected collectors to set up, and integrations that started failing (RBAC-filtered).", Schema: objSchema(nil),
			Call: func(a map[string]any) (string, error) { return s.get("/api/v1/digest", nil) }},
		{Name: "sluicio_metric_catalog", Description: "Search the metric catalog: each metric's current value, series count, and type. Optionally filter by a name query and/or scope to one service.", Schema: objSchema(map[string]any{
			"window": strProp("Time window, e.g. 1h, 24h. Default 1h."), "query": strProp("Substring to filter metric names by."), "service": strProp("Scope to a single service name."),
		}),
			Call: func(a map[string]any) (string, error) {
				q := rangeArg(a, "24h")
				if v := argStr(a, "query"); v != "" {
					q.Set("q", v)
				}
				if v := argStr(a, "service"); v != "" {
					q.Set("service", v)
				}
				return s.get("/api/v1/metric-catalog", q)
			}},
		{Name: "sluicio_search_traces", Description: "Search traces within a time window: filter by service, errors-only, and/or an error-message substring. Returns matching traces (trace_id, service, span, error flag, timing) — drill in with sluicio_get_trace. NOTE: returns up to `limit` traces (default 100); a non-null next_cursor in the response means more match beyond the limit, so treat the count as a lower bound.", Schema: objSchema(map[string]any{
			"service":     strProp("Scope to one service name (e.g. INT002)."),
			"query":       strProp("Substring to match against the error type / status message (e.g. 'timeout')."),
			"window":      strProp("Time window, e.g. 1h, 24h, 48h, 7d. Default 24h."),
			"errors_only": map[string]any{"type": "boolean", "description": "Only failed/error traces. Default true."},
			"limit":       map[string]any{"type": "integer", "description": "Max traces to return (1-1000). Default 100."},
		}),
			Call: func(a map[string]any) (string, error) {
				var filters []map[string]any
				if argBool(a, "errors_only", true) {
					filters = append(filters, map[string]any{"field": "status", "op": "is", "value": "err"})
				}
				if v := argStr(a, "service"); v != "" {
					filters = append(filters, map[string]any{"field": "service", "op": "is", "value": v})
				}
				if v := argStr(a, "query"); v != "" {
					filters = append(filters, map[string]any{"field": "errorType", "op": "contains", "value": v})
				}
				limit := argInt(a, "limit", 100)
				if limit <= 0 || limit > 1000 {
					limit = 100
				}
				return s.post("/api/v1/messages/search", rangeArg(a, "24h"), map[string]any{"filters": filters, "limit": limit})
			}},
		{Name: "sluicio_get_integration", Description: "Get one integration by id: its matchers, member services with per-service health over the window, tags, and aggregate status. Use an id from sluicio_list_integrations.", Schema: objSchema(map[string]any{
			"id":     strProp("The integration id (uuid)."),
			"window": strProp("Time window for the per-service stats, e.g. 1h, 24h. Default 24h."),
		}, "id"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "id")
				if id == "" {
					return "", fmt.Errorf("id is required")
				}
				return s.get("/api/v1/integrations/"+url.PathEscape(id), rangeArg(a, "24h"))
			}},
		{Name: "sluicio_search_logs", Description: "Search log events within a time window: free-text body match, OTLP severity floor (info≈9, warn≈13, error≈17, fatal≈21), scope to a service or an integration's member services, and attribute predicates. Results are RBAC-filtered to what the token may see. Returns up to `limit` logs plus a next_cursor when more match.", Schema: objSchema(map[string]any{
			"query":        strProp("Case-insensitive substring of the log body."),
			"min_severity": map[string]any{"type": "integer", "description": "OTLP SeverityNumber floor; 17 = errors and worse. Omit for any severity."},
			"service":      strProp("Scope to one service name."),
			"integration":  strProp("Scope to an integration name — its member services."),
			"window":       strProp("Time window, e.g. 1h, 24h, 7d. Default 24h."),
			"attrs": map[string]any{"type": "array", "description": "Attribute predicates, AND-ed. Each: {key, op, value} with op one of eq, neq, contains, not_contains, starts_with, ends_with, gt, gte, lt, lte, exists.",
				"items": map[string]any{"type": "object"}},
			"limit": map[string]any{"type": "integer", "description": "Max logs to return (1-500). Default 100."},
		}),
			Call: func(a map[string]any) (string, error) {
				q := rangeArg(a, "24h")
				if v := argStr(a, "query"); v != "" {
					q.Set("q", v)
				}
				if v := argInt(a, "min_severity", 0); v > 0 {
					q.Set("min_severity", fmt.Sprintf("%d", v))
				}
				if v := argStr(a, "service"); v != "" {
					q.Set("service", v)
				}
				if v := argStr(a, "integration"); v != "" {
					q.Set("integration", v)
				}
				limit := argInt(a, "limit", 100)
				if limit <= 0 || limit > 500 {
					limit = 100
				}
				q.Set("limit", fmt.Sprintf("%d", limit))
				if raw, ok := a["attrs"].([]any); ok {
					for _, item := range raw {
						if b, err := json.Marshal(item); err == nil {
							q.Add("attr", string(b))
						}
					}
				}
				return s.get("/api/v1/logs", q)
			}},
		{Name: "sluicio_metric_series", Description: "Fetch one metric's time series (per service) over a window — the values behind the catalog. Use a metric name from sluicio_metric_catalog.", Schema: objSchema(map[string]any{
			"metric":  strProp("The exact metric name (e.g. servicebus.queue.deadletter_messages)."),
			"service": strProp("Scope to one service name (optional; omit for all emitting services)."),
			"window":  strProp("Time window, e.g. 1h, 24h. Default 1h."),
		}, "metric"),
			Call: func(a map[string]any) (string, error) {
				m := argStr(a, "metric")
				if m == "" {
					return "", fmt.Errorf("metric is required")
				}
				q := rangeArg(a, "1h")
				q.Set("metric", m)
				if v := argStr(a, "service"); v != "" {
					q.Add("service", v)
				}
				return s.get("/api/v1/metric-series", q)
			}},
		{Name: "sluicio_alert_instances", Description: "Recent alert instances (rule firings) — each with its rule, severity, state (firing/resolved), summary, and timestamps. RBAC-filtered. The 'alerts' complement to sluicio_errors: this is rule-firing history, not the open-error feed.", Schema: objSchema(map[string]any{
			"limit": map[string]any{"type": "integer", "description": "Max instances (1-500). Default 100."},
		}),
			Call: func(a map[string]any) (string, error) {
				limit := argInt(a, "limit", 100)
				if limit <= 0 || limit > 500 {
					limit = 100
				}
				return s.get("/api/v1/alert-instances", url.Values{"limit": {fmt.Sprintf("%d", limit)}})
			}},
		{Name: "sluicio_usage_report", Description: "The admin usage report (Settings → Reports): per signal — metrics, logs, traces — how much of what's ingested is NOT watched by any alert rule, with storage estimates (bytes per day / per 30 days) and a per-service breakdown for logs and traces (services without alert coverage first, each with row count and estimated compressed size). Answers 'what are we storing that nobody alerts on?' and 'what would trimming save?'. To see WHICH metrics are unused, combine with sluicio_metric_catalog and filter rule_count == 0. Requires an admin token — others get a permission error.", Schema: objSchema(map[string]any{
			"window": strProp("Time window the report is computed over, e.g. 24h, 7d, 30d. Default 24h."),
		}),
			Call: func(a map[string]any) (string, error) { return s.get("/api/v1/reports/usage", rangeArg(a, "24h")) }},
		{Name: "sluicio_get_trace", Description: "Fetch one trace by id — all its spans across services, with timings and errors. Use a trace_id from sluicio_search_traces or a sample_trace_id from sluicio_errors.", Schema: objSchema(map[string]any{"trace_id": strProp("The trace id (hex string).")}, "trace_id"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "trace_id")
				if id == "" {
					return "", fmt.Errorf("trace_id is required")
				}
				return s.get("/api/v1/traces/"+url.PathEscape(id), nil)
			}},

		// The one tool that writes. It does not change monitoring config:
		// it files a proposal a human must approve, which is why it can
		// exist at all in a catalogue that is otherwise read-only.
		{Name: "sluicio_propose_check_tuning", Annotations: proposeAnnotations,
			Description: "Propose a tuning change to an existing alert rule — threshold, severity, for_window or enabled. This does NOT change anything: it files a reviewable proposal that a human with edit rights approves or rejects, and only approval applies it. Use it when a check is demonstrably too noisy or too quiet and you can say why, citing what you observed (e.g. 'fired 40 times in 24h, every instance auto-resolved within 2 minutes'). The rationale is shown verbatim to the reviewer and is required. Get rule ids from sluicio_health or sluicio_alert_instances; the current values come from the rule itself, so send only the fields you want changed.",
			Schema: objSchema(map[string]any{
				"rule_id":            strProp("The alert rule's id (uuid), from sluicio_health or sluicio_alert_instances."),
				"rationale":          strProp("Why this change is right, citing what you observed. Shown verbatim to the human reviewer. Required."),
				"threshold":          map[string]any{"type": "number", "description": "Proposed new threshold value."},
				"severity":           strProp("Proposed new severity: info, warning or critical."),
				"for_window":         strProp("Proposed new sustain window as a Go duration, e.g. \"5m\" — how long the condition must hold before firing."),
				"evaluation_seconds": map[string]any{"type": "integer", "description": "Proposed new evaluation interval in seconds."},
				"enabled":            map[string]any{"type": "boolean", "description": "Propose enabling or disabling the rule."},
			}, "rule_id", "rationale"),
			Call: func(a map[string]any) (string, error) {
				ruleID := argStr(a, "rule_id")
				if ruleID == "" {
					return "", fmt.Errorf("rule_id is required")
				}
				rationale := argStr(a, "rationale")
				if rationale == "" {
					return "", fmt.Errorf("rationale is required — a proposal without a reason cannot be reviewed")
				}
				// `before` is deliberately omitted: cell-api snapshots the
				// current values itself. An agent-supplied before could be
				// stale or wrong, and it is the input to the drift check
				// that protects a human's concurrent edit.
				changes := make([]map[string]any, 0, 5)
				for _, f := range []string{"threshold", "severity", "for_window", "enabled"} {
					if v, ok := a[f]; ok && v != nil {
						changes = append(changes, map[string]any{"field": f, "after": v})
					}
				}
				if len(changes) == 0 {
					return "", fmt.Errorf("propose at least one of: threshold, severity, for_window, enabled")
				}
				return s.post("/api/v1/proposals", nil, map[string]any{
					"target_kind": "alert_rule",
					"target_id":   ruleID,
					"rationale":   rationale,
					"changes":     changes,
				})
			}},
	}
}
