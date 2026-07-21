// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// The Errors feed: one org-wide view of everything currently wrong,
// scoped to what the caller is allowed to see and respecting the "clear
// errors" acknowledgements. Two complementary lists:
//
//   - failing_checks — every firing health check (alert instance),
//     attributed to the service or integration it guards. Filtered to
//     the teams the caller can see, exactly like the Alerts page.
//   - services — every service whose status is "errors" or "unhealthy"
//     after applying its clear-errors watermark. Filtered to the
//     caller's group-visible services. Acknowledged services drop out
//     automatically (their effective error count is zeroed), and
//     reappear only when new errors arrive after the watermark.

// FailingCheck is one firing health check on the Errors feed, with its
// guarded target resolved for display + linking.
type FailingCheck struct {
	ID              uuid.UUID         `json:"id"`
	RuleID          uuid.UUID         `json:"rule_id"`
	RuleName        string            `json:"rule_name"`
	Severity        alerting.Severity `json:"severity"`
	StartedAt       time.Time         `json:"started_at"`
	HandledAt       *time.Time        `json:"handled_at,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	TargetKind      string            `json:"target_kind"` // "service" | "integration" | "global"
	ServiceName     string            `json:"service_name,omitempty"`
	IntegrationID   *uuid.UUID        `json:"integration_id,omitempty"`
	IntegrationName string            `json:"integration_name,omitempty"`
}

// OpenServiceError is a persisted, unacknowledged error on the feed: a
// service that has produced ≥1 error trace since it was last cleared,
// surfaced regardless of the page's time window so an error can't scroll
// out of view before someone sees + acknowledges it. Acknowledging is the
// existing per-service "clear errors" (it bumps the watermark; new errors
// after that re-open it).
type OpenServiceError struct {
	ServiceName  string    `json:"service_name"`
	ErrorTraces  uint64    `json:"error_traces"`
	FirstErrorAt time.Time `json:"first_error_at"`
	LastErrorAt  time.Time `json:"last_error_at"`
	SampleTrace  string    `json:"sample_trace_id,omitempty"`
}

// openErrorLookback is how far back the errors feed scans for
// unacknowledged errors. Broad (vs the dashboards' window) so an error
// persists until acknowledged; bounded so the scan stays cheap and aligns
// with trace retention.
const openErrorLookback = 30 * 24 * time.Hour

// failingChecks returns the visibility-filtered firing health checks for the
// caller's org: every currently-firing alert instance the caller may see,
// resolved to the service/integration it guards. Shared by the errors feed and
// the unhealthy ("why") feed. Firing state is current — never windowed.
func (h *Handlers) failingChecks(r *http.Request) ([]FailingCheck, error) {
	orgID := middleware.OrgID(r)
	firing, err := h.Alerts.FiringInstances(r.Context(), orgID)
	if err != nil {
		return nil, err
	}
	// Resolve integration names once for the integration-bound checks.
	intNames := map[uuid.UUID]string{}
	if ints, err := h.Integrations.List(r.Context(), orgID); err != nil {
		h.Logger.Warn("failing checks: integration name lookup failed", "err", err)
	} else {
		for _, in := range ints {
			intNames[in.ID] = in.Name
		}
	}
	// Hide checks owned by teams the caller isn't a member of (org admins
	// see all) — same access model as the Alerts page — AND, the hard
	// security boundary, hide service/integration-bound checks whose
	// target the caller can't see (a service-scoped check with no team
	// must not leak the service's name/error to someone without access).
	visible, filter := h.alertVisibleGroups(r)
	checks := make([]FailingCheck, 0, len(firing))
	for _, fi := range firing {
		if !canSeeAlertGroup(fi.GroupID, visible, filter) {
			continue
		}
		if !h.canSeeAlertTarget(r, fi.ServiceName, fi.IntegrationID) {
			continue
		}
		fc := FailingCheck{
			ID:          fi.ID,
			RuleID:      fi.RuleID,
			RuleName:    fi.RuleName,
			Severity:    fi.Severity,
			StartedAt:   fi.StartedAt,
			HandledAt:   fi.HandledAt,
			Summary:     fi.Summary,
			ServiceName: fi.ServiceName,
		}
		switch {
		case fi.ServiceName != "":
			fc.TargetKind = "service"
		case fi.IntegrationID != nil:
			fc.TargetKind = "integration"
			fc.IntegrationID = fi.IntegrationID
			fc.IntegrationName = intNames[*fi.IntegrationID]
		default:
			fc.TargetKind = "global"
		}
		checks = append(checks, fc)
	}
	return checks, nil
}

// errorsFeed: GET /api/v1/errors[?range=1h]
func (h *Handlers) errorsFeed(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)

	// --- Failing health checks (firing alert instances) ----------------
	checks, err := h.failingChecks(r)
	if err != nil {
		h.Logger.Error("errors feed: firing instances failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// --- Affected services ---------------------------------------------
	// Reuse the exact summary build used by the services list (visibility
	// + ack-respecting status), then keep only the ones that are actually
	// in trouble.
	summaries, err := h.serviceSummaries(r, tr)
	if err != nil {
		h.Logger.Error("errors feed: service summaries failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	services := make([]ServiceSummary, 0, len(summaries))
	var nUnhealthy, nErrors int
	for _, s := range summaries {
		switch s.Status {
		case "unhealthy":
			nUnhealthy++
		case "errors":
			nErrors++
		default:
			continue
		}
		services = append(services, s)
	}
	// Worst first (unhealthy above errors), then most-recently-active.
	sort.SliceStable(services, func(i, j int) bool {
		ri, rj := statusRank(services[i].Status), statusRank(services[j].Status)
		if ri != rj {
			return ri > rj
		}
		return services[i].LastSeen.After(services[j].LastSeen)
	})

	// --- Unacknowledged errors (window-independent) --------------------
	// Scan a broad retention window for error traces per service and keep
	// the ones produced AFTER the service's clear-errors watermark — these
	// persist until acknowledged, regardless of the page's time selector.
	// Only services the caller can see (the summaries above are already
	// visibility-filtered) are considered.
	openErrors := h.openServiceErrors(r, summaries)

	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"window":         tr.Window(),
		"failing_checks": checks,
		"services":       services,
		"open_errors":    openErrors,
		"counts": map[string]int{
			"failing_checks":     len(checks),
			"services_unhealthy": nUnhealthy,
			"services_errors":    nErrors,
			"open_errors":        len(openErrors),
		},
	})
}

// openServiceErrors builds the persisted, unacknowledged-error list: for
// every visible service, scan the broad retention window for error traces
// and keep those whose latest error post-dates the service's clear-errors
// watermark. The displayed count is refined to "errors since the
// watermark" when the service was cleared inside the lookback, so it reads
// as the number of NEW (unacknowledged) errors.
func (h *Handlers) openServiceErrors(r *http.Request, visible []ServiceSummary) []OpenServiceError {
	to := time.Now().UTC()
	from := to.Add(-openErrorLookback)
	stats, err := h.Store.ErrorTraceStatsByService(r.Context(), from, to)
	if err != nil {
		h.Logger.Warn("errors feed: error-stats-by-service failed; skipping open errors", "err", err)
		return []OpenServiceError{}
	}
	statByService := make(map[string]store.ServiceErrorStat, len(stats))
	for _, s := range stats {
		statByService[s.ServiceName] = s
	}
	acks := h.errorAcks(r.Context(), middleware.OrgID(r))

	out := make([]OpenServiceError, 0)
	for _, svc := range visible {
		st, ok := statByService[svc.ServiceName]
		if !ok || st.ErrorTraces == 0 {
			continue
		}
		watermark := acks[svc.ServiceName].AcknowledgedUntil
		// Unacknowledged iff the most recent error is newer than the
		// watermark (or the service was never cleared).
		if !watermark.IsZero() && !st.LastErrorAt.After(watermark) {
			continue
		}
		count := st.ErrorTraces
		// If cleared inside the lookback, narrow the count to errors after
		// the watermark so it reflects only the new ones.
		if watermark.After(from) {
			if n, cErr := h.Store.ErrorTraceCountSince(r.Context(), svc.ServiceName, watermark, to, nil); cErr == nil {
				count = n
			}
		}
		if count == 0 {
			continue
		}
		out = append(out, OpenServiceError{
			ServiceName:  svc.ServiceName,
			ErrorTraces:  count,
			FirstErrorAt: st.FirstErrorAt,
			LastErrorAt:  st.LastErrorAt,
			SampleTrace:  st.SampleTraceID,
		})
	}
	// Most recent error first.
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastErrorAt.After(out[j].LastErrorAt) })
	return out
}

// statusRank orders service statuses worst-first for the Errors feed.
func statusRank(status string) int {
	switch status {
	case "unhealthy":
		return 2
	case "errors":
		return 1
	default:
		return 0
	}
}
