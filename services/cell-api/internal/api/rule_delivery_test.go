// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// A rule's own channels are the answer whatever the profiles say, and a
// rule with none and no profile store to ask goes nowhere, which the
// list must say rather than show an empty cell.
func TestRuleDeliveryOwnChannelsAndNone(t *testing.T) {
	d := &deliveryResolver{org: uuid.New(), cache: map[[2]uuid.UUID]ruleDelivery{}}
	own := []uuid.UUID{uuid.New()}
	if got := d.For(context.Background(), own, nil, nil); got.Via != "channels" || len(got.ChannelIDs) != 1 || got.ChannelIDs[0] != own[0] {
		t.Fatalf("own channels: %+v", got)
	}
	if got := d.For(context.Background(), nil, nil, nil); got.Via != "none" || got.ChannelIDs == nil {
		t.Fatalf("no channels, no profiles: %+v", got)
	}
}
