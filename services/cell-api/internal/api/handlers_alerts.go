// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// Alert rules + notification channels. A metric rule watches one OTLP
// metric (optionally scoped by attribute filters), aggregates it over a
// window, and fires when the aggregate crosses a threshold; matching
// alert instances are delivered to the routed channels by the evaluator
// + delivery worker. See package alerting and handlers_metrics_explorer.go.

type alertRuleRequest struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Severity    string                  `json:"severity"`
	Enabled     *bool                   `json:"enabled"`
	Signal      string                  `json:"signal"` // "metric" (default), "log", or "trace_error"
	Spec        alerting.MetricRuleSpec `json:"spec"`
	LogSpec     *alerting.LogRuleSpec   `json:"log_spec"` // required when signal == "log"
	// TraceErrorSpec is required for a failed-trace rule (signal "trace"
	// with no trace_latency_spec): fire when the bound integration
	// accumulates >= threshold failed traces over the window.
	TraceErrorSpec *alerting.TraceErrorRuleSpec `json:"trace_error_spec"`
	// TraceLatencySpec is required for a response-time rule (signal
	// "trace" with this spec set): fire when the bound scope's windowed
	// quantile span latency is at or above threshold_ms.
	TraceLatencySpec *alerting.TraceLatencyRuleSpec `json:"trace_latency_spec"`
	// TraceVolumeSpec is required for a low-traffic rule (signal "trace"
	// with this spec set): fire when the bound scope produces fewer than
	// threshold distinct traces over the window (zero counts as below).
	TraceVolumeSpec *alerting.TraceVolumeRuleSpec `json:"trace_volume_spec"`
	// TraceAttributeSpec is required for a span-attribute rule (signal
	// "trace", kind trace_attribute): count traces carrying a span whose
	// attributes match, error or not.
	TraceAttributeSpec *alerting.TraceAttributeRuleSpec `json:"trace_attribute_spec"`
	ChannelIDs         []string                         `json:"channel_ids"`
	// Runbook: what to do when this fires. Travels in notification and
	// event payloads and MCP responses, so responders (human or agent)
	// get the org's playbook without a second lookup.
	Runbook       string `json:"runbook"`
	IntegrationID string `json:"integration_id"` // "" = not bound to an integration
	SystemID      string `json:"system_id"`      // "" = not bound to a system
	ServiceName   string `json:"service_name"`   // "" = not bound to a service
	GroupID       string `json:"group_id"`       // "" = org-wide (no owning team)
	TitleTemplate string `json:"title_template"` // "" = built-in summary
	BodyTemplate  string `json:"body_template"`  // "" = built-in summary
	// NotificationContent: which enrichment blocks (service / integration /
	// metadata / failing check) the email + webhook include, plus an optional
	// inline Liquid email override. nil = no enrichment + org default.
	NotificationContent *alerting.NotificationContent `json:"notification_config"`
	// Source: "telemetry" (default — aggregate an OTLP metric) or
	// "pushed" (value POSTed in by a scraper). Metric rules only.
	Source string `json:"source"`
	// DisplayOnService surfaces the check's latest reading as a value
	// tile on its bound service page; Unit is that tile's display unit.
	DisplayOnService bool   `json:"display_on_service"`
	Unit             string `json:"unit"`
	// ResolveMode: "auto" (self-recovering) or "manual" (firing until
	// acknowledged). Empty → defaulted by signal (metric/pushed → auto,
	// log + failed-trace → manual) to preserve prior behaviour.
	ResolveMode string `json:"resolve_mode"`
}

// validateLogRuleSpec checks a log rule_spec before persistence. Reuses
// the same attribute key/op allow-lists as the logs filters.
func validateLogRuleSpec(spec *alerting.LogRuleSpec) error {
	spec.BodyContains = strings.TrimSpace(spec.BodyContains)
	if spec.Threshold < 1 {
		spec.Threshold = 1
	}
	// Direction: default to the historical "at_least" (flood) when unset;
	// only "fewer_than" (drought) is the alternative.
	switch spec.Comparison {
	case "", alerting.LogComparisonAtLeast:
		spec.Comparison = alerting.LogComparisonAtLeast
	case alerting.LogComparisonFewerThan:
		// ok
	default:
		return errors.New("log_spec.comparison must be 'at_least' or 'fewer_than'")
	}
	if spec.WindowSeconds < 60 || spec.WindowSeconds > alerting.MaxCheckWindowSeconds {
		return fmt.Errorf("log_spec.window_seconds must be between 60 and %d", alerting.MaxCheckWindowSeconds)
	}
	for i := range spec.Attrs {
		spec.Attrs[i].Key = strings.TrimSpace(spec.Attrs[i].Key)
		if !attrKeyRe.MatchString(spec.Attrs[i].Key) {
			return errors.New("invalid attribute key: " + spec.Attrs[i].Key)
		}
		if !validAttrOps[spec.Attrs[i].Op] {
			return errors.New("invalid attribute operator: " + spec.Attrs[i].Op)
		}
	}
	return nil
}

func ruleAttrsToStore(attrs []alerting.AttrFilter) []store.LogAttrFilter {
	out := make([]store.LogAttrFilter, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, store.LogAttrFilter{Key: strings.TrimSpace(a.Key), Op: a.Op, Value: a.Value})
	}
	return out
}

// validateMetricRuleSpec checks a metric rule_spec before persistence or
// preview. Attribute keys/ops reuse the same allow-lists as the logs and
// metric-explorer filters.
func validateMetricRuleSpec(spec *alerting.MetricRuleSpec) error {
	spec.MetricName = strings.TrimSpace(spec.MetricName)
	if spec.MetricName == "" {
		return errors.New("spec.metric_name is required")
	}
	if !alerting.ValidAggregation(spec.Aggregation) {
		return errors.New("invalid spec.aggregation")
	}
	if !alerting.ValidOperator(spec.Operator) {
		return errors.New("invalid spec.operator")
	}
	if spec.ForWindow == "" {
		spec.ForWindow = "5m"
	}
	if _, err := time.ParseDuration(spec.ForWindow); err != nil {
		return errors.New("invalid spec.for_window (use a Go duration like 5m)")
	}
	for i := range spec.Attrs {
		spec.Attrs[i].Key = strings.TrimSpace(spec.Attrs[i].Key)
		if !attrKeyRe.MatchString(spec.Attrs[i].Key) {
			return errors.New("invalid attribute key: " + spec.Attrs[i].Key)
		}
		if !validAttrOps[spec.Attrs[i].Op] {
			return errors.New("invalid attribute operator: " + spec.Attrs[i].Op)
		}
	}
	spec.SplitBy = strings.TrimSpace(spec.SplitBy)
	if spec.SplitBy != "" && !attrKeyRe.MatchString(spec.SplitBy) {
		return errors.New("invalid spec.split_by attribute key: " + spec.SplitBy)
	}
	return nil
}

// validatePushedRuleSpec checks the spec of a pushed-value health check.
// A pushed check has no metric binding (the value is fed in externally),
// so only the operator + threshold matter; metric_name/aggregation/window
// /attrs/split are cleared to harmless defaults so the stored spec is
// clean and the pushed evaluator never tries to aggregate ClickHouse.
func validatePushedRuleSpec(spec *alerting.MetricRuleSpec) error {
	if !alerting.ValidOperator(spec.Operator) {
		return errors.New("invalid spec.operator")
	}
	spec.MetricName = ""
	spec.Aggregation = alerting.AggLast
	spec.ForWindow = "5m"
	spec.Attrs = nil
	spec.SplitBy = ""
	// No ClickHouse series behind a pushed check, so there is no "the
	// metric stopped arriving" to detect. Cleared rather than ignored so
	// the stored spec never claims a condition nothing evaluates.
	// (Staleness of the pushed reading itself is a separate idea.)
	spec.FireOnNoData = false
	return nil
}

func parseChannelIDs(raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, errors.New("invalid channel id: " + s)
		}
		out = append(out, id)
	}
	return out, nil
}

// alertVisibleGroups returns the set of team (group) IDs the caller
// belongs to, plus whether team filtering applies at all. Org admins
// and unauthenticated/system contexts bypass — (nil, false) means "see
// everything". Otherwise (memberSet, true): a caller may see/act on an
// alert iff it's org-wide (group_id nil) or owned by a team in the set.
//
// Fails open (returns no-filter) on a membership-lookup error: a
// transient DB blip shouldn't hide a user's own alerts. The trade-off
// is acceptable because alerts aren't a secrecy boundary the way raw
// telemetry is — team scoping here is about focus/ownership.
func (h *Handlers) alertVisibleGroups(r *http.Request) (map[uuid.UUID]bool, bool) {
	ref, restricted := h.visibilityMember(r)
	if !restricted {
		return nil, false
	}
	groups, err := h.Identity.ListMemberGroups(r.Context(), ref, middleware.Principal(r).OrgID)
	if err != nil {
		h.Logger.Warn("alert team visibility lookup failed; allowing", "err", err)
		return nil, false
	}
	set := make(map[uuid.UUID]bool, len(groups))
	for _, g := range groups {
		set[g.GroupID] = true
	}
	return set, true
}

// canSeeAlertGroup reports whether a caller with the given visible-group
// set (and filter flag) may see/act on a resource owned by groupID.
// Used for both read visibility and write/assignment authorization —
// you can only own an alert with a team you're a member of (org-wide,
// nil, is always allowed).
func canSeeAlertGroup(groupID *uuid.UUID, visible map[uuid.UUID]bool, filter bool) bool {
	if !filter {
		return true
	}
	if groupID == nil {
		return true // org-wide, visible to everyone
	}
	return visible[*groupID]
}

// listAlertRules: GET /api/v1/alert-rules[?service=…][&integration=…][&system=…]
//
// Optional service/integration filters narrow to the health checks bound
// to one target (the service/integration detail pages). Team-owned
// rules are hidden from non-members (org admins see all).
func (h *Handlers) listAlertRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.Alerts.ListRules(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list alert rules failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	visible, filter := h.alertVisibleGroups(r)
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	integration := strings.TrimSpace(r.URL.Query().Get("integration"))
	system := strings.TrimSpace(r.URL.Query().Get("system"))
	out := make([]alerting.AlertRule, 0, len(rules))
	for _, rule := range rules {
		if !canSeeAlertGroup(rule.GroupID, visible, filter) {
			continue
		}
		if !h.canSeeAlertTarget(r, rule.ServiceName, rule.IntegrationID, rule.SystemID) {
			continue
		}
		if service != "" && rule.ServiceName != service {
			continue
		}
		if integration != "" && (rule.IntegrationID == nil || rule.IntegrationID.String() != integration) {
			continue
		}
		if system != "" && (rule.SystemID == nil || rule.SystemID.String() != system) {
			continue
		}
		out = append(out, rule)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"rules": out})
}

// createAlertRule: POST /api/v1/alert-rules
func (h *Handlers) createAlertRule(w http.ResponseWriter, r *http.Request) {
	var req alertRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	rule, err := h.buildRule(middleware.OrgID(r), uuid.Nil, req)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A non-admin may only assign a rule to a team they belong to.
	if visible, filter := h.alertVisibleGroups(r); !canSeeAlertGroup(rule.GroupID, visible, filter) {
		httpserver.WriteError(w, http.StatusForbidden, "you can only assign an alert to a team you belong to")
		return
	}
	if !h.requireManageAlertTarget(w, r, rule.ServiceName, rule.IntegrationID, rule.SystemID) {
		return
	}
	created, err := h.Alerts.CreateRule(r.Context(), rule)
	if err != nil {
		h.Logger.Error("create alert rule failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.recordAudit(r, "alert_rule.created", "alert_rule", created.ID.String(), map[string]any{"name": created.Name})
	httpserver.WriteJSON(w, http.StatusCreated, created)
}

// getAlertRule: GET /api/v1/alert-rules/{id}
func (h *Handlers) getAlertRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	rule, err := h.Alerts.GetRule(r.Context(), middleware.OrgID(r), id)
	if errors.Is(err, alerting.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "rule not found")
		return
	}
	if err != nil {
		h.Logger.Error("get alert rule failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// 404 (not 403) for a rule the caller can't see — don't leak that the
	// rule exists. Hidden when the rule's team isn't theirs OR (the hard
	// boundary) its bound service/integration isn't visible to them.
	visible, filter := h.alertVisibleGroups(r)
	if !canSeeAlertGroup(rule.GroupID, visible, filter) || !h.canSeeAlertTarget(r, rule.ServiceName, rule.IntegrationID, rule.SystemID) {
		httpserver.WriteError(w, http.StatusNotFound, "rule not found")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, rule)
}

// updateAlertRule: PUT /api/v1/alert-rules/{id}
func (h *Handlers) updateAlertRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	var req alertRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	rule, err := h.buildRule(middleware.OrgID(r), id, req)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireManageAlertTarget(w, r, rule.ServiceName, rule.IntegrationID, rule.SystemID) {
		return
	}
	// Team access control on the existing rule (can the caller touch
	// it?) and the target team (can they reassign it there?). Load the
	// current rule first so a non-member can't edit a team rule even if
	// they guess its id, and can't move it onto a team they're not in.
	if visible, filter := h.alertVisibleGroups(r); filter {
		existing, gerr := h.Alerts.GetRule(r.Context(), middleware.OrgID(r), id)
		if errors.Is(gerr, alerting.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
		if gerr != nil {
			h.Logger.Error("update alert rule: load existing failed", "err", gerr)
			httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
			return
		}
		if !canSeeAlertGroup(existing.GroupID, visible, filter) {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
		if !canSeeAlertGroup(rule.GroupID, visible, filter) {
			httpserver.WriteError(w, http.StatusForbidden, "you can only assign an alert to a team you belong to")
			return
		}
	}
	updated, err := h.Alerts.UpdateRule(r.Context(), middleware.OrgID(r), rule)
	if errors.Is(err, alerting.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "rule not found")
		return
	}
	if err != nil {
		h.Logger.Error("update alert rule failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "alert_rule.updated", "alert_rule", updated.ID.String(), map[string]any{"name": updated.Name})
	httpserver.WriteJSON(w, http.StatusOK, updated)
}

// deleteAlertRule: DELETE /api/v1/alert-rules/{id}
func (h *Handlers) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	// Team access control: a non-member can't delete a team rule.
	if visible, filter := h.alertVisibleGroups(r); filter {
		existing, gerr := h.Alerts.GetRule(r.Context(), middleware.OrgID(r), id)
		if errors.Is(gerr, alerting.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
		if gerr != nil {
			h.Logger.Error("delete alert rule: load existing failed", "err", gerr)
			httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
			return
		}
		if !canSeeAlertGroup(existing.GroupID, visible, filter) {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
	}
	// Scoped manage: a group-editor may only delete rules bound to
	// services/integrations within their managed scope.
	if _, restricted := h.managedServiceFilter(r); restricted {
		existing, gerr := h.Alerts.GetRule(r.Context(), middleware.OrgID(r), id)
		if errors.Is(gerr, alerting.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
		if gerr != nil {
			h.Logger.Error("delete alert rule: load existing failed", "err", gerr)
			httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
			return
		}
		if !h.requireManageAlertTarget(w, r, existing.ServiceName, existing.IntegrationID, existing.SystemID) {
			return
		}
	}
	if err := h.Alerts.DeleteRule(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, alerting.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
		h.Logger.Error("delete alert rule failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "alert_rule.deleted", "alert_rule", id.String(), nil)
	httpserver.WriteJSON(w, http.StatusNoContent, nil)
}

// buildRule validates a request into an alerting.AlertRule (id zero for
// create).
func (h *Handlers) buildRule(orgID uuid.UUID, id uuid.UUID, req alertRuleRequest) (alerting.AlertRule, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return alerting.AlertRule{}, errors.New("name is required")
	}
	sev := alerting.Severity(strings.TrimSpace(req.Severity))
	if sev == "" {
		sev = alerting.SeverityWarning
	}
	if !alerting.ValidSeverity(sev) {
		return alerting.AlertRule{}, errors.New("invalid severity")
	}
	channelIDs, err := parseChannelIDs(req.ChannelIDs)
	if err != nil {
		return alerting.AlertRule{}, err
	}
	var integrationID *uuid.UUID
	if s := strings.TrimSpace(req.IntegrationID); s != "" {
		iid, perr := uuid.Parse(s)
		if perr != nil {
			return alerting.AlertRule{}, errors.New("invalid integration_id")
		}
		integrationID = &iid
	}
	var systemID *uuid.UUID
	if s := strings.TrimSpace(req.SystemID); s != "" {
		sid, perr := uuid.Parse(s)
		if perr != nil {
			return alerting.AlertRule{}, errors.New("invalid system_id")
		}
		systemID = &sid
	}
	// The schema tolerates several scopes at once for backwards
	// compatibility (0009, 0077), and the evaluators resolve a fixed
	// precedence. But a NEW rule naming two targets is a mistake, not an
	// intent — the caller would be told it bound to one thing while it
	// evaluated over another. Reject it rather than silently picking.
	if systemID != nil && integrationID != nil {
		return alerting.AlertRule{}, errors.New("a rule binds to a system or an integration, not both")
	}
	if systemID != nil && strings.TrimSpace(req.ServiceName) != "" {
		return alerting.AlertRule{}, errors.New("a rule binds to a system or a service, not both")
	}
	var groupID *uuid.UUID
	if s := strings.TrimSpace(req.GroupID); s != "" {
		gid, perr := uuid.Parse(s)
		if perr != nil {
			return alerting.AlertRule{}, errors.New("invalid group_id")
		}
		groupID = &gid
	}
	serviceName := strings.TrimSpace(req.ServiceName)
	titleTemplate := strings.TrimSpace(req.TitleTemplate)
	bodyTemplate := strings.TrimSpace(req.BodyTemplate)
	if err := alerting.ValidateTemplate(titleTemplate); err != nil {
		return alerting.AlertRule{}, errors.New("invalid title_template: " + err.Error())
	}
	if err := alerting.ValidateTemplate(bodyTemplate); err != nil {
		return alerting.AlertRule{}, errors.New("invalid body_template: " + err.Error())
	}
	if req.NotificationContent != nil {
		if err := alerting.ValidateLiquid(req.NotificationContent.EmailSubject); err != nil {
			return alerting.AlertRule{}, errors.New("invalid notification email subject: " + err.Error())
		}
		if err := alerting.ValidateLiquid(req.NotificationContent.EmailBody); err != nil {
			return alerting.AlertRule{}, errors.New("invalid notification email body: " + err.Error())
		}
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	signal := strings.TrimSpace(req.Signal)
	if signal == "" {
		signal = "metric"
	}
	source := alerting.Source(strings.TrimSpace(req.Source))
	if source == "" {
		source = alerting.SourceTelemetry
	}
	if !alerting.ValidSource(source) {
		return alerting.AlertRule{}, errors.New("invalid source (use telemetry or pushed)")
	}
	// Resolve mode: explicit wins; otherwise default by signal so existing
	// behaviour is preserved (metric/pushed self-recover; log + failed-trace
	// require acknowledgement).
	resolveMode := strings.TrimSpace(req.ResolveMode)
	switch resolveMode {
	case alerting.ResolveAuto, alerting.ResolveManual:
	case "":
		if signal == "log" || signal == alerting.SignalTraceError {
			resolveMode = alerting.ResolveManual
		} else {
			resolveMode = alerting.ResolveAuto
		}
	default:
		return alerting.AlertRule{}, errors.New("invalid resolve_mode (use auto or manual)")
	}
	rule := alerting.AlertRule{
		ID:                  id,
		OrganizationID:      orgID,
		IntegrationID:       integrationID,
		SystemID:            systemID,
		ServiceName:         serviceName,
		GroupID:             groupID,
		Name:                req.Name,
		Description:         strings.TrimSpace(req.Description),
		Runbook:             strings.TrimSpace(req.Runbook),
		Severity:            sev,
		Signal:              signal,
		Enabled:             enabled,
		ChannelIDs:          channelIDs,
		TitleTemplate:       titleTemplate,
		BodyTemplate:        bodyTemplate,
		NotificationContent: req.NotificationContent,
		Source:              source,
		DisplayOnService:    req.DisplayOnService,
		Unit:                strings.TrimSpace(req.Unit),
		ResolveMode:         resolveMode,
	}
	switch signal {
	case "metric":
		if source == alerting.SourcePushed {
			// A pushed check carries no metric binding — its value is fed
			// in externally. Only the operator + threshold define a breach,
			// and it must be bound to a service to be fed/displayed.
			if serviceName == "" {
				return alerting.AlertRule{}, errors.New("a pushed health check must be bound to a service")
			}
			if err := validatePushedRuleSpec(&req.Spec); err != nil {
				return alerting.AlertRule{}, err
			}
		} else if err := validateMetricRuleSpec(&req.Spec); err != nil {
			return alerting.AlertRule{}, err
		}
		rule.Spec = req.Spec
	case "log":
		if source == alerting.SourcePushed {
			return alerting.AlertRule{}, errors.New("only metric health checks can be pushed")
		}
		if req.LogSpec == nil {
			return alerting.AlertRule{}, errors.New("log_spec is required for a log rule")
		}
		if err := validateLogRuleSpec(req.LogSpec); err != nil {
			return alerting.AlertRule{}, err
		}
		// Reject a criteria-less, scope-less rule — it would fire on every log.
		s := req.LogSpec
		if s.MinSeverity == 0 && s.BodyContains == "" && len(s.Attrs) == 0 && serviceName == "" && integrationID == nil {
			return alerting.AlertRule{}, errors.New("a log rule needs at least one match criterion (min severity, text, or attribute) or a service/integration scope")
		}
		rule.LogSpec = req.LogSpec
	case alerting.SignalTraceError:
		if source == alerting.SourcePushed {
			return alerting.AlertRule{}, errors.New("only metric health checks can be pushed")
		}
		// A trace rule is scoped to an integration (all its services) or a
		// single service. One is required — without a scope there's nothing
		// to measure.
		if integrationID == nil && serviceName == "" {
			return alerting.AlertRule{}, errors.New("a trace rule must be bound to an integration or a service")
		}
		// Four flavours share signal='trace', told apart by rule_spec->>'kind':
		// a response-time rule (trace_latency_spec), a low-traffic rule
		// (trace_volume_spec), a span-attribute rule (trace_attribute_spec),
		// or a failed-trace rule (trace_error_spec). Latency > volume >
		// attribute > error when more than one is sent.
		switch {
		case req.TraceLatencySpec != nil:
			if req.TraceLatencySpec.ThresholdMs < 1 {
				return alerting.AlertRule{}, errors.New("trace_latency_spec.threshold_ms must be at least 1")
			}
			req.TraceLatencySpec.Kind = alerting.TraceLatencySpecKind
			rule.TraceLatencySpec = req.TraceLatencySpec
		case req.TraceVolumeSpec != nil:
			if req.TraceVolumeSpec.Threshold < 1 {
				return alerting.AlertRule{}, errors.New("trace_volume_spec.threshold must be at least 1")
			}
			if req.TraceVolumeSpec.WindowSeconds < 60 || req.TraceVolumeSpec.WindowSeconds > alerting.MaxCheckWindowSeconds {
				return alerting.AlertRule{}, fmt.Errorf("trace_volume_spec.window_seconds must be between 60 and %d", alerting.MaxCheckWindowSeconds)
			}
			req.TraceVolumeSpec.Kind = alerting.TraceVolumeSpecKind
			rule.TraceVolumeSpec = req.TraceVolumeSpec
		case req.TraceAttributeSpec != nil:
			if req.TraceAttributeSpec.Threshold < 1 {
				return alerting.AlertRule{}, errors.New("trace_attribute_spec.threshold must be at least 1")
			}
			if req.TraceAttributeSpec.WindowSeconds < 60 || req.TraceAttributeSpec.WindowSeconds > alerting.MaxCheckWindowSeconds {
				return alerting.AlertRule{}, fmt.Errorf("trace_attribute_spec.window_seconds must be between 60 and %d", alerting.MaxCheckWindowSeconds)
			}
			// Unlike the failed-trace rule, the predicates are the whole
			// condition rather than a narrowing of one. An empty list has
			// no sensible reading: it would count every trace in scope and
			// fire on healthy traffic. Refuse it here rather than store a
			// rule that means something the author did not ask for.
			if len(req.TraceAttributeSpec.Attrs) == 0 {
				return alerting.AlertRule{}, errors.New("trace_attribute_spec.attrs must name at least one attribute condition")
			}
			for i := range req.TraceAttributeSpec.Attrs {
				req.TraceAttributeSpec.Attrs[i].Key = strings.TrimSpace(req.TraceAttributeSpec.Attrs[i].Key)
				if !attrKeyRe.MatchString(req.TraceAttributeSpec.Attrs[i].Key) {
					return alerting.AlertRule{}, errors.New("invalid attribute key: " + req.TraceAttributeSpec.Attrs[i].Key)
				}
				if !validAttrOps[req.TraceAttributeSpec.Attrs[i].Op] {
					return alerting.AlertRule{}, errors.New("invalid attribute operator: " + req.TraceAttributeSpec.Attrs[i].Op)
				}
			}
			req.TraceAttributeSpec.Kind = alerting.TraceAttributeSpecKind
			rule.TraceAttributeSpec = req.TraceAttributeSpec
		case req.TraceErrorSpec != nil:
			if req.TraceErrorSpec.Threshold < 1 {
				return alerting.AlertRule{}, errors.New("trace_error_spec.threshold must be at least 1")
			}
			// Optional attribute predicates narrow which error spans count
			// (same key/op vocabulary as log rules).
			for i := range req.TraceErrorSpec.Attrs {
				req.TraceErrorSpec.Attrs[i].Key = strings.TrimSpace(req.TraceErrorSpec.Attrs[i].Key)
				if !attrKeyRe.MatchString(req.TraceErrorSpec.Attrs[i].Key) {
					return alerting.AlertRule{}, errors.New("invalid attribute key: " + req.TraceErrorSpec.Attrs[i].Key)
				}
				if !validAttrOps[req.TraceErrorSpec.Attrs[i].Op] {
					return alerting.AlertRule{}, errors.New("invalid attribute operator: " + req.TraceErrorSpec.Attrs[i].Op)
				}
			}
			// Stamp the kind so this row is distinguishable from a
			// trace-completion rule (both use signal='trace'). Every reader
			// keys off rule_spec->>'kind'.
			req.TraceErrorSpec.Kind = alerting.TraceErrorSpecKind
			rule.TraceErrorSpec = req.TraceErrorSpec
		default:
			return alerting.AlertRule{}, errors.New("a trace rule needs trace_error_spec (failed traces), trace_latency_spec (response time), trace_volume_spec (low traffic), or trace_attribute_spec (span attribute)")
		}
	default:
		return alerting.AlertRule{}, errors.New("invalid signal (use metric, log, or trace)")
	}
	return rule, nil
}

// previewAlertRule: POST /api/v1/alert-rules/preview — evaluate a rule
// spec against the current window without persisting, so the builder can
// show a live "would fire now" banner.
func (h *Handlers) previewAlertRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Spec alerting.MetricRuleSpec `json:"spec"`
		// ServiceName, when set, scopes the preview to that one service —
		// matching how a service-bound rule actually evaluates. Empty =
		// integration/global preview (across all visible services).
		ServiceName string `json:"service_name"`
		// IntegrationID, when set, scopes the preview to that
		// integration's member services — matching how an
		// integration-bound rule actually evaluates. A preview that
		// silently fell back to "every visible service" would show a
		// number the saved rule never produces, which is worse than no
		// preview: it looks like a rehearsal and is not one.
		IntegrationID string `json:"integration_id"`
		// SystemID scopes the preview to the system's member services,
		// for the same reason IntegrationID does.
		SystemID string `json:"system_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := validateMetricRuleSpec(&req.Spec); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	to := time.Now().UTC()
	from := to.Add(-req.Spec.ForWindowDuration())

	// Constrain the preview to the caller's policy-visible services — this
	// endpoint runs an ad-hoc ClickHouse aggregate, so without the filter a
	// user could read metric data for services they can't see. When the rule
	// is service-bound, also scope to that one service so the preview matches
	// the real evaluation. A caller with no access (or asking for a service
	// they can't see) gets an empty (no-data) preview.
	var pf policyResolution
	switch {
	case req.ServiceName != "":
		pf = h.resolveServiceFilter(r, req.ServiceName, []string{req.ServiceName})
	case req.SystemID != "":
		id, err := uuid.Parse(req.SystemID)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid system_id")
			return
		}
		members, mErr := h.Catalog.SystemMemberNames(r.Context(), middleware.OrgID(r), id)
		if mErr != nil {
			h.Logger.Warn("alert preview: system members failed", "err", mErr, "system", id)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		if len(members) == 0 {
			// No members → the rule's own predicate is the scope, matching
			// how the evaluator treats a memberless system. Still policy
			// filtered, so this widens to the caller's VISIBLE services,
			// never to the cell.
			//
			// Integrations deliberately do not do this: their membership
			// reconciles asynchronously, so empty there means "not ready",
			// and a preview computed before the answer exists is worse
			// than no preview.
			pf = h.resolveServiceFilter(r, "", nil)
		} else {
			pf = h.resolveServiceFilter(r, "", members)
			// An intersection that empties out is empty ACCESS, not "no
			// filter" — downstream an empty ServiceIn means unscoped.
			if !pf.EmptyAccess && !pf.Blocked && len(pf.ServiceIn) == 0 {
				pf.EmptyAccess = true
			}
		}
	case req.IntegrationID != "":
		id, err := uuid.Parse(req.IntegrationID)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid integration_id")
			return
		}
		members, mErr := h.Catalog.IntegrationServices(r.Context(), id)
		if mErr != nil {
			h.Logger.Warn("alert preview: integration members failed", "err", mErr, "integration", id)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		// No members yet (a freshly created integration whose catalog
		// membership has not reconciled) has to read as "no data", not as
		// "every service" — resolveServiceFilter with an empty list would
		// widen the preview to the whole cell.
		if len(members) == 0 {
			httpserver.WriteJSON(w, http.StatusOK, map[string]any{
				"value": 0, "samples": 0, "has_data": false, "breached": false,
				"threshold": req.Spec.Threshold, "window": TimeRange{From: from, To: to}.Window(),
			})
			return
		}
		pf = h.resolveServiceFilter(r, "", members)
		// A caller who can see NONE of the members intersects down to an
		// empty list, and an empty ServiceIn reads downstream as "no
		// filter" — i.e. the whole cell. The ?service= path is protected
		// by Blocked; this one has to say so itself, or previewing an
		// integration you cannot see would aggregate metrics you cannot
		// see. Empty access and empty intersection are the same answer
		// here: no data.
		if !pf.EmptyAccess && !pf.Blocked && len(pf.ServiceIn) == 0 {
			pf.EmptyAccess = true
		}
	default:
		pf = h.resolveServiceFilter(r, "", nil)
	}
	if pf.EmptyAccess || pf.Blocked {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{
			"value": 0, "samples": 0, "has_data": false, "breached": false,
			"threshold": req.Spec.Threshold, "window": TimeRange{From: from, To: to}.Window(),
		})
		return
	}

	// Split-by preview: per-value breakdown so the builder can show
	// exactly which values (e.g. queues) would fire and how many.
	if req.Spec.SplitBy != "" {
		groups, err := h.Store.MetricAggregateGrouped(
			r.Context(), req.Spec.MetricName, ruleAttrsToStore(req.Spec.Attrs),
			string(req.Spec.Aggregation), req.Spec.SplitBy, from, to, pf.ServiceIn,
		)
		if err != nil {
			h.Logger.Error("alert preview grouped aggregate failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		out := make([]map[string]any, 0, len(groups))
		breachCount := 0
		var worst float64
		var worstSet bool
		for _, g := range groups {
			gb := g.Samples > 0 && alerting.EvaluateBreach(req.Spec.Operator, g.Value, req.Spec.Threshold)
			if gb {
				breachCount++
			}
			if !worstSet || g.Value > worst {
				worst, worstSet = g.Value, true
			}
			out = append(out, map[string]any{"label": g.Label, "value": g.Value, "breached": gb})
		}
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{
			"value":        worst, // the highest split value, for the scalar banner
			"samples":      len(groups),
			"has_data":     len(groups) > 0,
			"breached":     breachCount > 0,
			"threshold":    req.Spec.Threshold,
			"split_by":     req.Spec.SplitBy,
			"breach_count": breachCount,
			"groups":       out,
			"window":       TimeRange{From: from, To: to}.Window(),
		})
		return
	}

	value, samples, err := h.Store.MetricAggregate(
		r.Context(), req.Spec.MetricName, ruleAttrsToStore(req.Spec.Attrs),
		string(req.Spec.Aggregation), from, to, pf.ServiceIn,
	)
	if err != nil {
		h.Logger.Error("alert preview aggregate failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	breached := samples > 0 && alerting.EvaluateBreach(req.Spec.Operator, value, req.Spec.Threshold)
	resp := map[string]any{
		"value":     value,
		"samples":   samples,
		"has_data":  samples > 0,
		"breached":  breached,
		"threshold": req.Spec.Threshold,
		"window":    TimeRange{From: from, To: to}.Window(),
	}
	// How many series the filter left in the group. Only meaningful for
	// the aggregations that pick one sample by timestamp — for the rest
	// the group size is a design choice, not a hazard — so the extra
	// query is only run where it can change what the builder says.
	if samples > 0 && alerting.AggregationPicksBySample(string(req.Spec.Aggregation)) {
		series, err := h.Store.MetricSeriesCount(
			r.Context(), req.Spec.MetricName, ruleAttrsToStore(req.Spec.Attrs), from, to, pf.ServiceIn,
		)
		if err != nil {
			// A missing count degrades the warning, not the preview.
			h.Logger.Warn("alert preview series count failed", "err", err)
		} else {
			resp["series"] = series
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

// listAlertInstances: GET /api/v1/alert-instances — recent firing/
// resolved instances joined to their rule, for the Alerts page.
func (h *Handlers) listAlertInstances(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	instances, err := h.Alerts.RecentInstances(r.Context(), middleware.OrgID(r), limit)
	if err != nil {
		h.Logger.Error("list alert instances failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Hide instances of team rules the caller isn't a member of, and —
	// the hard security boundary — instances bound to a service/integration
	// the caller can't see (a service-scoped instance with no team must not
	// leak the service's name/error).
	visible, filter := h.alertVisibleGroups(r)
	out := make([]alerting.InstanceView, 0, len(instances))
	for _, inst := range instances {
		if !canSeeAlertGroup(inst.GroupID, visible, filter) {
			continue
		}
		if !h.canSeeAlertTarget(r, inst.ServiceName, inst.IntegrationID, inst.SystemID) {
			continue
		}
		out = append(out, inst)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"instances": out})
}

// listAlertDeliveries: GET /api/v1/alert-deliveries — recent
// notification jobs (what was sent, to which channel, and whether it
// succeeded), for the Alerts page "Sent" view. Filtered to the teams
// the caller can see, same as rules/instances.
func (h *Handlers) listAlertDeliveries(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, 24*time.Hour)
	f := alerting.DeliveryFilter{
		From:        tr.From,
		To:          tr.To,
		ServiceName: strings.TrimSpace(r.URL.Query().Get("service")),
		Name:        strings.TrimSpace(r.URL.Query().Get("name")),
	}
	if v := r.URL.Query().Get("integration"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			f.IntegrationID = id
		}
	}
	if v := r.URL.Query().Get("system"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			f.SystemID = id
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	deliveries, err := h.Alerts.ListDeliveries(r.Context(), middleware.OrgID(r), f)
	if err != nil {
		h.Logger.Error("list alert deliveries failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	visible, filter := h.alertVisibleGroups(r)
	out := make([]alerting.DeliveryView, 0, len(deliveries))
	for _, d := range deliveries {
		if !canSeeAlertGroup(d.GroupID, visible, filter) {
			continue
		}
		if !h.canSeeAlertTarget(r, d.ServiceName, d.IntegrationID, d.SystemID) {
			continue
		}
		out = append(out, d)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"deliveries": out})
}

// acknowledgeAlertInstance: POST /api/v1/alert-instances/{id}/acknowledge
// — marks a firing alert as being worked on ("Acknowledged"). It stays
// firing but the engine stops sending notifications for it.
func (h *Handlers) acknowledgeAlertInstance(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	// Scoped manage (RBAC v2 §5.2): group-editors may only acknowledge
	// instances of rules bound to services they manage.
	if _, restricted := h.managedServiceFilter(r); restricted {
		svc, serr := h.Alerts.InstanceServiceName(r.Context(), middleware.OrgID(r), id)
		if serr != nil || svc == "" || !h.canManageService(r, svc) {
			httpserver.WriteError(w, http.StatusForbidden, "this alert's service is outside your managed scope")
			return
		}
	}
	if err := h.Alerts.AcknowledgeInstance(r.Context(), middleware.OrgID(r), id); err != nil {
		h.Logger.Error("acknowledge alert instance failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "acknowledge failed")
		return
	}
	h.recordAudit(r, "alert_instance.acknowledged", "alert_instance", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// resolveAlertInstance: POST /api/v1/alert-instances/{id}/resolve — closes
// an alert on user request ("Resolved"). Marked handled too, so the engine
// won't re-notify while the underlying condition persists.
func (h *Handlers) resolveAlertInstance(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	// Scoped manage (RBAC v2 §5.2): group-editors may only resolve
	// instances of rules bound to services they manage.
	if _, restricted := h.managedServiceFilter(r); restricted {
		svc, serr := h.Alerts.InstanceServiceName(r.Context(), middleware.OrgID(r), id)
		if serr != nil || svc == "" || !h.canManageService(r, svc) {
			httpserver.WriteError(w, http.StatusForbidden, "this alert's service is outside your managed scope")
			return
		}
	}
	if err := h.Alerts.ResolveInstanceManual(r.Context(), middleware.OrgID(r), id); err != nil {
		h.Logger.Error("resolve alert instance failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "resolve failed")
		return
	}
	h.recordAudit(r, "alert_instance.resolved", "alert_instance", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- notification channels ---------------------------------------------

type channelRequest struct {
	Name   string            `json:"name"`
	Kind   string            `json:"kind"`
	Config map[string]string `json:"config"`
}

// validateChannel checks the kind + the kind-specific config keys.
func validateChannel(req *channelRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	req.Kind = strings.TrimSpace(req.Kind)
	if req.Name == "" {
		return errors.New("name is required")
	}
	if !alerting.ValidChannelKind(req.Kind) {
		return errors.New("invalid kind (webhook, slack, pagerduty, email)")
	}
	if req.Config == nil {
		req.Config = map[string]string{}
	}
	// Payload format is a webhook-only knob: "" (canonical JSON, the
	// default) or "cloudevents" (CE 1.0 structured mode).
	if f := strings.ToLower(strings.TrimSpace(req.Config["format"])); f != "" {
		if req.Kind != alerting.ChannelWebhook {
			return errors.New("config.format is only supported on webhook channels")
		}
		if f != alerting.FormatCloudEvents && f != alerting.FormatTemplate {
			return errors.New(`config.format must be empty, "cloudevents" or "template"`)
		}
		if f == alerting.FormatTemplate {
			// Validated at SAVE time, which is the point of a structural
			// mapping: a template that cannot render is refused now,
			// rather than discovered as an alert that never arrived.
			if _, err := alerting.ValidateWebhookTemplate(req.Config["body_template"]); err != nil {
				return fmt.Errorf("config.body_template: %v", err)
			}
		}
	}
	// A body template with no format selected would be stored and never
	// used, which reads as the feature being broken.
	if strings.TrimSpace(req.Config["body_template"]) != "" &&
		!strings.EqualFold(strings.TrimSpace(req.Config["format"]), alerting.FormatTemplate) {
		return errors.New(`config.body_template needs config.format = "template"`)
	}
	// The Authorization value is sent verbatim; it is only ever meaningful
	// on a channel that makes an HTTP request we control the headers of.
	if strings.TrimSpace(req.Config["auth_header"]) != "" && req.Kind != alerting.ChannelWebhook {
		return errors.New("config.auth_header is only supported on webhook channels")
	}
	switch req.Kind {
	case alerting.ChannelWebhook, alerting.ChannelSlack:
		u := strings.TrimSpace(req.Config["url"])
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return errors.New("config.url must be an http(s) URL")
		}
	case alerting.ChannelPagerDuty:
		if strings.TrimSpace(req.Config["routing_key"]) == "" {
			return errors.New("config.routing_key is required")
		}
		// Optional Events API endpoint override (EU service region:
		// https://events.eu.pagerduty.com/v2/enqueue). Empty = US default.
		if u := strings.TrimSpace(req.Config["events_url"]); u != "" &&
			!strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return errors.New("config.events_url must be an http(s) URL when set")
		}
	case alerting.ChannelEmail:
		// Recipients are always required. The SMTP server (smtp_host / from /
		// auth) is optional: when omitted, delivery falls back to the org's
		// system email settings (Settings → System email).
		if strings.TrimSpace(req.Config["to"]) == "" {
			return errors.New("config.to is required (comma-separated recipients)")
		}
	}
	return nil
}

// ChannelSecretKeys are the config values that are credentials rather
// than configuration.
//
// GET /notification-channels is open to every authenticated member, by
// design: the alert pages need channel NAMES to show where a rule
// delivers. It returned the whole config with them, so a viewer could
// read the HMAC signing secret, a PagerDuty routing key, an SMTP
// password — and, once webhook auth landed, a bearer token.
//
// Masked on read, preserved on write: a client that sends the mask back
// unchanged keeps the stored value, so an edit form that never saw the
// secret cannot erase it either.
var ChannelSecretKeys = []string{"secret", "auth_header", "routing_key", "password"}

// SecretMask is what a credential reads as on the way out. A fixed
// string rather than a blank, so the form shows that something IS set:
// an empty field would invite somebody to "fix" it by retyping.
const SecretMask = "••••••••"

func maskChannelSecrets(chs []alerting.NotificationChannel) []alerting.NotificationChannel {
	out := make([]alerting.NotificationChannel, len(chs))
	for i, c := range chs {
		cfg := make(map[string]string, len(c.Config))
		for k, v := range c.Config {
			cfg[k] = v
		}
		for _, k := range ChannelSecretKeys {
			if strings.TrimSpace(cfg[k]) != "" {
				cfg[k] = SecretMask
			}
		}
		c.Config = cfg
		out[i] = c
	}
	return out
}

// keepMaskedSecrets restores any credential the caller echoed back as the
// mask. Without this, saving a channel from a form that only ever saw
// "••••••••" would store that string as the token.
func keepMaskedSecrets(next map[string]string, prev map[string]string) map[string]string {
	if next == nil {
		return next
	}
	for _, k := range ChannelSecretKeys {
		if next[k] == SecretMask {
			if prev != nil && prev[k] != "" {
				next[k] = prev[k]
			} else {
				delete(next, k)
			}
		}
	}
	return next
}

// listChannels: GET /api/v1/notification-channels
func (h *Handlers) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.Alerts.ListChannels(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list channels failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if channels == nil {
		channels = []alerting.NotificationChannel{}
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"channels": maskChannelSecrets(channels)})
}

// createChannel: POST /api/v1/notification-channels
func (h *Handlers) createChannel(w http.ResponseWriter, r *http.Request) {
	var req channelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := validateChannel(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := h.Alerts.CreateChannel(r.Context(), alerting.NotificationChannel{
		OrganizationID: middleware.OrgID(r),
		Name:           req.Name,
		Kind:           req.Kind,
		Config:         req.Config,
	})
	if err != nil {
		if isUniqueViolation(err) {
			httpserver.WriteError(w, http.StatusConflict, "a notification channel with that name already exists")
			return
		}
		h.Logger.Error("create channel failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	// Metadata stays name+kind only — channel configs carry webhook URLs
	// and credentials that must not land in the audit log.
	h.recordAudit(r, "notification_channel.created", "notification_channel", created.ID.String(), map[string]any{"name": created.Name, "kind": created.Kind})
	// Masked like every other read of a channel: the caller supplied the
	// token, but the response is what lands in a browser devtools panel
	// or a shared screen, and one rule is easier to keep than two.
	httpserver.WriteJSON(w, http.StatusCreated, maskChannelSecrets([]alerting.NotificationChannel{created})[0])
}

// updateChannel: PUT /api/v1/notification-channels/{id}
func (h *Handlers) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	var req channelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Restore any credential the form echoed back as the mask BEFORE
	// validating: a body template or an auth header that came back
	// masked is unchanged, not absent, and validating the mask would
	// reject an edit that touched neither.
	if existing, err := h.Alerts.GetChannel(r.Context(), middleware.OrgID(r), id); err == nil {
		req.Config = keepMaskedSecrets(req.Config, existing.Config)
	}
	if err := validateChannel(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := h.Alerts.UpdateChannel(r.Context(), middleware.OrgID(r), alerting.NotificationChannel{
		ID:     id,
		Name:   req.Name,
		Kind:   req.Kind,
		Config: req.Config,
	})
	if errors.Is(err, alerting.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		h.Logger.Error("update channel failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "notification_channel.updated", "notification_channel", updated.ID.String(), map[string]any{"name": updated.Name, "kind": updated.Kind})
	httpserver.WriteJSON(w, http.StatusOK, maskChannelSecrets([]alerting.NotificationChannel{updated})[0])
}

// testChannel: POST /api/v1/notification-channels/{id}/test
//
// Sends a sample notification to the channel so the operator can verify
// the destination (SMTP creds, webhook URL, …) before routing a rule to
// it. Delivery failures come back as 502 with the underlying error.
func (h *Handlers) testChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	ch, err := h.Alerts.GetChannel(r.Context(), middleware.OrgID(r), id)
	if errors.Is(err, alerting.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		h.Logger.Error("get channel for test failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if err := alerting.DeliverTest(r.Context(), ch); err != nil {
		httpserver.WriteError(w, http.StatusBadGateway, "delivery failed: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteChannel: DELETE /api/v1/notification-channels/{id}
func (h *Handlers) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	if err := h.Alerts.DeleteChannel(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, alerting.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "channel not found")
			return
		}
		h.Logger.Error("delete channel failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "notification_channel.deleted", "notification_channel", id.String(), nil)
	httpserver.WriteJSON(w, http.StatusNoContent, nil)
}

// requireManageAlertTarget enforces the scoped-manage rule for alert
// rules (RBAC v2 §5.2): a group-scoped editor may only bind rules to
// services they manage (or integrations fully within their managed
// scope); rules with no service/integration target are org-editor-only.
// Org editors/admins pass untouched.
func (h *Handlers) requireManageAlertTarget(w http.ResponseWriter, r *http.Request, serviceName string, integrationID, systemID *uuid.UUID) bool {
	if h.AuthMW == nil {
		return true
	}
	if _, restricted := h.managedServiceFilter(r); !restricted {
		return true
	}
	// Order matches the evaluators' resolution precedence, so the scope
	// a caller is checked against is the scope the rule evaluates over.
	switch {
	case systemID != nil:
		if !h.canManageSystem(r, *systemID) {
			httpserver.WriteError(w, http.StatusForbidden, "this rule's system spans services outside your managed scope")
			return false
		}
	case integrationID != nil:
		if !h.canManageIntegration(r, *integrationID) {
			httpserver.WriteError(w, http.StatusForbidden, "this rule's integration spans services outside your managed scope")
			return false
		}
	case serviceName != "":
		if !h.canManageService(r, serviceName) {
			httpserver.WriteError(w, http.StatusForbidden, "this rule's service is outside your managed scope")
			return false
		}
	default:
		httpserver.WriteError(w, http.StatusForbidden, "org-wide rules require an org-wide editor role")
		return false
	}
	return true
}
