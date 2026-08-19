// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Output schemas (issue #8, WS1) — what each tool RETURNS, declared up
// front so a client knows the shape before it calls.
//
// Why bother, when the payload is already JSON? Because without a
// declared shape a model has to call a tool to find out what a tool
// gives back. It burns a turn discovering that services live under
// `services` and their health under `status`, and on a cold start it
// guesses — usually a plausible-but-wrong field name, then retries. The
// schema turns that discovery into something the client reads once,
// alongside the input schema, and plans against.
//
// Two rules keep these from becoming a liability.
//
// Every object is `additionalProperties: true`. A schema is a promise a
// client may VALIDATE against, so an exhaustive one turns "cell-api grew
// a field" into "the agent's call failed" — the schema would make the
// API less evolvable than it is. These describe what is worth keying
// off, not everything present.
//
// `required` names only the envelope key, never a field inside an item.
// The envelope is the contract (renaming `services` is a breaking API
// change either way); item fields are frequently conditional —
// `sample_trace_id` is absent when nothing errored, `slug` only on
// integrations — and marking them required would fail valid responses.
//
// The one runtime claim is that a tool's payload is a JSON OBJECT, since
// structuredContent must be one. That holds for every endpoint here;
// structuredResult enforces it rather than trusting it, and
// TestEveryToolDeclaresAnOutputSchema plus the e2e round-trip check
// catch a handler that changes shape.

package mcp

import (
	"encoding/json"
	"fmt"
)

// outSchema builds a result schema. Extra fields are always allowed —
// see the file comment.
func outSchema(props map[string]any, required ...string) map[string]any {
	out := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": true,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// arrOf describes a list of objects by the fields worth keying off. Nil
// props means "objects, shape not worth pinning" — a free-form block
// where naming a few keys would imply the rest don't exist.
func arrOf(desc string, itemProps map[string]any) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       objOf("", itemProps),
	}
}

func objOf(desc string, props map[string]any) map[string]any {
	out := map[string]any{"type": "object", "additionalProperties": true}
	if desc != "" {
		out["description"] = desc
	}
	// An empty `properties` is legal but reads as "this object has no
	// fields"; omitting it says "unconstrained", which is what we mean.
	if len(props) > 0 {
		out["properties"] = props
	}
	return out
}

func strOut(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func numOut(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
func intOut(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func boolOut(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func strArr(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string"}}
}

// windowOut is the from/to echo most endpoints attach, so an agent can
// state the period it is reporting on instead of inferring it.
var windowOut = objOf("The period the numbers cover.", map[string]any{
	"from": strOut("Start of the window (RFC3339)."),
	"to":   strOut("End of the window (RFC3339)."),
})

// ── shared item shapes ─────────────────────────────────────────────────

// failingCheckItem is one firing alert instance as the health feeds
// report it. Same shape in /errors, /unhealthy and the cell brief.
var failingCheckItem = map[string]any{
	"rule_name":        strOut("The alert rule that is firing."),
	"severity":         strOut("info | warning | critical."),
	"summary":          strOut("Human-readable description of the firing condition."),
	"started_at":       strOut("When it started firing (RFC3339). Current state — not windowed."),
	"target_kind":      strOut("service | integration | global."),
	"service_name":     strOut("The service it fired on, when target_kind is service."),
	"integration_name": strOut("The integration it fired on, when target_kind is integration."),
}

var errorServiceItem = map[string]any{
	"service_name":    strOut("The erroring service."),
	"error_traces":    intOut("Number of error traces in the window."),
	"first_error_at":  strOut("First error in the window (RFC3339)."),
	"last_error_at":   strOut("Most recent error (RFC3339)."),
	"sample_trace_id": strOut("A trace id to pass to sluicio_get_trace. Absent if none was captured."),
}

// serviceSummaryItem is the per-service rollup the services list
// returns. The errors feed reuses it verbatim, so it lives here rather
// than being described twice and drifting apart.
var serviceSummaryItem = map[string]any{
	"service_name":      strOut("The OTel service.name — the id used everywhere else."),
	"service_namespace": strOut("The OTel service.namespace, when set."),
	"status":            strOut("ok | errors | unhealthy | quiet. quiet means no traffic in the window — widen it before concluding a service is idle."),
	"trace_count":       intOut("Traces in the window."),
	"error_trace_count": intOut("Error traces in the window."),
	"first_seen":        strOut("First ever seen (RFC3339)."),
	"last_seen":         strOut("Most recent telemetry (RFC3339)."),
	"integrations": arrOf("Integrations this service belongs to.", map[string]any{
		"id": strOut("Integration id (uuid)."), "slug": strOut("Integration slug."), "name": strOut("Integration name."),
	}),
	"service_facets": arrOf("What the service does — the input/output kinds it handles (http, queue, database, …). A service may carry several.", map[string]any{
		"slug":   strOut("Facet key, e.g. http, queue, database."),
		"name":   strOut("Display label."),
		"source": strOut("auto when detected from telemetry, manual when an operator set it."),
	}),
	"is_system":        boolOut("True when the service is marked as a system (broker, database, …)."),
	"upstream_count":   intOut("Distinct callers in the dependency graph."),
	"downstream_count": intOut("Distinct callees."),
	"tags":             arrOf("Tags applied to the service.", map[string]any{"slug": strOut("Tag slug."), "name": strOut("Tag name.")}),
}

// unhealthyEntityItem is an integration or system grouped with the
// reasons it is unhealthy — the shape that makes sluicio_health worth
// calling over sluicio_errors.
var unhealthyEntityItem = map[string]any{
	"id":             strOut("Entity id (uuid)."),
	"name":           strOut("Entity name."),
	"slug":           strOut("Integration slug (integrations only)."),
	"type_key":       strOut("System type key (systems only)."),
	"status":         strOut("unhealthy | errors."),
	"failing_checks": arrOf("The firing health checks that explain the status.", failingCheckItem),
	"error_services": arrOf("Member services with errors in the window.", errorServiceItem),
}

// ── per-tool schemas ───────────────────────────────────────────────────

var (
	cellBriefOut = outSchema(map[string]any{
		"company":      strOut("Organisation name."),
		"environment":  strOut("Deployment environment label, e.g. production. Name it before acting."),
		"generated_at": strOut("When the brief was built (RFC3339)."),
		"window":       strOut("The period the counts cover, e.g. 24h."),
		"counts": objOf("Cell shape and health rollup.", map[string]any{
			"integrations":       intOut("Total integrations."),
			"systems":            intOut("Total systems."),
			"services":           intOut("Total discovered services."),
			"unhealthy_services": intOut("Services currently unhealthy."),
			"erroring_services":  intOut("Services with errors in the window."),
			"quiet_services":     intOut("Services with no traffic in the window."),
			"alert_rules":        intOut("Alert rules configured."),
		}),
		"incidents": arrOf("What is firing right now, worst severity first. Capped.", map[string]any{
			"rule_name": strOut("The rule that is firing."),
			"severity":  strOut("info | warning | critical."),
			"target":    strOut("The service or integration it fired on."),
			"since":     strOut("When it started firing (RFC3339)."),
			"summary":   strOut("Human-readable description."),
			"runbook":   strOut("The org's playbook for this rule. Follow it rather than inventing one."),
			"link":      strOut("Deep link into the UI, when available."),
		}),
		"unmonitored_services":  strArr("Services with traffic that no alert rule watches."),
		"unmonitored_truncated": boolOut("True when the unmonitored list was capped."),
		"pending_proposals":     intOut("Agent proposals awaiting human review."),
		"hint":                  strOut("Which tool to reach for next."),
	}, "counts", "incidents")

	listIntegrationsOut = outSchema(map[string]any{
		"integrations": arrOf("The org's integrations, visibility-filtered.", map[string]any{
			"id":                  strOut("Integration id (uuid) — pass to sluicio_get_integration."),
			"name":                strOut("Display name."),
			"slug":                strOut("Stable slug."),
			"description":         strOut("Free-text description."),
			"status":              strOut("ok | errors | unhealthy | quiet."),
			"trace_count":         intOut("Traces in the window."),
			"error_trace_count":   intOut("Error traces in the window."),
			"delayed_trace_count": intOut("Traces that breached a completion SLA."),
			"service_count":       intOut("Member services."),
			"services":            strArr("Member service names."),
			"tags":                arrOf("Tags applied to the integration.", map[string]any{"slug": strOut("Tag slug."), "name": strOut("Tag name.")}),
		}),
		"metadata_fields": arrOf("Custom metadata fields defined for this org.", map[string]any{
			"key": strOut("Field key."), "label": strOut("Display label."), "type": strOut("Value type."),
		}),
		"window": windowOut,
	}, "integrations")

	listServicesOut = outSchema(map[string]any{
		"services": arrOf("Discovered services with traffic and health over the window.", serviceSummaryItem),
		"window":   windowOut,
	}, "services")

	listSystemsOut = outSchema(map[string]any{
		"systems": arrOf("Systems — entities spanning member services.", map[string]any{
			"id":           strOut("System id (uuid) — pass to sluicio_get_system."),
			"name":         strOut("Display name."),
			"type_key":     strOut("System type key, e.g. rabbitmq. Look it up in sluicio_system_types."),
			"description":  strOut("Free-text description."),
			"members":      strArr("Member service names."),
			"member_count": intOut("Number of members."),
			"status":       strOut("Rolled-up health across members."),
			"docs_url":     strOut("Documentation for this system type on docs.sluicio.com."),
		}),
	}, "systems")

	getSystemOut = outSchema(map[string]any{
		"system": objOf("The system entity.", map[string]any{
			"id": strOut("System id (uuid)."), "name": strOut("Display name."),
			"type_key": strOut("System type key."), "description": strOut("Free-text description."),
			"members": strArr("Member service names."), "member_count": intOut("Number of members."),
			"status": strOut("Rolled-up health."), "docs_url": strOut("Type documentation URL."),
		}),
		"members":    arrOf("Member services with their own health over the window.", serviceSummaryItem),
		"can_manage": boolOut("Whether the calling token could edit this system."),
		"window":     windowOut,
	}, "system")

	systemTypesOut = outSchema(map[string]any{
		"system_types": arrOf("The system-types catalog: built-in plus this org's custom types.", map[string]any{
			"key":             strOut("Type key, e.g. rabbitmq."),
			"label":           strOut("Display label."),
			"built_in":        boolOut("False for org-defined types."),
			"is_system":       boolOut("Whether services of this type are treated as systems."),
			"detect_prefixes": strArr("Attribute prefixes that identify this type in telemetry."),
			"checks": arrOf("Starter health checks shipped with the type — what a new instance gets when the template is applied.", map[string]any{
				"name":        strOut("Check name."),
				"description": strOut("What the check is for."),
				"metric":      strOut("The metric it watches."),
				"agg":         strOut("Aggregation: avg, max, sum, …"),
				"op":          strOut("Comparison: gt, lt, …"),
				"severity":    strOut("info | warning | critical."),
			}),
			"docs_url": strOut("This type's page on docs.sluicio.com."),
		}),
	}, "system_types")

	errorsOut = outSchema(map[string]any{
		"failing_checks": arrOf("Health checks firing right now. Current state — not windowed.", failingCheckItem),
		"open_errors":    arrOf("Services with unacknowledged errors in the window.", errorServiceItem),
		"services":       arrOf("The unhealthy and erroring services themselves, worst first — the same per-service summary sluicio_list_services returns, filtered to the ones in trouble.", serviceSummaryItem),
		"counts":         objOf("Totals per category.", nil),
		"window":         windowOut,
	}, "failing_checks", "open_errors")

	unhealthyOut = outSchema(map[string]any{
		"integrations": arrOf("Unhealthy or erroring integrations, each with the reasons.", unhealthyEntityItem),
		"systems":      arrOf("Unhealthy or erroring systems, each with the reasons.", unhealthyEntityItem),
		"other": objOf("Failures that belong to no integration or system.", map[string]any{
			"failing_checks": arrOf("Firing checks with no owning entity.", failingCheckItem),
			"error_services": arrOf("Erroring services with no owning entity.", errorServiceItem),
		}),
		"counts": objOf("Totals per category.", nil),
		"window": windowOut,
	}, "integrations", "systems")

	digestOut = outSchema(map[string]any{
		"new_services": arrOf("Services first seen since the last visit.", map[string]any{
			"service_name": strOut("Service name."), "namespace": strOut("OTel namespace, when set."),
			"first_seen_at": strOut("When it first appeared (RFC3339)."),
		}),
		"failures": arrOf("Checks that started failing since the last visit.", map[string]any{
			"rule_name": strOut("The rule."), "severity": strOut("info | warning | critical."),
			"state": strOut("firing | resolved."), "started_at": strOut("When it started (RFC3339)."),
		}),
		"shared": arrOf("Newly shared resources.", nil),
		"counts": objOf("Totals per category.", nil),
		"since":  strOut("The digest's starting point (RFC3339)."),
	}, "new_services", "failures")

	metricCatalogOut = outSchema(map[string]any{
		"metrics": arrOf("Metrics ingested in the window.", map[string]any{
			"name":         strOut("Metric name — pass to sluicio_metric_series."),
			"type":         strOut("OTLP instrument type (gauge, sum, histogram)."),
			"unit":         strOut("Unit, when declared."),
			"value":        numOut("Most recent value."),
			"aggregation":  strOut("How series are combined for display."),
			"series_count": intOut("Distinct series under this name."),
			"point_count":  intOut("Datapoints in the window."),
			"rule_count":   intOut("Alert rules watching this metric. 0 means nobody is alerting on it."),
			"last_seen":    strOut("Most recent datapoint (RFC3339)."),
		}),
		"total_series": intOut("Series across all matching metrics."),
		"step_seconds": intOut("Bucket width used for the sparklines."),
		"window":       windowOut,
	}, "metrics")

	metricSeriesOut = outSchema(map[string]any{
		"metric":      strOut("The metric name queried."),
		"type":        strOut("OTLP instrument type."),
		"aggregation": strOut("How datapoints were aggregated per bucket."),
		"series": arrOf("One entry per emitting service.", map[string]any{
			"service_name": strOut("The emitting service."),
			"points": arrOf("Time-ordered datapoints, one per bucket.", map[string]any{
				"bucket": strOut("Bucket start (RFC3339)."),
				"value":  numOut("Aggregated value in that bucket."),
			}),
		}),
		"step_seconds": intOut("Bucket width in seconds."),
		"window":       windowOut,
	}, "series")

	searchTracesOut = outSchema(map[string]any{
		"results": arrOf("Matching traces. Drill in with sluicio_get_trace.", map[string]any{
			"trace_id":          strOut("Trace id (hex) — pass to sluicio_get_trace."),
			"trace_start":       strOut("When the trace started (RFC3339)."),
			"duration_ms":       numOut("End-to-end duration in milliseconds."),
			"has_error":         boolOut("Whether any span failed."),
			"matched_service":   strOut("The service whose span matched the filters."),
			"matched_span_name": strOut("The matching span's name."),
			"service_count":     intOut("Distinct services in the trace."),
			"total_spans":       intOut("Spans in the trace."),
		}),
		"total":       intOut("Matches returned. With a non-null next_cursor this is a lower bound, not the true total."),
		"next_cursor": objOf("Pass back to page further. Null when the result set is complete.", nil),
		"window":      windowOut,
	}, "results")

	getIntegrationOut = outSchema(map[string]any{
		"integration": objOf("The integration itself.", map[string]any{
			"id": strOut("Integration id (uuid)."), "name": strOut("Display name."),
			"slug": strOut("Stable slug."), "description": strOut("Free-text description."),
		}),
		"services": arrOf("Member services with per-service stats over the window.", serviceSummaryItem),
		"matchers": arrOf("The rules that attach services to this integration.", map[string]any{
			"attribute": strOut("The telemetry attribute matched, e.g. service.name."),
			"operator":  strOut("How it is compared: equals, prefix, …"),
			"value":     strOut("The value matched against."),
		}),
		"status":                strOut("Rolled-up status across members."),
		"tags":                  arrOf("Tags applied.", map[string]any{"slug": strOut("Tag slug."), "name": strOut("Tag name.")}),
		"message_count":         intOut("Messages (traces) in the window."),
		"error_message_count":   intOut("Failed messages in the window."),
		"delayed_message_count": intOut("Messages that breached a completion SLA."),
		"open_error_count":      intOut("Unacknowledged errors."),
		"failing_check_count":   intOut("Health checks firing now."),
		"window":                windowOut,
	}, "integration")

	searchLogsOut = outSchema(map[string]any{
		"logs": arrOf("Matching log records, newest first.", map[string]any{
			"timestamp":           strOut("Event time (RFC3339)."),
			"severity_number":     intOut("OTLP SeverityNumber: 9 info, 13 warn, 17 error, 21 fatal."),
			"severity_text":       strOut("The emitter's own severity label."),
			"body":                strOut("The log message."),
			"service_name":        strOut("Emitting service."),
			"trace_id":            strOut("Correlated trace id, when the record carries one — pass to sluicio_get_trace."),
			"span_id":             strOut("Correlated span id, when present."),
			"scope_name":          strOut("Instrumentation scope."),
			"log_attributes":      objOf("Record-level attributes.", nil),
			"resource_attributes": objOf("Resource-level attributes.", nil),
		}),
		"next_cursor": objOf("Pass back to page further. Null when the result set is complete.", nil),
		"window":      windowOut,
	}, "logs")

	alertInstancesOut = outSchema(map[string]any{
		"instances": arrOf("Rule firings, newest first — history, not the open-error feed.", map[string]any{
			"id":            strOut("Instance id (uuid)."),
			"alert_rule_id": strOut("The rule's id (uuid) — pass to sluicio_propose_check_tuning."),
			"rule_name":     strOut("Rule name."),
			"severity":      strOut("info | warning | critical."),
			"state":         strOut("firing | resolved."),
			"summary":       strOut("Human-readable description of the firing condition."),
			"started_at":    strOut("When it began firing (RFC3339)."),
			"ended_at":      strOut("When it resolved (RFC3339). Absent while still firing."),
		}),
	}, "instances")

	// usageSignalOut is the per-signal block; identical for metrics, logs
	// and traces except that only logs and traces break down by service.
	usageSignalOut = func(withServices bool) map[string]any {
		props := map[string]any{
			"total":             intOut("Rows ingested in the window."),
			"unused":            intOut("How many of them no alert rule watches."),
			"unused_rows":       intOut("Row count behind the unused figure."),
			"est_bytes_per_day": numOut("Estimated compressed bytes per day."),
			"est_bytes_per_30d": numOut("Estimated compressed bytes per 30 days — the number to quote when proposing a trim."),
		}
		if withServices {
			props["services"] = arrOf("Per-service breakdown, services without alert coverage first.", map[string]any{
				"service_name": strOut("Service name."),
				"rows":         intOut("Rows from this service."),
				"est_bytes":    numOut("Estimated compressed size."),
				"covered":      boolOut("Whether any alert rule watches this service. False is the trimming candidate."),
			})
		}
		return objOf("Ingest-versus-watched for this signal.", props)
	}

	usageReportOut = outSchema(map[string]any{
		"metrics": usageSignalOut(false),
		"logs":    usageSignalOut(true),
		"traces":  usageSignalOut(true),
		"window":  windowOut,
	}, "metrics", "logs", "traces")

	getTraceOut = outSchema(map[string]any{
		"trace_id": strOut("The trace id queried."),
		"spans": arrOf("Every span in the trace, across services.", map[string]any{
			"span_id":             strOut("Span id (hex)."),
			"span_name":           strOut("Operation name."),
			"span_kind":           strOut("OTel span kind: server, client, producer, consumer, internal."),
			"service_name":        strOut("The service that emitted the span."),
			"timestamp":           strOut("Span start (RFC3339)."),
			"duration_ms":         numOut("Span duration in milliseconds."),
			"status_code":         strOut("OTel status: unset, ok or error."),
			"status_message":      strOut("Error detail when the span failed."),
			"span_attributes":     objOf("Span-level attributes.", nil),
			"resource_attributes": objOf("Resource-level attributes.", nil),
		}),
	}, "trace_id", "spans")

	advisorOut = outSchema(map[string]any{
		"suggestions": arrOf("Findings the advisor currently stands behind, most valuable first.", map[string]any{
			"id":                  strOut("Suggestion id (uuid) — needed to accept or dismiss it in the UI."),
			"class":               strOut("Which evaluator produced it: T1-T6 (telemetry) or F1-F5 (alerting)."),
			"advisor":             strOut("telemetry | alerting."),
			"scope_kind":          strOut("What it is about: metric, service, attribute or rule."),
			"scope_id":            strOut("The thing itself — a metric name, a service, or a rule id."),
			"title":               strOut("The finding in one sentence."),
			"loss":                strOut("What is given up by acting. Quote this alongside the saving, never instead of it."),
			"snippet":             strOut("Ready-to-paste OTel collector config. Empty for alerting findings, where the change is inside Sluicio, and empty when snippet_unavailable explains why none could be written."),
			"snippet_target":      strOut("The collector version this snippet is written for. Collector configuration is not version-stable, so a snippet is only correct for the version named here. State it when you hand the config to someone."),
			"snippet_unavailable": strOut("Why there is no snippet, when the change cannot be expressed for the target collector version. The finding still stands - the cost it describes is real - but do not invent YAML in its place: a config that does not start is worse than none."),
			"evidence":            objOf("The counted facts behind the finding — volumes, last-consumed dates, firing counts.", nil),
			"weight":              intOut("Ranking key: estimated bytes/day for telemetry, firings for alerting."),
			"state":               strOut("open | accepted | verified | dismissed."),
			"first_seen_at":       strOut("When the advisor first made this finding (RFC3339) — how long it has been true."),
		}),
		"window_days": intOut("The observation window the findings were measured over. A number without its period is not evidence."),
	}, "suggestions")

	proposeOut = outSchema(map[string]any{
		"id":           strOut("The proposal's id (uuid)."),
		"state":        strOut("pending until a human decides. Nothing has changed yet."),
		"target_kind":  strOut("What kind of thing it would change, e.g. alert_rule."),
		"target_id":    strOut("The target's id (uuid)."),
		"target_label": strOut("The target's name, for quoting back to a human."),
		"changes": arrOf("The diff a reviewer sees: current value and proposed value per field.", map[string]any{
			"field":  strOut("The field being changed."),
			"before": map[string]any{"description": "The current value, snapshotted by the cell."},
			"after":  map[string]any{"description": "The proposed value."},
		}),
		"rationale":  strOut("The reason you gave, shown verbatim to the reviewer."),
		"expires_at": strOut("When the proposal lapses if nobody decides (RFC3339)."),
		"created_at": strOut("When it was filed (RFC3339)."),
	}, "id", "state", "changes")
)

// structuredResult parses a tool's payload for the structuredContent
// field of a tools/call result.
//
// It insists on a JSON object because the spec requires structuredContent
// to be one, and because every endpoint behind this catalogue returns an
// envelope object today. If one ever stops, this fails loudly at the call
// rather than shipping a payload that contradicts the declared schema —
// a client that validates would reject it anyway, and an error naming the
// tool is far easier to act on than a schema violation deep in a client.
func structuredResult(name, body string) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, fmt.Errorf("%s: cell-api returned a payload that is not a JSON object, "+
			"which contradicts this tool's declared output schema: %w", name, err)
	}
	return out, nil
}

// listProposalsOut describes the agent's own queue (issue #10).
//
// The states matter more than the fields: an agent that cannot tell
// "already pending" from "a human rejected this" will re-file the
// second kind, which spends a reviewer's attention on a decision they
// already made.
var listProposalsOut = outSchema(map[string]any{
	"proposals": arrOf("Proposals filed against this org, newest first.", map[string]any{
		"id":                strOut("Proposal id (uuid)."),
		"target_kind":       strOut("What kind of thing it changes, e.g. alert_rule."),
		"target_id":         strOut("The thing it changes. ABSENT when the proposal would CREATE something new."),
		"target_label":      strOut("Human-readable name of the target, or of what would be created."),
		"rationale":         strOut("Why it was proposed, shown verbatim to the reviewer."),
		"state":             strOut("pending | approved | rejected | expired | superseded. Only pending awaits a decision; re-filing something rejected wastes the reviewer's attention."),
		"proposed_by_label": strOut("Who or what filed it."),
		"dedup_key":         strOut("Identifies what a CREATE proposal would create. An identical pending proposal is refused as a duplicate."),
		"expires_at":        strOut("When it stops being reviewable (RFC3339)."),
		"created_at":        strOut("When it was filed (RFC3339)."),
	}),
})

// integrationCandidatesOut describes groupings derived from the call
// graph, deliberately labelled as candidates rather than conclusions.
var integrationCandidatesOut = outSchema(map[string]any{
	"candidates": arrOf("Groups of services that call each other and belong to no integration.", map[string]any{
		"services":        strArr("The service names in this group, sorted."),
		"internal_traces": intOut("Traces observed on hops INSIDE the group. This is the evidence the grouping exists; cite it in a rationale."),
		"dedup_key":       strOut("Matches what a create proposal for this grouping would use, so you can check sluicio_list_proposals before filing."),
	}),
	"unassigned_services": intOut("How many services belong to no integration at all. Lets you tell 'nothing to suggest' from 'nothing left to assign'."),
	"skipped_oversized": arrOf("Groupings too large to be useful, usually a hub service chaining unrelated flows together. Reported so a cap does not read as 'nothing else to find'.", map[string]any{
		"services":        strArr("The service names in the oversized component."),
		"internal_traces": intOut("Traces on hops inside it."),
	}),
})
