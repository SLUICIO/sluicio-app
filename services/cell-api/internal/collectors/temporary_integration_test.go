// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

package collectors_test

import (
	"errors"
	"testing"
	"time"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
)

// A temporary change and what the collector returns to: its local
// configuration when nothing permanent was ever sent, a permanent change
// made meanwhile (held until the end, the newest winning), or the last
// permanent configuration it applied.
func TestTemporaryDeployments(t *testing.T) {
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
	apply := func(d collectors.Deployment) {
		t.Helper()
		if err := s.ReportStatus(ctx, col, d.Hash, collectors.DeploymentApplied, ""); err != nil {
			t.Fatal(err)
		}
	}
	get := func(id collectors.Deployment) collectors.Deployment {
		t.Helper()
		d, err := s.GetDeployment(ctx, col, id.ID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	body := func(d collectors.Deployment) string {
		t.Helper()
		_, b, err := s.DeploymentBody(ctx, col, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	// 1. Nothing permanent was ever sent: the end restores the local
	// configuration, an empty remote one.
	tmp, err := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "debug: on\n", Duration: time.Hour})
	if err != nil || tmp.DurationSeconds == nil || *tmp.DurationSeconds != 3600 || tmp.ExpiresAt != nil {
		t.Fatalf("temporary: %+v %v", tmp, err)
	}
	if _, err := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "again\n", Duration: time.Hour}); !errors.Is(err, collectors.ErrTemporaryActive) {
		t.Fatalf("a second temporary change: %v", err)
	}
	apply(tmp)
	if e := get(tmp).ExpiresAt; e == nil || time.Until(*e) < 59*time.Minute {
		t.Fatalf("the hour starts when it is applied: %v", e)
	}
	// Not due yet.
	if ended, _, err := s.ExpireDue(ctx, time.Now()); err != nil || len(ended) != 0 {
		t.Fatalf("expired early: %v %v", ended, err)
	}
	ended, _, err := s.ExpireDue(ctx, time.Now().Add(61*time.Minute))
	if err != nil || len(ended) != 1 || ended[0].Ended != tmp.ID {
		t.Fatalf("expiry: %+v %v", ended, err)
	}
	if d := get(tmp); d.Status != collectors.DeploymentEnded || d.EndedReason != collectors.EndedExpired {
		t.Fatalf("expired deployment: %s %s", d.Status, d.EndedReason)
	}
	base := ended[0].Base
	if base.Via != "revert" || base.Status != collectors.DeploymentPending || body(base) != "" {
		t.Fatalf("base after a first temporary change: %+v body %q", base, body(base))
	}
	next, _, _ := s.NextToDeliver(ctx, col)
	if next == nil || next.ID != base.ID {
		t.Fatalf("next to deliver: %+v", next)
	}
	apply(base)

	// 2. A permanent change, then a temporary one; a permanent change
	// made meanwhile is held, the newest held one wins, and the end
	// sends it.
	perm, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "v1\n"})
	apply(perm)
	tmp2, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "debug\n", Duration: 30 * time.Minute})
	apply(tmp2)
	h1, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "v2\n"})
	h2, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "v3\n"})
	if h1.Status != collectors.DeploymentHeld || h2.Status != collectors.DeploymentHeld || get(h1).Status != collectors.DeploymentSuperseded {
		t.Fatalf("held: %s, %s, then %s", h1.Status, h2.Status, get(h1).Status)
	}
	if d, _, _ := s.NextToDeliver(ctx, col); d != nil {
		t.Fatalf("a held change would be delivered while the temporary one runs: %+v", d)
	}
	if get(tmp2).Status != collectors.DeploymentApplied {
		t.Fatal("a permanent change superseded the running temporary one")
	}
	b, err := s.EndTemporary(ctx, col, tmp2.ID, collectors.EndedEarly)
	if err != nil || b.ID != h2.ID || b.Status != collectors.DeploymentPending {
		t.Fatalf("ended early: %+v %v", b, err)
	}
	if _, err := s.EndTemporary(ctx, col, tmp2.ID, collectors.EndedEarly); !errors.Is(err, collectors.ErrNotFound) {
		t.Fatalf("ending twice: %v", err)
	}
	apply(h2)

	// 3. No change made meanwhile: the last permanent one applied (v3) is
	// sent again.
	tmp3, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "debug\n", Duration: 10 * time.Minute})
	apply(tmp3)
	b, err = s.EndTemporary(ctx, col, tmp3.ID, collectors.EndedEarly)
	if err != nil || body(b) != "v3\n" || b.RolledBackFrom == nil || *b.RolledBackFrom != h2.ID {
		t.Fatalf("back to the last permanent configuration: %+v %v", b, err)
	}
	apply(b)

	// 4. A temporary change that fails: nothing to revert, and a change
	// held behind it is sent on by the sweep.
	tmp4, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "broken\n", Duration: 10 * time.Minute})
	held, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "v4\n"})
	if held.Status != collectors.DeploymentHeld {
		t.Fatalf("held behind a pending temporary change: %s", held.Status)
	}
	if err := s.ReportStatus(ctx, col, tmp4.Hash, collectors.DeploymentFailed, "bad"); err != nil {
		t.Fatal(err)
	}
	_, free, err := s.ExpireDue(ctx, time.Now())
	if err != nil || len(free) != 1 || free[0] != col || get(held).Status != collectors.DeploymentPending {
		t.Fatalf("held after a failed temporary change: %v %v %s", free, err, get(held).Status)
	}

	// 5. An approved temporary change goes through the same rules.
	prop, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "debug\n", Duration: 10 * time.Minute, Proposed: true})
	if err := s.Release(ctx, col, prop.ID); err != nil || get(prop).Status != collectors.DeploymentPending {
		t.Fatalf("release temporary: %v %s", err, get(prop).Status)
	}
	prop2, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "more debug\n", Duration: 10 * time.Minute, Proposed: true})
	if err := s.Release(ctx, col, prop2.ID); !errors.Is(err, collectors.ErrTemporaryActive) {
		t.Fatalf("approving a second temporary change: %v", err)
	}
}
