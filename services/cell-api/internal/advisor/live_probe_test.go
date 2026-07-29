//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A manual probe, not part of the suite (build tag `liveprobe`). Runs a
// real evaluation against a live cell so the evaluators can be seen
// producing findings from actual telemetry rather than fixtures.
//
//	go test -tags liveprobe ./services/cell-api/internal/advisor/ -run TestLiveProbe -v \
//	  -args  # needs ADVISOR_PROBE_CH and ADVISOR_PROBE_PG

package advisor

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLiveProbe(t *testing.T) {
	chAddr := os.Getenv("ADVISOR_PROBE_CH")
	pgDSN := os.Getenv("ADVISOR_PROBE_PG")
	if chAddr == "" || pgDSN == "" {
		t.Skip("set ADVISOR_PROBE_CH and ADVISOR_PROBE_PG")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{chAddr},
		Auth: clickhouse.Auth{Database: "telemetry", Username: os.Getenv("ADVISOR_PROBE_CH_USER"), Password: os.Getenv("ADVISOR_PROBE_CH_PASS")},
	})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	now := time.Now()
	// Window from env so the probe can be pointed at whatever history
	// the cell actually has.
	win := 2 * 24 * time.Hour
	if v := os.Getenv("ADVISOR_PROBE_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			win = d
		}
	}
	from := now.Add(-win)

	dem, err := LoadDemand(ctx, conn, orgID, now.Add(-evidenceHorizon))
	if err != nil {
		t.Fatalf("demand: %v", err)
	}
	t.Logf("ledger earliest=%v mature(2d)=%v", dem.Earliest.Format("2006-01-02"), dem.Mature(from))

	tele, err := EvaluateTelemetry(ctx, TelemetryInput{
		OrgID: orgID, Conn: conn, Demand: dem, From: from, To: now,
		IntegrationServices: map[string]bool{},
	})
	if err != nil {
		t.Fatalf("telemetry evaluation: %v", err)
	}
	t.Logf("TELEMETRY findings: %d", len(tele))
	for _, s := range tele {
		t.Logf("  [%s] %s", s.Class, s.Title)
		t.Logf("        loss: %.110s", s.Loss)
		t.Logf("        evidence: %v", s.Evidence)
		if s.Loss == "" {
			t.Errorf("%s has no loss statement", s.Fingerprint)
		}
		if s.Class != "F1" && s.Advisor == "telemetry" && s.Snippet == "" {
			t.Errorf("%s has no snippet", s.Fingerprint)
		}
	}

	fat, err := EvaluateFatigue(ctx, FatigueInput{
		OrgID: orgID, Pool: pool, Demand: dem, From: from, To: now,
	})
	if err != nil {
		t.Fatalf("fatigue evaluation: %v", err)
	}
	t.Logf("ALERTING findings: %d", len(fat))
	for _, s := range fat {
		t.Logf("  [%s] %s", s.Class, s.Title)
		if s.Snippet != "" {
			t.Errorf("%s carries a collector snippet; alerting changes live in Sluicio", s.Fingerprint)
		}
	}
	_ = slog.Default()
}
