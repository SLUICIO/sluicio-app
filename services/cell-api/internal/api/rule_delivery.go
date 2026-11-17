// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/notifyprofiles"
)

// ruleDelivery says where a check's firings go, for the lists that show
// checks. The rule's own channels were the only thing shown, so a check
// routed by its profile, which is most of them, looked as if it went
// nowhere.
type ruleDelivery struct {
	// Via is "channels" (the rule's own), "profile" or "none".
	Via         string     `json:"via"`
	ProfileID   *uuid.UUID `json:"profile_id,omitempty"`
	ProfileName string     `json:"profile_name,omitempty"`
	// ChannelIDs are the channels a firing is sent to.
	ChannelIDs []uuid.UUID `json:"channel_ids"`
	// Restricted marks a profile that applies to some environments only:
	// a firing in another one goes further down the ladder.
	Restricted bool `json:"restricted,omitempty"`
}

// deliveryResolver resolves ruleDelivery for many rules, reading each
// (integration, team) scope's profile once.
type deliveryResolver struct {
	profiles *notifyprofiles.Store
	org      uuid.UUID
	cache    map[[2]uuid.UUID]ruleDelivery
}

func (h *Handlers) newDeliveryResolver(org uuid.UUID) *deliveryResolver {
	return &deliveryResolver{profiles: h.Profiles, org: org, cache: map[[2]uuid.UUID]ruleDelivery{}}
}

// For mirrors notifyprofiles.Store.Resolve: the rule's own channels win,
// else the first profile up the ladder from its integration and team.
// It answers without an environment, so a profile restricted to some
// environments is shown with Restricted set.
func (d *deliveryResolver) For(ctx context.Context, explicit []uuid.UUID, integrationID, groupID *uuid.UUID) ruleDelivery {
	if len(explicit) > 0 {
		return ruleDelivery{Via: "channels", ChannelIDs: explicit}
	}
	if d.profiles == nil {
		return ruleDelivery{Via: "none", ChannelIDs: []uuid.UUID{}}
	}
	var key [2]uuid.UUID
	if integrationID != nil {
		key[0] = *integrationID
	}
	if groupID != nil {
		key[1] = *groupID
	}
	if got, ok := d.cache[key]; ok {
		return got
	}
	out := ruleDelivery{Via: "none", ChannelIDs: []uuid.UUID{}}
	p, err := d.profiles.ResolveProfile(ctx, d.org, integrationID, groupID)
	if err == nil {
		id := p.ID
		out = ruleDelivery{
			Via: "profile", ProfileID: &id, ProfileName: p.Name, ChannelIDs: p.ChannelIDs,
			Restricted: p.EnvironmentMode == notifyprofiles.EnvironmentOnly || p.EnvironmentMode == notifyprofiles.EnvironmentExcept,
		}
		if out.ChannelIDs == nil {
			out.ChannelIDs = []uuid.UUID{}
		}
	} else if !errors.Is(err, notifyprofiles.ErrNotFound) {
		return ruleDelivery{Via: "unknown", ChannelIDs: []uuid.UUID{}}
	}
	d.cache[key] = out
	return out
}
