// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/erroracks"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/integrations"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/store"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/tags"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/tracecompletion"
)

// Request / response shapes

type matcherInput struct {
	Attribute  string                `json:"attribute"`
	Operator   integrations.Operator `json:"operator"`
	Value      string                `json:"value"`
	MatchGroup int                   `json:"match_group"`
}

type createIntegrationRequest struct {
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Matchers    []matcherInput `json:"matchers"`
}

type updateIntegrationRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// listIntegrations: GET /api/v1/integrations?window=1h
//
// The response enriches each integration with an aggregate health
// status derived from the services that currently match its matchers.
// Priority: unhealthy > errors > ok, with "quiet" for integrations
// whose matchers don't match any active service.
func (h *Handlers) listIntegrations(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)
	// Opt-in: per-integration traffic sparkline series. Only the dashboard
	// asks for it (?series=1); the plain Integrations list skips the extra
	// per-row ClickHouse query.
	wantSeries := r.URL.Query().Get("series") == "1"

	rows, err := h.Integrations.List(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list integrations failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// Fetch the matchers, services, and metric snapshots once and
	// reuse them across every integration row.
	all, err := h.Integrations.AllMatchersWithIntegration(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("all matchers failed", "err", err)
	}
	matchersByIntegration := map[uuid.UUID][]integrations.Matcher{}
	for _, mi := range all {
		matchersByIntegration[mi.Integration.ID] = append(matchersByIntegration[mi.Integration.ID], mi.Matcher)
	}

	services, err := h.Store.ListServices(r.Context(), tr.From, tr.To)
	if err != nil {
		h.Logger.Warn("list services for integration list failed", "err", err)
		services = nil
	}

	// Persisted integration → service membership (catalog). This is the source
	// of truth for which services belong to an integration, independent of
	// whether they had traffic in the window — so a "quiet" integration still
	// reports its members. Window traffic/health is layered on top per service.
	membersByIntegration, mErr := h.Catalog.IntegrationServicesBulk(r.Context(), middleware.OrgID(r))
	if mErr != nil {
		h.Logger.Warn("integration services (membership) bulk failed", "err", mErr)
		membersByIntegration = map[uuid.UUID][]string{}
	}

	// Group-policy visibility (G5): a non-admin only sees integrations that
	// contain at least one service their team policies grant. We filter the
	// service set up-front so every per-integration aggregate below is
	// computed over visible services only, and skip integrations that end up
	// with no visible service. A user in zero teams sees an empty list.
	// Admins and wildcard-policy holders are unfiltered (vFiltered=false).
	visibleNames, vFiltered := h.visibleServiceFilter(r)
	var allowed map[string]struct{}
	if vFiltered {
		allowed = make(map[string]struct{}, len(visibleNames))
		for _, n := range visibleNames {
			allowed[n] = struct{}{}
		}
		visible := make([]store.ServiceRow, 0, len(services))
		for _, s := range services {
			if _, ok := allowed[s.ServiceName]; ok {
				visible = append(visible, s)
			}
		}
		services = visible
	}
	// Window traffic by service name, for layering counts/health onto the
	// persisted membership below.
	svcByName := make(map[string]store.ServiceRow, len(services))
	for _, s := range services {
		svcByName[s.ServiceName] = s
	}
	errAcks := h.errorAcks(r.Context(), middleware.OrgID(r)) // "clear errors" watermarks, applied per service below
	// An integration's health is derived from its services: a service made
	// unhealthy by a firing health check pulls its integrations down too.
	firingServices, err := h.Alerts.FiringHealthServices(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("firing health services failed", "err", err)
		firingServices = map[string]bool{}
	}
	// Integration-scoped firing health checks (e.g. a log/metric/failed-trace
	// rule bound to the integration itself). These make the integration
	// unhealthy on their own — a member service isn't necessarily involved.
	firingIntegrations, err := h.Alerts.FiringHealthIntegrations(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("firing health integrations failed", "err", err)
		firingIntegrations = map[uuid.UUID]bool{}
	}
	// Services with persisted, unacknowledged trace errors → unhealthy (and
	// thus their integrations), independent of the active window.
	openErrors := h.openErrorServices(r.Context(), middleware.OrgID(r))

	// One round trip to grab every (integration, tag) pair for the
	// org; we fan it out into the per-row Tags field below.
	integIDs := make([]uuid.UUID, 0, len(rows))
	for _, integ := range rows {
		integIDs = append(integIDs, integ.ID)
	}
	tagsByIntegration, err := h.Tags.ListForIntegrations(r.Context(), middleware.OrgID(r), integIDs)
	if err != nil {
		h.Logger.Warn("list tags for integrations failed", "err", err)
		tagsByIntegration = map[uuid.UUID][]tags.Tag{}
	}

	// User-defined metadata: pull the org schema and every integration's
	// saved values in two round-trips, then index by key for the per-row
	// payload below.
	allMetadataFields, mfErr := h.Metadata.ListFields(r.Context(), middleware.OrgID(r))
	if mfErr != nil {
		h.Logger.Warn("list metadata fields failed", "err", mfErr)
	}
	integrationFields := scopedFields(allMetadataFields, true)
	keyByFieldID := make(map[uuid.UUID]string, len(allMetadataFields))
	for _, f := range allMetadataFields {
		keyByFieldID[f.ID] = f.Key
	}
	bulkValues, mvErr := h.Metadata.IntegrationValuesBulk(r.Context(), integIDs)
	if mvErr != nil {
		h.Logger.Warn("bulk integration metadata values failed", "err", mvErr)
		bulkValues = map[uuid.UUID]map[uuid.UUID]string{}
	}

	// Enabled trace-completion rules for the org, grouped by integration.
	// They gate the message scope (a rule with a start span counts only
	// traces beginning with it) and drive the delayed-trace failure count.
	enabledTraceRules, trErr := h.TraceCompletion.EnabledForEval(r.Context(), middleware.OrgID(r))
	if trErr != nil {
		h.Logger.Warn("list enabled trace rules failed", "err", trErr)
	}
	rulesByIntegration := map[uuid.UUID][]tracecompletion.Rule{}
	for _, rule := range enabledTraceRules {
		rulesByIntegration[rule.IntegrationID] = append(rulesByIntegration[rule.IntegrationID], rule)
	}
	// Open (unhandled) delayed trace ids per integration. Intersected with
	// each integration's window traffic below so the per-card "delayed"
	// count is window-consistent (can't exceed messages → success rate
	// never underflows).
	openDelayedIDs, odErr := h.TraceCompletion.OpenDelayedTraceIDsByIntegration(r.Context(), middleware.OrgID(r))
	if odErr != nil {
		h.Logger.Warn("open delayed trace ids failed", "err", odErr)
		openDelayedIDs = map[uuid.UUID][]string{}
	}

	summaries := make([]IntegrationSummary, 0, len(rows))
	for _, integ := range rows {
		matchers := matchersByIntegration[integ.ID]
		statuses := make([]string, 0)
		matchedNames := make([]string, 0)
		unhealthy := 0
		var traces, errors uint64
		// statuses (health rollup) + window traffic come from services that
		// actually emitted in the window. An integration with no window traffic
		// rolls up to "quiet" (statuses stays empty).
		for _, svc := range services {
			if !anyMatcherMatches(matchers, svc.ServiceName) {
				continue
			}
			effErr := h.effectiveErrorCount(r.Context(), svc.ServiceName, svc.ErrorTraceCount, tr.From, tr.To, errAcks)
			st := statusWithOpenErrors(
				computeServiceStatus(effErr, firingServices[svc.ServiceName]),
				openErrors[svc.ServiceName],
			)
			statuses = append(statuses, st)
			if st == "unhealthy" {
				unhealthy++
			}
			traces += svc.TraceCount
			errors += effErr
		}
		// matchedNames = the integration's MEMBER services from the persisted
		// catalog (so quiet members are still listed), unioned with any service
		// matched only this window (freshly discovered, not yet reconciled).
		// Visibility-filtered. Drives ServiceCount/Services + the per-integration
		// traffic queries below (quiet members contribute zero).
		memberSeen := make(map[string]bool)
		addMember := func(name string) {
			if memberSeen[name] {
				return
			}
			if vFiltered {
				if _, ok := allowed[name]; !ok {
					return
				}
			}
			memberSeen[name] = true
			matchedNames = append(matchedNames, name)
		}
		for _, name := range membersByIntegration[integ.ID] {
			addMember(name)
		}
		for _, svc := range services {
			if anyMatcherMatches(matchers, svc.ServiceName) {
				addMember(svc.ServiceName)
			}
		}
		sort.Strings(matchedNames)

		// Visibility skip: a filtered (non-admin) caller with no visible
		// service in this integration doesn't see it at all. Done before the
		// per-integration ClickHouse queries below so we also skip that work.
		if vFiltered && len(matchedNames) == 0 {
			continue
		}

		// When the integration has a start-span gate, the message count
		// is the distinct traces that begin with a start span — not the
		// per-service sum. Delayed traces are a window-scoped failure.
		// Attribute-defined integrations also narrow the count to their
		// matching slice (and switch to a distinct-trace count so the list
		// card matches the detail header).
		integAttrs := h.integrationGroups(r.Context(), integ.ID)
		intRules := rulesByIntegration[integ.ID]
		if startSpans := startSpansOf(intRules); len(startSpans) > 0 {
			gt, ge, gErr := h.Store.DistinctTraceCountsGated(r.Context(), matchedNames, startSpans, tr.From, tr.To, integAttrs)
			if gErr != nil {
				h.Logger.Warn("gated distinct trace counts failed", "integration", integ.ID, "err", gErr)
			} else {
				traces, errors = gt, ge
			}
		} else if len(integAttrs) > 0 {
			t2, e2, cErr := h.Store.DistinctTraceCounts(r.Context(), matchedNames, tr.From, tr.To, integAttrs)
			if cErr != nil {
				h.Logger.Warn("attribute distinct trace counts failed", "integration", integ.ID, "err", cErr)
			} else {
				traces, errors = t2, e2
			}
		}
		// Delayed-in-window: how many of this integration's window traces
		// are currently delayed (intersect sticky open-delayed ids with the
		// window's traffic). Bounded — one CH query only when there are
		// open delays for this integration.
		var delayed uint64
		if dIDs := openDelayedIDs[integ.ID]; len(dIDs) > 0 {
			n, cErr := h.Store.CountDistinctTracesIn(r.Context(), matchedNames, dIDs, tr.From, tr.To, integAttrs)
			if cErr != nil {
				h.Logger.Warn("delayed-in-window count failed", "integration", integ.ID, "err", cErr)
			} else {
				delayed = n
			}
		}
		// Per-integration traffic sparkline (opt-in): distinct traces
		// bucketed across the window over the integration's services.
		var trafficSeries []int
		if wantSeries {
			s, sErr := h.Store.IntegrationTrafficSeries(r.Context(), matchedNames, tr.From, tr.To, 24)
			if sErr != nil {
				h.Logger.Warn("integration traffic series failed", "integration", integ.ID, "err", sErr)
			}
			trafficSeries = s
		}
		integTags := tagsByIntegration[integ.ID]
		if integTags == nil {
			integTags = []tags.Tag{}
		}
		// Translate field-id keyed values to field-key keyed for the
		// frontend, which doesn't carry uuid → key mapping.
		valuesByKey := map[string]string{}
		for fid, v := range bulkValues[integ.ID] {
			if k, ok := keyByFieldID[fid]; ok {
				valuesByKey[k] = v
			}
		}
		// A member with persisted, unacknowledged errors makes the
		// integration unhealthy even if that service had no traffic in the
		// current window (so it isn't in `statuses` above) — the error is
		// open until acknowledged, regardless of the view's time range.
		integHasOpenErr := false
		for svcName := range openErrors {
			if anyMatcherMatches(matchers, svcName) {
				integHasOpenErr = true
				break
			}
		}
		// Likewise, a matched member with a firing service-bound health check
		// makes the integration unhealthy even when that service had no
		// traffic this window (so it never entered `statuses`). Mirrors
		// integHasOpenErr; without it the list could read healthy/quiet while
		// the detail view — which folds each member's status directly — reads
		// unhealthy, and the dashboard pip (driven off the list) wouldn't flip.
		integHasFiringCheck := false
		for svcName := range firingServices {
			if anyMatcherMatches(matchers, svcName) {
				integHasFiringCheck = true
				break
			}
		}
		summaries = append(summaries, IntegrationSummary{
			Integration:       integ,
			Status:            statusWithIntegrationCheck(statusWithOpenErrors(statusWithDelays(aggregateStatus(statuses), delayed), integHasOpenErr), firingIntegrations[integ.ID] || integHasFiringCheck),
			ServiceCount:      len(matchedNames),
			Services:          matchedNames,
			UnhealthyCount:    unhealthy,
			TraceCount:        traces,
			ErrorTraceCount:   errors,
			DelayedTraceCount: delayed,
			TrafficSeries:     trafficSeries,
			Tags:              integTags,
			MetadataValues:    valuesByKey,
		})
	}

	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"integrations":    summaries,
		"window":          tr.Window(),
		"metadata_fields": integrationFields,
	})
}

// integrationFlow: GET /api/v1/integrations/{id}/flow?range=...
//
// Returns the service flow graph for an integration: nodes are the
// services currently matching the integration in the window; edges
// are cross-service span hops (parent→child) observed in their
// traces. Both endpoints of an edge must be within the integration
// — external services are not surfaced on the graph.
func (h *Handlers) integrationFlow(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	tr := ParseRange(r, time.Hour)

	// Existence check: the integration must still be there; we don't
	// otherwise need anything off the integration itself for the flow
	// graph now that membership lives in the catalog.
	if _, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, integrations.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "integration not found")
			return
		}
		h.Logger.Error("get integration for flow failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	allServices, err := h.Store.ListServices(r.Context(), tr.From, tr.To)
	if err != nil {
		h.Logger.Error("list services for flow failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// Nodes come from the persisted membership (catalog), per-window
	// counts from CH. A node with no traffic in the window renders with
	// zero counts but still appears.
	memberNames, mErr := h.Catalog.IntegrationServices(r.Context(), id)
	if mErr != nil {
		h.Logger.Warn("read integration_services for flow failed", "err", mErr)
	}
	// Access + visibility: 404 if the caller can see none of the members,
	// and draw only the member services they're allowed to see (so a
	// restricted caller can't read the graph of an integration outside their
	// policy, nor see member nodes/errors they have no access to).
	visibleMembers, anyVisible := h.filterVisibleMembers(r, memberNames)
	if !anyVisible {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return
	}
	memberNames = visibleMembers
	byName := make(map[string]store.ServiceRow, len(allServices))
	for _, s := range allServices {
		byName[s.ServiceName] = s
	}
	// Node colour reflects health (configured health checks), not raw errors.
	firingServices, fErr := h.Alerts.FiringHealthServices(r.Context(), middleware.OrgID(r))
	if fErr != nil {
		h.Logger.Warn("firing health services for flow failed", "err", fErr)
		firingServices = map[string]bool{}
	}
	// Attribute-defined integration: per-node counts reflect only the
	// integration's matching slice of each member's traffic (empty map for
	// service-only integrations, where the catalog's per-service counts
	// stand).
	flowAttrs := h.integrationGroups(r.Context(), id)
	filteredCounts, fcErr := h.Store.ServiceTraceCountsFiltered(r.Context(), memberNames, tr.From, tr.To, flowAttrs)
	if fcErr != nil {
		h.Logger.Warn("filtered flow node counts failed", "err", fcErr)
		filteredCounts = nil
	}
	nodes := make([]FlowNode, 0, len(memberNames))
	serviceNames := make([]string, 0, len(memberNames))
	for _, name := range memberNames {
		row := byName[name]
		traceCount, errorCount := row.TraceCount, row.ErrorTraceCount
		if len(flowAttrs) > 0 {
			c := filteredCounts[name] // zero-valued if this member has no matching traffic
			traceCount, errorCount = c[0], c[1]
		}
		nodes = append(nodes, FlowNode{
			ServiceName:     name,
			TraceCount:      traceCount,
			ErrorTraceCount: errorCount,
			Status:          computeServiceStatus(0, firingServices[name]),
		})
		serviceNames = append(serviceNames, name)
	}

	// Edges: query the user's window first; if it has no hops, fall
	// back to a wide historical window with zeroed counts so the user
	// still sees the structural connections. Historical is reported so
	// the UI can badge the panel.
	edgesFrom, edgesTo := tr.From, tr.To
	historical := false

	edgeRows, err := h.Store.ServiceEdges(r.Context(), serviceNames, edgesFrom, edgesTo, flowAttrs)
	if err != nil {
		h.Logger.Error("service edges failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if len(edgeRows) == 0 && len(serviceNames) > 1 {
		// No hops in this window — fall back to the structural shape
		// over a wide historical window. Per-edge counts get zeroed
		// below so they reflect the user's (empty) window.
		histFrom := time.Now().UTC().Add(-90 * 24 * time.Hour)
		histTo := time.Now().UTC()
		if histEdges, errH := h.Store.ServiceEdges(r.Context(), serviceNames, histFrom, histTo, flowAttrs); errH == nil && len(histEdges) > 0 {
			edgeRows = histEdges
			edgesFrom, edgesTo = histFrom, histTo
			historical = true
		}
	}
	_ = edgesFrom
	_ = edgesTo
	edges := make([]FlowEdge, 0, len(edgeRows))
	for _, e := range edgeRows {
		// In historical fallback, zero counts on edges too — the hop
		// exists structurally but had no calls / errors in the picked
		// window. The structural edge is still informative.
		callCount := e.TraceCount
		errorCount := e.ErrorCount
		if historical {
			callCount = 0
			errorCount = 0
		}
		edges = append(edges, FlowEdge{
			Source:     e.Source,
			Target:     e.Target,
			CallCount:  callCount,
			ErrorCount: errorCount,
		})
	}

	// Data-shape overlay: the schemas pinned to member services (with
	// in/out direction) and the maps that transform between them. This
	// is structural knowledge from the Schemas + Maps catalogue, not
	// per-trace data — "schema 1 in → map x → schema 2 out". Failures
	// here are non-fatal: the graph still renders without the overlay.
	memberSet := make(map[string]struct{}, len(memberNames))
	for _, n := range memberNames {
		memberSet[n] = struct{}{}
	}
	serviceSchemas := map[string][]FlowSchemaRef{}
	relevantSchemaIDs := map[string]struct{}{}
	if schemaRows, sErr := h.Schemas.List(r.Context(), middleware.OrgID(r)); sErr != nil {
		h.Logger.Warn("list schemas for flow failed", "err", sErr)
	} else {
		for _, s := range schemaRows {
			for _, u := range s.Usage {
				if _, ok := memberSet[u.ServiceName]; !ok {
					continue
				}
				serviceSchemas[u.ServiceName] = append(serviceSchemas[u.ServiceName], FlowSchemaRef{
					SchemaID:  s.ID.String(),
					Name:      s.Name,
					Direction: string(u.Direction),
				})
				relevantSchemaIDs[s.ID.String()] = struct{}{}
			}
		}
	}
	var flowMaps []FlowMap
	if len(relevantSchemaIDs) > 0 {
		if mapRows, mErr := h.Maps.List(r.Context(), middleware.OrgID(r)); mErr != nil {
			h.Logger.Warn("list maps for flow failed", "err", mErr)
		} else {
			for _, m := range mapRows {
				fromID, toID := "", ""
				if m.FromSchemaID != nil {
					fromID = m.FromSchemaID.String()
				}
				if m.ToSchemaID != nil {
					toID = m.ToSchemaID.String()
				}
				_, fromRel := relevantSchemaIDs[fromID]
				_, toRel := relevantSchemaIDs[toID]
				if !fromRel && !toRel {
					continue
				}
				fm := FlowMap{ID: m.ID.String(), Name: m.Name, Format: m.Format}
				if m.FromSchema != nil {
					fm.FromSchema = m.FromSchema.Name
				}
				if m.ToSchema != nil {
					fm.ToSchema = m.ToSchema.Name
				}
				flowMaps = append(flowMaps, fm)
			}
		}
	}
	if len(serviceSchemas) == 0 {
		serviceSchemas = nil // omit the key entirely when empty
	}

	httpserver.WriteJSON(w, http.StatusOK, FlowResponse{
		Window:         tr.Window(),
		Nodes:          nodes,
		Edges:          edges,
		Historical:     historical,
		ServiceSchemas: serviceSchemas,
		Maps:           flowMaps,
	})
}

// anyMatcherMatches reports whether any of the matchers matches the
// service name. Convenience over a double-loop.
func anyMatcherMatches(matchers []integrations.Matcher, serviceName string) bool {
	for _, m := range matchers {
		// Only service matchers decide membership; attribute matchers
		// (producer, consumer, …) refine telemetry at query time.
		if m.IsServiceMatcher() && m.Match(serviceName) {
			return true
		}
	}
	return false
}

// startSpansOf returns the distinct start-span names across an
// integration's enabled trace-completion rules. These gate what counts
// as one of the integration's messages. Empty when no enabled rule has
// a start span (legacy / ungated → fall back to counting every trace).
func startSpansOf(rules []tracecompletion.Rule) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		s := strings.TrimSpace(r.Spec.StartSpanName)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// createIntegration: POST /api/v1/integrations
func (h *Handlers) createIntegration(w http.ResponseWriter, r *http.Request) {
	var req createIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	req.Name = strings.TrimSpace(req.Name)
	if req.Slug == "" || req.Name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "slug and name are required")
		return
	}

	in := integrations.IntegrationWithMatchers{
		Integration: integrations.Integration{
			OrganizationID: middleware.OrgID(r),
			Slug:           req.Slug,
			Name:           req.Name,
			Description:    req.Description,
		},
		Matchers: make([]integrations.Matcher, 0, len(req.Matchers)),
	}
	for _, m := range req.Matchers {
		attr := m.Attribute
		if attr == "" {
			attr = "service.name"
		}
		in.Matchers = append(in.Matchers, integrations.Matcher{
			Attribute:  attr,
			Operator:   m.Operator,
			Value:      m.Value,
			MatchGroup: m.MatchGroup,
		})
	}

	if ok, why := h.matcherContainmentOK(r, in.Matchers); !ok {
		httpserver.WriteError(w, http.StatusForbidden, why)
		return
	}
	created, err := h.Integrations.Create(r.Context(), in)
	if err != nil {
		if integrations.IsValidationError(err) {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if isUniqueViolation(err) {
			httpserver.WriteError(w, http.StatusConflict, "an integration with that slug already exists")
			return
		}
		h.Logger.Error("create integration failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.Resolver.Invalidate()
	h.reconcileCatalog(r.Context())
	h.recordAudit(r, "integration.created", "integration", created.Integration.ID.String(), map[string]any{"name": created.Integration.Name})
	// Match the GET shape so the frontend can use one type for both
	// endpoints. Services and window are omitted on create — the
	// frontend redirects to GET /integrations/{id} which fills them in.
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{
		"integration": created.Integration,
		"matchers":    created.Matchers,
	})
}

// getIntegration: GET /api/v1/integrations/{id}
//
// Returns the integration, its matchers, and the services currently
// matching it (with health stats over the configured window).
func (h *Handlers) getIntegration(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	tr := ParseRange(r, time.Hour)

	full, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		if errors.Is(err, integrations.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "integration not found")
			return
		}
		h.Logger.Error("get integration failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// Find which services currently match the integration's matchers.
	allServices, err := h.Store.ListServices(r.Context(), tr.From, tr.To)
	if err != nil {
		h.Logger.Error("list services for integration failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	firingServices, err := h.Alerts.FiringHealthServices(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("firing health services failed", "err", err)
		firingServices = map[string]bool{}
	}
	firingIntegrations, err := h.Alerts.FiringHealthIntegrations(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("firing health integrations failed", "err", err)
		firingIntegrations = map[uuid.UUID]bool{}
	}
	// Persisted membership: the (integration → services) relationship
	// is materialised in Postgres by the catalog reconciler. We use it
	// instead of running matchers per request so the list is stable
	// across empty windows and queryable from SQL.
	memberNames, err := h.Catalog.IntegrationServices(r.Context(), id)
	if err != nil {
		h.Logger.Warn("read integration_services failed", "err", err, "integration", id)
		memberNames = nil
	}
	// Access + per-service visibility: a caller who can see none of this
	// integration's services has no business viewing it — 404 so a direct
	// URL can't enumerate or leak it. Otherwise restrict every downstream
	// count/summary to the members they're allowed to see.
	visibleMembers, anyVisible := h.filterVisibleMembers(r, memberNames)
	if !anyVisible {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return
	}
	memberNames = visibleMembers
	openErrors := h.openErrorServices(r.Context(), middleware.OrgID(r))
	matched := h.servicesFromMembers(r.Context(), memberNames, allServices, firingServices, h.errorAcks(r.Context(), middleware.OrgID(r)), openErrors, tr)

	// Trace-completion rules gate the message scope and drive the
	// delayed-trace failure count. With a start-span gate, a "message"
	// is a distinct trace that begins with one of the start spans;
	// without any gate we fall back to counting every distinct trace
	// across the matched services (the historical behaviour).
	traceRules, trErr := h.TraceCompletion.ListForIntegration(r.Context(), middleware.OrgID(r), id)
	if trErr != nil {
		h.Logger.Warn("list completion rules for integration failed", "err", trErr)
	}
	// Attribute-defined integrations narrow these aggregate counts to the
	// matching slice of their member services' traffic.
	integAttrs := h.integrationGroups(r.Context(), id)
	var messageCount, errorMessageCount uint64
	var cErr error
	if startSpans := startSpansOf(traceRules); len(startSpans) > 0 {
		messageCount, errorMessageCount, cErr = h.Store.DistinctTraceCountsGated(r.Context(), memberNames, startSpans, tr.From, tr.To, integAttrs)
	} else {
		messageCount, errorMessageCount, cErr = h.Store.DistinctTraceCounts(r.Context(), memberNames, tr.From, tr.To, integAttrs)
	}
	if cErr != nil {
		h.Logger.Warn("distinct trace counts failed", "err", cErr)
	}
	// Delayed traces, window-consistent: of the messages in THIS window,
	// how many are currently delayed (an open, unhandled firing). We
	// intersect the sticky open-delayed trace ids with the window's
	// traffic so "delayed" can never exceed "messages" (which would make
	// the success rate underflow to 0%). Handled firings are excluded.
	var delayedMessageCount uint64
	if delayedIDs, derr := h.TraceCompletion.OpenDelayedTraceIDs(r.Context(), middleware.OrgID(r), id); derr != nil {
		h.Logger.Warn("open delayed trace ids failed", "err", derr)
	} else if len(delayedIDs) > 0 {
		n, cerr := h.Store.CountDistinctTracesIn(r.Context(), memberNames, delayedIDs, tr.From, tr.To, integAttrs)
		if cerr != nil {
			h.Logger.Warn("delayed-in-window count failed", "err", cerr)
		} else {
			delayedMessageCount = n
		}
	}

	// The integration's aggregate health is the worst of its services'
	// (which already reflect any firing service health checks), with any
	// open SLA delay pulling it down to at least "errors".
	statuses := make([]string, 0, len(matched))
	for _, s := range matched {
		statuses = append(statuses, s.Status)
	}
	status := statusWithIntegrationCheck(
		statusWithDelays(aggregateStatus(statuses), uint64(delayedMessageCount)),
		firingIntegrations[id],
	)

	integTags, err := h.Tags.ListForIntegration(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		h.Logger.Warn("list integration tags failed", "err", err)
		integTags = []tags.Tag{}
	}

	// User-defined metadata: applicable field defs + the integration's
	// saved values. Both are returned so the detail page can render
	// without a second round-trip.
	allMetadataFields, mfErr := h.Metadata.ListFields(r.Context(), middleware.OrgID(r))
	if mfErr != nil {
		h.Logger.Warn("list metadata fields failed", "err", mfErr)
	}
	integrationMetadataFields := scopedFields(allMetadataFields, true)
	integrationMetadataValues, mvErr := h.integrationMetadataValues(r.Context(), id)
	if mvErr != nil {
		h.Logger.Warn("integration metadata values failed", "err", mvErr)
		integrationMetadataValues = map[string]string{}
	}

	// Scoped problem counts so the Errors-tab badge reflects every failure
	// mode the tab surfaces — not just failed traces. Failing health checks
	// (firing alert instances) and unacknowledged errors are matched to this
	// integration's members (or the integration itself), mirroring the Errors
	// feed's scoping. Members are already visibility-filtered above.
	failingCheckCount, openErrorCount := 0, 0
	memberSet := make(map[string]bool, len(memberNames))
	for _, m := range memberNames {
		memberSet[m] = true
		if openErrors[m] {
			openErrorCount++
		}
	}
	if firing, ferr := h.Alerts.FiringInstances(r.Context(), middleware.OrgID(r)); ferr != nil {
		h.Logger.Warn("integration detail: firing instances failed", "err", ferr)
	} else {
		for _, fi := range firing {
			if fi.IntegrationID != nil && *fi.IntegrationID == id {
				failingCheckCount++
			} else if fi.ServiceName != "" && memberSet[fi.ServiceName] {
				failingCheckCount++
			}
		}
	}

	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"integration":           full.Integration,
		"can_manage":            h.canManageIntegration(r, id),
		"matchers":              full.Matchers,
		"services":              matched,
		"status":                status,
		"tags":                  integTags,
		"window":                tr.Window(),
		"message_count":         messageCount,
		"error_message_count":   errorMessageCount,
		"delayed_message_count": delayedMessageCount,
		"failing_check_count":   failingCheckCount,
		"open_error_count":      openErrorCount,
		"metadata_fields":       integrationMetadataFields,
		"metadata_values":       integrationMetadataValues,
	})
}

// updateIntegration: PUT /api/v1/integrations/{id}
func (h *Handlers) updateIntegration(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	var req updateIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	updated, err := h.Integrations.Update(r.Context(), middleware.OrgID(r), id, req.Name, req.Description)
	if err != nil {
		if errors.Is(err, integrations.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "integration not found")
			return
		}
		h.Logger.Error("update integration failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.Resolver.Invalidate()
	h.reconcileCatalog(r.Context())
	h.recordAudit(r, "integration.updated", "integration", id.String(), map[string]any{"name": updated.Name})
	httpserver.WriteJSON(w, http.StatusOK, updated)
}

// deleteIntegration: DELETE /api/v1/integrations/{id}
func (h *Handlers) deleteIntegration(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	if err := h.Integrations.Delete(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, integrations.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "integration not found")
			return
		}
		h.Logger.Error("delete integration failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.Resolver.Invalidate()
	h.reconcileCatalog(r.Context())
	// Shares point at this integration without an FK — clear them.
	if err := h.Identity.DeleteSharesForResource(r.Context(), middleware.OrgID(r), identity.ShareIntegration, id); err != nil {
		h.Logger.Warn("delete integration: clear shares failed", "err", err)
	}
	h.recordAudit(r, "integration.deleted", "integration", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// addMatcher: POST /api/v1/integrations/{id}/matchers
func (h *Handlers) addMatcher(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	var in matcherInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	attr := in.Attribute
	if attr == "" {
		attr = "service.name"
	}
	prospective := integrations.Matcher{
		Attribute:  attr,
		Operator:   in.Operator,
		Value:      in.Value,
		MatchGroup: in.MatchGroup,
	}
	if ok, why := h.matcherContainmentOK(r, []integrations.Matcher{prospective}); !ok {
		httpserver.WriteError(w, http.StatusForbidden, why)
		return
	}
	created, err := h.Integrations.AddMatcher(r.Context(), id, integrations.Matcher{
		Attribute:  attr,
		Operator:   in.Operator,
		Value:      in.Value,
		MatchGroup: in.MatchGroup,
	})
	if err != nil {
		if integrations.IsValidationError(err) {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("add matcher failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.Resolver.Invalidate()
	h.reconcileCatalog(r.Context())
	h.recordAudit(r, "integration_matcher.added", "integration", id.String(), map[string]any{"operator": string(in.Operator), "value": in.Value})
	httpserver.WriteJSON(w, http.StatusCreated, created)
}

// removeMatcher: DELETE /api/v1/integrations/{id}/matchers/{matcherId}
func (h *Handlers) removeMatcher(w http.ResponseWriter, r *http.Request) {
	matcherID, err := uuid.Parse(r.PathValue("matcherId"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid matcher id")
		return
	}
	if err := h.Integrations.RemoveMatcher(r.Context(), matcherID); err != nil {
		if errors.Is(err, integrations.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "matcher not found")
			return
		}
		h.Logger.Error("remove matcher failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.Resolver.Invalidate()
	h.reconcileCatalog(r.Context())
	h.recordAudit(r, "integration_matcher.removed", "integration", r.PathValue("id"), map[string]any{"matcher_id": matcherID.String()})
	w.WriteHeader(http.StatusNoContent)
}

// removeServiceFromIntegration: DELETE /api/v1/integrations/{id}/services/{name}
//
// Convenience inverse of "add service to integration": removes the exact
// service.name=equals matcher(s) for the service. Returns {removed: n};
// removed=0 means the service is matched by a broader rule, which we don't
// touch here (the user must edit the integration's matchers directly).
func (h *Handlers) removeServiceFromIntegration(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "missing service name")
		return
	}
	// Org-scope check: the integration must belong to the caller's org.
	if _, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return
	}
	removed, err := h.Integrations.RemoveServiceMatchers(r.Context(), id, name)
	if err != nil {
		h.Logger.Error("remove service from integration failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if removed > 0 {
		h.Resolver.Invalidate()
		h.reconcileCatalog(r.Context())
		h.recordAudit(r, "integration_service.removed", "integration", id.String(), map[string]any{"service": name})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

// integrationSpanNames: GET /api/v1/integrations/{id}/span-names?range=24h
//
// Distinct span names observed across the integration's matched services in
// the window, most-frequent first. Powers the start/stage span pickers in
// the trace-completion rule editor; an empty list (new/quiet integration)
// just means the UI falls back to free text.
func (h *Handlers) integrationSpanNames(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	if _, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return
	}
	if _, ok := h.gateIntegrationMembers(w, r, id); !ok {
		return
	}
	// Default to a wide window so even low-traffic integrations surface
	// their span vocabulary.
	tr := ParseRange(r, 24*time.Hour)
	windowRows, err := h.Store.ListServices(r.Context(), tr.From, tr.To)
	if err != nil {
		h.Logger.Warn("span-names: list services failed", "err", err)
	}
	candidates := make([]string, 0, len(windowRows))
	for _, wrow := range windowRows {
		candidates = append(candidates, wrow.ServiceName)
	}
	matched, err := h.Resolver.ServicesForIntegration(r.Context(), id, candidates)
	if err != nil {
		h.Logger.Warn("span-names: resolve services failed", "err", err)
	}
	names, err := h.Store.DistinctSpanNames(r.Context(), matched, tr.From, tr.To, 200, h.integrationGroups(r.Context(), id))
	if err != nil {
		h.Logger.Error("span-names: distinct span names failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"span_names": names})
}

// integrationAttributeKeys: GET /api/v1/integrations/{id}/attribute-keys?range=24h
//
// Distinct payload attribute keys (span + resource) observed on the
// integration's matched traffic in the window, most-used first. Powers the
// payload-field typeahead on the integration's Messages tab so the user
// picks from attributes that actually flow through *this* integration
// instead of typing blind. An empty list (new/quiet integration, or an
// attribute-only integration that matches no services yet) just means the
// picker falls back to free text.
func (h *Handlers) integrationAttributeKeys(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	if _, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return
	}
	if _, ok := h.gateIntegrationMembers(w, r, id); !ok {
		return
	}
	// Default to a wide window so even low-traffic integrations surface
	// their attribute vocabulary.
	tr := ParseRange(r, 24*time.Hour)
	windowRows, err := h.Store.ListServices(r.Context(), tr.From, tr.To)
	if err != nil {
		h.Logger.Warn("attribute-keys: list services failed", "err", err)
	}
	candidates := make([]string, 0, len(windowRows))
	for _, wrow := range windowRows {
		candidates = append(candidates, wrow.ServiceName)
	}
	matched, err := h.Resolver.ServicesForIntegration(r.Context(), id, candidates)
	if err != nil {
		h.Logger.Warn("attribute-keys: resolve services failed", "err", err)
	}
	keys, err := h.Store.DistinctAttributeKeysScoped(r.Context(), matched, tr.From, tr.To, 2000, h.integrationGroups(r.Context(), id))
	if err != nil {
		h.Logger.Error("attribute-keys: distinct attribute keys failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]AttributeKeyInfo, 0, len(keys))
	for _, k := range keys {
		out = append(out, AttributeKeyInfo{Key: k.Key, Source: k.Source, UseCount: k.UseCount})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"attribute_keys": out})
}

// integrationAttributeValues: GET /api/v1/integrations/{id}/attribute-values?key=order.id&range=24h
//
// Top-N values (max 50) for one payload attribute key within the
// integration's matched traffic, ranked by span count. Backs the value
// typeahead on the integration's Messages tab. High-cardinality keys are
// bounded by the LIMIT; the UI keeps a free-text fallback for an exact value
// outside the top-N.
func (h *Handlers) integrationAttributeValues(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if !attrKeyRe.MatchString(key) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid attribute key")
		return
	}
	if _, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return
	}
	if _, ok := h.gateIntegrationMembers(w, r, id); !ok {
		return
	}
	tr := ParseRange(r, 24*time.Hour)
	windowRows, err := h.Store.ListServices(r.Context(), tr.From, tr.To)
	if err != nil {
		h.Logger.Warn("attribute-values: list services failed", "err", err)
	}
	candidates := make([]string, 0, len(windowRows))
	for _, wrow := range windowRows {
		candidates = append(candidates, wrow.ServiceName)
	}
	matched, err := h.Resolver.ServicesForIntegration(r.Context(), id, candidates)
	if err != nil {
		h.Logger.Warn("attribute-values: resolve services failed", "err", err)
	}
	rows, err := h.Store.TraceAttrValuesScoped(r.Context(), matched, key, tr.From, tr.To, 50, h.integrationGroups(r.Context(), id))
	if err != nil {
		h.Logger.Error("attribute-values: query failed", "err", err, "key", key)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	values := make([]LogAttrValue, 0, len(rows))
	for _, v := range rows {
		values = append(values, LogAttrValue{Value: v.Value, Events: v.Events})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"key": key, "values": values})
}

// matchServices returns the subset of services whose names match any
// of the supplied matchers, decorated with classified service type
// and current health status (including custom-metric breaches via the
// supplied snapshot map).
// reconcileCatalog kicks the catalog reconciler so any matcher /
// integration edit shows up in integration_services immediately,
// without waiting for the next periodic tick. Failures are logged
// but never block the API response — the tick will catch up.
func (h *Handlers) reconcileCatalog(ctx context.Context) {
	if h.CatalogReconciler == nil {
		return
	}
	if err := h.CatalogReconciler.RunOnce(ctx); err != nil {
		h.Logger.Warn("on-demand catalog reconcile failed", "err", err)
	}
}

// servicesFromMembers projects the persisted (integration → services)
// list into ServiceSummary rows. Window stats (TraceCount, ErrorTraceCount,
// LastSeen, ServiceNamespace) are looked up from the in-window store rows
// keyed by service name; members with no row in the current window get
// zero counts. Status is recomputed against those zeroed counts —
// custom-metric breaches + firing health rules still drive unhealthy
// since those reflect current state, not the window.
func (h *Handlers) servicesFromMembers(
	ctx context.Context,
	members []string,
	all []store.ServiceRow,
	firingServices map[string]bool,
	errAcks map[string]erroracks.Ack,
	openErrors map[string]bool,
	tr TimeRange,
) []ServiceSummary {
	byName := make(map[string]store.ServiceRow, len(all))
	for _, s := range all {
		byName[s.ServiceName] = s
	}
	out := make([]ServiceSummary, 0, len(members))
	for _, name := range members {
		row, ok := byName[name]
		if !ok {
			row = store.ServiceRow{ServiceName: name}
		}
		effErr := h.effectiveErrorCount(ctx, name, row.ErrorTraceCount, tr.From, tr.To, errAcks)
		out = append(out, ServiceSummary{
			ServiceName:      row.ServiceName,
			ServiceNamespace: row.ServiceNamespace,
			LastSeen:         row.LastSeen,
			TraceCount:       row.TraceCount,
			ErrorTraceCount:  effErr,
			ServiceFacets:    h.classifyServiceFacets(ctx, name, tr),
			Tags:             []tags.Tag{},
			Status: statusWithOpenErrors(
				computeServiceStatus(effErr, firingServices[name]),
				openErrors[name],
			),
		})
	}
	return out
}

// matchServices is the older matcher-driven projection. It's still used
// by code paths that haven't (yet) been switched to the persisted
// membership; new callers should prefer servicesFromMembers.
func (h *Handlers) matchServices(
	ctx context.Context,
	matchers []integrations.Matcher,
	all []store.ServiceRow,
	firingServices map[string]bool,
	tr TimeRange,
) []ServiceSummary {
	out := make([]ServiceSummary, 0)
	for _, s := range all {
		for _, m := range matchers {
			if m.Match(s.ServiceName) {
				out = append(out, ServiceSummary{
					ServiceName:      s.ServiceName,
					ServiceNamespace: s.ServiceNamespace,
					LastSeen:         s.LastSeen,
					TraceCount:       s.TraceCount,
					ErrorTraceCount:  s.ErrorTraceCount,
					ServiceFacets:    h.classifyServiceFacets(ctx, s.ServiceName, tr),
					Tags:             []tags.Tag{},
					Status:           computeServiceStatus(s.ErrorTraceCount, firingServices[s.ServiceName]),
				})
				break
			}
		}
	}
	return out
}
