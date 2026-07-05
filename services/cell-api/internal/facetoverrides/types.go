// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package facetoverrides holds the cell-local model for manual
// service-facet overrides. Where package facetmappings translates span
// attributes into io.kind / io.role so the built-in detection still
// runs off telemetry, an override is a direct human decision used when
// the OTLP data can't express the facet at all:
//
//	action = "include"  -> force the facet ON  even if not auto-detected
//	action = "exclude"  -> force the facet OFF even if auto-detected
//
// The cell-api resolves a service's effective facet set as
// (auto-detected ∪ includes) − excludes. Overrides are stored as deltas
// rather than a snapshot so they stay correct as the underlying
// telemetry changes: an exclude only bites when the facet would
// otherwise appear, and an include always adds.
package facetoverrides

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Action is the direction of an override. The closed set mirrors the
// Postgres enum service_facet_override_action.
type Action string

const (
	ActionInclude Action = "include"
	ActionExclude Action = "exclude"
)

// Valid reports whether a is a recognised action.
func (a Action) Valid() bool {
	return a == ActionInclude || a == ActionExclude
}

// Override is one manual decision for one (service, facet) pair.
// FacetSlug is a built-in registry slug (file-input, queue-output,
// worker, …); the API layer validates it against the registry before
// persisting so a typo can't strand a row pointing at a facet that
// doesn't exist.
type Override struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	ServiceName    string    `json:"service_name"`
	FacetSlug      string    `json:"facet_slug"`
	Action         Action    `json:"action"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Set is a service's overrides split into fast include / exclude
// lookups, the shape the classification path actually wants.
type Set struct {
	Include map[string]bool
	Exclude map[string]bool
}

// NewSet folds a list of overrides into a Set. An empty or nil input
// yields a Set with empty (non-nil) maps, so callers never branch on
// nil before indexing.
func NewSet(overrides []Override) Set {
	s := Set{
		Include: make(map[string]bool, len(overrides)),
		Exclude: make(map[string]bool, len(overrides)),
	}
	for _, o := range overrides {
		switch o.Action {
		case ActionInclude:
			s.Include[o.FacetSlug] = true
		case ActionExclude:
			s.Exclude[o.FacetSlug] = true
		}
	}
	return s
}

// Empty reports whether the set carries no overrides — lets the
// resolution path skip work for the common (un-overridden) service.
func (s Set) Empty() bool {
	return len(s.Include) == 0 && len(s.Exclude) == 0
}

// ErrNotFound is returned when an override row is not in the store.
var ErrNotFound = errors.New("service facet override not found")
