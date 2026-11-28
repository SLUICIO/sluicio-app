// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

package collectors_test

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
)

// A proposed deployment whose proposal could not be filed is removed;
// one that has its proposal, and anything not proposed, stays.
func TestDiscardProposed(t *testing.T) {
	pool, ctx := newDB(t)
	org := seedOrg(t, ctx, pool, "acme")
	team := seedTeam(t, ctx, pool, org, "platform")
	s := collectors.New(pool, newKeys(t))
	_, tok, _ := s.CreateToken(ctx, org, team, "t", nil)
	caps := uint64(1 | 2 | 4 | 2048)
	col, _, err := s.Observe(ctx, collectors.Observation{OrgID: org, GroupID: team, TokenID: tok.ID, InstanceUID: "gw", Transport: "websocket", Capabilities: &caps})
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "a: 1\n", Proposed: true})
	if err != nil {
		t.Fatal(err)
	}
	sent, err := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "b: 1\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DiscardProposed(ctx, col, orphan.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DiscardProposed(ctx, col, sent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDeployment(ctx, col, orphan.ID); err == nil {
		t.Fatal("the proposed deployment without a proposal is still there")
	}
	if d, err := s.GetDeployment(ctx, col, sent.ID); err != nil || d.Status != collectors.DeploymentPending {
		t.Fatalf("a deployment to deliver was discarded: %+v %v", d, err)
	}
}
