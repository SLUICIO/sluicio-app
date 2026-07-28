// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The cell brief (issue #8, WS4): one call that answers "what am I
// looking at, and is anything wrong right now?"
//
// Without it an agent orients by making five or six calls — list
// integrations, list services, list systems, errors, alert instances —
// and spends most of its first turn on plumbing. Worse, it has to guess
// which of those matter. The brief is the opinionated answer: shape
// first, then what is currently broken, then the gaps worth knowing
// about.
//
// Two rules govern what goes in.
//
// It is COMPACT on purpose. This is an orientation call, not a data
// dump; anything that would grow without bound is a count plus a
// pointer to the tool that lists it. An agent that pastes the brief into
// its context every turn should not be paying for the whole estate.
//
// It is built from the SAME helpers the ordinary endpoints use, so
// visibility cannot drift. A scoped token gets a brief of its own slice
// of the world, and nothing here can accidentally become the one place
// that reports across a boundary the rest of the API enforces.

package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
)

// CellBrief is the orientation payload.
type CellBrief struct {
	// Org identity, so an agent can name the environment it is acting on
	// before it does anything — "production" in a sentence prevents a
	// class of mistake no permission check will catch.
	Company     string `json:"company,omitempty"`
	Environment string `json:"environment,omitempty"`
	GeneratedAt string `json:"generated_at"`
	Window      string `json:"window"`

	Counts CellBriefCounts `json:"counts"`
	// Incidents is what is firing NOW, worst first, each with the rule's
	// runbook when it has one. This is the part an agent acts on, so it
	// carries enough to start work without a follow-up call.
	Incidents []CellBriefIncident `json:"incidents"`
	// Unmonitored names services with traffic that no alert rule watches
	// — the gap most worth reporting, and the one nobody notices because
	// silence looks like health. Capped; Truncated says when.
	Unmonitored          []string `json:"unmonitored_services"`
	UnmonitoredTruncated bool     `json:"unmonitored_truncated,omitempty"`
	// PendingProposals lets an agent see its own queue without a second
	// call, and reminds it not to re-file what is already waiting.
	PendingProposals int `json:"pending_proposals"`
	// Hint tells the agent where to go next. Cheaper than making it
	// rediscover the catalogue every session.
	Hint string `json:"hint"`
}

type CellBriefCounts struct {
	Integrations int `json:"integrations"`
	Systems      int `json:"systems"`
	Services     int `json:"services"`
	// Services by health, so "is anything wrong" is answerable from the
	// counts alone.
	Unhealthy  int `json:"unhealthy_services"`
	Erroring   int `json:"erroring_services"`
	Quiet      int `json:"quiet_services"`
	AlertRules int `json:"alert_rules"`
}

type CellBriefIncident struct {
	RuleName  string `json:"rule_name"`
	Severity  string `json:"severity"`
	Target    string `json:"target,omitempty"`
	Since     string `json:"since"`
	Summary   string `json:"summary,omitempty"`
	Runbook   string `json:"runbook,omitempty"`
	SampleURL string `json:"link,omitempty"`
}

const (
	briefMaxIncidents   = 15
	briefMaxUnmonitored = 20
)

// cellBrief: GET /api/v1/cell-brief?window=24h  (any authed)
func (h *Handlers) cellBrief(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, 24*time.Hour)
	orgID := middleware.OrgID(r)

	brief := CellBrief{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		// The window the counts cover, echoed back so an agent does not
		// have to remember what it asked for.
		Window:      humanWindow(tr.To.Sub(tr.From)),
		Incidents:   []CellBriefIncident{},
		Unmonitored: []string{},
		Hint: "Use sluicio_health for why an entity is unhealthy, sluicio_search_traces / sluicio_search_logs to investigate, " +
			"and sluicio_propose_check_tuning to suggest a change (a human approves it).",
	}

	// Same accessor the notifier uses, so the brief names the environment
	// exactly as an alert email would — an agent and a human reading the
	// same incident should not see two different labels for the cell.
	brief.Environment, brief.Company = alerting.DeploymentContext(r.Context())

	// Services: counts + health rollup, through the visibility-filtered
	// helper the services list itself uses.
	summaries, err := h.serviceSummaries(r, tr)
	if err != nil {
		h.Logger.Warn("cell brief: service summaries failed", "err", err)
	}
	watched := map[string]bool{}
	brief.Counts.Services = len(summaries)
	for _, s := range summaries {
		switch s.Status {
		case "unhealthy":
			brief.Counts.Unhealthy++
		case "errors":
			brief.Counts.Erroring++
		case "quiet":
			brief.Counts.Quiet++
		}
	}

	if rules, rErr := h.Alerts.ListRules(r.Context(), orgID); rErr == nil {
		brief.Counts.AlertRules = len(rules)
		for _, rule := range rules {
			if rule.ServiceName != "" {
				watched[rule.ServiceName] = true
			}
		}
	} else {
		h.Logger.Warn("cell brief: list rules failed", "err", rErr)
	}

	// A service with traffic and no rule bound to it is monitored only by
	// the built-in error signal. Quiet services are excluded: "no rule on
	// a service that emitted nothing" is noise, not a finding.
	for _, s := range summaries {
		if watched[s.ServiceName] || s.Status == "quiet" || s.TraceCount == 0 {
			continue
		}
		if len(brief.Unmonitored) >= briefMaxUnmonitored {
			brief.UnmonitoredTruncated = true
			break
		}
		brief.Unmonitored = append(brief.Unmonitored, s.ServiceName)
	}
	sort.Strings(brief.Unmonitored)

	if ints, iErr := h.Integrations.List(r.Context(), orgID); iErr == nil {
		brief.Counts.Integrations = len(ints)
	}
	if sys, sErr := h.Catalog.ListSystems(r.Context(), orgID); sErr == nil {
		brief.Counts.Systems = len(sys)
	}

	// What is firing right now, worst first. failingChecks is the same
	// source the Errors feed uses, so the brief cannot disagree with the
	// UI about what is broken.
	if checks, cErr := h.failingChecks(r); cErr == nil {
		sort.SliceStable(checks, func(i, j int) bool {
			return severityRank(string(checks[i].Severity)) > severityRank(string(checks[j].Severity))
		})
		runbooks := map[string]string{}
		for _, c := range checks {
			if len(brief.Incidents) >= briefMaxIncidents {
				break
			}
			target := c.ServiceName
			if target == "" {
				target = c.IntegrationName
			}
			rb, ok := runbooks[c.RuleID.String()]
			if !ok {
				if rule, gErr := h.Alerts.GetRule(r.Context(), orgID, c.RuleID); gErr == nil {
					rb = rule.Runbook
				}
				runbooks[c.RuleID.String()] = rb
			}
			brief.Incidents = append(brief.Incidents, CellBriefIncident{
				RuleName: c.RuleName,
				Severity: string(c.Severity),
				Target:   target,
				Since:    c.StartedAt.UTC().Format(time.RFC3339),
				Summary:  c.Summary,
				Runbook:  rb,
			})
		}
	} else {
		h.Logger.Warn("cell brief: failing checks failed", "err", cErr)
	}

	if h.Proposals != nil {
		if n, pErr := h.Proposals.PendingCount(r.Context(), orgID); pErr == nil {
			brief.PendingProposals = n
		}
	}

	httpserver.WriteJSON(w, http.StatusOK, brief)
}

// humanWindow renders a duration the way the caller wrote it — "24h",
// not "24h0m0s". The brief is read by language models as much as by
// code, and trailing zero units are noise that invites misreading.
func humanWindow(d time.Duration) string {
	// Trim only genuine zero units. Blind suffix-trimming turns "1h30m0s"
	// into "1h3", because the remainder ends in "0m" — the minutes digit
	// is not a zero unit.
	out := d.Round(time.Minute).String()
	out = strings.TrimSuffix(out, "0s")
	if strings.HasSuffix(out, "h0m") {
		out = strings.TrimSuffix(out, "0m")
	}
	return out
}

// severityRank orders critical > warning > info so the incident list
// leads with what matters.
func severityRank(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 0
}
