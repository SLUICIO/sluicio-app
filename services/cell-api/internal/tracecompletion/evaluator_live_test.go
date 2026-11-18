//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The fan-out as reported: one extractor publishes, two loaders
// subscribe, and all three emit the same span name, Step.Send. Runs the
// evaluator's scan and its late-arrival query against a real
// ClickHouse, under service names of its own.
//
//	SUBTREE_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/tracecompletion/ -run TestFanOutLive -v

package tracecompletion

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

func TestFanOutLive(t *testing.T) {
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
	pub, erp, wms, other := fmt.Sprintf("tcprobe-pub-%d", run), fmt.Sprintf("tcprobe-erp-%d", run),
		fmt.Sprintf("tcprobe-wms-%d", run), fmt.Sprintf("tcprobe-other-%d", run)
	both := fmt.Sprintf("tcprobeb%024d", run)  // both loaders delivered
	stuck := fmt.Sprintf("tcprobes%024d", run) // wms never did
	now := time.Now().UTC()
	start := now.Add(-5 * time.Minute)

	batch, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, OrganizationId, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	// The stuck trace runs in an environment sent as "prd", which the
	// cell's setting maps onto "production"; the other names none.
	add := func(trace, id, svc, name string, at time.Time) {
		res := map[string]string{}
		if trace == stuck {
			res["deployment.environment.name"] = "prd"
		}
		if err := batch.Append(at, trace, id, name, svc, "tcprobe", res); err != nil {
			t.Fatal(err)
		}
	}
	for _, tr := range []string{both, stuck} {
		add(tr, "p", pub, "process orders", start)
		add(tr, "e", pub, "Step.Send", start.Add(time.Second))
		add(tr, "i", erp, "Step.Send", start.Add(2*time.Second))
	}
	add(both, "a", wms, "Step.Send", start.Add(3*time.Second))
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}

	spec := RuleSpec{
		Kind: "trace_completion", StartSpanName: "process orders",
		Stages: []Stage{
			{Spans: []SpanMatch{{Service: pub, SpanName: "Step.Send"}}, TimeoutSeconds: 30},
			{Spans: []SpanMatch{{Service: erp, SpanName: "Step.Send"}, {Service: wms, SpanName: "Step.Send"}},
				Require: RequireAll, TimeoutSeconds: 60},
		},
		LookbackSeconds: 3600,
	}
	spec.Normalize()
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	e := &Evaluator{ch: conn}
	services := []string{pub, erp, wms}
	store.SetEnvironmentConfig(store.EnvironmentConfig{Aliases: map[string]string{"prd": "production"}})
	defer store.SetEnvironmentConfig(store.EnvironmentConfig{})

	aggs, err := e.scanTraces(ctx, spec, services, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]TraceClassification{}
	for _, a := range aggs {
		got[a.TraceID] = classify(spec, a, now)
	}
	if c := got[both]; c.State != StateCompleted {
		t.Errorf("both loaders delivered: %+v", c)
	}
	if c := got[stuck]; c.State != StateDelayed || c.DelayedStage != 2 || c.DelayedStageName != wms+": Step.Send" {
		t.Errorf("wms never delivered: %+v", c)
	}
	// Each delayed trace names its environment, read as every other
	// environment read is: through the cell's aliases.
	if c := got[stuck]; c.Environment != "production" {
		t.Errorf("stuck trace environment %q, want production (prd mapped by the alias)", c.Environment)
	}
	if c := got[both]; c.Environment != "" {
		t.Errorf("trace without an environment reads %q", c.Environment)
	}

	// A dry run sees the same: one complete, one stuck at stage 2 waiting
	// for wms, and how far the traces got.
	dry, err := e.DryRun(ctx, spec, services, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Traces != 2 || dry.TracesOnServices != 2 || dry.Completed != 1 || dry.Delayed != 1 ||
		dry.ReachedStage[0] != 2 || dry.ReachedStage[1] != 1 || dry.DelayedAtStage[1] != 1 ||
		len(dry.Samples) != 1 || dry.Samples[0].TraceID != stuck || dry.Samples[0].WaitingFor != wms+": Step.Send" {
		t.Errorf("dry run: %+v", dry)
	}
	// A start span no trace carries follows nothing, out of the two there.
	wrong := spec
	wrong.StartSpanName = "Pipeline.process orders"
	if dry, err := e.DryRun(ctx, wrong, services, now.Add(-time.Hour), now); err != nil || dry.Traces != 0 || dry.TracesOnServices != 2 {
		t.Errorf("wrong start span: %+v %v", dry, err)
	}

	// The late-arrival query: the stuck trace is not closed, and a
	// Step.Send from a service outside the integration does not close it.
	late, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	if err := late.Append(now, stuck, "o", "Step.Send", other, "tcprobe"); err != nil {
		t.Fatal(err)
	}
	if err := late.Send(); err != nil {
		t.Fatal(err)
	}
	stage := spec.EffectiveStages()[1]
	closed, err := e.findClosedTraces(ctx, stage, services, []string{both, stuck})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := closed[stuck]; ok {
		t.Errorf("an outside service's Step.Send closed the stuck trace")
	}
	if at, ok := closed[both]; !ok || at.Sub(start.Add(3*time.Second)).Abs() > time.Millisecond {
		t.Errorf("the delivered trace is closed at its last loader: %v %v", at, ok)
	}
}
