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
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/advisor"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectorversion"
)

// adviceFor is what a suggestion asks a collector to change, for the
// classes that can be turned into a collector change: an unused metric
// (T1), a service's unread logs below a severity (T2), an unread
// attribute (T3), echoed request metadata (T5), personal-data-shaped
// values (T6: deleted, or hashed when asked for, see withHash) and
// internal spans nothing reads (T7). A high-cardinality attribute (T4) is
// not one: the Advisor flags it for review because it is often the
// useful key, and a button that deletes it would say the opposite.
func adviceFor(s advisor.Suggestion) (collectors.AdvisorTarget, error) {
	// Attribute findings name service and key in the fingerprint
	// (T3|attr|<service>|<key>); scope_id joins them with a dot that
	// either may contain.
	parts := strings.SplitN(s.Fingerprint, "|", 4)
	svc, key := "", ""
	if len(parts) == 4 {
		svc, key = parts[2], parts[3]
	}
	switch {
	case s.Class == "T1" && s.ScopeKind == "metric" && s.ScopeID != "":
		return collectors.AdvisorTarget{Metric: s.ScopeID}, nil
	case s.Class == "T2" && s.ScopeKind == "service" && s.ScopeID != "":
		band, _ := s.Evidence["severity_band"].(string)
		if floor, ok := advisor.FloorForBand(band); ok {
			return collectors.AdvisorTarget{Service: s.ScopeID, Below: floor}, nil
		}
	case s.Class == "T3" && svc != "" && key != "":
		return collectors.AdvisorTarget{Service: svc, Attribute: key}, nil
	case s.Class == "T5" && svc != "" && key != "":
		return collectors.AdvisorTarget{Service: svc, Attribute: key, Prefix: true}, nil
	case s.Class == "T7" && svc != "" && key != "":
		return collectors.AdvisorTarget{Service: svc, SpanName: key}, nil
	case s.Class == "T6" && svc != "" && key != "":
		referenced, known := s.Evidence["referenced_by_config"].(bool)
		if !known {
			return collectors.AdvisorTarget{}, errors.New("run the Advisor again first: this finding predates the check for whether Sluicio's own configuration reads the attribute")
		}
		if referenced {
			return collectors.AdvisorTarget{}, errors.New("Sluicio's own configuration (a matcher, facet or rule) reads this attribute, and deleting or hashing it would break that; change that configuration first, or handle the attribute at its source")
		}
		return collectors.AdvisorTarget{Service: svc, Attribute: key}, nil
	case s.Class == "T4":
		return collectors.AdvisorTarget{}, errors.New("a high-cardinality attribute is flagged for review, not removal: it is often the key you search by; if it is not needed, remove it at its source or with the snippet")
	}
	return collectors.AdvisorTarget{}, errors.New("this suggestion cannot be applied to a collector")
}

// withHash turns a personal-data change (T6) from deleting the attribute,
// the default, into hashing its value: asked for when "the same value
// twice" has to keep matching. Pseudonymous, not anonymous. Any other
// class is returned as it is.
func withHash(s advisor.Suggestion, t collectors.AdvisorTarget, hash bool) collectors.AdvisorTarget {
	if hash && s.Class == "T6" {
		t.Hash = true
	}
	return t
}

// subjectOf names what an Advisor change acts on, for its measured effect.
func subjectOf(t collectors.AdvisorTarget) string {
	switch {
	case t.Metric != "":
		return t.Metric
	case t.Below != "":
		return t.Service + "'s logs below " + t.Below
	case t.SpanName != "":
		return t.Service + "'s internal \"" + t.SpanName + "\" spans"
	case t.Hash:
		return `unhashed "` + t.Attribute + `" values on ` + t.Service + "'s spans"
	case t.Prefix:
		return `"` + t.Attribute + `*" on ` + t.Service + "'s spans"
	default:
		return `"` + t.Attribute + `" on ` + t.Service + "'s spans"
	}
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
	if _, err := adviceFor(s); err != nil {
		httpserver.WriteError(w, http.StatusConflict, err.Error())
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

func (h *Handlers) adviseCollector(r *http.Request, s advisor.Suggestion, c collectors.Collector, hash bool) advisorChange {
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
	t, _ := adviceFor(s)
	t = withHash(s, t, hash)
	component, signal := collectorversion.FilterProcessor, "metrics"
	switch {
	case t.Below != "":
		signal = "logs"
	case t.SpanName != "":
		signal = "traces"
	case t.Attribute != "":
		component, signal = collectorversion.TransformProcessor, "traces"
	}
	proc, err := collectorversion.Name(component, target)
	if err != nil {
		out.Unavailable = err.Error()
		return out
	}
	sl := h.sluicioExporter(r)
	var drop collectors.FilterChange
	switch {
	case t.Metric != "":
		drop, err = collectors.DropMetric(masked, proc, t.Metric, sl)
	case t.Below != "":
		drop, err = collectors.DropLogsBelow(masked, proc, t.Service, "SEVERITY_NUMBER_"+t.Below, sl)
	case t.SpanName != "":
		drop, err = collectors.DropInternalSpans(masked, proc, t.Service, t.SpanName, sl)
	case t.Hash:
		drop, err = collectors.HashSpanAttribute(masked, proc, t.Service, t.Attribute, sl)
	case t.Prefix:
		drop, err = collectors.DeleteSpanAttributePrefix(masked, proc, t.Service, t.Attribute, sl)
	default:
		drop, err = collectors.DeleteSpanAttribute(masked, proc, t.Service, t.Attribute, sl)
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
		// Stamped: the collector stamps its data, so Carried says how
		// much of this telemetry came through it in the last 24 hours.
		Stamped bool    `json:"stamped"`
		Carried *uint64 `json:"carried,omitempty"`
	}
	// Which collector carries it, from the stamp on the data. Nil when
	// telemetry storage cannot be read: the list still works, unordered.
	var carried map[string]uint64
	if h.ClickHouseConn != nil {
		t, _ := adviceFor(s)
		now := time.Now()
		if m, err := advisor.CarriedBy(r.Context(), h.ClickHouseConn, middleware.OrgID(r), selectionOf(t), now.Add(-carriedWindow), now); err == nil {
			carried = m
		} else {
			h.Logger.Warn("advisor: carried by collector failed", "err", err)
		}
	}
	out := []row{}
	for _, c := range list {
		if c.Kind != collectors.KindSupervisor {
			continue
		}
		a := h.adviseCollector(r, s, c, false)
		rw := row{Collector: c, Changed: a.Changed, Skipped: a.Skipped, AlreadyDropped: a.AlreadyDropped, Unavailable: a.Unavailable,
			Stamped: h.collectorStamped(r, c)}
		if carried != nil && rw.Stamped {
			n := carried[c.InstanceUID]
			rw.Carried = &n
		}
		out = append(out, rw)
	}
	// The collectors that carry it first, most first.
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := out[i].Carried, out[j].Carried
		switch {
		case ci == nil || cj == nil:
			return ci != nil && cj == nil
		default:
			return *ci > *cj
		}
	})
	resp := map[string]any{"suggestion": s, "collectors": out, "window_hours": int(carriedWindow.Hours())}
	if carried != nil {
		resp["unstamped"] = carried[""]
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

// previewAdvisorCollectorChange: GET /api/v1/advisor/suggestions/{id}/collectors/{cid}
//
// ?hash=true hashes a personal-data value (T6) instead of deleting it.
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
	httpserver.WriteJSON(w, http.StatusOK, h.adviseCollector(r, s, c, r.URL.Query().Get("hash") == "true"))
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
	if t == nil || (t.Metric == "" && (t.Service == "" || (t.Attribute == "" && t.SpanName == "" && severityNumbers[t.Below] == 0))) {
		httpserver.WriteError(w, http.StatusConflict, "only a change made from an Advisor suggestion has a measured effect")
		return
	}
	subject := subjectOf(*t)
	if d.Status != collectors.DeploymentApplied || d.FinishedAt == nil {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"subject": subject, "applied": false})
		return
	}
	if h.ClickHouseConn == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "telemetry storage is not available")
		return
	}
	org, at, now := middleware.OrgID(r), *d.FinishedAt, time.Now()
	sel := selectionOf(*t)
	e, err := advisor.Measure(r.Context(), h.ClickHouseConn, org, sel, "", at, now)
	if err != nil {
		h.Logger.Error("measure collector change failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "measuring failed")
		return
	}
	out := map[string]any{"subject": subject, "applied": true, "effect": e}
	// With the collector stamped, its own share: what came through this
	// collector, before and after, beside everything Sluicio received.
	if h.collectorStamped(r, c) {
		own, err := advisor.Measure(r.Context(), h.ClickHouseConn, org, sel, c.InstanceUID, at, now)
		if err == nil {
			out["collector_effect"] = own
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, out)
}

// selectionOf is the telemetry an Advisor change acts on.
func selectionOf(t collectors.AdvisorTarget) advisor.Selection {
	switch {
	case t.Metric != "":
		return advisor.MetricSelection(t.Metric)
	case t.Below != "":
		return advisor.LogSelection(t.Service, severityNumbers[t.Below])
	case t.SpanName != "":
		return advisor.SpanNameSelection(t.Service, t.SpanName)
	default:
		return advisor.SpanSelection(t.Service, t.Attribute, t.Prefix, t.Hash)
	}
}

// carriedWindow is how far back "which collector carries it" looks.
const carriedWindow = 24 * time.Hour

// collectorVolume: GET /api/v1/collectors/{id}/volume
//
// What a stamped collector carried to Sluicio in the last 24 hours, per
// signal, from the stamp on the data (the CollectorId column). Enterprise,
// like the stamp. Zero for a collector that is not stamped.
func (h *Handlers) collectorVolume(w http.ResponseWriter, r *http.Request) {
	c, ok := h.collectorFromPath(w, r)
	if !ok {
		return
	}
	if h.ClickHouseConn == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "telemetry storage is not available")
		return
	}
	now := time.Now()
	v, err := advisor.VolumeOf(r.Context(), h.ClickHouseConn, middleware.OrgID(r), c.InstanceUID, now.Add(-carriedWindow), now)
	if err != nil {
		h.Logger.Error("collector volume failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"volume": v, "window_hours": int(carriedWindow.Hours())})
}

// previewCollectorStamp: GET /api/v1/collectors/{id}/stamp
//
// The change that makes a collector put its own id on the data it
// carries (sluicio.collector.id, moved into its own column at ingest),
// so Sluicio can tell which collector telemetry came through. Only in
// pipelines that send to Sluicio alone: another backend never sees the
// attribute. Enterprise; sent with "stamp": true it is always proposed
// to the owner team, since it changes what the collector sends.
func (h *Handlers) previewCollectorStamp(w http.ResponseWriter, r *http.Request) {
	c, ok := h.pushableCollector(w, r)
	if !ok {
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, h.stampChange(r, c))
}

func (h *Handlers) stampChange(r *http.Request, c collectors.Collector) advisorChange {
	out := advisorChange{FilterChange: collectors.FilterChange{Changed: []string{}, Skipped: []collectors.PipelineSkip{}}}
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
	change, err := collectors.AddStamp(masked, c.InstanceUID, h.sluicioExporter(r))
	if err != nil {
		out.Unavailable = err.Error()
		return out
	}
	out.FilterChange, out.Before = change, masked
	if !change.AlreadyDropped && len(change.Changed) == 0 {
		out.Unavailable = "none of its pipelines sends only to Sluicio"
	}
	return out
}

// collectorStamped says whether the collector's newest configuration as
// written already stamps its data with its own id.
func (h *Handlers) collectorStamped(r *http.Request, c collectors.Collector) bool {
	if c.Kind != collectors.KindSupervisor {
		return false
	}
	raw, err := h.Collectors.LatestRawOnly(r.Context(), c.ID)
	if err != nil {
		return false
	}
	change, err := collectors.AddStamp(raw, c.InstanceUID, h.sluicioExporter(r))
	return err == nil && change.AlreadyDropped
}

