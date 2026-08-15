// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Persisted auto-detected service facets (issue #26).
//
// A facet is a classification — what kind of work a service does — not
// a measurement of a time window. Deriving it from the reader's window
// made a service's identity flicker, and made "nothing in this window"
// look identical to "no facets". These read and write the stored
// answer; manual overrides are layered on top elsewhere, unchanged.

package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DetectedFacet is one stored classification with its evidence dates.
type DetectedFacet struct {
	FacetSlug string
	// FirstDetectedAt is when this facet was first seen on the service;
	// LastDetectedAt is the most recent pass that still saw it. The
	// second drives expiry, the first is worth keeping because "this
	// service has looked like an HTTP input since March" is a different
	// and more useful statement than "it did an hour ago".
	FirstDetectedAt time.Time
	LastDetectedAt  time.Time
}

// ReplaceDetectedFacets records the facets a detection pass saw for one
// service, in one statement.
//
// Bumps last_detected_at for facets still present and inserts new ones,
// but does NOT delete facets that were absent this pass. Absence in one
// window is not evidence against a facet — a monthly flow is invisible
// most days — so removal is left to PruneDetectedFacets, which waits
// for the evidence itself to age out.
func (s *Store) ReplaceDetectedFacets(ctx context.Context, orgID uuid.UUID, serviceName string, slugs []string, at time.Time) error {
	if len(slugs) == 0 {
		return nil
	}
	const q = `
		INSERT INTO service_detected_facets
			(organization_id, service_name, facet_slug, first_detected_at, last_detected_at)
		SELECT $1, $2, slug, $4, $4
		FROM unnest($3::text[]) AS slug
		ON CONFLICT (organization_id, service_name, facet_slug)
		DO UPDATE SET last_detected_at = EXCLUDED.last_detected_at
	`
	if _, err := s.pool.Exec(ctx, q, orgID, serviceName, slugs, at); err != nil {
		return fmt.Errorf("replace detected facets: %w", err)
	}
	return nil
}

// DetectedFacetsFor returns the stored facets for a set of services,
// keyed by service name.
func (s *Store) DetectedFacetsFor(ctx context.Context, orgID uuid.UUID, services []string) (map[string][]DetectedFacet, error) {
	out := map[string][]DetectedFacet{}
	if len(services) == 0 {
		return out, nil
	}
	const q = `
		SELECT service_name, facet_slug, first_detected_at, last_detected_at
		FROM service_detected_facets
		WHERE organization_id = $1 AND service_name = ANY($2::text[])
		ORDER BY service_name, facet_slug
	`
	rows, err := s.pool.Query(ctx, q, orgID, services)
	if err != nil {
		return nil, fmt.Errorf("detected facets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var f DetectedFacet
		if err := rows.Scan(&name, &f.FacetSlug, &f.FirstDetectedAt, &f.LastDetectedAt); err != nil {
			return nil, err
		}
		out[name] = append(out[name], f)
	}
	return out, rows.Err()
}

// AnyDetectedFacets reports whether ANY pass has stored anything for
// this org.
//
// The read paths use it to decide between the stored answer and a live
// computation: between deploying this and the first detection pass
// completing, the table is empty, and reading it would blank every
// facet in the product. Falling back until there is something to read
// makes the change invisible rather than briefly destructive.
func (s *Store) AnyDetectedFacets(ctx context.Context, orgID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM service_detected_facets WHERE organization_id = $1)`
	var ok bool
	if err := s.pool.QueryRow(ctx, q, orgID).Scan(&ok); err != nil {
		return false, fmt.Errorf("any detected facets: %w", err)
	}
	return ok, nil
}

// PruneDetectedFacets drops facets not seen since `before`.
//
// The caller passes now minus the telemetry retention: a facet survives
// exactly as long as the spans that could re-detect it. Pruning sooner
// would discard a monthly flow's classification between its runs;
// pruning never would keep boundaries a service has genuinely retired.
func (s *Store) PruneDetectedFacets(ctx context.Context, orgID uuid.UUID, before time.Time) (int64, error) {
	const q = `
		DELETE FROM service_detected_facets
		WHERE organization_id = $1 AND last_detected_at < $2
	`
	tag, err := s.pool.Exec(ctx, q, orgID, before)
	if err != nil {
		return 0, fmt.Errorf("prune detected facets: %w", err)
	}
	return tag.RowsAffected(), nil
}
