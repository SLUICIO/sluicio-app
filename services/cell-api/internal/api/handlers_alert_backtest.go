// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
)

// backtestBody names the check to dry-run: an existing rule, optionally
// with the threshold or window a tuning proposal would set, or a rule
// that does not exist yet, in the shape POST /alert-rules takes.
type backtestBody struct {
	RuleID    string            `json:"rule_id"`
	Rule      *alertRuleRequest `json:"rule"`
	Threshold *float64          `json:"threshold"`
	// ForWindow overrides a metric check's window ("10m"); WindowSeconds
	// overrides the window of a log or trace check.
	ForWindow     string `json:"for_window"`
	WindowSeconds int    `json:"window_seconds"`
	// Days of history to look back over: 7 by default, at most 30.
	Days int `json:"days"`
}

// backtestAlertRule: POST /api/v1/alert-rules/backtest
//
// How often a check would have fired over the stored history, so a
// proposed threshold (from a person or an agent) comes with evidence.
// Reads only: nothing is saved and no instance is opened. The engine's
// evaluators are a trusted path that does not apply the caller's
// visibility, so the caller must be able to see the check's scope, and
// an unscoped check needs org-wide visibility.
func (h *Handlers) backtestAlertRule(w http.ResponseWriter, r *http.Request) {
	if h.AlertEngine == nil {
		httpserver.WriteError(w, http.StatusNotImplemented, "dry runs are not available on this cell")
		return
	}
	var body backtestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	orgID := middleware.OrgID(r)

	var rule alerting.AlertRule
	switch {
	case body.RuleID != "":
		id, err := uuid.Parse(strings.TrimSpace(body.RuleID))
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid rule_id")
			return
		}
		rule, err = h.Alerts.GetRule(r.Context(), orgID, id)
		if errors.Is(err, alerting.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
		if err != nil {
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		if visible, filter := h.alertVisibleGroups(r); !canSeeAlertGroup(rule.GroupID, visible, filter) {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
	case body.Rule != nil:
		built, err := h.buildRule(orgID, uuid.Nil, *body.Rule)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		rule = built
	default:
		httpserver.WriteError(w, http.StatusBadRequest, "give rule_id, or rule")
		return
	}
	if !h.canSeeAlertTarget(r, rule.ServiceName, rule.IntegrationID, rule.SystemID) {
		httpserver.WriteError(w, http.StatusNotFound, "the check's scope was not found")
		return
	}
	if rule.ServiceName == "" && rule.IntegrationID == nil && rule.SystemID == nil {
		if _, restricted := h.visibleServiceFilter(r); restricted {
			httpserver.WriteError(w, http.StatusForbidden, "a check with no scope reads every service; dry-running it needs org-wide visibility")
			return
		}
	}
	if err := applyBacktestOverrides(&rule, body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	days := body.Days
	if days <= 0 {
		days = 7
	}
	if days > 30 {
		httpserver.WriteError(w, http.StatusBadRequest, "days is at most 30")
		return
	}
	to := time.Now().UTC().Truncate(time.Minute)
	res, err := h.AlertEngine.Backtest(r.Context(), rule, to.Add(-time.Duration(days)*24*time.Hour), to)
	if errors.Is(err, alerting.ErrBacktestUnsupported) {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		h.Logger.Warn("alert backtest failed", "err", err)
		httpserver.WriteError(w, http.StatusBadRequest, "dry run failed: "+err.Error())
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, res)
}

// applyBacktestOverrides sets the threshold and window a tuning would
// change, on whichever spec the rule's signal reads.
func applyBacktestOverrides(rule *alerting.AlertRule, body backtestBody) error {
	if body.Threshold != nil {
		v := *body.Threshold
		switch {
		case rule.LogSpec != nil:
			rule.LogSpec.Threshold = int(v)
		case rule.TraceErrorSpec != nil:
			rule.TraceErrorSpec.Threshold = int(v)
		case rule.TraceAttributeSpec != nil:
			rule.TraceAttributeSpec.Threshold = int(v)
		case rule.TraceLatencySpec != nil:
			rule.TraceLatencySpec.ThresholdMs = int(v)
		case rule.TraceVolumeSpec != nil:
			rule.TraceVolumeSpec.Threshold = int(v)
		default:
			rule.Spec.Threshold = v
		}
	}
	if body.ForWindow != "" {
		d, err := time.ParseDuration(body.ForWindow)
		if err != nil || d <= 0 {
			return errors.New("for_window is not a duration, e.g. \"10m\"")
		}
		rule.Spec.ForWindow = body.ForWindow
	}
	if s := body.WindowSeconds; s > 0 {
		switch {
		case rule.LogSpec != nil:
			rule.LogSpec.WindowSeconds = s
		case rule.TraceErrorSpec != nil:
			rule.TraceErrorSpec.WindowSeconds = s
		case rule.TraceAttributeSpec != nil:
			rule.TraceAttributeSpec.WindowSeconds = s
		case rule.TraceLatencySpec != nil:
			rule.TraceLatencySpec.WindowSeconds = s
		case rule.TraceVolumeSpec != nil:
			rule.TraceVolumeSpec.WindowSeconds = s
		default:
			return errors.New("window_seconds is for log and trace checks; a metric check takes for_window")
		}
	}
	return nil
}
