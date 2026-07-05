// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// CRUD for user-defined monitoring templates + the conversions between the
// stored Check shape, the built-in systemCheck, and alert rules. Built-ins
// stay code-defined (read-only); these custom templates are created from a
// service's current checks, forked from a built-in, or built by hand, then
// applied via applyTemplate (template_id).

package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/alerting"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/monitoringtemplates"
)

// ── conversions ──────────────────────────────────────────────────────

// customCheckToSystemCheck maps a stored Check to the built-in check shape
// the apply path (createTemplateChecks) consumes.
func customCheckToSystemCheck(c monitoringtemplates.Check) systemCheck {
	attrs := make([]alerting.AttrFilter, len(c.Attrs))
	for i, a := range c.Attrs {
		attrs[i] = alerting.AttrFilter{Key: a.Key, Op: a.Op, Value: a.Value}
	}
	return systemCheck{
		Name:         c.Name,
		Description:  c.Description,
		Signal:       c.Signal,
		Metric:       c.Metric,
		Agg:          alerting.Aggregation(c.Agg),
		Op:           alerting.Operator(c.Op),
		Threshold:    c.Threshold,
		Attrs:        attrs,
		MinSeverity:  c.MinSeverity,
		BodyContains: c.BodyContains,
		LogThreshold: c.LogThreshold,
		Severity:     alerting.Severity(c.Severity),
		Unit:         c.Unit,
		Display:      c.Display,
	}
}

// systemCheckToCustom is the inverse — used when forking a built-in.
func systemCheckToCustom(c systemCheck) monitoringtemplates.Check {
	attrs := make([]monitoringtemplates.AttrFilter, len(c.Attrs))
	for i, a := range c.Attrs {
		attrs[i] = monitoringtemplates.AttrFilter{Key: a.Key, Op: a.Op, Value: a.Value}
	}
	return monitoringtemplates.Check{
		Name:         c.Name,
		Description:  c.Description,
		Signal:       c.Signal,
		Metric:       c.Metric,
		Agg:          string(c.Agg),
		Op:           string(c.Op),
		Threshold:    c.Threshold,
		Attrs:        attrs,
		MinSeverity:  c.MinSeverity,
		BodyContains: c.BodyContains,
		LogThreshold: c.LogThreshold,
		Severity:     string(c.Severity),
		Unit:         c.Unit,
		Display:      c.Display,
	}
}

// alertRuleToCustomCheck converts a service's existing health check (alert
// rule) into a template Check. Only metric + log rules are templatable; other
// signals (trace) return ok=false and are skipped.
func alertRuleToCustomCheck(r alerting.AlertRule) (monitoringtemplates.Check, bool) {
	switch r.Signal {
	case alerting.SignalMetric:
		attrs := make([]monitoringtemplates.AttrFilter, len(r.Spec.Attrs))
		for i, a := range r.Spec.Attrs {
			attrs[i] = monitoringtemplates.AttrFilter{Key: a.Key, Op: a.Op, Value: a.Value}
		}
		return monitoringtemplates.Check{
			Name:        r.Name,
			Description: r.Description,
			Signal:      alerting.SignalMetric,
			Metric:      r.Spec.MetricName,
			Agg:         string(r.Spec.Aggregation),
			Op:          string(r.Spec.Operator),
			Threshold:   r.Spec.Threshold,
			Attrs:       attrs,
			Severity:    string(r.Severity),
			Unit:        r.Unit,
			Display:     r.DisplayOnService,
		}, true
	case alerting.SignalLog:
		if r.LogSpec == nil {
			return monitoringtemplates.Check{}, false
		}
		return monitoringtemplates.Check{
			Name:         r.Name,
			Description:  r.Description,
			Signal:       alerting.SignalLog,
			MinSeverity:  r.LogSpec.MinSeverity,
			BodyContains: r.LogSpec.BodyContains,
			LogThreshold: r.LogSpec.Threshold,
			Severity:     string(r.Severity),
		}, true
	default:
		return monitoringtemplates.Check{}, false
	}
}

// ── handlers ─────────────────────────────────────────────────────────

// listMonitoringTemplates: GET /api/v1/monitoring-templates
func (h *Handlers) listMonitoringTemplates(w http.ResponseWriter, r *http.Request) {
	out, err := h.Templates.List(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list monitoring templates failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"templates": out})
}

// createMonitoringTemplate: POST /api/v1/monitoring-templates  (writer+)
// Body: { name, description?, checks?, from_service?, fork_kind? }. Exactly
// one source of checks: explicit checks, a service's current checks, or a
// forked built-in.
func (h *Handlers) createMonitoringTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string                      `json:"name"`
		Description string                      `json:"description"`
		Checks      []monitoringtemplates.Check `json:"checks"`
		FromService string                      `json:"from_service"`
		ForkKind    string                      `json:"fork_kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	orgID := middleware.OrgID(r)

	var checks []monitoringtemplates.Check
	source := "custom"
	switch {
	case strings.TrimSpace(body.ForkKind) != "":
		kind := strings.ToLower(strings.TrimSpace(body.ForkKind))
		tmpl, has, terr := h.templateByKind(r.Context(), orgID, kind)
		if terr != nil {
			h.Logger.Error("create template: catalog failed", "err", terr)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		if !has || len(tmpl.Checks) == 0 {
			httpserver.WriteError(w, http.StatusBadRequest, "unknown built-in kind to fork")
			return
		}
		for _, c := range tmpl.Checks {
			checks = append(checks, systemCheckToCustom(c))
		}
		source = "fork:" + kind
	case strings.TrimSpace(body.FromService) != "":
		svc := strings.TrimSpace(body.FromService)
		rules, err := h.Alerts.ListRules(r.Context(), orgID)
		if err != nil {
			h.Logger.Error("create template: list rules failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		for _, rule := range rules {
			if rule.ServiceName != svc {
				continue
			}
			if c, ok := alertRuleToCustomCheck(rule); ok {
				checks = append(checks, c)
			}
		}
		if len(checks) == 0 {
			httpserver.WriteError(w, http.StatusBadRequest, "service has no metric/log health checks to capture")
			return
		}
		source = "service:" + svc
	default:
		checks = body.Checks
	}

	t, err := h.Templates.Create(r.Context(), orgID, name, strings.TrimSpace(body.Description), source, checks)
	if err != nil {
		h.Logger.Error("create monitoring template failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.recordAudit(r, "monitoring_template.created", "monitoring_template", t.ID.String(), map[string]any{"name": t.Name})
	httpserver.WriteJSON(w, http.StatusOK, t)
}

// updateMonitoringTemplate: PUT /api/v1/monitoring-templates/{id}  (writer+)
func (h *Handlers) updateMonitoringTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Name        string                      `json:"name"`
		Description string                      `json:"description"`
		Checks      []monitoringtemplates.Check `json:"checks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	t, ok, err := h.Templates.Update(r.Context(), middleware.OrgID(r), id, name, strings.TrimSpace(body.Description), body.Checks)
	if err != nil {
		h.Logger.Error("update monitoring template failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !ok {
		httpserver.WriteError(w, http.StatusNotFound, "template not found")
		return
	}
	h.recordAudit(r, "monitoring_template.updated", "monitoring_template", t.ID.String(), map[string]any{"name": t.Name})
	httpserver.WriteJSON(w, http.StatusOK, t)
}

// deleteMonitoringTemplate: DELETE /api/v1/monitoring-templates/{id}  (writer+)
func (h *Handlers) deleteMonitoringTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Templates.Delete(r.Context(), middleware.OrgID(r), id); err != nil {
		if err == monitoringtemplates.ErrNotFound {
			httpserver.WriteError(w, http.StatusNotFound, "template not found")
			return
		}
		h.Logger.Error("delete monitoring template failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "monitoring_template.deleted", "monitoring_template", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}
