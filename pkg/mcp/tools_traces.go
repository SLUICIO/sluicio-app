// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Trace questions are asked in the integration's own words. A person
// asks about "Invoice Id 123", the telemetry carries iId=123, and the
// integration's message columns are where one is translated into the
// other. Without them an agent could only page through the latest
// traces and read every span, which is what it did.

// maxTracesWithSpans caps include_spans: each trace is one more read,
// and a question about one invoice is answered by a handful of them.
const maxTracesWithSpans = 10

// payloadOps are the operators the message search accepts on an
// attribute, as the Messages view offers them.
var payloadOps = map[string]bool{
	"equals": true, "not_equals": true, "contains": true, "not_contains": true,
	"in": true, "matches": true, "exists": true, "not_exists": true,
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// messageColumn is one column of an integration's Messages view.
type messageColumn struct {
	Kind  string `json:"kind,omitempty"`
	Key   string `json:"key"`
	Label string `json:"label"`
}

// isAttribute mirrors integrations.MessageColumn.IsAttribute: an empty
// kind is an attribute column written before built-ins existed.
func (c messageColumn) isAttribute() bool { return c.Kind != "builtin" }

// integrationRef is an integration as the trace tools need it: the id
// for the trace read, the name for the message search, and the columns
// that name its attributes.
type integrationRef struct {
	ID      string
	Name    string
	Columns []messageColumn
}

// resolveIntegration finds an integration by id, name or slug among
// those the token may see.
func (s *Server) resolveIntegration(ref string) (integrationRef, error) {
	id := ref
	if !uuidPattern.MatchString(ref) {
		raw, err := s.get("/api/v1/integrations", url.Values{"range": {"1h"}})
		if err != nil {
			return integrationRef{}, err
		}
		var list struct {
			Integrations []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Slug string `json:"slug"`
			} `json:"integrations"`
		}
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return integrationRef{}, fmt.Errorf("reading integrations: %w", err)
		}
		id = ""
		for _, i := range list.Integrations {
			if strings.EqualFold(i.Name, ref) || strings.EqualFold(i.Slug, ref) {
				id = i.ID
				break
			}
		}
		if id == "" {
			return integrationRef{}, fmt.Errorf("no integration named %q is visible to this token; sluicio_list_integrations lists them", ref)
		}
	}
	raw, err := s.get("/api/v1/integrations/"+url.PathEscape(id), url.Values{"range": {"1h"}})
	if err != nil {
		return integrationRef{}, err
	}
	var detail struct {
		Integration struct {
			ID             string          `json:"id"`
			Name           string          `json:"name"`
			MessageColumns []messageColumn `json:"message_columns"`
		} `json:"integration"`
	}
	if err := json.Unmarshal([]byte(raw), &detail); err != nil || detail.Integration.ID == "" {
		return integrationRef{}, fmt.Errorf("integration %q could not be read", ref)
	}
	return integrationRef{ID: detail.Integration.ID, Name: detail.Integration.Name, Columns: detail.Integration.MessageColumns}, nil
}

// attributeKey translates what a caller named an attribute into the key
// the telemetry carries: a key is kept, a column label becomes its key.
// The key wins when both match, since that is what the data says.
func attributeKey(given string, cols []messageColumn) string {
	for _, c := range cols {
		if c.isAttribute() && c.Key == given {
			return given
		}
	}
	for _, c := range cols {
		if c.isAttribute() && strings.EqualFold(strings.TrimSpace(c.Label), given) {
			return c.Key
		}
	}
	return given
}

// attributeLabels maps each attribute column's key to its label.
func attributeLabels(cols []messageColumn) map[string]string {
	out := map[string]string{}
	for _, c := range cols {
		if c.isAttribute() && c.Key != "" && c.Label != "" {
			out[c.Key] = c.Label
		}
	}
	return out
}

// attributeFilters turns the attributes argument into message search
// filters, and reports each label it translated.
func attributeFilters(raw any, cols []messageColumn) ([]map[string]any, []map[string]any, error) {
	list, _ := raw.([]any)
	var filters, resolved []map[string]any
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("attributes[%d] must be an object {key, op, value}", i)
		}
		given := strings.TrimSpace(fmt.Sprint(m["key"]))
		if m["key"] == nil || given == "" {
			return nil, nil, fmt.Errorf("attributes[%d] needs a key", i)
		}
		op := "equals"
		if v, ok := m["op"].(string); ok && strings.TrimSpace(v) != "" {
			op = strings.TrimSpace(v)
		}
		if !payloadOps[op] {
			return nil, nil, fmt.Errorf("attributes[%d]: op %q is not one of equals, not_equals, contains, not_contains, in, matches, exists, not_exists", i, op)
		}
		value := ""
		switch v := m["value"].(type) {
		case nil:
		case []any:
			parts := make([]string, 0, len(v))
			for _, p := range v {
				parts = append(parts, fmt.Sprint(p))
			}
			value = strings.Join(parts, ",")
		case float64:
			value = strconv.FormatFloat(v, 'f', -1, 64)
		default:
			value = fmt.Sprint(v)
		}
		if value == "" && op != "exists" && op != "not_exists" {
			return nil, nil, fmt.Errorf("attributes[%d] (%s) needs a value", i, given)
		}
		key := attributeKey(given, cols)
		if key != given {
			resolved = append(resolved, map[string]any{"given": given, "key": key})
		}
		filters = append(filters, map[string]any{"field": "payload", "fieldPath": key, "op": op, "value": value})
	}
	return filters, resolved, nil
}

// searchTraces is sluicio_search_traces.
func (s *Server) searchTraces(a map[string]any) (string, error) {
	var integ integrationRef
	if ref := argStr(a, "integration"); ref != "" {
		var err error
		if integ, err = s.resolveIntegration(ref); err != nil {
			return "", err
		}
	}
	attrFilters, resolved, err := attributeFilters(a["attributes"], integ.Columns)
	if err != nil {
		return "", err
	}

	// Errors only is the default for triage, and wrong for a lookup: the
	// invoice someone asks about is most often one that went through.
	var filters []map[string]any
	if argBool(a, "errors_only", len(attrFilters) == 0 && integ.ID == "") {
		filters = append(filters, map[string]any{"field": "status", "op": "is", "value": "err"})
	}
	if integ.Name != "" {
		filters = append(filters, map[string]any{"field": "integration", "op": "is", "value": integ.Name})
	}
	if v := argStr(a, "service"); v != "" {
		filters = append(filters, map[string]any{"field": "service", "op": "is", "value": v})
	}
	if v := argStr(a, "query"); v != "" {
		filters = append(filters, map[string]any{"field": "errorType", "op": "contains", "value": v})
	}
	filters = append(filters, attrFilters...)

	limit := argInt(a, "limit", 100)
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := rangeArg(a, "24h")
	envArg(q, a)
	raw, err := s.post("/api/v1/messages/search", q, map[string]any{"filters": filters, "limit": limit})
	if err != nil {
		return "", err
	}
	var payload map[string]any
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return raw, nil
	}

	// The columns travel with the rows, so the values are labelled from
	// the configuration they were queried under.
	var cols []messageColumn
	if b, err := json.Marshal(payload["message_columns"]); err == nil {
		_ = json.Unmarshal(b, &cols)
	}
	labels := attributeLabels(cols)
	results, _ := payload["results"].([]any)
	for _, r := range results {
		row, ok := r.(map[string]any)
		if !ok {
			continue
		}
		promoted, _ := row["promoted"].(map[string]any)
		if len(promoted) == 0 {
			continue
		}
		named := map[string]any{}
		for k, v := range promoted {
			if l, ok := labels[k]; ok {
				named[l] = v
			} else {
				named[k] = v
			}
		}
		row["columns"] = named
	}
	if len(resolved) > 0 {
		payload["resolved_attributes"] = resolved
	}

	if argBool(a, "include_spans", false) {
		n := 0
		for _, r := range results {
			if n == maxTracesWithSpans {
				break
			}
			row, ok := r.(map[string]any)
			if !ok {
				continue
			}
			id, _ := row["trace_id"].(string)
			if id == "" {
				continue
			}
			tq := url.Values{}
			if integ.ID != "" {
				tq.Set("integration", integ.ID)
			}
			traw, err := s.get("/api/v1/traces/"+url.PathEscape(id), tq)
			if err != nil {
				row["spans_error"] = err.Error()
				continue
			}
			var t struct {
				Spans []any `json:"spans"`
			}
			if json.Unmarshal([]byte(traw), &t) == nil {
				row["spans"] = t.Spans
				n++
			}
		}
		payload["spans_included"] = n
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return raw, nil
	}
	return string(out), nil
}

// getTrace is sluicio_get_trace.
func (s *Server) getTrace(a map[string]any) (string, error) {
	id := argStr(a, "trace_id")
	if id == "" {
		return "", fmt.Errorf("trace_id is required")
	}
	ref := argStr(a, "integration")
	if ref == "" {
		return s.get("/api/v1/traces/"+url.PathEscape(id), nil)
	}
	integ, err := s.resolveIntegration(ref)
	if err != nil {
		return "", err
	}
	raw, err := s.get("/api/v1/traces/"+url.PathEscape(id), url.Values{"integration": {integ.ID}})
	if err != nil {
		return "", err
	}
	var payload map[string]any
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return raw, nil
	}
	if labels := attributeLabels(integ.Columns); len(labels) > 0 {
		payload["attribute_labels"] = labels
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return raw, nil
	}
	return string(out), nil
}
