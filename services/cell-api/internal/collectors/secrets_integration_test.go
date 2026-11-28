// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

package collectors_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
)

// A configuration as written with a secret in plain text is flagged on
// the collector, and its team told once per new path.
func TestCollectorPlainSecrets(t *testing.T) {
	pool, ctx := newDB(t)
	org := seedOrg(t, ctx, pool, "acme")
	team := seedTeam(t, ctx, pool, org, "platform")
	s := collectors.New(pool, newKeys(t))
	var told []collectors.PlainSecretsNotice
	s.OnPlainSecrets = func(_ context.Context, n collectors.PlainSecretsNotice) { told = append(told, n) }
	_, tok, _ := s.CreateToken(ctx, org, team, "t", nil)
	caps := uint64(1 | 2 | 4 | 2048)
	col, _, err := s.Observe(ctx, collectors.Observation{OrgID: org, GroupID: team, TokenID: tok.ID, InstanceUID: "gw", Transport: "websocket", Capabilities: &caps,
		Description: map[string]string{"host.name": "gw-1"}})
	if err != nil {
		t.Fatal(err)
	}
	scope := collectors.Scope{OrgID: org, All: true}
	get := func() collectors.Collector {
		t.Helper()
		c, err := s.Get(ctx, scope, col)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	report := func(files ...collectors.ConfigFile) {
		t.Helper()
		if _, err := s.RecordConfig(ctx, col, files); err != nil {
			t.Fatal(err)
		}
	}
	ref := "exporters:\n  otlp_http/sluicio:\n    headers:\n      authorization: Bearer ${env:TOKEN}\n"
	plain := "exporters:\n  otlp_http/sluicio:\n    headers:\n      authorization: Bearer abc\n"
	both := plain + "  otlp_http/b:\n    headers:\n      x-api-key: k\n"

	// Only the effective configuration: a resolved reference reads as
	// plain text there, so nothing is told.
	report(collectors.ConfigFile{Body: plain})
	if c := get(); len(c.PlainSecrets) != 0 || len(told) != 0 {
		t.Fatalf("flagged from the effective configuration: %+v %v", c.PlainSecrets, told)
	}

	report(collectors.ConfigFile{Body: "e1"}, collectors.ConfigFile{Name: collectors.RawConfigFile, Body: ref})
	if c := get(); len(c.PlainSecrets) != 0 || c.PlainSecretsAt != nil {
		t.Fatalf("a reference flagged: %+v", c.PlainSecrets)
	}

	report(collectors.ConfigFile{Body: "e2"}, collectors.ConfigFile{Name: collectors.RawConfigFile, Body: plain})
	c := get()
	if strings.Join(c.PlainSecrets, ",") != "exporters.otlp_http/sluicio.headers.authorization" || c.PlainSecretsAt == nil {
		t.Fatalf("plain text not flagged: %+v", c)
	}
	if len(told) != 1 || told[0].GroupID != team || told[0].Name != "gw-1" || len(told[0].Added) != 1 {
		t.Fatalf("told: %+v", told)
	}
	since := *c.PlainSecretsAt

	// A second secret: told about that one only; the first is still dated
	// from when it appeared.
	report(collectors.ConfigFile{Body: "e3"}, collectors.ConfigFile{Name: collectors.RawConfigFile, Body: both})
	if c := get(); len(c.PlainSecrets) != 2 || !c.PlainSecretsAt.Equal(since) {
		t.Fatalf("second secret: %+v", c)
	}
	if len(told) != 2 || strings.Join(told[1].Added, ",") != "exporters.otlp_http/b.headers.x-api-key" || len(told[1].All) != 2 {
		t.Fatalf("told the second time: %+v", told)
	}

	// An effective-only report changes nothing; moving them into
	// variables clears the flag.
	report(collectors.ConfigFile{Body: "e4"})
	if c := get(); len(c.PlainSecrets) != 2 {
		t.Fatalf("an effective-only report cleared the flag: %+v", c.PlainSecrets)
	}
	report(collectors.ConfigFile{Body: "e5"}, collectors.ConfigFile{Name: collectors.RawConfigFile, Body: ref})
	if c := get(); len(c.PlainSecrets) != 0 || c.PlainSecretsAt != nil || len(told) != 2 {
		t.Fatalf("not cleared: %+v", c)
	}

	// A collector reports an opaque value as [REDACTED], literal or not;
	// what Sluicio sent it is known as written, so a secret in the
	// configuration it applied is found there.
	redacted := "exporters:\n  otlp_http/sluicio:\n    headers:\n      authorization: \"[REDACTED]\"\n"
	report(collectors.ConfigFile{Body: "e6"}, collectors.ConfigFile{Name: collectors.RawConfigFile, Body: redacted})
	if c := get(); len(c.PlainSecrets) != 0 {
		t.Fatalf("[REDACTED] counted: %+v", c.PlainSecrets)
	}
	d, err := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: plain})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReportStatus(ctx, col, d.Hash, collectors.DeploymentApplied, ""); err != nil {
		t.Fatal(err)
	}
	if c := get(); strings.Join(c.PlainSecrets, ",") != "exporters.otlp_http/sluicio.headers.authorization" || len(told) != 3 {
		t.Fatalf("a secret sent as written not found: %+v %d", c.PlainSecrets, len(told))
	}
	d2, _ := s.CreateDeployment(ctx, collectors.NewDeployment{CollectorID: col, Body: ref})
	if err := s.ReportStatus(ctx, col, d2.Hash, collectors.DeploymentApplied, ""); err != nil {
		t.Fatal(err)
	}
	if c := get(); len(c.PlainSecrets) != 0 {
		t.Fatalf("still flagged after a configuration with a reference applied: %+v", c.PlainSecrets)
	}
}
