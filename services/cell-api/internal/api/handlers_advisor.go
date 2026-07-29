// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The Advisor API (issue #1, design §5).
//
// Admin-only, deliberately. A suggestion states what the whole
// organisation ingests and what it costs — that is a different kind of
// information from "the services my team owns", and a group-scoped
// editor has no business seeing the org's bill. There is no partial
// view: an advisor filtered to one team's services would rank findings
// against a total the reader cannot see, which is worse than no answer.
//
// Enterprise-gated (`advisor`), while the demand LEDGER underneath is
// Community and always recording. That split is the point: a cell that
// only began measuring on the day someone bought a licence would have
// no history to advise from, and the advisor would be useless for its
// first month of ownership.

package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/advisor"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/demand"
)

// listAdvisorSuggestions: GET /api/v1/advisor/suggestions?advisor=&state=
func (h *Handlers) listAdvisorSuggestions(w http.ResponseWriter, r *http.Request) {
	if h.Advisor == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "the advisor is not available on this cell")
		return
	}
	q := r.URL.Query()
	name := q.Get("advisor")
	if name != "" && name != "telemetry" && name != "alerting" {
		httpserver.WriteError(w, http.StatusBadRequest, "advisor must be telemetry or alerting")
		return
	}
	state := q.Get("state")
	switch state {
	case "", "open", "accepted", "verified", "dismissed":
	default:
		httpserver.WriteError(w, http.StatusBadRequest, "unknown state")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))

	items, err := h.Advisor.List(r.Context(), middleware.OrgID(r), name, state, limit)
	if err != nil {
		h.Logger.Error("advisor: list failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "listing suggestions failed")
		return
	}
	// An empty advisor has two very different causes — "everything is
	// used" and "we have not been watching long enough to say" — and
	// they look identical on screen. Saying which is the difference
	// between a feature that looks broken and one that is honest about
	// needing time.
	ledger := advisor.LedgerStatus{Ready: true}
	if h.AdvisorEngine != nil {
		ledger = h.AdvisorEngine.Ledger(r.Context(), middleware.OrgID(r))
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"suggestions": items,
		// The EFFECTIVE window is echoed, not the constant — a cell
		// running a shortened window for testing must say so, or its
		// findings read as if they came from a month of observation.
		"window_days": ledger.NeedsDays,
		"ledger":      ledger,
	})
}

// decideAdvisorSuggestion handles accept and dismiss.
func (h *Handlers) decideAdvisorSuggestion(state string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Advisor == nil {
			httpserver.WriteError(w, http.StatusServiceUnavailable, "the advisor is not available on this cell")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid suggestion id")
			return
		}
		var body struct {
			Note string `json:"note"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body) // body is optional

		userID := uuid.Nil
		if p := middleware.Principal(r); p.UserID != nil {
			userID = *p.UserID
		}
		out, err := h.Advisor.Decide(r.Context(), middleware.OrgID(r), id, userID, state, body.Note)
		if err == advisor.ErrNotFound {
			httpserver.WriteError(w, http.StatusNotFound, "suggestion not found")
			return
		}
		if err != nil {
			h.Logger.Error("advisor: decide failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "recording the decision failed")
			return
		}
		// Accepting is the paper trail for "why did we stop collecting
		// that" — the suggestion's own text is the justification, which
		// is why it is stored on the row rather than recomputed.
		h.recordAudit(r, "advisor.suggestion_"+state, "advisor_suggestion", id.String(), map[string]any{
			"class": out.Class,
			"scope": out.ScopeID,
			"title": out.Title,
		})
		httpserver.WriteJSON(w, http.StatusOK, out)
	}
}

// runAdvisor: POST /api/v1/advisor/run — evaluate now.
//
// Exists for demos and for the first day after enabling the licence,
// when waiting until tonight makes the feature look broken. Rate-limited
// because a full evaluation is the most expensive query this service
// runs: a month of spans, sampled, per org.
func (h *Handlers) runAdvisor(w http.ResponseWriter, r *http.Request) {
	if h.AdvisorEngine == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "the advisor is not available on this cell")
		return
	}
	orgID := middleware.OrgID(r)
	if !h.advisorRunAllowed(orgID) {
		w.Header().Set("Retry-After", strconv.Itoa(int(advisorRunCooldown.Seconds())))
		httpserver.WriteError(w, http.StatusTooManyRequests,
			"an evaluation ran recently — it scans a month of telemetry, so it is limited to one every "+
				advisorRunCooldown.String())
		return
	}
	if err := h.AdvisorEngine.RunOrg(r.Context(), orgID); err != nil {
		h.Logger.Error("advisor: manual run failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "evaluation failed")
		return
	}
	h.recordAudit(r, "advisor.evaluated", "advisor", orgID.String(), nil)
	items, err := h.Advisor.List(r.Context(), orgID, "", "open", 500)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "evaluation finished but listing failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"open_suggestions": len(items)})
}

// advisorRunCooldown bounds manual evaluations per org.
const advisorRunCooldown = 10 * time.Minute

func (h *Handlers) advisorRunAllowed(orgID uuid.UUID) bool {
	h.advisorRunMu.Lock()
	defer h.advisorRunMu.Unlock()
	if h.advisorRunAt == nil {
		h.advisorRunAt = map[uuid.UUID]time.Time{}
	}
	if last, ok := h.advisorRunAt[orgID]; ok && time.Since(last) < advisorRunCooldown {
		return false
	}
	h.advisorRunAt[orgID] = time.Now()
	return true
}

// alertInstanceOpened: POST /api/v1/alert-instances/{id}/opened
//
// The engagement signal behind the Alert Fatigue Advisor (design §4).
// Notification emails and webhooks deep-link back with ?instance=<id>;
// when the app lands on one it calls this, which is the only way to
// tell "nobody looked at this page" from "everybody looked and decided
// it was fine".
//
// Deliberately not an ack. Following a link is engagement, not
// acknowledgement, and conflating the two would let a glance silence a
// real alert. Nothing about the instance changes here.
func (h *Handlers) alertInstanceOpened(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	orgID := middleware.OrgID(r)
	ruleID, err := h.Alerts.InstanceRuleID(r.Context(), orgID, id)
	if err != nil {
		// A stale link from an old email is ordinary, not an error worth
		// surfacing: the instance has simply been cleaned up.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.Demand.Record(orgID, demand.SignalAlert, "", ruleID.String(), demand.KindHuman)
	w.WriteHeader(http.StatusNoContent)
}
