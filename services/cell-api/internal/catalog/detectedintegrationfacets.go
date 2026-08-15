// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Persisted auto-detected facets for an INTEGRATION.
//
// The service-level twin of these lives in detectedfacets.go. The
// integrations list used to derive its facets by unioning its member
// services', which collapses as soon as a service belongs to more than
// one integration: every integration on a shared runtime got the union
// of everything that runtime does. See 0085 for the worked example.
//
// An integration is a slice of its members' traffic, so it is
// classified from that slice and the answer is stored against the
// integration. Expiry is evidence-based, exactly as for services.

package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ReplaceDetectedIntegrationFacets records the facets a detection pass
// saw for one integration.
//
// Like the service version it bumps what it saw and inserts what is new,
// but never deletes on absence: one pass not seeing a facet is not
// evidence against it. Removal is PruneDetectedIntegrationFacets' job.
func (s *Store) ReplaceDetectedIntegrationFacets(ctx context.Context, orgID, integrationID uuid.UUID, slugs []string, at time.Time) error {
	if len(slugs) == 0 {
		return nil
	}
	const q = `
		INSERT INTO integration_detected_facets
			(organization_id, integration_id, facet_slug, first_detected_at, last_detected_at)
		SELECT $1, $2, slug, $4, $4
		FROM unnest($3::text[]) AS slug
		ON CONFLICT (organization_id, integration_id, facet_slug)
		DO UPDATE SET last_detected_at = EXCLUDED.last_detected_at
	`
	if _, err := s.pool.Exec(ctx, q, orgID, integrationID, slugs, at); err != nil {
		return fmt.Errorf("replace detected integration facets: %w", err)
	}
	return nil
}

// DetectedIntegrationFacetsFor returns the stored facets for a set of
// integrations, keyed by integration id.
func (s *Store) DetectedIntegrationFacetsFor(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]DetectedFacet, error) {
	out := map[uuid.UUID][]DetectedFacet{}
	if len(ids) == 0 {
		return out, nil
	}
	const q = `
		SELECT integration_id, facet_slug, first_detected_at, last_detected_at
		FROM integration_detected_facets
		WHERE organization_id = $1 AND integration_id = ANY($2::uuid[])
		ORDER BY integration_id, facet_slug
	`
	rows, err := s.pool.Query(ctx, q, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("detected integration facets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var f DetectedFacet
		if err := rows.Scan(&id, &f.FacetSlug, &f.FirstDetectedAt, &f.LastDetectedAt); err != nil {
			return nil, err
		}
		out[id] = append(out[id], f)
	}
	return out, rows.Err()
}

// AnyDetectedIntegrationFacets reports whether any pass has stored
// anything for this org.
//
// Same purpose as the service version: between deploying this and the
// first pass the table is empty, and reading it regardless would blank
// the facet column on every integration. Callers fall back to the old
// member rollup until there is something to read, so the change lands
// without a window where the product looks broken.
func (s *Store) AnyDetectedIntegrationFacets(ctx context.Context, orgID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM integration_detected_facets WHERE organization_id = $1)`
	var ok bool
	if err := s.pool.QueryRow(ctx, q, orgID).Scan(&ok); err != nil {
		return false, fmt.Errorf("any detected integration facets: %w", err)
	}
	return ok, nil
}

// PruneDetectedIntegrationFacets drops facets not seen since `before`.
//
// The caller passes now minus the telemetry retention, so a
// classification lasts exactly as long as the spans that could
// re-detect it.
func (s *Store) PruneDetectedIntegrationFacets(ctx context.Context, orgID uuid.UUID, before time.Time) (int64, error) {
	const q = `
		DELETE FROM integration_detected_facets
		WHERE organization_id = $1 AND last_detected_at < $2
	`
	tag, err := s.pool.Exec(ctx, q, orgID, before)
	if err != nil {
		return 0, fmt.Errorf("prune detected integration facets: %w", err)
	}
	return tag.RowsAffected(), nil
}
