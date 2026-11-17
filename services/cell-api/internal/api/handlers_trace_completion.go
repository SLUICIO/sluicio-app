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
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tracecompletion"
)

// HTTP surface for trace-completion rules:
//
//   GET    /api/v1/integrations/{id}/completion-rules       — list
//   POST   /api/v1/integrations/{id}/completion-rules       — create
//   PATCH  /api/v1/integrations/{id}/completion-rules/{rid} — update
//   DELETE /api/v1/integrations/{id}/completion-rules/{rid} — delete
//
//   GET    /api/v1/integrations/{id}/completion-counts      — live
//          counts (completed/pending/delayed) across all enabled rules
//          on this integration, for the chip in the integration detail
//
// Mutating routes are gated to org admin (RequireRole) — same level
// as the rest of the integration-config surface.

// traceCompletionStageWire is one hop in the pipeline, on the wire.
// spans (with an optional service each) and require are the current
// shape; span_names alone is still accepted and means those names
// from any service.
type traceCompletionStageWire struct {
	Spans          []traceCompletionSpanWire `json:"spans,omitempty"`
	Require        string                    `json:"require,omitempty"`
	SpanNames      []string                  `json:"span_names"`
	TimeoutSeconds int                       `json:"timeout_seconds,omitempty"`
}

// traceCompletionSpanWire is one span a stage waits for; an empty
// service means any of the integration's services.
type traceCompletionSpanWire struct {
	Service  string `json:"service,omitempty"`
	SpanName string `json:"span_name"`
}

func stageToWire(st tracecompletion.Stage) traceCompletionStageWire {
	spans := make([]traceCompletionSpanWire, 0, len(st.Spans))
	for _, m := range st.Spans {
		spans = append(spans, traceCompletionSpanWire{Service: m.Service, SpanName: m.SpanName})
	}
	return traceCompletionStageWire{Spans: spans, Require: st.Require, SpanNames: st.SpanNames, TimeoutSeconds: st.TimeoutSeconds}
}

func stageFromWire(st traceCompletionStageWire) tracecompletion.Stage {
	spans := make([]tracecompletion.SpanMatch, 0, len(st.Spans))
	for _, m := range st.Spans {
		spans = append(spans, tracecompletion.SpanMatch{Service: m.Service, SpanName: m.SpanName})
	}
	return tracecompletion.Stage{Spans: spans, Require: st.Require, SpanNames: st.SpanNames, TimeoutSeconds: st.TimeoutSeconds}
}

// traceCompletionRuleInput is the wire shape used by POST and PATCH.
// New rules send start_span_name + stages; closing_span_names /
// timeout_seconds are accepted for back-compat and folded into a single
// stage by Normalize.
type traceCompletionRuleInput struct {
	Name                  string                     `json:"name"`
	Description           string                     `json:"description"`
	Severity              string                     `json:"severity"`
	Enabled               bool                       `json:"enabled"`
	StartSpanName         string                     `json:"start_span_name"`
	Stages                []traceCompletionStageWire `json:"stages"`
	DefaultTimeoutSeconds int                        `json:"default_timeout_seconds"`
	ClosingSpanNames      []string                   `json:"closing_span_names"`
	TimeoutSeconds        int                        `json:"timeout_seconds"`
	LookbackSeconds       int                        `json:"lookback_seconds"`
	ChannelIDs            []uuid.UUID                `json:"channel_ids"`
}

// traceCompletionRuleResponse mirrors tracecompletion.Rule with the
// spec inlined into top-level JSON keys (friendlier for the SPA).
type traceCompletionRuleResponse struct {
	ID                    uuid.UUID                  `json:"id"`
	IntegrationID         uuid.UUID                  `json:"integration_id"`
	Name                  string                     `json:"name"`
	Description           string                     `json:"description"`
	Severity              string                     `json:"severity"`
	Enabled               bool                       `json:"enabled"`
	StartSpanName         string                     `json:"start_span_name"`
	Stages                []traceCompletionStageWire `json:"stages"`
	DefaultTimeoutSeconds int                        `json:"default_timeout_seconds"`
	ClosingSpanNames      []string                   `json:"closing_span_names"`
	TimeoutSeconds        int                        `json:"timeout_seconds"`
	LookbackSeconds       int                        `json:"lookback_seconds"`
	ChannelIDs            []uuid.UUID                `json:"channel_ids"`
	// Delivery is where a delayed trace is sent: the rule's own channels
	// or its integration's profile. Set on the list only.
	Delivery  *ruleDelivery `json:"delivery,omitempty"`
	CreatedAt string        `json:"created_at"`
	UpdatedAt string        `json:"updated_at"`
}

func ruleToResponse(r tracecompletion.Rule) traceCompletionRuleResponse {
	stages := make([]traceCompletionStageWire, 0, len(r.Spec.Stages))
	for _, st := range r.Spec.Stages {
		stages = append(stages, stageToWire(st))
	}
	return traceCompletionRuleResponse{
		ID:                    r.ID,
		IntegrationID:         r.IntegrationID,
		Name:                  r.Name,
		Description:           r.Description,
		Severity:              r.Severity,
		Enabled:               r.Enabled,
		StartSpanName:         r.Spec.StartSpanName,
		Stages:                stages,
		DefaultTimeoutSeconds: r.Spec.DefaultTimeoutSeconds,
		ClosingSpanNames:      r.Spec.ClosingSpanNames,
		TimeoutSeconds:        r.Spec.TimeoutSeconds,
		LookbackSeconds:       r.Spec.LookbackSeconds,
		ChannelIDs:            r.ChannelIDs,
		CreatedAt:             r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:             r.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// listTraceCompletionRules: GET /integrations/{id}/completion-rules
func (h *Handlers) listTraceCompletionRules(w http.ResponseWriter, r *http.Request) {
	integID, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	if _, vok := h.gateIntegrationMembers(w, r, integID); !vok {
		return
	}
	orgID := middleware.OrgID(r)
	rules, err := h.TraceCompletion.ListForIntegration(r.Context(), orgID, integID)
	if err != nil {
		h.Logger.Error("list trace completion rules", "err", err, "integration_id", integID)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]traceCompletionRuleResponse, 0, len(rules))
	dr := h.newDeliveryResolver(orgID)
	for _, rule := range rules {
		resp := ruleToResponse(rule)
		integ := rule.IntegrationID
		d := dr.For(r.Context(), rule.ChannelIDs, &integ, nil)
		resp.Delivery = &d
		out = append(out, resp)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"rules": out})
}

// createTraceCompletionRule: POST /integrations/{id}/completion-rules
func (h *Handlers) createTraceCompletionRule(w http.ResponseWriter, r *http.Request) {
	integID, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	var body traceCompletionRuleInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	in, perr := h.parseTraceCompletionInput(body, integID)
	if perr != "" {
		httpserver.WriteError(w, http.StatusBadRequest, perr)
		return
	}
	orgID := middleware.OrgID(r)
	created, err := h.TraceCompletion.Create(r.Context(), orgID, in)
	if err != nil {
		if isUserError(err) {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("create trace completion rule", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.recordAudit(r, "completion_rule.created", "completion_rule", created.ID.String(), map[string]any{"name": created.Name, "integration_id": integID.String()})
	httpserver.WriteJSON(w, http.StatusCreated, ruleToResponse(created))
}

// updateTraceCompletionRule: PATCH /integrations/{id}/completion-rules/{rid}
func (h *Handlers) updateTraceCompletionRule(w http.ResponseWriter, r *http.Request) {
	integID, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	ruleID, ok := h.parsePathUUID(w, r, "rid")
	if !ok {
		return
	}
	var body traceCompletionRuleInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	in, perr := h.parseTraceCompletionInput(body, integID)
	if perr != "" {
		httpserver.WriteError(w, http.StatusBadRequest, perr)
		return
	}
	orgID := middleware.OrgID(r)
	updated, err := h.TraceCompletion.Update(r.Context(), orgID, ruleID, in)
	if err != nil {
		if errors.Is(err, tracecompletion.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
		if isUserError(err) {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("update trace completion rule", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "completion_rule.updated", "completion_rule", updated.ID.String(), map[string]any{"name": updated.Name, "integration_id": integID.String()})
	httpserver.WriteJSON(w, http.StatusOK, ruleToResponse(updated))
}

// deleteTraceCompletionRule: DELETE /integrations/{id}/completion-rules/{rid}
func (h *Handlers) deleteTraceCompletionRule(w http.ResponseWriter, r *http.Request) {
	integID, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	ruleID, ok := h.parsePathUUID(w, r, "rid")
	if !ok {
		return
	}
	orgID := middleware.OrgID(r)
	if err := h.TraceCompletion.Delete(r.Context(), orgID, ruleID); err != nil {
		if errors.Is(err, tracecompletion.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
		h.Logger.Error("delete trace completion rule", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "completion_rule.deleted", "completion_rule", ruleID.String(), map[string]any{"integration_id": integID.String()})
	w.WriteHeader(http.StatusNoContent)
}

// completionFiringsResponse is one row in the firings list.
type completionFiringResponse struct {
	InstanceID uuid.UUID `json:"instance_id"`
	RuleID     uuid.UUID `json:"rule_id"`
	RuleName   string    `json:"rule_name"`
	// IntegrationID is the integration the firing belongs to. A
	// trace can participate in multiple integrations; the UI uses
	// this to filter firings to "the integration the user is
	// looking at right now" — without it, a trace that's late in
	// integration A would also light up red in integration B even
	// though B has its own (passing) SLA.
	IntegrationID uuid.UUID `json:"integration_id"`
	// Severity is inherited from the rule that opened the firing —
	// the frontend uses it to drive the trace's StatusPip colour:
	// warning → yellow, critical → red (same as a real error span).
	Severity        string  `json:"severity"`
	TraceID         string  `json:"trace_id"`
	State           string  `json:"state"`
	StartedAt       string  `json:"started_at"` // when the firing opened
	LastEvaluatedAt string  `json:"last_evaluated_at"`
	EndedAt         *string `json:"ended_at,omitempty"`
	Summary         string  `json:"summary"`
	TraceStartedAt  *string `json:"trace_started_at,omitempty"` // when the trace itself began
	// HandledAt is set when an operator marked the delayed trace as
	// handled; the UI renders it benign (no longer warning/error).
	HandledAt *string `json:"handled_at,omitempty"`
	// Stage is the 1-based stage the trace stalled at (0: a firing from
	// before stages, meaning the last one), and WaitingFor the span(s)
	// it had not reached.
	Stage      int    `json:"stage"`
	WaitingFor string `json:"waiting_for,omitempty"`
}

// firingToResponse converts a tracecompletion.Firing into the wire
// shape. Centralised so the two endpoints that return firings
// (per-integration list + per-trace lookup) stay in lockstep.
func firingToResponse(f tracecompletion.Firing) completionFiringResponse {
	row := completionFiringResponse{
		InstanceID:      f.InstanceID,
		RuleID:          f.RuleID,
		RuleName:        f.RuleName,
		IntegrationID:   f.IntegrationID,
		Severity:        f.Severity,
		TraceID:         f.TraceID,
		State:           f.State,
		StartedAt:       f.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
		LastEvaluatedAt: f.LastEvaluatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Summary:         f.Summary,
		Stage:           f.Stage,
		WaitingFor:      f.WaitingFor,
	}
	if f.EndedAt != nil {
		s := f.EndedAt.UTC().Format("2006-01-02T15:04:05Z")
		row.EndedAt = &s
	}
	if f.TraceStartedAt != nil {
		s := f.TraceStartedAt.UTC().Format("2006-01-02T15:04:05Z")
		row.TraceStartedAt = &s
	}
	if f.HandledAt != nil {
		s := f.HandledAt.UTC().Format("2006-01-02T15:04:05Z")
		row.HandledAt = &s
	}
	return row
}

// listCompletionFirings: GET /integrations/{id}/completion-firings
//
// Lists alert_instances opened by trace-completion rules on this
// integration. Sticky-delayed means a delayed trace remains 'firing'
// in the table even after the rule's lookback window expires — so
// this endpoint is the canonical "where are my SLA breaches?" view.
func (h *Handlers) listCompletionFirings(w http.ResponseWriter, r *http.Request) {
	integID, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	if _, vok := h.gateIntegrationMembers(w, r, integID); !vok {
		return
	}
	orgID := middleware.OrgID(r)
	tr := ParseRange(r, time.Hour)
	firings, err := h.TraceCompletion.ListFirings(r.Context(), orgID, integID, tr.From, tr.To, 200)
	if err != nil {
		h.Logger.Error("list completion firings", "err", err, "integration_id", integID)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]completionFiringResponse, 0, len(firings))
	for _, f := range firings {
		out = append(out, firingToResponse(f))
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"firings": out})
}

// listCompletionFiringsForTrace: GET /traces/{id}/completion-firings
//
// Returns all firings (any state) opened for a specific trace_id,
// across all rules in the org. Drives the trace-level StatusPip on
// the TraceDetail page: a 'firing' instance from a 'warning' rule
// flips the trace to warn-styled; 'critical' flips to err-styled.
func (h *Handlers) listCompletionFiringsForTrace(w http.ResponseWriter, r *http.Request) {
	traceID := strings.TrimSpace(r.PathValue("id"))
	if traceID == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "missing trace id")
		return
	}
	orgID := middleware.OrgID(r)
	firings, err := h.TraceCompletion.ListFiringsForTrace(r.Context(), orgID, traceID)
	if err != nil {
		h.Logger.Error("list completion firings for trace", "err", err, "trace_id", traceID)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Same group-visibility rule as the trace detail itself: firings name
	// the rule + integration, so drop those whose integration has no
	// services in the caller's policy allowlist. Cached per integration —
	// a trace touches one or two, not many.
	if allowed, hasFilter := h.visibleServiceFilter(r); hasFilter {
		allowedSet := make(map[string]struct{}, len(allowed))
		for _, n := range allowed {
			allowedSet[n] = struct{}{}
		}
		visible := map[uuid.UUID]bool{}
		kept := firings[:0]
		for _, f := range firings {
			ok, seen := visible[f.IntegrationID]
			if !seen {
				if members, err := h.integrationExpander(r.Context(), orgID, f.IntegrationID); err == nil {
					for _, m := range members {
						if _, in := allowedSet[m]; in {
							ok = true
							break
						}
					}
				}
				visible[f.IntegrationID] = ok
			}
			if ok {
				kept = append(kept, f)
			}
		}
		firings = kept
	}
	out := make([]completionFiringResponse, 0, len(firings))
	for _, f := range firings {
		out = append(out, firingToResponse(f))
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"firings": out})
}

// handleCompletionFiring: POST /integrations/{id}/completion-firings/{iid}/handle
//
// Marks a delayed trace's firing as operator-handled — e.g. the message
// was manually resent. The firing stays open (so the evaluator never
// re-fires the same delay) but stops counting as delayed and renders
// benign (no longer warning/error). Idempotent from the UI's view: a
// firing that's already handled / resolved / gone returns 404.
func (h *Handlers) handleCompletionFiring(w http.ResponseWriter, r *http.Request) {
	integID, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	instanceID, ok := h.parsePathUUID(w, r, "iid")
	if !ok {
		return
	}
	orgID := middleware.OrgID(r)
	if err := h.TraceCompletion.HandleFiring(r.Context(), orgID, integID, instanceID); err != nil {
		if errors.Is(err, tracecompletion.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "no open delayed firing to handle")
			return
		}
		h.Logger.Error("handle completion firing", "err", err, "instance_id", instanceID)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "completion_firing.handled", "completion_firing", instanceID.String(), map[string]any{"integration_id": integID.String()})
	w.WriteHeader(http.StatusNoContent)
}

// completionCounts: GET /integrations/{id}/completion-counts
//
// Returns aggregated counts across all enabled rules on the
// integration. If multiple rules exist, the counts are summed (each
// trace counted under each rule it matches — which is the "are there
// any delays anywhere" view the chip needs).
func (h *Handlers) completionCounts(w http.ResponseWriter, r *http.Request) {
	integID, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	if _, vok := h.gateIntegrationMembers(w, r, integID); !vok {
		return
	}
	orgID := middleware.OrgID(r)
	rules, err := h.TraceCompletion.ListForIntegration(r.Context(), orgID, integID)
	if err != nil {
		h.Logger.Error("completion counts: list rules", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	total := tracecompletion.Counts{}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		c, err := h.TraceCompletionEvaluator.CountsFor(r.Context(), rule)
		if err != nil {
			h.Logger.Warn("completion counts: rule failed", "rule_id", rule.ID, "err", err)
			continue
		}
		total.Completed += c.Completed
		total.Pending += c.Pending
		total.Delayed += c.Delayed
	}
	httpserver.WriteJSON(w, http.StatusOK, total)
}

// ── helpers ──────────────────────────────────────────────────────────

// parseTraceCompletionInput converts the wire shape to the storage
// shape + does basic field validation. Returns a non-empty error
// string for any user-facing problem; "" for OK.
func (h *Handlers) parseTraceCompletionInput(body traceCompletionRuleInput, integID uuid.UUID) (tracecompletion.RuleInput, string) {
	// Trim legacy closing span names — leading/trailing whitespace is
	// almost always an accident. Normalize folds these into a stage.
	names := make([]string, 0, len(body.ClosingSpanNames))
	for _, n := range body.ClosingSpanNames {
		if t := strings.TrimSpace(n); t != "" {
			names = append(names, t)
		}
	}
	stages := make([]tracecompletion.Stage, 0, len(body.Stages))
	for _, st := range body.Stages {
		stages = append(stages, stageFromWire(st))
	}
	in := tracecompletion.RuleInput{
		IntegrationID: integID,
		Name:          strings.TrimSpace(body.Name),
		Description:   body.Description,
		Severity:      body.Severity,
		Enabled:       body.Enabled,
		Spec: tracecompletion.RuleSpec{
			Kind:                  "trace_completion",
			StartSpanName:         body.StartSpanName,
			Stages:                stages,
			DefaultTimeoutSeconds: body.DefaultTimeoutSeconds,
			ClosingSpanNames:      names,
			TimeoutSeconds:        body.TimeoutSeconds,
			LookbackSeconds:       body.LookbackSeconds,
		},
		ChannelIDs: body.ChannelIDs,
	}
	// Canonicalise: synthesize stages from legacy fields, trim names,
	// and fill the default lookback (4× the longest hop).
	in.Spec.Normalize()
	return in, ""
}

// parsePathUUID extracts a UUID path param, writing a 400 if it's
// missing or malformed. Returns (uuid.Nil, false) on failure.
func (h *Handlers) parsePathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	raw := r.PathValue(name)
	if raw == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "missing "+name)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}

// isUserError flags errors that should map to 4xx rather than 500.
// The tracecompletion package returns plain errors for validation
// failures — match the prefix to distinguish them.
func isUserError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "tracecompletion:") ||
		strings.HasPrefix(msg, "integration_id") ||
		strings.HasPrefix(msg, "name") ||
		strings.HasPrefix(msg, "severity")
}

// completionDryRunBody is a draft rule, or an existing rule with the
// fields a completion_rule proposal would change.
type completionDryRunBody struct {
	Rule   *traceCompletionRuleInput  `json:"rule"`
	RuleID string                     `json:"rule_id"`
	Patch  map[string]json.RawMessage `json:"patch"`
	Hours  int                        `json:"hours"`
}

// dryRunCompletionRule: POST /integrations/{id}/completion-rules/dry-run
//
// What a draft rule would make of the integration's traces over the last
// hours, judged as of now, without saving or firing anything. A wrong
// start span is the costly mistake (the rule then follows no trace and
// never fires), and it shows here as 0 of N traces before the rule goes
// live. A read: anyone who can see the integration may run it.
func (h *Handlers) dryRunCompletionRule(w http.ResponseWriter, r *http.Request) {
	integID, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	members, vok := h.gateIntegrationMembers(w, r, integID)
	if !vok {
		return
	}
	if h.TraceCompletionEvaluator == nil {
		httpserver.WriteError(w, http.StatusNotImplemented, "trace completion is not running on this cell")
		return
	}
	var body completionDryRunBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var draft traceCompletionRuleInput
	switch {
	case body.Rule != nil && body.RuleID == "":
		draft = *body.Rule
	case body.Rule == nil && body.RuleID != "":
		rid, err := uuid.Parse(body.RuleID)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid rule_id")
			return
		}
		rule, err := h.TraceCompletion.Get(r.Context(), middleware.OrgID(r), rid)
		if err != nil || rule.IntegrationID != integID {
			httpserver.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
		changes := make([]proposals.Change, 0, len(body.Patch))
		for f, v := range body.Patch {
			if !completionRuleFields[f] {
				httpserver.WriteError(w, http.StatusBadRequest, "field is not editable on a completion rule: "+f)
				return
			}
			changes = append(changes, proposals.Change{Field: f, After: v})
		}
		merged, err := mergeCompletionRule(rule, changes)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		draft = merged
	default:
		httpserver.WriteError(w, http.StatusBadRequest, "send rule (a draft) or rule_id (an existing rule, optionally with patch)")
		return
	}
	// Name and severity do not change what a dry run finds; a draft
	// written only to be tried need not have them.
	if strings.TrimSpace(draft.Name) == "" {
		draft.Name = "dry run"
	}
	if draft.Severity == "" {
		draft.Severity = "warning"
	}
	in, perr := h.parseTraceCompletionInput(draft, integID)
	if perr != "" {
		httpserver.WriteError(w, http.StatusBadRequest, perr)
		return
	}
	if err := in.Validate(); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	window := 24 * time.Hour
	if body.Hours > 0 {
		window = time.Duration(body.Hours) * time.Hour
	}
	if window > tracecompletion.MaxDryRunWindow {
		window = tracecompletion.MaxDryRunWindow
	}
	to := time.Now().UTC()
	res, err := h.TraceCompletionEvaluator.DryRun(r.Context(), in.Spec, members, to.Add(-window), to)
	if err != nil {
		h.Logger.Error("completion dry run failed", "err", err, "integration_id", integID)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, res)
}
