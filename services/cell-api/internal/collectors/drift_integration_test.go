// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

package collectors_test

import (
	"testing"
	"time"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
)

// Drift is checked when a collector reports its configuration, against
// the last deployment it applied, and kept on the collector.
func TestCollectorDrift(t *testing.T) {
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
	scope := collectors.Scope{OrgID: org, All: true}
	drift := func() *collectors.Drift {
		t.Helper()
		c, err := s.Get(ctx, scope, col)
		if err != nil {
			t.Fatal(err)
		}
		return c.Drift
	}
	report := func(raw string) {
		t.Helper()
		if _, err := s.RecordConfig(ctx, col, []collectors.ConfigFile{{Body: "effective: " + raw[:5]}, {Name: collectors.RawConfigFile, Body: raw}}); err != nil {
			t.Fatal(err)
		}
	}
	sent := "receivers:\n  host_metrics:\n    scrapers:\n      load: {}\n      memory: {}\n"

	// Nothing rolled out: not applicable.
	report("receivers:\n  host_metrics:\n    scrapers:\n      load: {}\n")
	if d := drift(); d != nil {
		t.Fatalf("drift with no deployment: %+v", d)
	}

	// Sent and on its way: nothing changes until it is applied and the
	// collector has reported since.
	d, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: sent})
	report("receivers:\n  host_metrics:\n    scrapers:\n      cpu: {}\n")
	if got := drift(); got != nil {
		t.Fatalf("checked while a deployment is on its way: %+v", got)
	}
	if err := s.ReportStatus(ctx, col, d.Hash, collectors.DeploymentApplied, ""); err != nil {
		t.Fatal(err)
	}
	if got := drift(); got != nil {
		t.Fatalf("checked against a configuration reported before the deployment applied: %+v", got)
	}
	time.Sleep(10 * time.Millisecond)
	report("receivers:\n  host_metrics:\n    collection_interval: 30s\n    scrapers:\n      load: {}\n      memory: {}\n")
	if got := drift(); got == nil || got.DeploymentID != d.ID || len(got.Paths) != 0 {
		t.Fatalf("in sync after applying: %+v", got)
	}

	// The order a supervisor reports in: sent, the merged configuration
	// (which includes it) at once, "applied" seconds later.
	d2, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "receivers:\n  host_metrics:\n    scrapers:\n      paging: {}\n"})
	if err := s.MarkSent(ctx, d2.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	report("receivers:\n  host_metrics:\n    scrapers:\n      load: {}\n      paging: {}\n")
	if err := s.ReportStatus(ctx, col, d2.Hash, collectors.DeploymentApplied, ""); err != nil {
		t.Fatal(err)
	}
	if got := drift(); got == nil || got.DeploymentID != d2.ID || len(got.Paths) != 0 {
		t.Fatalf("in sync when the merged configuration came before \"applied\": %+v", got)
	}

	// The supervisor loses its stored remote configuration and runs its
	// local files only: what was sent is gone.
	report("receivers:\n  host_metrics:\n    scrapers:\n      load: {}\n")
	got := drift()
	if got == nil || len(got.Paths) != 1 || got.Paths[0].Path != "receivers.host_metrics.scrapers.paging" || got.Paths[0].Kind != "missing" {
		t.Fatalf("drifted: %+v", got)
	}

	// Everything sent is there, but the local files gained a scraper
	// since the rollout: changed outside Sluicio.
	report("receivers:\n  host_metrics:\n    scrapers:\n      load: {}\n      paging: {}\n      cpu: {}\n")
	got = drift()
	if got == nil || len(got.Paths) != 1 || got.Paths[0].Path != "receivers.host_metrics.scrapers.cpu" || got.Paths[0].Kind != "added" {
		t.Fatalf("added outside Sluicio: %+v", got)
	}

	// Accepted: the change on the host stays, nothing is sent, drift is
	// measured from here.
	before, _ := s.Deployments(ctx, col, 50)
	acc, err := s.AcceptDrift(ctx, col, "editor@acme")
	if err != nil || acc == nil || len(acc.Paths) != 0 || acc.AcceptedBy != "editor@acme" || acc.AcceptedAt == nil {
		t.Fatalf("accept: %+v %v", acc, err)
	}
	if after, _ := s.Deployments(ctx, col, 50); len(after) != len(before) {
		t.Fatal("accepting sent something to the collector")
	}
	report("receivers:\n  host_metrics:\n    scrapers:\n      load: {}\n      paging: {}\n      cpu: {}\n      disk: {}\n")
	if got := drift(); got == nil || len(got.Paths) != 1 || got.Paths[0].Path != "receivers.host_metrics.scrapers.disk" || got.AcceptedBy == "" {
		t.Fatalf("drift after accepting: %+v", got)
	}

	// A new rollout sets the baseline again.
	d3, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: "receivers:\n  host_metrics:\n    scrapers:\n      disk: {}\n"})
	_ = s.MarkSent(ctx, d3.ID)
	time.Sleep(10 * time.Millisecond)
	report("receivers:\n  host_metrics:\n    scrapers:\n      load: {}\n      paging: {}\n      cpu: {}\n      disk: {}\n      network: {}\n")
	_ = s.ReportStatus(ctx, col, d3.Hash, collectors.DeploymentApplied, "")
	if got := drift(); got == nil || got.DeploymentID != d3.ID || len(got.Paths) != 0 || got.AcceptedAt != nil {
		t.Fatalf("after a new rollout: %+v", got)
	}
}
