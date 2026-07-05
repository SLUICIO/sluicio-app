// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package servicetypes

// CoreSlug is the slug of the always-on baseline "Overview" facet. It
// matches every service and is exempt from manual facet overrides — a
// service always keeps its baseline dashboard even if a user tries to
// exclude everything.
const CoreSlug = "core"

// Registry is an immutable, ordered list of service facets. A service
// is described by every facet whose MatchFn fires for its profile, so
// MatchAll can return zero, one, or many facets. The `core` facet
// always matches and is always last in declaration order, so MatchAll
// is never empty for any service.
type Registry struct {
	facets []ServiceFacet
}

// NewRegistry returns a Registry populated with the built-in facets.
func NewRegistry() *Registry {
	return &Registry{facets: Builtin()}
}

// All returns every registered facet in declaration order.
func (r *Registry) All() []ServiceFacet {
	return r.facets
}

// Get returns the facet with the given slug, or nil if not found.
func (r *Registry) Get(slug string) *ServiceFacet {
	for i := range r.facets {
		if r.facets[i].Slug == slug {
			return &r.facets[i]
		}
	}
	return nil
}

// MatchAll returns every facet whose MatchFn fires for the profile,
// in declaration order. The `core` facet always matches, so the
// returned slice is never empty for a service with any spans at all.
func (r *Registry) MatchAll(p ServiceProfile) []ServiceFacet {
	out := make([]ServiceFacet, 0, len(r.facets))
	for _, f := range r.facets {
		if f.MatchFn(p) {
			out = append(out, f)
		}
	}
	return out
}
