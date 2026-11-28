// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

package collectors_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
)

// Deployments sent to one collector at the same moment leave one to
// deliver: each supersedes what came before it, and none stays pending
// behind a newer one, where nothing would ever deliver it.
func TestConcurrentDeploymentsLeaveOnePending(t *testing.T) {
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
	for round := 0; round < 5; round++ {
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, err := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: fmt.Sprintf("n: %d-%d\n", round, i)})
				errs <- err
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var pending int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM collector_deployments WHERE collector_id = $1 AND status IN ('pending', 'sent')`, col).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending != 1 {
			t.Fatalf("round %d: %d deployments waiting to be delivered, want 1", round, pending)
		}
	}
}
