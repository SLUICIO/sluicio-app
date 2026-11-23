//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Where each message of a list stands against a completion rule, read
// for those messages alone, against a real ClickHouse: complete, waiting
// at a stage, delayed past it, and a trace the rule does not follow
// (no start span), which the evaluator's own scan never returns.
//
//	SUBTREE_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/tracecompletion/ -run TestStatusOfLive -v

package tracecompletion

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
)

type oneIntegration []string

func (s oneIntegration) IntegrationServices(context.Context, uuid.UUID) ([]string, error) {
	return s, nil
}

func TestStatusOfLive(t *testing.T) {
	addr := os.Getenv("SUBTREE_PROBE_CH")
	if addr == "" {
		t.Skip("set SUBTREE_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	run := time.Now().UnixNano()
	extractor, sender := fmt.Sprintf("stprobe-ext-%d", run), fmt.Sprintf("stprobe-sftp-%d", run)
	id := func(tag string) string { return fmt.Sprintf("st%s%029d", tag, run) }
	done, waiting, late, unrelated, absent := id("d"), id("w"), id("l"), id("u"), id("a")
	now := time.Now().UTC()

	batch, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	add := func(trace, span, svc, name string, at time.Time) {
		if err := batch.Append(at, trace, span, name, svc, "stprobe"); err != nil {
			t.Fatal(err)
		}
	}
	add(done, "s", extractor, "Process.m3-ftp", now.Add(-10*time.Minute))
	add(done, "c", sender, "create iqpl-sftp", now.Add(-9*time.Minute))
	add(waiting, "s", extractor, "Process.m3-ftp", now.Add(-time.Minute))
	add(late, "s", extractor, "Process.m3-ftp", now.Add(-30*time.Minute))
	add(unrelated, "x", extractor, "Health.ping", now.Add(-2*time.Minute))
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}

	rule := Rule{ID: uuid.New(), Name: "Send step must complete", Enabled: true, Spec: RuleSpec{
		Kind: "trace_completion", StartSpanName: "Process.m3-ftp",
		Stages:          []Stage{{Spans: []SpanMatch{{SpanName: "create iqpl-sftp"}}, TimeoutSeconds: 20 * 60}},
		LookbackSeconds: 3600,
	}}
	rule.Spec.Normalize()
	e := &Evaluator{ch: conn, catalog: oneIntegration{extractor, sender}}

	got, err := e.StatusOf(ctx, rule, []string{done, waiting, late, unrelated, absent}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for trace, want := range map[string]struct {
		state   string
		stage   int
		reached int
	}{
		done:      {StatusComplete, 0, 1},
		waiting:   {StatusWaiting, 1, 0},
		late:      {StatusDelayed, 1, 0},
		unrelated: {StatusNotStarted, 0, 0},
		absent:    {StatusNotStarted, 0, 0},
	} {
		st := got[trace]
		if st.State != want.state || st.Stage != want.stage || st.Reached != want.reached || st.Stages != 1 {
			t.Errorf("%s: %+v, want %s at stage %d with %d reached", trace, st, want.state, want.stage, want.reached)
		}
	}
	if st := got[late]; st.StageName != "create iqpl-sftp" {
		t.Errorf("delayed stage named %q", st.StageName)
	}
}
