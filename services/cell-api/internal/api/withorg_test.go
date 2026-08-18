// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The bug this guards is silent. A background pass with no principal
// looked up the org's facet mappings under the nil UUID, got zero rows,
// and classified every service as though no rule existed. Nothing
// errored and nothing logged: the rules simply had no effect, and since
// the classification is persisted that is what the UI showed.
//
// So what is pinned here is the one property the whole chain rests on —
// that the org survives the hop onto a background context and comes
// back out where every per-org lookup reads it.

package api

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
)

func TestWithOrgIsReadableByTheLookupsThatNeedIt(t *testing.T) {
	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	ctx := withOrg(context.Background(), orgID)

	// middleware.OrgIDFromContext is what ioResolverFor, facetOverridesFor
	// and mergedFacets all call. If it cannot see the org here, they read
	// the wrong tenant's rows — in practice none at all.
	if got := middleware.OrgIDFromContext(ctx); got != orgID {
		t.Errorf("org did not survive onto the background context: got %s, want %s", got, orgID)
	}
}

func TestBackgroundContextWithoutWithOrgHasNoOrg(t *testing.T) {
	// The failing state, stated so the test above cannot pass vacuously:
	// a bare context really does resolve to the nil UUID, which is the
	// value that silently matched no rows.
	if got := middleware.OrgIDFromContext(context.Background()); got != uuid.Nil {
		t.Errorf("a bare context reported org %s; the premise of withOrg no longer holds", got)
	}
}
