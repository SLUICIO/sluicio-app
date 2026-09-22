// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What the OTLP state export sends (issue #36), assembled here rather
// than in the exporter.
//
// The state of an integration is decided by rules that already exist and
// that several surfaces read: the list, the detail page, the dashboard
// pip. An exporter that recomputed them would be a fourth opinion, and
// the estate tool reading it would eventually disagree with the page
// somebody opens to check. So the exporter asks this, and this uses the
// same folds the handlers use.
//
// No *http.Request and no principal: a background job exports the org,
// not a caller's view of it. Visibility filtering is a property of a
// reader, and there is no reader here.

package api

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
)

// EntityState is one integration or system, as the export sees it.
type EntityState struct {
	ID   uuid.UUID
	Name string
	// Slug for an integration, type key for a system. Empty when the
	// entity has none.
	Slug string
	Kind string
	// Status is the vocabulary the product already uses: ok, errors,
	// unhealthy, quiet.
	Status string
	// Messages is how many messages the integration carried in the count
	// window. Zero for a system: a system is not a flow and carries none.
	Messages uint64
}

// IntegrationStates returns every integration's state over the health
// window, and its message count over the count window.
//
// Two windows because they answer different questions. State is "is this
// integration all right", which is asked of a stretch long enough to be
// meaningful - the same hour the page defaults to. The count is "how many
// messages since the last export", which is exactly the export interval.
// Folding them into one would make every integration read "quiet" between
// two runs of a nightly flow.
func (h *Handlers) IntegrationStates(ctx context.Context, orgID uuid.UUID, healthFrom, healthTo, countFrom, countTo time.Time) ([]EntityState, error) {
	ctx = withOrg(ctx, orgID)
	rows, err := h.Integrations.List(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	all, err := h.Integrations.AllMatchersWithIntegration(ctx, orgID)
	if err != nil {
		return nil, err
	}
	matchersByIntegration := map[uuid.UUID][]integrations.Matcher{}
	for _, mi := range all {
		matchersByIntegration[mi.Integration.ID] = append(matchersByIntegration[mi.Integration.ID], mi.Matcher)
	}

	members, err := h.Catalog.IntegrationServicesBulk(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// Health inputs. Each is one round trip for the whole org, so the
	// per-integration loop below only does its own counting.
	firingServices, err := h.Alerts.FiringHealthServices(ctx, orgID)
	if err != nil {
		h.Logger.Warn("state export: firing health services failed", "err", err)
		firingServices = map[string]bool{}
	}
	firingIntegrations, err := h.Alerts.FiringHealthIntegrations(ctx, orgID)
	if err != nil {
		h.Logger.Warn("state export: firing health integrations failed", "err", err)
		firingIntegrations = map[uuid.UUID]bool{}
	}
	openDelayed := map[uuid.UUID][]string{}
	if h.TraceCompletion != nil {
		if d, dErr := h.TraceCompletion.OpenDelayedTraceIDsByIntegration(ctx, orgID); dErr == nil {
			openDelayed = d
		} else {
			h.Logger.Warn("state export: open delayed traces failed", "err", dErr)
		}
	}

	out := make([]EntityState, 0, len(rows))
	for _, integ := range rows {
		names := members[integ.ID]
		groups := AttrGroupsFromMatchers(matchersByIntegration[integ.ID], integ.RuleMatch)

		// Per-member health, for the members that emitted in the health
		// window. A member that was quiet contributes nothing, so an
		// integration with no traffic rolls up to "quiet" - the same
		// reading the list gives.
		statuses := make([]string, 0, len(names))
		if len(names) > 0 {
			counts, cErr := h.Store.ServiceTraceCountsFiltered(ctx, names, healthFrom, healthTo, groups)
			if cErr != nil {
				h.Logger.Warn("state export: service counts failed", "integration", integ.ID, "err", cErr)
			}
			for _, name := range names {
				c, seen := counts[name]
				if !seen || c[0] == 0 {
					continue
				}
				statuses = append(statuses, computeServiceStatus(c[1], firingServices[name]))
			}
		}

		// An open SLA breach counts only if the trace it belongs to is in
		// the window, so a breach from last week does not hold an
		// integration red forever.
		var delayed uint64
		if ids := openDelayed[integ.ID]; len(ids) > 0 && len(names) > 0 {
			if n, dErr := h.Store.CountDistinctTracesIn(ctx, names, ids, healthFrom, healthTo, groups); dErr == nil {
				delayed = n
			} else {
				h.Logger.Warn("state export: delayed-in-window count failed", "integration", integ.ID, "err", dErr)
			}
		}

		// A firing check bound to a member makes the integration
		// unhealthy even if that member was quiet this window, which is
		// the rule the list applies too.
		firingMember := false
		for _, name := range names {
			if firingServices[name] {
				firingMember = true
				break
			}
		}

		var messages uint64
		if len(names) > 0 {
			if n, _, mErr := h.Store.DistinctTraceCounts(ctx, names, countFrom, countTo, groups); mErr == nil {
				messages = n
			} else {
				h.Logger.Warn("state export: message count failed", "integration", integ.ID, "err", mErr)
			}
		}

		out = append(out, EntityState{
			ID:       integ.ID,
			Name:     integ.Name,
			Slug:     integ.Slug,
			Status:   integrationRollupStatus(statuses, delayed, firingIntegrations[integ.ID] || firingMember),
			Messages: messages,
		})
	}
	return out, nil
}

// SystemStates returns every system's state over the health window.
//
// A system carries no messages of its own, so there is no count here. It
// is a thing that is up or is not: a broker, a database, a gateway.
func (h *Handlers) SystemStates(ctx context.Context, orgID uuid.UUID, healthFrom, healthTo time.Time) ([]EntityState, error) {
	ctx = withOrg(ctx, orgID)
	systems, err := h.Catalog.ListSystems(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if len(systems) == 0 {
		return nil, nil
	}

	firingServices, err := h.Alerts.FiringHealthServices(ctx, orgID)
	if err != nil {
		h.Logger.Warn("state export: firing health services failed", "err", err)
		firingServices = map[string]bool{}
	}
	firingSystems, err := h.Alerts.FiringHealthSystems(ctx, orgID)
	if err != nil {
		h.Logger.Warn("state export: firing health systems failed", "err", err)
		firingSystems = map[uuid.UUID]bool{}
	}
	checked, err := h.Alerts.SystemsWithHealthChecks(ctx, orgID)
	if err != nil {
		h.Logger.Warn("state export: systems with health checks failed", "err", err)
		checked = map[uuid.UUID]bool{}
	}

	// One traffic read for every member of every system, rather than one
	// per system: the members overlap, and a system is usually one
	// service seen from several sides.
	seen := map[string]struct{}{}
	allMembers := make([]string, 0)
	for _, sy := range systems {
		for _, m := range sy.Members {
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			allMembers = append(allMembers, m)
		}
	}
	counts := map[string][2]uint64{}
	if len(allMembers) > 0 {
		c, cErr := h.Store.ServiceTraceCountsFiltered(ctx, allMembers, healthFrom, healthTo, nil)
		if cErr != nil {
			h.Logger.Warn("state export: system member counts failed", "err", cErr)
		} else {
			counts = c
		}
	}

	out := make([]EntityState, 0, len(systems))
	for _, sy := range systems {
		statuses := make([]string, 0, len(sy.Members))
		for _, m := range sy.Members {
			c, ok := counts[m]
			if !ok || c[0] == 0 {
				continue
			}
			statuses = append(statuses, computeServiceStatus(c[1], firingServices[m]))
		}
		out = append(out, EntityState{
			ID:     sy.ID,
			Name:   sy.Name,
			Kind:   sy.TypeKey,
			Status: systemRollupStatus(statuses, checked[sy.ID], firingSystems[sy.ID]),
		})
	}
	return out, nil
}
