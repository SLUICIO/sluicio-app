// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"testing"

	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/facetoverrides"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/servicetypes"
)

// resolveFacets is the heart of the manual-override feature: it folds a
// service's include/exclude overrides into its auto-detected facet set.
// These tests pin the four behaviours the API contract promises.
func TestResolveFacets(t *testing.T) {
	reg := servicetypes.NewRegistry()
	h := &Handlers{ServiceFacets: reg}

	// facets builds an auto-matched slice from slugs, mirroring what
	// Registry.MatchAll would return for a service with that profile.
	facets := func(slugs ...string) []servicetypes.ServiceFacet {
		out := make([]servicetypes.ServiceFacet, 0, len(slugs))
		for _, s := range slugs {
			f := reg.Get(s)
			if f == nil {
				t.Fatalf("unknown facet slug in test setup: %q", s)
			}
			out = append(out, *f)
		}
		return out
	}
	set := func(include, exclude []string) facetoverrides.Set {
		s := facetoverrides.Set{Include: map[string]bool{}, Exclude: map[string]bool{}}
		for _, x := range include {
			s.Include[x] = true
		}
		for _, x := range exclude {
			s.Exclude[x] = true
		}
		return s
	}
	sources := func(rfs []resolvedFacet) map[string]string {
		m := make(map[string]string, len(rfs))
		for _, rf := range rfs {
			m[rf.facet.Slug] = rf.source
		}
		return m
	}

	tests := []struct {
		name        string
		auto        []servicetypes.ServiceFacet
		overrides   facetoverrides.Set
		wantOrder   []string          // effective slugs, in registry declaration order
		wantSources map[string]string // slug -> "auto"|"manual"
	}{
		{
			name:        "no overrides passes auto through unchanged",
			auto:        facets("worker", "core"),
			overrides:   set(nil, nil),
			wantOrder:   []string{"worker", "core"},
			wantSources: map[string]string{"worker": FacetSourceAuto, "core": FacetSourceAuto},
		},
		{
			name:      "include adds a non-detected facet tagged manual",
			auto:      facets("worker", "core"),
			overrides: set([]string{"db-output"}, nil),
			// db-output (decl index 6) sorts before worker (8) and core (9).
			wantOrder: []string{"db-output", "worker", "core"},
			wantSources: map[string]string{
				"db-output": FacetSourceManual,
				"worker":    FacetSourceAuto,
				"core":      FacetSourceAuto,
			},
		},
		{
			name:        "exclude removes an auto-detected facet",
			auto:        facets("worker", "core"),
			overrides:   set(nil, []string{"worker"}),
			wantOrder:   []string{"core"},
			wantSources: map[string]string{"core": FacetSourceAuto},
		},
		{
			name:        "exclude of core is ignored",
			auto:        facets("core"),
			overrides:   set(nil, []string{"core"}),
			wantOrder:   []string{"core"},
			wantSources: map[string]string{"core": FacetSourceAuto},
		},
		{
			name:      "include and exclude combine",
			auto:      facets("queue-input", "core"),
			overrides: set([]string{"db-output"}, []string{"queue-input"}),
			wantOrder: []string{"db-output", "core"},
			wantSources: map[string]string{
				"db-output": FacetSourceManual,
				"core":      FacetSourceAuto,
			},
		},
		{
			name:      "facet that is both auto and included stays auto",
			auto:      facets("db-output", "core"),
			overrides: set([]string{"db-output"}, nil),
			wantOrder: []string{"db-output", "core"},
			wantSources: map[string]string{
				"db-output": FacetSourceAuto,
				"core":      FacetSourceAuto,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := h.resolveFacets(h.ServiceFacets.All(), tt.auto, tt.overrides)

			gotOrder := make([]string, 0, len(got))
			for _, rf := range got {
				gotOrder = append(gotOrder, rf.facet.Slug)
			}
			if !equalStrings(gotOrder, tt.wantOrder) {
				t.Fatalf("effective facets = %v, want %v", gotOrder, tt.wantOrder)
			}
			gotSources := sources(got)
			for slug, want := range tt.wantSources {
				if gotSources[slug] != want {
					t.Errorf("facet %q source = %q, want %q", slug, gotSources[slug], want)
				}
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
