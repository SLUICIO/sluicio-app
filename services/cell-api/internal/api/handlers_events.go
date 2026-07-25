// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Outbound event emission glue (issue #4): maps the audit action
// vocabulary onto com.sluicio.<entity>.<verb> events, decides each
// event's audience, and answers "can this team see this audience?" for
// the emitter. Emission is edition-independent (events are
// notifications; the audit log is the EE record) and always
// fire-and-forget.

package api

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/eventsubs"
)

// entityTargetTypes are the audit target types that describe MONITORED
// entities — their events reach team-scoped subscriptions (visibility
// permitting). Everything else is org administration and goes to
// org-wide subscriptions only.
var entityTargetTypes = map[string]bool{
	"integration": true,
	"service":     true,
	"system":      true,
}

// emitConfigEvent turns one audit recording into a domain event. Runs on
// every recordAudit call — cheap when no subscriptions exist (one
// indexed query against a tiny table).
func (h *Handlers) emitConfigEvent(ctx context.Context, orgID uuid.UUID, action, targetType, targetID string, metadata map[string]any) {
	if h.Events == nil {
		return
	}
	aud := eventsubs.Audience{OrgLevel: !entityTargetTypes[targetType]}
	if !aud.OrgLevel {
		switch targetType {
		case "integration":
			if id, err := uuid.Parse(targetID); err == nil {
				aud.IntegrationID = &id
			}
		case "service":
			aud.ServiceNames = []string{targetID}
		case "system":
			if id, err := uuid.Parse(targetID); err == nil {
				aud.SystemID = &id
			}
		}
	}
	data := map[string]any{"target_type": targetType, "target_id": targetID}
	for k, v := range metadata {
		data[k] = v
	}
	h.Events.Emit(ctx, orgID, "com.sluicio."+action, targetID, aud, data)
}

// EmitAlertDomainEvent adapts the alerting engine's hook
// (alerting.SetDomainEventEmitter) onto the emitter.
func (h *Handlers) EmitAlertDomainEvent(ctx context.Context, orgID uuid.UUID, ev alerting.DomainEvent) {
	if h.Events == nil {
		return
	}
	aud := eventsubs.Audience{IntegrationID: ev.IntegrationID}
	if ev.ServiceName != "" {
		aud.ServiceNames = []string{ev.ServiceName}
	}
	h.Events.Emit(ctx, orgID, ev.Type, ev.Subject, aud, ev.Data)
}

// ── team-visibility answering (with a short cache) ───────────────────

type groupVisEntry struct {
	at       time.Time
	services map[string]struct{}
	wildcard bool
}

var (
	groupVisMu    sync.Mutex
	groupVisCache = map[uuid.UUID]groupVisEntry{}
	groupVisTTL   = 10 * time.Second
)

// CanGroupSeeAudience answers the emitter's visibility question: may
// this team receive an event about this audience? Resolution errors and
// hint-less entity events fail toward NOT delivering (less data), the
// same posture as the rest of RBAC.
func (h *Handlers) CanGroupSeeAudience(ctx context.Context, orgID, groupID uuid.UUID, aud eventsubs.Audience) bool {
	if aud.OrgLevel {
		return false
	}
	groupVisMu.Lock()
	e, ok := groupVisCache[groupID]
	groupVisMu.Unlock()
	if !ok || time.Since(e.at) > groupVisTTL {
		services, wildcard, err := h.Identity.ResolveGroupVisibleServices(ctx, orgID, groupID, h.integrationExpander, h.systemExpander)
		if err != nil {
			h.Logger.Warn("event visibility resolve failed; not delivering to team", "group", groupID, "err", err)
			return false
		}
		e = groupVisEntry{at: time.Now(), services: services, wildcard: wildcard}
		groupVisMu.Lock()
		groupVisCache[groupID] = e
		groupVisMu.Unlock()
	}
	if e.wildcard {
		return true
	}
	// Service-name hints: any overlap grants.
	for _, name := range aud.ServiceNames {
		if _, ok := e.services[name]; ok {
			return true
		}
	}
	// Integration/system hints expand to member services, then overlap.
	if aud.IntegrationID != nil {
		if names, err := h.integrationExpander(ctx, orgID, *aud.IntegrationID); err == nil {
			for _, name := range names {
				if _, ok := e.services[name]; ok {
					return true
				}
			}
		}
	}
	if aud.SystemID != nil {
		if names, err := h.systemExpander(ctx, orgID, "", aud.SystemID); err == nil {
			for _, name := range names {
				if _, ok := e.services[name]; ok {
					return true
				}
			}
		}
	}
	return false
}
