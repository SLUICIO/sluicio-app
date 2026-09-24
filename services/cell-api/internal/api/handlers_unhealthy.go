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
	"github.com/sluicio/sluicio-app/services/cell-api/internal/catalog"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// The Unhealthy ("why") feed: the entities that are wrong, each with the reason.
// Where the Errors feed lists failing checks + services flat, this one groups
// them BY the integration or system they belong to and rolls up a status, so a
// caller (esp. the MCP client) gets "INT002 is unhealthy BECAUSE these checks
// are firing" in one call. It reuses the exact same inputs as the Errors feed
// — visibility-filtered firing checks, ack-respecting service statuses, open
// errors — plus system membership (services.system_id) for the grouping.
//
// Status semantics match the rest of the app: "unhealthy" = a health check is
// firing on a member (or on the integration itself); "errors" = a member has
// unacknowledged error traces but nothing firing. Firing checks are current
// state; the window scopes only the error/traffic portion. Every listed entity
// carries its "why" — that's the whole point.
//
// An integration that selects a SLICE of a shared service (an Airflow DAG in
// the scheduler, a Node-RED flow in its runtime) is filed the way
// integrationRollupStatus reads it: its member's errors are not its errors
// unless they are in its slice, and its member's checks are not its checks
// unless they describe the whole process. Filed service-wide, one failing
// DAG listed every DAG on the scheduler as broken.

// UnhealthyCheck is one firing health check attributed to an entity — the
// "why" behind an unhealthy status.
type UnhealthyCheck struct {
	RuleName  string            `json:"rule_name"`
	Severity  alerting.Severity `json:"severity"`
	Summary   string            `json:"summary,omitempty"`
	StartedAt time.Time         `json:"started_at"`
	// OnService is the member service the check guards; empty for a check
	// bound directly to the integration (or a global check).
	OnService string `json:"on_service,omitempty"`
}

// UnhealthyErrorService is one member service producing unacknowledged errors
// — the "why" behind an errors status.
type UnhealthyErrorService struct {
	ServiceName string    `json:"service_name"`
	ErrorTraces uint64    `json:"error_traces"`
	LastErrorAt time.Time `json:"last_error_at"`
	SampleTrace string    `json:"sample_trace_id,omitempty"`
}

// UnhealthyEntity is one integration or system that is unhealthy/in-error, with
// the checks + errors that explain why. TypeKey/Slug are populated per kind.
type UnhealthyEntity struct {
	ID            string                  `json:"id"`
	Name          string                  `json:"name"`
	Slug          string                  `json:"slug,omitempty"`     // integrations
	TypeKey       string                  `json:"type_key,omitempty"` // systems
	Status        string                  `json:"status"`             // "unhealthy" | "errors"
	FailingChecks []UnhealthyCheck        `json:"failing_checks"`
	ErrorServices []UnhealthyErrorService `json:"error_services"`
}

// unhealthyResponse is the /api/v1/unhealthy body. `other` holds anything not
// attributable to a listed integration/system (org-wide checks, or a check/
// error on a service not yet assigned to either) so nothing is silently lost.
type unhealthyResponse struct {
	Window       WindowSummary     `json:"window"`
	Integrations []UnhealthyEntity `json:"integrations"`
	Systems      []UnhealthyEntity `json:"systems"`
	Other        struct {
		FailingChecks []UnhealthyCheck        `json:"failing_checks"`
		ErrorServices []UnhealthyErrorService `json:"error_services"`
	} `json:"other"`
	Counts map[string]int `json:"counts"`
}

// entityAccum accumulates the "why" for one entity before it's finalized.
type entityAccum struct {
	id       string
	name     string
	slug     string
	typeKey  string
	checks   []UnhealthyCheck
	errors   []UnhealthyErrorService
	hasCheck bool
}

// buildUnhealthyView groups failing checks + open errors by the integration and
// system they belong to. Pure (no I/O) so it's unit-testable: service→
// integration comes from each summary's Integrations, service→system from each
// system's Members. An entity appears only if it has a check or an error, so
// every entry is explained.
func buildUnhealthyView(
	window WindowSummary,
	checks []FailingCheck,
	summaries []ServiceSummary,
	openErrors []OpenServiceError,
	systems []catalog.System,
	slices map[string]*UnhealthyErrorService,
) unhealthyResponse {
	svcInts := make(map[string][]IntegrationRef, len(summaries))
	refByID := map[string]IntegrationRef{}
	for _, s := range summaries {
		if len(s.Integrations) > 0 {
			svcInts[s.ServiceName] = s.Integrations
		}
		for _, ref := range s.Integrations {
			refByID[ref.ID] = ref
		}
	}
	isSlice := func(id string) bool { _, ok := slices[id]; return ok }
	type sysRef struct{ id, name, typeKey string }
	svcSystems := map[string][]sysRef{}
	for _, sy := range systems {
		ref := sysRef{sy.ID.String(), sy.Name, sy.TypeKey}
		for _, m := range sy.Members {
			svcSystems[m] = append(svcSystems[m], ref)
		}
	}

	integ := map[string]*entityAccum{}
	sys := map[string]*entityAccum{}
	var otherChecks []UnhealthyCheck
	var otherErrors []UnhealthyErrorService

	getInt := func(ref IntegrationRef) *entityAccum {
		a := integ[ref.ID]
		if a == nil {
			a = &entityAccum{id: ref.ID, name: ref.Name, slug: ref.Slug}
			integ[ref.ID] = a
		}
		// Backfill display fields a check-created accum couldn't know.
		if a.name == "" {
			a.name = ref.Name
		}
		if a.slug == "" {
			a.slug = ref.Slug
		}
		return a
	}
	getSys := func(ref sysRef) *entityAccum {
		a := sys[ref.id]
		if a == nil {
			a = &entityAccum{id: ref.id, name: ref.name, typeKey: ref.typeKey}
			sys[ref.id] = a
		}
		if a.name == "" {
			a.name = ref.name
		}
		if a.typeKey == "" {
			a.typeKey = ref.typeKey
		}
		return a
	}

	// Attribute failing checks (the "why" for unhealthy).
	for _, c := range checks {
		uc := UnhealthyCheck{RuleName: c.RuleName, Severity: c.Severity, Summary: c.Summary, StartedAt: c.StartedAt, OnService: c.ServiceName}
		switch c.TargetKind {
		case "integration":
			if c.IntegrationID != nil {
				id := c.IntegrationID.String()
				a := integ[id]
				if a == nil {
					a = &entityAccum{id: id, name: c.IntegrationName}
					integ[id] = a
				}
				a.checks = append(a.checks, uc)
				a.hasCheck = true
			}
		case "service":
			filed := false
			for _, ref := range svcInts[c.ServiceName] {
				// A span-level check on a shared service fired for one
				// slice, and which one it cannot say.
				if isSlice(ref.ID) && !c.wholeService {
					continue
				}
				a := getInt(ref)
				a.checks = append(a.checks, uc)
				a.hasCheck = true
				filed = true
			}
			for _, ref := range svcSystems[c.ServiceName] {
				a := getSys(ref)
				a.checks = append(a.checks, uc)
				a.hasCheck = true
				filed = true
			}
			if !filed {
				otherChecks = append(otherChecks, uc)
			}
		default: // global
			otherChecks = append(otherChecks, uc)
		}
	}

	// Attribute open errors (the "why" for errors).
	for _, oe := range openErrors {
		ue := UnhealthyErrorService{ServiceName: oe.ServiceName, ErrorTraces: oe.ErrorTraces, LastErrorAt: oe.LastErrorAt, SampleTrace: oe.SampleTrace}
		filed := false
		for _, ref := range svcInts[oe.ServiceName] {
			if isSlice(ref.ID) {
				// Filed below from the slice's own errors, or not at all.
				filed = filed || slices[ref.ID] != nil
				continue
			}
			a := getInt(ref)
			a.errors = append(a.errors, ue)
			filed = true
		}
		for _, ref := range svcSystems[oe.ServiceName] {
			a := getSys(ref)
			a.errors = append(a.errors, ue)
			filed = true
		}
		// An error on a shared service in a flow no integration selects
		// belongs to nobody listed, so it is shown here rather than lost.
		if !filed {
			otherErrors = append(otherErrors, ue)
		}
	}
	for id, ue := range slices {
		ref, ok := refByID[id]
		if ue == nil || !ok {
			continue
		}
		a := getInt(ref)
		a.errors = append(a.errors, *ue)
	}

	resp := unhealthyResponse{Window: window}
	resp.Integrations = finalizeEntities(integ)
	resp.Systems = finalizeEntities(sys)
	sortChecks(otherChecks)
	sortErrorServices(otherErrors)
	resp.Other.FailingChecks = nonNilChecks(otherChecks)
	resp.Other.ErrorServices = nonNilErrors(otherErrors)
	resp.Counts = map[string]int{
		"integrations_unhealthy": countStatus(resp.Integrations, "unhealthy"),
		"integrations_errors":    countStatus(resp.Integrations, "errors"),
		"systems_unhealthy":      countStatus(resp.Systems, "unhealthy"),
		"systems_errors":         countStatus(resp.Systems, "errors"),
	}
	return resp
}

// finalizeEntities turns accumulators into sorted, status-stamped entities:
// "unhealthy" when a check is firing, else "errors". Worst-first, then by name.
func finalizeEntities(m map[string]*entityAccum) []UnhealthyEntity {
	out := make([]UnhealthyEntity, 0, len(m))
	for _, a := range m {
		status := "errors"
		if a.hasCheck {
			status = "unhealthy"
		}
		sortChecks(a.checks)
		sortErrorServices(a.errors)
		out = append(out, UnhealthyEntity{
			ID:            a.id,
			Name:          a.name,
			Slug:          a.slug,
			TypeKey:       a.typeKey,
			Status:        status,
			FailingChecks: nonNilChecks(a.checks),
			ErrorServices: nonNilErrors(a.errors),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := statusRank(out[i].Status), statusRank(out[j].Status)
		if ri != rj {
			return ri > rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortChecks(cs []UnhealthyCheck) {
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].StartedAt.After(cs[j].StartedAt) })
}

func sortErrorServices(es []UnhealthyErrorService) {
	sort.SliceStable(es, func(i, j int) bool { return es[i].LastErrorAt.After(es[j].LastErrorAt) })
}

func nonNilChecks(cs []UnhealthyCheck) []UnhealthyCheck {
	if cs == nil {
		return []UnhealthyCheck{}
	}
	return cs
}

func nonNilErrors(es []UnhealthyErrorService) []UnhealthyErrorService {
	if es == nil {
		return []UnhealthyErrorService{}
	}
	return es
}

func countStatus(es []UnhealthyEntity, status string) int {
	n := 0
	for _, e := range es {
		if e.Status == status {
			n++
		}
	}
	return n
}

// unhealthyFeed: GET /api/v1/unhealthy[?range=24h] — integrations and systems
// that are unhealthy or in error, each with the failing checks + error services
// that explain why.
func (h *Handlers) unhealthyFeed(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, 24*time.Hour)

	checks, err := h.failingChecks(r)
	if err != nil {
		h.Logger.Error("unhealthy feed: firing instances failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	summaries, err := h.serviceSummaries(r, tr)
	if err != nil {
		h.Logger.Error("unhealthy feed: service summaries failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	openErrors := h.openServiceErrors(r, summaries)
	systems, err := h.Catalog.ListSystems(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("unhealthy feed: list systems failed; systems omitted", "err", err)
		systems = nil
	}

	httpserver.WriteJSON(w, http.StatusOK, buildUnhealthyView(tr.Window(), checks, summaries, openErrors, systems, h.sliceOpenErrors(r, tr, summaries)))
}

// sliceOpenErrors reads the open errors of every slice integration the
// caller can reach through a visible service, in one batched query.
// Keyed by integration id; a nil value is a slice with nothing open.
// Members the caller cannot see are left out of the count, as they are
// everywhere on this feed.
//
// Over the feed's window, not openServiceErrors' month-long lookback.
// That is the window integrationRollupStatus reads a slice over, so the
// feed and the list agree about the same slice; and a slice with
// include_descendants walks every trace holding an anchor, which over a
// month is every run of every DAG.
//
// Any failure returns an empty map, which files every integration the
// way it was filed before slices were told apart: wider, never silent.
func (h *Handlers) sliceOpenErrors(r *http.Request, tr TimeRange, visible []ServiceSummary) map[string]*UnhealthyErrorService {
	ctx, orgID := r.Context(), middleware.OrgID(r)
	reachable := map[string]bool{}
	visibleSvc := make(map[string]bool, len(visible))
	for _, s := range visible {
		visibleSvc[s.ServiceName] = true
		for _, ref := range s.Integrations {
			reachable[ref.ID] = true
		}
	}
	if len(reachable) == 0 {
		return map[string]*UnhealthyErrorService{}
	}
	all, err := h.Integrations.AllMatchersWithIntegration(ctx, orgID)
	if err != nil {
		h.Logger.Warn("unhealthy feed: matchers failed; slices filed service-wide", "err", err)
		return map[string]*UnhealthyErrorService{}
	}
	members, err := h.Catalog.IntegrationServicesBulk(ctx, orgID)
	if err != nil {
		h.Logger.Warn("unhealthy feed: members failed; slices filed service-wide", "err", err)
		return map[string]*UnhealthyErrorService{}
	}
	matchers := map[uuid.UUID][]integrations.Matcher{}
	modes := map[uuid.UUID]integrations.RuleMatch{}
	for _, mi := range all {
		matchers[mi.Integration.ID] = append(matchers[mi.Integration.ID], mi.Matcher)
		modes[mi.Integration.ID] = mi.Integration.RuleMatch
	}
	slices := make([]store.IntegrationSlice, 0)
	for id, ms := range matchers {
		if !reachable[id.String()] || !integrations.SelectsSlice(ms, modes[id]) {
			continue
		}
		names := make([]string, 0)
		for _, n := range members[id] {
			if visibleSvc[n] {
				names = append(names, n)
			}
		}
		slices = append(slices, integrationSlice(id, names, ms, modes[id]))
	}
	stats := h.sliceStats(ctx, slices, tr.From, tr.To, h.errorAcks(ctx, orgID), true)
	if stats == nil {
		return map[string]*UnhealthyErrorService{}
	}
	out := make(map[string]*UnhealthyErrorService, len(slices))
	for _, sl := range slices {
		st := stats[sl.Key]
		if st.OpenErrorTraces == 0 {
			out[sl.Key] = nil
			continue
		}
		out[sl.Key] = &UnhealthyErrorService{
			ServiceName: st.LastErrorService,
			ErrorTraces: st.OpenErrorTraces,
			LastErrorAt: st.LastErrorAt,
			SampleTrace: st.SampleTraceID,
		}
	}
	return out
}
