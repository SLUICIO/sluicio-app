// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// An Advisor suggestion as a change to a collector's configuration
// (stage 3). The Telemetry Advisor finds a metric nothing reads, or a
// service's logs below a severity nobody opens; here that becomes a diff against the configuration a supervisor-type collector
// runs, which then goes the way every configuration change goes: checked,
// reviewed, and sent. A change that comes from the Advisor is always
// approved by the team that owns the collector, whatever the team's own
// setting, because the person applying it (an org admin reading the
// org's suggestions) is not the person who answers for the collector.
//
// Which collector carries the telemetry is not known yet: Sluicio sees
// the telemetry, not the path it took. So the admin chooses the collector; the
// list says where the change would apply and where it would not.

package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/advisor"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectorversion"
)

// adviceFor is what a suggestion asks a collector to drop, for the
// classes that can be turned into a collector change: an unused metric
// (T1) and a service's unread logs below a severity (T2).
func adviceFor(s advisor.Suggestion) (collectors.AdvisorTarget, bool) {
	switch {
	case s.Class == "T1" && s.ScopeKind == "metric" && s.ScopeID != "":
		return collectors.AdvisorTarget{Metric: s.ScopeID}, true
	case s.Class == "T2" && s.ScopeKind == "service" && s.ScopeID != "":
		band, _ := s.Evidence["severity_band"].(string)
		if floor, ok := advisor.FloorForBand(band); ok {
			return collectors.AdvisorTarget{Service: s.ScopeID, Below: floor}, true
		}
	}
	return collectors.AdvisorTarget{}, false
}

// severityNumbers are the OTTL severities a T2 change drops below, as
// stored severity numbers, for measuring it.
var severityNumbers = map[string]int32{"INFO": 9, "WARN": 13}

// collectorSuggestion loads a suggestion that can become a collector
// change.
func (h *Handlers) collectorSuggestion(w http.ResponseWriter, r *http.Request, rawID string) (advisor.Suggestion, bool) {
	if h.Advisor == nil || h.Collectors == nil {
		httpserver.WriteError(w, http.StatusNotFound, "not found")
		return advisor.Suggestion{}, false
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid suggestion id")
		return advisor.Suggestion{}, false
	}
	s, err := h.Advisor.Get(r.Context(), middleware.OrgID(r), id)
	if errors.Is(err, advisor.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "suggestion not found")
		return advisor.Suggestion{}, false
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return advisor.Suggestion{}, false
	}
	if _, ok := adviceFor(s); !ok {
		httpserver.WriteError(w, http.StatusConflict, "this suggestion cannot be applied to a collector yet; only unused metrics and unread logs can")
		return advisor.Suggestion{}, false
	}
	if s.State == "dismissed" {
		httpserver.WriteError(w, http.StatusConflict, "this suggestion was dismissed")
		return advisor.Suggestion{}, false
	}
	return s, true
}

// sluicioExporter recognises this cell's exporter in a configuration.
func (h *Handlers) sluicioExporter(r *http.Request) collectors.SluicioExporter {
	var hosts []string
	if h.Settings != nil {
		if ss, err := h.systemSettings(r); err == nil && ss.IngestBaseURL != "" {
			if u, err := url.Parse(ss.IngestBaseURL); err == nil && u.Hostname() != "" {
				hosts = append(hosts, strings.ToLower(u.Hostname()))
			}
		}
	}
	return collectors.SluicioExporter{IngestHosts: hosts}
}

// advisorChange is a suggestion applied to one collector's configuration.
type advisorChange struct {
	collectors.FilterChange
	// Before is the configuration as written, masked, that the change is
	// made against; Config (After) is the same with the change.
	Before string `json:"before"`
	// Unavailable says why the change cannot be made on this collector.
	Unavailable string `json:"unavailable,omitempty"`
}

func (h *Handlers) adviseCollector(r *http.Request, s advisor.Suggestion, c collectors.Collector) advisorChange {
	out := advisorChange{FilterChange: collectors.FilterChange{Changed: []string{}, Skipped: []collectors.PipelineSkip{}}}
	if c.Kind != collectors.KindSupervisor {
		out.Unavailable = "read only: it does not accept a configuration"
		return out
	}
	raw, err := h.Collectors.LatestRawOnly(r.Context(), c.ID)
	if errors.Is(err, collectors.ErrNotFound) {
		out.Unavailable = "it does not report its configuration as written (reports_raw_config)"
		return out
	}
	if err != nil {
		out.Unavailable = "its configuration could not be read"
		return out
	}
	masked, ok := collectors.MaskConfig(raw)
	if !ok {
		out.Unavailable = "its configuration is not YAML"
		return out
	}
	target := collectorversion.Default()
	if v := strings.TrimPrefix(c.Description["service.version"], "v"); collectorversion.Valid(v) {
		target.Version = v
	}
	filter, err := collectorversion.Name(collectorversion.FilterProcessor, target)
	if err != nil {
		out.Unavailable = err.Error()
		return out
	}
	t, _ := adviceFor(s)
	var drop collectors.FilterChange
	signal := "metrics"
	if t.Metric != "" {
		drop, err = collectors.DropMetric(masked, filter, t.Metric, h.sluicioExporter(r))
	} else {
		signal = "logs"
		drop, err = collectors.DropLogsBelow(masked, filter, t.Service, "SEVERITY_NUMBER_"+t.Below, h.sluicioExporter(r))
	}
	if err != nil {
		out.Unavailable = err.Error()
		return out
	}
	out.FilterChange = drop
	out.Before = masked
	if !drop.AlreadyDropped && len(drop.Changed) == 0 {
		out.Unavailable = "none of its " + signal + " pipelines sends only to Sluicio"
	}
	return out
}

// listAdvisorSuggestionCollectors: GET /api/v1/advisor/suggestions/{id}/collectors
//
// Every supervisor-type collector, with whether and where the suggestion
// would change it. No configurations in the answer; preview one for that.
func (h *Handlers) listAdvisorSuggestionCollectors(w http.ResponseWriter, r *http.Request) {
	s, ok := h.collectorSuggestion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	list, err := h.Collectors.List(r.Context(), h.collectorScope(r))
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	type row struct {
		Collector      collectors.Collector      `json:"collector"`
		Changed        []string                  `json:"changed"`
		Skipped        []collectors.PipelineSkip `json:"skipped"`
		AlreadyDropped bool                      `json:"already_dropped"`
		Unavailable    string                    `json:"unavailable,omitempty"`
	}
	out := []row{}
	for _, c := range list {
		if c.Kind != collectors.KindSupervisor {
			continue
		}
		a := h.adviseCollector(r, s, c)
		out = append(out, row{Collector: c, Changed: a.Changed, Skipped: a.Skipped, AlreadyDropped: a.AlreadyDropped, Unavailable: a.Unavailable})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"suggestion": s, "collectors": out})
}

// previewAdvisorCollectorChange: GET /api/v1/advisor/suggestions/{id}/collectors/{cid}
func (h *Handlers) previewAdvisorCollectorChange(w http.ResponseWriter, r *http.Request) {
	s, ok := h.collectorSuggestion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	cid, err := uuid.Parse(r.PathValue("cid"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid collector id")
		return
	}
	c, err := h.Collectors.Get(r.Context(), h.collectorScope(r), cid)
	if err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "collector not found")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, h.adviseCollector(r, s, c))
}

// collectorDeploymentEffect: GET /api/v1/collectors/{id}/deployments/{dep}/effect
//
// What an applied Advisor change did: the volume of what it drops (the
// metric, or the service's logs below the severity) in the window before
// the collector applied it and the window after. Enterprise. The numbers
// are the org's ingest, not this collector's share (Sluicio cannot tell
// that apart yet), so telemetry that also arrives through another
// collector does not drop to zero. Visible to
// org admins and to whoever may change this collector: the team that
// approved the change.
func (h *Handlers) collectorDeploymentEffect(w http.ResponseWriter, r *http.Request) {
	c, ok := h.collectorFromPath(w, r)
	if !ok {
		return
	}
	if !h.canPushCollector(r, c.GroupID) {
		httpserver.WriteError(w, http.StatusForbidden, "only the owner team's editors and admins, and org admins, can see the effect of a change")
		return
	}
	depID, ok := deploymentFromPath(w, r)
	if !ok {
		return
	}
	d, err := h.Collectors.GetDeployment(r.Context(), c.ID, depID)
	if errors.Is(err, collectors.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	t := d.AdvisorTarget
	if t == nil || (t.Metric == "" && (t.Service == "" || severityNumbers[t.Below] == 0)) {
		httpserver.WriteError(w, http.StatusConflict, "only a change made from an Advisor suggestion has a measured effect")
		return
	}
	subject := t.Metric
	if subject == "" {
		subject = t.Service + "'s logs below " + t.Below
	}
	if d.Status != collectors.DeploymentApplied || d.FinishedAt == nil {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"subject": subject, "applied": false})
		return
	}
	if h.ClickHouseConn == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "telemetry storage is not available")
		return
	}
	var e advisor.Effect
	if t.Metric != "" {
		e, err = advisor.MeasureMetric(r.Context(), h.ClickHouseConn, middleware.OrgID(r), t.Metric, *d.FinishedAt, time.Now())
	} else {
		e, err = advisor.MeasureLogs(r.Context(), h.ClickHouseConn, middleware.OrgID(r), t.Service, severityNumbers[t.Below], *d.FinishedAt, time.Now())
	}
	if err != nil {
		h.Logger.Error("measure collector change failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "measuring failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"subject": subject, "applied": true, "effect": e})
}
