// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/eventsubs"
)

// An event about a team's own entity (a collector) reaches that team's
// subscriptions and no other team's, without resolving what the team
// sees: a team that sees every service still does not own the collector.
func TestCanGroupSeeOwningTeamOnly(t *testing.T) {
	h := &Handlers{} // no identity store: resolving would panic
	owner, other := uuid.New(), uuid.New()
	aud := eventsubs.Audience{GroupID: &owner}
	if !h.CanGroupSeeAudience(context.Background(), uuid.New(), owner, aud) {
		t.Fatal("the owning team does not get the event")
	}
	if h.CanGroupSeeAudience(context.Background(), uuid.New(), other, aud) {
		t.Fatal("another team gets the event")
	}
}
