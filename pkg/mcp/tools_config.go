// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Tools for reading and proposing monitoring configuration: the checks
// (health checks are alert rules bound to an integration, service or
// system), the trace-completion rules and the messages they flag, where
// notifications go, and proposals that create checks, completion rules
// and integration settings. Every proposal is inert until a human with
// the rights to make the change by hand approves it; approval replays
// that change as the reviewer.

package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// checkTuningFields are what sluicio_propose_check_tuning and a batch item
// of kind alert_rule may change: the API's alertRuleTunables.
var checkTuningFields = []string{"threshold", "severity", "for_window", "enabled", "channel_ids", "environment_mode", "environments"}

// completionRuleFields are what sluicio_propose_completion_rule_change
// may change: the API's completionRuleFields.
var completionRuleFields = []string{"name", "description", "severity", "enabled", "start_span_name", "stages", "default_timeout_seconds", "lookback_seconds", "channel_ids"}

func objProp(desc string) map[string]any {
	return map[string]any{"type": "object", "description": desc}
}

// proposeBody files a proposal with one change.
func (s *Server) propose(kind, targetID, field string, after any, rationale string) (string, error) {
	if rationale == "" {
		return "", fmt.Errorf("rationale is required — a proposal without a reason cannot be reviewed")
	}
	body := map[string]any{
		"target_kind": kind,
		"rationale":   rationale,
		"changes":     []map[string]any{{"field": field, "after": after}},
	}
	if targetID != "" {
		body["target_id"] = targetID
	}
	return s.post("/api/v1/proposals", nil, body)
}

// mergeInto adds key: the JSON object read from path to an object
// payload, or key_error when that read failed, so one missing piece
// does not cost the caller the rest.
func (s *Server) mergeInto(payload map[string]any, key, path string, q url.Values) {
	raw, err := s.get(path, q)
	if err != nil {
		payload[key+"_error"] = err.Error()
		return
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		payload[key+"_error"] = "unreadable response"
		return
	}
	payload[key] = v
}

func configTools(s *Server) []tool {
	return []tool{
		{Name: "sluicio_list_alert_rules", Output: listAlertRulesOut,
			Description: "List alert rules with where each stands now. A health check is an alert rule bound to an integration, a service or a system; filter by one of them to get that entity's checks. Each rule carries its signal and spec (metric aggregation, operator, threshold and for_window; or a log, failed-trace, span-attribute, low-traffic or latency spec), severity, enabled, channel_ids, runbook and environment scope. states maps rule id to firing | ok | disabled with firing_since, the last value a metric check read and when it last evaluated. delivery maps rule id to where its firings go: its own channels or the notification profile that routes it. Rules you may not see are left out.",
			Schema: objSchema(map[string]any{
				"integration": strProp("Only checks bound to this integration (uuid)."),
				"service":     strProp("Only checks bound to this service name."),
				"system":      strProp("Only checks bound to this system (uuid)."),
			}),
			Call: func(a map[string]any) (string, error) {
				q := url.Values{}
				for _, k := range []string{"integration", "service", "system"} {
					if v := argStr(a, k); v != "" {
						q.Set(k, v)
					}
				}
				return s.get("/api/v1/alert-rules", q)
			}},
		{Name: "sluicio_get_alert_rule", Output: getAlertRuleOut,
			Description: "Get one alert rule by id, with its full spec, scope, channel_ids, runbook, state (firing | ok | disabled, firing_since, last value, last evaluated) and delivery (where its firings go).",
			Schema:      objSchema(map[string]any{"id": strProp("The alert rule id (uuid).")}, "id"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "id")
				if id == "" {
					return "", fmt.Errorf("id is required")
				}
				return s.get("/api/v1/alert-rules/"+url.PathEscape(id), nil)
			}},
		{Name: "sluicio_list_completion_rules", Output: listCompletionRulesOut,
			Description: "List an integration's trace-completion rules: the start span that makes a trace one of its messages, the ordered stages a message must reach (each stage's spans, optionally naming the service that must emit them, and whether any one or all of them must arrive), the timeout of each stage, severity, enabled, channel_ids and delivery (where a delayed message is sent: its own channels or the integration's notification profile). A message is complete only when every stage is reached. A message is a trace: the trace id is the correlation key, there is no other. A rule reads the integration's member services in every environment; it has no environment filter.",
			Schema:      objSchema(map[string]any{"integration_id": strProp("The integration id (uuid).")}, "integration_id"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "integration_id")
				if id == "" {
					return "", fmt.Errorf("integration_id is required")
				}
				return s.get("/api/v1/integrations/"+url.PathEscape(id)+"/completion-rules", nil)
			}},
		{Name: "sluicio_delayed_messages", Output: delayedMessagesOut,
			Description: "The messages an integration's trace-completion rules flagged as delayed: trace id, the rule, the stage the message is stuck at and the span(s) it is still waiting for, when the stage's clock started, and whether it later arrived (resolved, delivered with delay) or was marked handled. Pass a trace id to sluicio_get_trace to see where it stopped.",
			Schema: objSchema(map[string]any{
				"integration_id": strProp("The integration id (uuid)."),
				"window":         strProp("How far back, e.g. 24h, 7d. Default 7d."),
			}, "integration_id"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "integration_id")
				if id == "" {
					return "", fmt.Errorf("integration_id is required")
				}
				return s.get("/api/v1/integrations/"+url.PathEscape(id)+"/completion-firings", rangeArg(a, "7d"))
			}},
		{Name: "sluicio_list_notification_channels", Output: notificationRoutingOut,
			Description: "Where notifications go: the channels (id, name, kind, the destination host of a webhook or Slack channel, the number of e-mail recipients, recent delivery failures) and the notification profiles that route to them. Credentials, URLs and addresses are not returned. A check with channel_ids sends to those; one without them uses the first profile that applies to the firing's environment: its integration's, then its team's default, then the org default. Trace-completion rules route the same way.",
			Schema:      objSchema(nil),
			Call: func(map[string]any) (string, error) {
				return s.get("/api/v1/notification-routing", nil)
			}},
		{Name: "sluicio_span_names", Output: spanNamesOut,
			Description: "The span names an integration's traffic carries, most frequent first, and its member services. Name a service to get only the spans that service emits: what a trace-completion stage or a span check (attribute key span.name) can wait for.",
			Schema: objSchema(map[string]any{
				"integration_id": strProp("The integration id (uuid)."),
				"service":        strProp("Only spans from this member service."),
				"window":         strProp("How far back, e.g. 24h, 7d. Default 24h."),
			}, "integration_id"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "integration_id")
				if id == "" {
					return "", fmt.Errorf("integration_id is required")
				}
				q := rangeArg(a, "24h")
				if v := argStr(a, "service"); v != "" {
					q.Set("service", v)
				}
				return s.get("/api/v1/integrations/"+url.PathEscape(id)+"/span-names", q)
			}},
		{Name: "sluicio_metric_label_values", Output: metricLabelValuesOut,
			Description: "The values one metric attribute (label) takes, with how many points carry each: which topic, subscription, queue or environment the series of a metric belong to. Use it before writing a check's attrs or split_by, instead of guessing names.",
			Schema: objSchema(map[string]any{
				"key":         strProp("The attribute key, e.g. messaging.destination.subscription.name."),
				"metric":      strProp("Only points of this metric."),
				"integration": strProp("Only points in this integration's slice and its metric sources (the integration's name)."),
				"search":      strProp("Only values containing this text."),
				"window":      strProp("How far back, e.g. 24h. Default 24h."),
			}, "key"),
			Call: func(a map[string]any) (string, error) {
				key := argStr(a, "key")
				if key == "" {
					return "", fmt.Errorf("key is required")
				}
				q := rangeArg(a, "24h")
				for arg, param := range map[string]string{"metric": "metric", "integration": "integration", "search": "q"} {
					if v := argStr(a, arg); v != "" {
						q.Set(param, v)
					}
				}
				return s.get("/api/v1/metric-attributes/"+url.PathEscape(key)+"/values", q)
			}},
		{Name: "sluicio_backtest_rule", Output: backtestOut,
			Description: "Dry-run a check against stored telemetry: how many times it would have fired over the last days, when, and the range of values it read. Pass rule_id for an existing check, optionally with the threshold or window a tuning would set, or rule (the body sluicio_propose_alert_rule takes) for a new one. Changes nothing. Cite the result in a proposal's rationale: a reviewer approves evidence, not a guess. Sampled at most 400 times per environment, so a breach shorter than step_seconds can be missed.",
			Schema: objSchema(map[string]any{
				"rule_id":        strProp("An existing alert rule (uuid)."),
				"rule":           objProp("A check that does not exist yet, as sluicio_propose_alert_rule takes it."),
				"threshold":      map[string]any{"type": "number", "description": "Override the threshold (threshold_ms for a latency check)."},
				"for_window":     strProp("Override a metric check's window, e.g. \"10m\"."),
				"window_seconds": map[string]any{"type": "integer", "description": "Override a log or trace check's window."},
				"days":           map[string]any{"type": "integer", "description": "Days of history, 1 to 30. Default 7."},
			}),
			Call: func(a map[string]any) (string, error) {
				body := map[string]any{}
				if v := argStr(a, "rule_id"); v != "" {
					body["rule_id"] = v
				}
				if v, ok := a["rule"].(map[string]any); ok {
					body["rule"] = v
				}
				if body["rule_id"] == nil && body["rule"] == nil {
					return "", fmt.Errorf("give rule_id or rule")
				}
				for _, k := range []string{"threshold", "window_seconds", "days"} {
					if v, ok := a[k]; ok && v != nil {
						body[k] = v
					}
				}
				if v := argStr(a, "for_window"); v != "" {
					body["for_window"] = v
				}
				return s.post("/api/v1/alert-rules/backtest", nil, body)
			}},
		{Name: "sluicio_proposal_options", Output: proposalOptionsOut,
			Description: "Every value a proposal may use, so you write a valid one instead of guessing: the proposal kinds and their fields; the check types with their spec fields, aggregations, operators, comparisons and window bounds; attribute filter operators; severities; matcher operators; source signals; the completion rule shape; and the channels and notification profiles a check may name (ids, without credentials). Read it before sluicio_propose_alert_rule, sluicio_propose_trace_completion_rule or sluicio_propose_integration_settings.",
			Schema:      objSchema(nil),
			Call: func(map[string]any) (string, error) {
				return s.get("/api/v1/proposals/options", nil)
			}},

		{Name: "sluicio_propose_alert_rule", Output: proposeOut, Annotations: proposeAnnotations,
			Description: "Propose a NEW check (health check). This does NOT create it: it files a proposal that a human who may create checks approves or rejects, and only approval creates it. rule is the body of POST /api/v1/alert-rules: name, severity, signal (metric, log or trace) with its spec (spec, log_spec, trace_error_spec, trace_attribute_spec, trace_volume_spec or trace_latency_spec), exactly one scope (integration_id, service_name or system_id) and optionally channel_ids, runbook and environment_mode. sluicio_proposal_options lists the valid values; dry-run it with sluicio_backtest_rule first and cite the result. The cell checks the rule when filed, and refuses one it would refuse to create.",
			Schema: objSchema(map[string]any{
				"rule":      objProp("The new check, as POST /api/v1/alert-rules takes it."),
				"rationale": strProp("Why the check is needed, citing what you observed and the dry run. Shown verbatim to the reviewer. Required."),
			}, "rule", "rationale"),
			Call: func(a map[string]any) (string, error) {
				rule, ok := a["rule"].(map[string]any)
				if !ok {
					return "", fmt.Errorf("rule is required, as an object")
				}
				return s.propose("new_alert_rule", "", "rule", rule, argStr(a, "rationale"))
			}},
		{Name: "sluicio_propose_trace_completion_rule", Output: proposeOut, Annotations: proposeAnnotations,
			Description: "Propose a NEW trace-completion rule on an integration. Nothing changes until an org admin approves it. rule: name, severity, enabled, start_span_name (the span that makes a trace one of the integration's messages), stages (in order; each has spans [{service (optional), span_name}], require any | all, and timeout_seconds), and optionally default_timeout_seconds and channel_ids. Name the service on a span when several services emit the same span name, and use require all when a message fans out to several receivers that must each deliver. Find span names with sluicio_span_names.",
			Schema: objSchema(map[string]any{
				"integration_id": strProp("The integration the rule goes on (uuid)."),
				"rule":           objProp("The new completion rule."),
				"rationale":      strProp("Why, citing what you observed. Shown verbatim to the reviewer. Required."),
			}, "integration_id", "rule", "rationale"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "integration_id")
				rule, ok := a["rule"].(map[string]any)
				if id == "" || !ok {
					return "", fmt.Errorf("integration_id and rule are required")
				}
				return s.propose("new_completion_rule", id, "rule", rule, argStr(a, "rationale"))
			}},
		{Name: "sluicio_propose_completion_rule_change", Output: proposeOut, Annotations: proposeAnnotations,
			Description: "Propose a change to an EXISTING trace-completion rule: its start span, stages, timeouts, channels, name, description, severity or enabled. Nothing changes until an org admin approves. Send only the fields that change; each takes the shape sluicio_list_completion_rules shows, and stages replaces every stage. A wrong start_span_name makes a rule follow no trace and never fire, so dry-run the change with sluicio_dry_run_completion_rule (rule_id and patch) and cite how many traces it would follow. The reviewer sees today's value beside yours.",
			Schema: objSchema(func() map[string]any {
				props := map[string]any{
					"rule_id":                 strProp("The completion rule's id (uuid), from sluicio_list_completion_rules."),
					"name":                    strProp("New name."),
					"description":             strProp("New description."),
					"severity":                strProp("info, warning or critical."),
					"enabled":                 map[string]any{"type": "boolean", "description": "Enable or disable the rule."},
					"start_span_name":         strProp("The span that makes a trace one of the integration's messages; its clock starts there. Confirm it with sluicio_span_names."),
					"stages":                  map[string]any{"type": "array", "description": "Every stage, in order: [{spans: [{service (optional), span_name}], require: any | all, timeout_seconds}]."},
					"default_timeout_seconds": map[string]any{"type": "integer", "description": "Timeout of a stage that sets none."},
					"lookback_seconds":        map[string]any{"type": "integer", "description": "How far back it looks for traces. Leave it out to keep it following the timeouts."},
					"channel_ids":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Its own channels; [] to follow the integration's notification profile."},
					"rationale":               strProp("Why, citing what you observed and the dry run. Shown verbatim to the reviewer. Required."),
				}
				return props
			}(), "rule_id", "rationale"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "rule_id")
				if id == "" {
					return "", fmt.Errorf("rule_id is required")
				}
				rationale := argStr(a, "rationale")
				if rationale == "" {
					return "", fmt.Errorf("rationale is required — a proposal without a reason cannot be reviewed")
				}
				changes := []map[string]any{}
				for _, f := range completionRuleFields {
					if v, ok := a[f]; ok && v != nil {
						changes = append(changes, map[string]any{"field": f, "after": v})
					}
				}
				if len(changes) == 0 {
					return "", fmt.Errorf("propose at least one of: %s", strings.Join(completionRuleFields, ", "))
				}
				return s.post("/api/v1/proposals", nil, map[string]any{
					"target_kind": "completion_rule", "target_id": id, "rationale": rationale, "changes": changes,
				})
			}},
		{Name: "sluicio_dry_run_completion_rule", Output: completionDryRunOut,
			Description: "Try a trace-completion rule against the integration's stored traces without saving or firing anything: how many traces it would follow (and how many the integration's services had, so a start span no trace carries shows as 0 of N), how many completed, are pending or are delayed, how many reached each stage, and some delayed traces with the span they wait for. Judged as of now over the last hours (default 24, at most 72). Pass rule (a draft, as sluicio_propose_trace_completion_rule takes it) or rule_id with patch (the fields a sluicio_propose_completion_rule_change would set). Cite the result in the proposal's rationale.",
			Schema: objSchema(map[string]any{
				"integration_id": strProp("The integration (uuid)."),
				"rule":           objProp("A draft rule: name, start_span_name, stages, default_timeout_seconds."),
				"rule_id":        strProp("An existing rule (uuid), instead of rule."),
				"patch":          objProp("With rule_id: the fields to change, e.g. {\"start_span_name\": \"Pipeline.orders.Receive\"}."),
				"hours":          map[string]any{"type": "integer", "description": "How many hours back to look. Default 24, at most 72."},
			}, "integration_id"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "integration_id")
				if id == "" {
					return "", fmt.Errorf("integration_id is required")
				}
				body := map[string]any{}
				for _, k := range []string{"rule", "rule_id", "patch", "hours"} {
					if v, ok := a[k]; ok && v != nil {
						body[k] = v
					}
				}
				return s.post("/api/v1/integrations/"+url.PathEscape(id)+"/completion-rules/dry-run", nil, body)
			}},
		{Name: "sluicio_propose_changes", Output: proposeBatchOut, Annotations: proposeAnnotations,
			Description: "File several proposals as ONE batch with one rationale, reviewed and approved together: setting up a cell's alerting (channels on every check, the start span of every completion rule) is one review instead of dozens. Nothing changes until a human approves. Each item is {target_kind, target_id, changes: [{field, after}]} with the kinds and fields of sluicio_proposal_options: alert_rule (a check's threshold, severity, for_window, enabled, channel_ids, environment_mode, environments), completion_rule (an existing completion rule's fields), integration_settings, new_alert_rule (no target_id; one change, field rule), new_completion_rule (target_id the integration; one change, field rule). At most 100 items, one item per target. Every item is checked when filed and an invalid one refuses the whole batch, naming it. On approval the cell checks every target for edits made since, then applies the items in order, stopping at the first that fails.",
			Schema: objSchema(map[string]any{
				"items": map[string]any{"type": "array", "description": "The proposals: [{target_kind, target_id, changes: [{field, after}]}].",
					"items": map[string]any{"type": "object"}},
				"rationale": strProp("Why, citing what you observed. Shown verbatim to the reviewer, once for the batch. Required."),
			}, "items", "rationale"),
			Call: func(a map[string]any) (string, error) {
				rationale := argStr(a, "rationale")
				if rationale == "" {
					return "", fmt.Errorf("rationale is required — a proposal without a reason cannot be reviewed")
				}
				items, ok := a["items"].([]any)
				if !ok || len(items) == 0 {
					return "", fmt.Errorf("items is required: at least one {target_kind, target_id, changes}")
				}
				return s.post("/api/v1/proposals/batch", nil, map[string]any{"rationale": rationale, "items": items})
			}},
		{Name: "sluicio_propose_integration_settings", Output: proposeOut, Annotations: proposeAnnotations,
			Description: "Propose changes to an integration's settings. Nothing changes until a human who may edit the integration approves. Each field you send REPLACES that setting whole, so read the current value with sluicio_get_integration first and send the complete new one: matchers (the full list, as [{attribute, operator, value, match_group}]; for example add a not_equals condition to exclude unrouted traffic), notify_unacknowledged_errors (true or false), declared_environments (list of names), sources (the full list of metric and log sources; sluicio_propose_metric_source adds one for you). The reviewer sees today's value beside yours.",
			Schema: objSchema(map[string]any{
				"integration_id":               strProp("The integration (uuid)."),
				"matchers":                     map[string]any{"type": "array", "description": "The complete new matcher list."},
				"notify_unacknowledged_errors": map[string]any{"type": "boolean", "description": "Notify through the integration's profile when its services record errors nobody acknowledged."},
				"declared_environments":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The environments the integration is expected to run in."},
				"sources":                      map[string]any{"type": "array", "description": "The complete new list of metric and log sources."},
				"rationale":                    strProp("Why, citing what you observed. Shown verbatim to the reviewer. Required."),
			}, "integration_id", "rationale"),
			Call: func(a map[string]any) (string, error) {
				id := argStr(a, "integration_id")
				if id == "" {
					return "", fmt.Errorf("integration_id is required")
				}
				rationale := argStr(a, "rationale")
				if rationale == "" {
					return "", fmt.Errorf("rationale is required — a proposal without a reason cannot be reviewed")
				}
				changes := []map[string]any{}
				for _, f := range []string{"matchers", "notify_unacknowledged_errors", "declared_environments", "sources"} {
					if v, ok := a[f]; ok && v != nil {
						changes = append(changes, map[string]any{"field": f, "after": v})
					}
				}
				if len(changes) == 0 {
					return "", fmt.Errorf("propose at least one of: matchers, notify_unacknowledged_errors, declared_environments, sources")
				}
				return s.post("/api/v1/proposals", nil, map[string]any{
					"target_kind": "integration_settings", "target_id": id, "rationale": rationale, "changes": changes,
				})
			}},
		{Name: "sluicio_propose_metric_source", Output: proposeOut, Annotations: proposeAnnotations,
			Description: "Propose adding one metric or log source to an integration: telemetry from a service or system that is NOT a member, such as a broker's queue or a Service Bus subscription, as evidence for its checks. It reads the integration's current sources and proposes the list with yours added, so the reviewer sees exactly one addition. Narrow it with conditions (e.g. the subscription name; find values with sluicio_metric_label_values) and metric_names globs.",
			Schema: objSchema(map[string]any{
				"integration_id": strProp("The integration (uuid)."),
				"signal":         strProp("metrics or logs. Default metrics."),
				"origin_kind":    strProp("service or system. Default service."),
				"origin":         strProp("The service name, or the system id."),
				"conditions":     map[string]any{"type": "array", "description": "Conditions on the rows' attributes, AND-ed: [{attribute, operator, value}]."},
				"metric_names":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Which metrics, as globs such as servicebus.topic.subscription.*. Empty is all."},
				"rationale":      strProp("Why, citing what you observed. Shown verbatim to the reviewer. Required."),
			}, "integration_id", "origin", "rationale"),
			Call: func(a map[string]any) (string, error) {
				id, origin := argStr(a, "integration_id"), argStr(a, "origin")
				if id == "" || origin == "" {
					return "", fmt.Errorf("integration_id and origin are required")
				}
				rationale := argStr(a, "rationale")
				if rationale == "" {
					return "", fmt.Errorf("rationale is required — a proposal without a reason cannot be reviewed")
				}
				raw, err := s.get("/api/v1/integrations/"+url.PathEscape(id)+"/sources", nil)
				if err != nil {
					return "", err
				}
				var current struct {
					Sources []map[string]any `json:"sources"`
				}
				if err := json.Unmarshal([]byte(raw), &current); err != nil {
					return "", fmt.Errorf("current sources unreadable: %w", err)
				}
				list := make([]map[string]any, 0, len(current.Sources)+1)
				for _, src := range current.Sources {
					list = append(list, sourceAsInput(src))
				}
				signal, kind := argStr(a, "signal"), argStr(a, "origin_kind")
				if signal == "" {
					signal = "metrics"
				}
				if kind == "" {
					kind = "service"
				}
				added := map[string]any{"signal": signal, "origin_kind": kind, "origin": origin, "conditions": []any{}, "metric_names": []any{}}
				if v, ok := a["conditions"].([]any); ok {
					added["conditions"] = v
				}
				if v, ok := a["metric_names"].([]any); ok {
					added["metric_names"] = v
				}
				list = append(list, added)
				return s.post("/api/v1/proposals", nil, map[string]any{
					"target_kind": "integration_settings", "target_id": id, "rationale": rationale,
					"changes": []map[string]any{{"field": "sources", "after": list}},
				})
			}},
	}
}

// sourceAsInput keeps the fields PUT /sources takes, so the proposed
// list compares like for like with the current one.
func sourceAsInput(src map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"signal", "origin_kind", "origin", "conditions", "metric_names"} {
		out[k] = src[k]
	}
	if out["conditions"] == nil {
		out["conditions"] = []any{}
	}
	if out["metric_names"] == nil {
		out["metric_names"] = []any{}
	}
	return out
}
