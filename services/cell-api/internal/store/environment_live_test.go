//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// #40 against a real ClickHouse: the derived Environment column, the
// read-time setting, and an integration's slice read per environment.
//
//	ENV_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestEnvironmentLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestEnvironmentLive(t *testing.T) {
	addr := os.Getenv("ENV_PROBE_CH")
	if addr == "" {
		t.Skip("set ENV_PROBE_CH")
	}
	t.Cleanup(func() { SetEnvironmentConfig(EnvironmentConfig{}) })
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	s := New(conn)
	run := time.Now().UnixNano()
	svc := fmt.Sprintf("env-svc-%d", run)
	now := time.Now().UTC()

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, ResourceAttributes, SpanAttributes, StatusCode, OrganizationId)`)
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		trace  string
		res    map[string]string
		flow   string
		status string
	}{
		{"p1", map[string]string{"deployment.environment.name": "production"}, "a", "Ok"},
		{"p2", map[string]string{"deployment.environment.name": "production"}, "b", "Error"},
		// The deprecated key still counts, and the current one wins.
		{"t1", map[string]string{"deployment.environment": "test"}, "a", "Error"},
		{"t2", map[string]string{"deployment.environment": "old", "deployment.environment.name": "prd"}, "a", "Ok"},
		{"n1", map[string]string{}, "a", "Ok"},
	}
	for i, r := range rows {
		if err := batch.Append(now.Add(-time.Duration(i+1)*time.Second), fmt.Sprintf("%s-%d", r.trace, run), fmt.Sprintf("s%d", i), "op", svc,
			r.res, map[string]string{"flow": r.flow}, r.status, "env-probe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	from, to := now.Add(-time.Minute), now.Add(time.Minute)
	svcs := []string{svc}

	envs := func() map[string]EnvironmentVolume {
		t.Helper()
		vols, err := s.EnvironmentsIn(ctx, svcs, nil, from, to)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]EnvironmentVolume{}
		for _, v := range vols {
			out[v.Environment] = v
		}
		return out
	}

	got := envs()
	if got["production"].Messages != 2 || got["production"].ErrorTraces != 1 || got["test"].Messages != 1 ||
		got["prd"].Messages != 1 || got[""].Messages != 1 {
		t.Errorf("environments as sent: %+v", got)
	}

	// Aliases fold prd into production at read time; nothing rewritten.
	SetEnvironmentConfig(EnvironmentConfig{Aliases: map[string]string{"PRD": "production"}})
	got = envs()
	if got["production"].Messages != 3 || got["prd"].Messages != 0 {
		t.Errorf("with aliases: %+v", got)
	}

	// The integration's slice read for one environment.
	groups := [][]LogAttrFilter{{{Key: "service.name", Op: AttrOpEq, Value: svc}, {Key: "flow", Op: AttrOpEq, Value: "a"}}}
	total, errs, err := s.DistinctTraceCounts(ctx, svcs, from, to, WithEnvironment(groups, "production"))
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || errs != 0 { // p1 and t2 (prd -> production), flow a
		t.Errorf("production slice: total %d errors %d", total, errs)
	}
	total, _, err = s.DistinctTraceCounts(ctx, svcs, from, to, WithEnvironment(groups, NoEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("(no environment) slice: %d", total)
	}

	// The list's per-environment batch agrees.
	byEnv, err := s.SliceTraceStatsByEnvironment(ctx, []IntegrationSlice{{Key: "k", Services: svcs, Groups: groups}}, SliceQuery{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if byEnv["k"]["production"].Traces != 2 || byEnv["k"]["test"].ErrorTraces != 1 || byEnv["k"][""].Traces != 1 {
		t.Errorf("by environment: %+v", byEnv)
	}

	// A configured key replaces the default ones.
	SetEnvironmentConfig(EnvironmentConfig{Key: "deployment.environment"})
	got = envs()
	if got["old"].Messages != 1 || got["test"].Messages != 1 || got[""].Messages != 3 {
		t.Errorf("custom key: %+v", got)
	}
}
