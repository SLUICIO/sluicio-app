// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Classification of an INTEGRATION, as opposed to of a service.
//
// Issue #26 persisted service facets, and the integrations list built
// its own facets by unioning its member services'. That reads fine until
// a service belongs to more than one integration, which is the normal
// case for a runtime that hosts several flows: three Node-RED
// integrations sharing one service each got the union of everything that
// service does. Every row showed the same chips and the facet filter
// selected all of them or none.
//
// The fix is to classify what the integration actually is — its member
// services intersected with its attribute predicate — rather than what
// its members are.

package api

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetmappings"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetoverrides"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/servicetypes"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// IntegrationFacetScope is everything needed to classify one
// integration: which services belong to it, and which of their spans
// are its own.
type IntegrationFacetScope struct {
	ID       uuid.UUID
	Services []string
	// Groups is the integration's matchers as a DNF predicate — the same
	// one the message search uses, so an integration is classified over
	// exactly the spans it shows you.
	//
	// It renders the service.name matchers too, which is redundant with
	// the profile query's own IN clause and harmless. What matters is the
	// attribute terms: with none, the slice is all of the members'
	// traffic and the classification equals the old member rollup; with
	// some, the slice narrows and two integrations sharing a service
	// stop agreeing. That is why this needs no special case for the
	// simple integration.
	Groups [][]store.LogAttrFilter
}

// classifyIntegrationFacets profiles the integration's own span slice
// and returns its effective facets, each tagged auto or manual.
//
// Members are grouped by resolver signature first, exactly as
// classifyServiceFacetsBulk does, because a facet mapping can be pinned
// to a single service and the io_facet expression is baked into the SQL.
// The resulting profiles are unioned before matching: a profile is a set
// of observations, so combining two members' observations is the same
// operation as observing them together.
//
// Manual overrides on member services are still layered on. They are a
// human's explicit claim, and discarding one because it was made against
// the service rather than the integration would lose information the
// automatic path cannot recover. The residue is that a manual facet on a
// SHARED service does still reach every integration that service is in —
// unlike the auto facets, which are now sliced correctly.
func (h *Handlers) classifyIntegrationFacets(ctx context.Context, scope IntegrationFacetScope, tr TimeRange) []ServiceFacetRef {
	if len(scope.Services) == 0 {
		return nil
	}
	type group struct {
		resolver facetmappings.Resolver
		names    []string
	}
	groups := map[string]*group{}
	for _, name := range scope.Services {
		r := h.ioResolverFor(ctx, name)
		key := r.KindExpr + "\x00" + r.RoleExpr + "\x00" + fmt.Sprint(r.KindArgs, r.RoleArgs)
		g := groups[key]
		if g == nil {
			g = &group{resolver: r}
			groups[key] = g
		}
		g.names = append(g.names, name)
	}

	var merged store.ServiceProfileRow
	for _, g := range groups {
		row, err := h.Store.IntegrationProfile(ctx, g.resolver, g.names, scope.Groups, tr.From, tr.To)
		if err != nil {
			h.Logger.Warn("integration profile failed", "err", err, "integration", scope.ID)
			continue
		}
		merged.SpanKinds = append(merged.SpanKinds, row.SpanKinds...)
		merged.ResourceAttrKeys = append(merged.ResourceAttrKeys, row.ResourceAttrKeys...)
		merged.SpanAttrKeys = append(merged.SpanAttrKeys, row.SpanAttrKeys...)
		merged.IOFacets = append(merged.IOFacets, row.IOFacets...)
	}

	// toProfile de-duplicates via its maps, so the concatenation above
	// needs no distinct pass.
	profile := toProfile("", merged)
	auto := h.ServiceFacets.MatchAll(profile)

	var overrides []facetoverrides.Override
	if h.FacetOverrides != nil {
		orgID := middleware.OrgIDFromContext(ctx)
		for _, name := range scope.Services {
			rows, err := h.FacetOverrides.ListForService(ctx, orgID, name)
			if err != nil {
				h.Logger.Warn("load facet overrides failed", "err", err, "service", name)
				continue
			}
			overrides = append(overrides, rows...)
		}
	}

	resolved := h.resolveFacets(
		h.mergedFacets(ctx, middleware.OrgIDFromContext(ctx)),
		auto,
		facetoverrides.NewSet(overrides),
	)
	out := make([]ServiceFacetRef, 0, len(resolved))
	for _, rf := range resolved {
		out = append(out, ServiceFacetRef{Slug: rf.facet.Slug, Name: rf.facet.Name, Source: rf.source})
	}
	return out
}

// DetectIntegrationFacets is the reconciler's hook for integrations:
// classify each scope over the window and return the AUTO-detected facet
// slugs per integration.
//
// Auto only, for the same reason as DetectServiceFacets — manual
// assignments have their own table and are layered on at read time, and
// persisting a copy here would let the two drift.
func (h *Handlers) DetectIntegrationFacets(ctx context.Context, scopes []IntegrationFacetScope, from, to time.Time) map[uuid.UUID][]string {
	out := make(map[uuid.UUID][]string, len(scopes))
	tr := TimeRange{From: from, To: to}
	for _, scope := range scopes {
		refs := h.classifyIntegrationFacets(ctx, scope, tr)
		slugs := make([]string, 0, len(refs))
		for _, f := range refs {
			if f.Source == FacetSourceManual {
				continue
			}
			slugs = append(slugs, f.Slug)
		}
		out[scope.ID] = slugs
	}
	return out
}

// DetectIntegrationFacetsForOrg is the reconciler's entry point: build a
// scope for every integration in the org and classify each one.
//
// Scopes are built from the persisted catalog membership rather than
// from window traffic, so a quiet integration is still classified. A
// service discovered since the last reconcile is picked up on the next
// pass, which is soon enough for something that runs every 15 minutes
// and describes what an integration IS.
func (h *Handlers) DetectIntegrationFacetsForOrg(ctx context.Context, orgID uuid.UUID, from, to time.Time) map[uuid.UUID][]string {
	if h.Integrations == nil || h.Catalog == nil {
		return nil
	}
	// Background tick: no principal, so the org has to be put on the
	// context or every per-org lookup below (facet mappings above all)
	// silently reads nothing. See withOrg.
	ctx = withOrg(ctx, orgID)
	all, err := h.Integrations.AllMatchersWithIntegration(ctx, orgID)
	if err != nil {
		h.Logger.Warn("integration facet detection: matchers failed", "err", err)
		return nil
	}
	matchersByIntegration := map[uuid.UUID][]integrations.Matcher{}
	for _, mi := range all {
		matchersByIntegration[mi.Integration.ID] = append(matchersByIntegration[mi.Integration.ID], mi.Matcher)
	}
	members, err := h.Catalog.IntegrationServicesBulk(ctx, orgID)
	if err != nil {
		h.Logger.Warn("integration facet detection: membership failed", "err", err)
		return nil
	}

	scopes := make([]IntegrationFacetScope, 0, len(members))
	for id, names := range members {
		if len(names) == 0 {
			continue
		}
		scopes = append(scopes, IntegrationFacetScope{
			ID:       id,
			Services: names,
			Groups:   AttrGroupsFromMatchers(matchersByIntegration[id]),
		})
	}
	return h.DetectIntegrationFacets(ctx, scopes, from, to)
}

// storedIntegrationFacets returns the persisted classification for a set
// of integrations, with member manual overrides layered on top.
//
// ok=false when no detection pass has stored anything yet, so callers
// fall back to the member rollup. Without that fallback every
// integration would show no facets between deploying this and the first
// pass completing.
func (h *Handlers) storedIntegrationFacets(ctx context.Context, scopes []IntegrationFacetScope) (map[uuid.UUID][]ServiceFacetRef, bool) {
	if h.Catalog == nil || len(scopes) == 0 {
		return nil, false
	}
	orgID := middleware.OrgIDFromContext(ctx)
	any, err := h.Catalog.AnyDetectedIntegrationFacets(ctx, orgID)
	if err != nil || !any {
		return nil, false
	}
	ids := make([]uuid.UUID, 0, len(scopes))
	for _, s := range scopes {
		ids = append(ids, s.ID)
	}
	stored, err := h.Catalog.DetectedIntegrationFacetsFor(ctx, orgID, ids)
	if err != nil {
		h.Logger.Warn("read detected integration facets failed", "err", err)
		return nil, false
	}

	merged := h.mergedFacets(ctx, orgID)
	bySlug := make(map[string]servicetypes.ServiceFacet, len(merged))
	for _, f := range merged {
		bySlug[f.Slug] = f
	}

	out := make(map[uuid.UUID][]ServiceFacetRef, len(scopes))
	for _, scope := range scopes {
		// resolveFacets takes the matched facets, not their slugs, so the
		// stored slugs are mapped back through the registry — which also
		// drops any facet a user has since deleted.
		auto := make([]servicetypes.ServiceFacet, 0, len(stored[scope.ID]))
		for _, df := range stored[scope.ID] {
			if f, ok := bySlug[df.FacetSlug]; ok {
				auto = append(auto, f)
			}
		}
		var overrides []facetoverrides.Override
		if h.FacetOverrides != nil {
			for _, name := range scope.Services {
				rows, err := h.FacetOverrides.ListForService(ctx, orgID, name)
				if err != nil {
					continue
				}
				overrides = append(overrides, rows...)
			}
		}
		resolved := h.resolveFacets(merged, auto, facetoverrides.NewSet(overrides))
		refs := make([]ServiceFacetRef, 0, len(resolved))
		for _, rf := range resolved {
			refs = append(refs, ServiceFacetRef{Slug: rf.facet.Slug, Name: rf.facet.Name, Source: rf.source})
		}
		out[scope.ID] = refs
	}
	return out, true
}
