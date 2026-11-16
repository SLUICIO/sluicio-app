// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
)

// channelSummary is a notification channel as an agent may read it:
// enough to tell whether a check reaches anyone, nothing that would let
// the reader send to it. A webhook or Slack URL carries its own token,
// so only its host is given; e-mail recipients are counted, not listed.
type channelSummary struct {
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	Kind            string     `json:"kind"`
	DestinationHost string     `json:"destination_host,omitempty"`
	Recipients      int        `json:"recipients,omitempty"`
	RecentFailures  int        `json:"recent_failures,omitempty"`
	LastFailureAt   *time.Time `json:"last_failure_at,omitempty"`
}

// profileSummary is a notification profile: which channels it sends to
// and for which environments. The ladder that picks one for a firing is
// check, then team, then integration, then the org default.
type profileSummary struct {
	ID              uuid.UUID   `json:"id"`
	Name            string      `json:"name"`
	IsDefault       bool        `json:"is_default"`
	TeamID          *uuid.UUID  `json:"team_id,omitempty"`
	ChannelIDs      []uuid.UUID `json:"channel_ids"`
	EnvironmentMode string      `json:"environment_mode"`
	Environments    []string    `json:"environments"`
}

func (h *Handlers) channelSummaries(r *http.Request) ([]channelSummary, error) {
	channels, err := h.Alerts.ListChannels(r.Context(), middleware.OrgID(r))
	if err != nil {
		return nil, err
	}
	failures := map[uuid.UUID]alerting.ChannelFailure{}
	if recent, ferr := h.Alerts.RecentDeliveryFailures(r.Context(), middleware.OrgID(r), time.Now().Add(-deliveryFailureWindow)); ferr == nil {
		for _, f := range recent {
			failures[f.ChannelID] = f
		}
	}
	out := make([]channelSummary, 0, len(channels))
	for _, c := range channels {
		s := channelSummary{ID: c.ID, Name: c.Name, Kind: c.Kind}
		switch c.Kind {
		case alerting.ChannelEmail:
			for _, a := range strings.FieldsFunc(c.Config["to"], func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
				if strings.Contains(a, "@") {
					s.Recipients++
				}
			}
		default:
			if u, perr := url.Parse(strings.TrimSpace(c.Config["url"])); perr == nil {
				s.DestinationHost = u.Hostname()
			}
		}
		if f, ok := failures[c.ID]; ok {
			s.RecentFailures = f.Count
			at := f.LastAt
			s.LastFailureAt = &at
		}
		out = append(out, s)
	}
	return out, nil
}

func (h *Handlers) profileSummaries(r *http.Request) ([]profileSummary, error) {
	out := []profileSummary{}
	if h.Profiles == nil {
		return out, nil
	}
	profiles, err := h.Profiles.List(r.Context(), middleware.OrgID(r))
	if err != nil {
		return nil, err
	}
	for _, p := range profiles {
		ids := p.ChannelIDs
		if ids == nil {
			ids = []uuid.UUID{}
		}
		envs := p.Environments
		if envs == nil {
			envs = []string{}
		}
		out = append(out, profileSummary{
			ID: p.ID, Name: p.Name, IsDefault: p.IsDefault, TeamID: p.GroupID,
			ChannelIDs: ids, EnvironmentMode: p.EnvironmentMode, Environments: envs,
		})
	}
	return out, nil
}

// notificationRouting: GET /api/v1/notification-routing
//
// The channels and notification profiles, summarised for a reader that
// needs to know who a check reaches (an agent, a reviewer) but must not
// be handed the credentials the channel list carries.
func (h *Handlers) notificationRouting(w http.ResponseWriter, r *http.Request) {
	channels, err := h.channelSummaries(r)
	if err != nil {
		h.Logger.Error("notification routing: channels", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	profiles, err := h.profileSummaries(r)
	if err != nil {
		h.Logger.Error("notification routing: profiles", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"channels":              channels,
		"notification_profiles": profiles,
		"routing": "A check with channel_ids sends to those channels. One without them uses the first notification profile " +
			"that applies to the firing's environment: the check's team's profile, then its integration's, then the org default.",
	})
}

// proposalOptions: GET /api/v1/proposals/options
//
// Every value a proposal may use, so an agent writes a valid one instead
// of guessing: the kinds and their fields, the check types with their
// spec fields and allowed values, and the channels a check may name.
// Built from the same lists the validators use.
func (h *Handlers) proposalOptions(w http.ResponseWriter, r *http.Request) {
	channels, err := h.channelSummaries(r)
	if err != nil {
		h.Logger.Error("proposal options: channels", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	profiles, err := h.profileSummaries(r)
	if err != nil {
		h.Logger.Error("proposal options: profiles", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	attrOps := make([]string, 0, len(validAttrOps))
	for op := range validAttrOps {
		attrOps = append(attrOps, op)
	}
	sort.Strings(attrOps)
	settings := make([]string, 0, len(integrationSettingFields))
	for f := range integrationSettingFields {
		settings = append(settings, f)
	}
	sort.Strings(settings)
	tunables := make([]string, 0, len(alertRuleTunables))
	for f := range alertRuleTunables {
		tunables = append(tunables, f)
	}
	sort.Strings(tunables)
	maxWindow := alerting.MaxCheckWindowSeconds

	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"proposal_kinds": []map[string]any{
			{"kind": proposalAlertRule, "target": "the alert rule's id", "fields": tunables,
				"about": "Tune an existing check. Send only the fields that change."},
			{"kind": proposalNewAlertRule, "target": "none", "fields": []string{"rule"},
				"about": "A new check (health check). rule is the body of POST /api/v1/alert-rules: name, severity, signal and its spec, the scope (integration_id, service_name or system_id) and optionally channel_ids and runbook."},
			{"kind": proposalNewCompletionRule, "target": "the integration's id", "fields": []string{"rule"},
				"about": "A new trace-completion rule. rule is the body of POST /api/v1/integrations/{id}/completion-rules: name, severity, start_span_name and stages."},
			{"kind": proposalIntegrationSetting, "target": "the integration's id", "fields": settings,
				"about": "Change an integration's settings. Each field replaces the whole value, in the shape its route takes: matchers as PUT /matchers, sources as PUT /sources, declared_environments a list of names, notify_unacknowledged_errors true or false."},
		},
		"severities": []string{string(alerting.SeverityInfo), string(alerting.SeverityWarning), string(alerting.SeverityCritical)},
		"check_types": []map[string]any{
			{
				"signal": "metric", "spec_field": "spec",
				"fields":       []string{"metric_name", "aggregation", "operator", "threshold", "for_window", "attrs", "split_by", "fire_on_no_data"},
				"aggregations": []string{"max", "avg", "min", "sum", "p95", "last", "increase", "rate", "age"},
				"operators":    []string{"gt", "gte", "lt", "lte", "eq", "neq"},
				"for_window":   "a Go duration such as \"5m\"; the window the aggregation reads",
			},
			{
				"signal": "log", "spec_field": "log_spec",
				"fields":         []string{"min_severity", "body_contains", "attrs", "threshold", "window_seconds", "comparison"},
				"comparison":     []string{alerting.LogComparisonAtLeast, alerting.LogComparisonFewerThan},
				"window_seconds": map[string]int{"min": 60, "max": maxWindow},
				"about":          "Counts matching log records. fewer_than fires on silence: no matching record in the window.",
			},
			{
				"signal": "trace", "spec_field": "trace_error_spec", "kind": alerting.TraceErrorSpecKind,
				"fields": []string{"kind", "threshold", "window_seconds", "attrs"}, "window_seconds": map[string]int{"min": 60, "max": maxWindow},
				"about": "Fires when at least threshold failed traces arrive in the window.",
			},
			{
				"signal": "trace", "spec_field": "trace_attribute_spec", "kind": alerting.TraceAttributeSpecKind,
				"fields": []string{"kind", "threshold", "window_seconds", "attrs"}, "window_seconds": map[string]int{"min": 60, "max": maxWindow},
				"about": "Fires when at least threshold traces carry a span matching attrs, failed or not. The attribute key span.name matches the span's name.",
			},
			{
				"signal": "trace", "spec_field": "trace_volume_spec", "kind": alerting.TraceVolumeSpecKind,
				"fields": []string{"kind", "threshold", "window_seconds"}, "window_seconds": map[string]int{"min": 60, "max": maxWindow},
				"about": "Fires when the scope sends fewer than threshold traces in the window; zero counts.",
			},
			{
				"signal": "trace", "spec_field": "trace_latency_spec", "kind": alerting.TraceLatencySpecKind,
				"fields": []string{"kind", "threshold_ms", "window_seconds", "aggregation"}, "aggregations": []string{"p95", "max"},
				"about": "Fires when the scope's span latency at the aggregation reaches threshold_ms.",
			},
		},
		"attribute_filter": map[string]any{
			"shape":     map[string]string{"key": "attribute key", "op": "operator", "value": "value"},
			"operators": attrOps,
			"keys":      "Any span, log or metric attribute key. service.name matches the service, span.name the span's name.",
		},
		"scopes":           "Exactly one of integration_id, service_name or system_id, or none for a check on the whole cell (needs org-wide rights to approve).",
		"environment_mode": []string{"each", "only"},
		"resolve_mode":     []string{"auto", "manual"},
		"matcher_operators": []string{
			string(integrations.OperatorEquals), string(integrations.OperatorPrefix), string(integrations.OperatorSuffix),
			string(integrations.OperatorContains), string(integrations.OperatorRegex), string(integrations.OperatorNotEquals),
			string(integrations.OperatorNotContains), string(integrations.OperatorExists), string(integrations.OperatorNotExists),
		},
		"source": map[string]any{
			"signals":      []string{integrations.SourceMetrics, integrations.SourceLogs},
			"origin_kinds": []string{integrations.OriginService, integrations.OriginSystem},
			"about":        "A metric or log source adds another service's or system's telemetry to an integration as evidence, never as a member.",
		},
		"completion_rule": map[string]any{
			"fields": []string{"name", "severity", "enabled", "start_span_name", "stages", "default_timeout_seconds", "channel_ids"},
			"stage":  map[string]any{"spans": "[{service (optional), span_name}]", "require": []string{"any", "all"}, "timeout_seconds": map[string]int{"min": 1, "max": 86400}},
			"about":  "A message is a trace: the trace id is the correlation key. A rule reads the integration's member services in every environment; it has no environment filter.",
		},
		"channels":              channels,
		"notification_profiles": profiles,
	})
}
