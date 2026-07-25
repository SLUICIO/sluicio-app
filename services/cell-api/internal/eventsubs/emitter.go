// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package eventsubs

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Audience says who may receive an event. Org-level events (settings,
// members, tokens, groups, …) go only to org-wide subscriptions; an
// entity-scoped event additionally reaches team subscriptions whose
// team can see the entity (resolved via CanGroupSee).
type Audience struct {
	// OrgLevel marks events about org administration rather than a
	// monitored entity — never delivered to team-scoped subscriptions.
	OrgLevel bool
	// Entity hints for team-visibility resolution (any may be empty).
	ServiceNames  []string
	IntegrationID *uuid.UUID
	SystemID      *uuid.UUID
}

// Emitter matches events against the org's enabled subscriptions and
// enqueues deliveries. Fire-and-forget: failures log and drop — events
// are best-effort notifications, never the record (that's the audit
// log).
type Emitter struct {
	Store *Store
	// CanGroupSee answers "may this team see this audience?" — injected
	// from the API layer, which owns the visibility machinery. Nil =
	// team subscriptions receive no entity events (fail toward less
	// data, same posture as everything else).
	CanGroupSee func(ctx context.Context, orgID, groupID uuid.UUID, aud Audience) bool
	Log         *slog.Logger
}

// Emit fans one domain event out to every matching subscription. One
// event id is minted per emission and shared across subscriptions, so
// consumers listening on duplicate routes can dedupe.
func (e *Emitter) Emit(ctx context.Context, orgID uuid.UUID, eventType, subject string, aud Audience, data map[string]any) {
	if e == nil || e.Store == nil {
		return
	}
	subs, err := e.Store.EnabledForOrg(ctx, orgID)
	if err != nil {
		e.Log.Warn("event emit: subscriptions load failed; dropping", "type", eventType, "err", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	eventID := uuid.NewString()
	occurred := time.Now().UTC()
	for _, sub := range subs {
		if !MatchesAny(eventType, sub.EventFilters) {
			continue
		}
		if sub.GroupID != nil {
			// Team-scoped: entity events only, and only when the team
			// can see the entity.
			if aud.OrgLevel {
				continue
			}
			if e.CanGroupSee == nil || !e.CanGroupSee(ctx, orgID, *sub.GroupID, aud) {
				continue
			}
		}
		if err := e.Store.Enqueue(ctx, sub.ID, eventID, eventType, subject, occurred, data); err != nil {
			e.Log.Warn("event enqueue failed; dropping for subscription", "type", eventType, "subscription", sub.ID, "err", err)
		}
	}
}
