//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Payload links against a real ClickHouse: every span of the asked-for
// traces that carries a link attribute comes back with only those values,
// a span without one does not, and the service list narrows the read.
//
//	PAYLOAD_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestPayloadLinkSpansLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetmappings"
)

func TestPayloadLinkSpansLive(t *testing.T) {
	addr := os.Getenv("PAYLOAD_PROBE_CH")
	if addr == "" {
		t.Skip("set PAYLOAD_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	s := New(conn)
	run := time.Now().UnixNano()
	mft := fmt.Sprintf("pl-mft-%d", run)
	arc := fmt.Sprintf("pl-arc-%d", run)
	tid := fmt.Sprintf("pl%030d", run)
	other := fmt.Sprintf("po%030d", run)
	now := time.Now().UTC()

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, SpanAttributes, OrganizationId)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		at          time.Duration
		trace, span string
		svc, name   string
		attrs       map[string]string
	}{
		{0, tid, "root", mft, "transfer", map[string]string{"file.name": "INV_42.pdf"}},
		{time.Second, tid, "pickup", mft, "pickup", map[string]string{"io.role": "input", "io.payload.url": "https://a.example.com/in/INV_42.pdf"}},
		{2 * time.Second, tid, "deliver", mft, "deliver", map[string]string{"io.role": "output", "io.payload.url": "https://a.example.com/out/INV_42.pdf", "other": "x"}},
		{3 * time.Second, tid, "store", arc, "store", map[string]string{"archive.path": "2026/INV_42.pdf"}},
		{3 * time.Second, tid, "empty", arc, "store", map[string]string{"archive.path": ""}},
		{0, other, "x", mft, "pickup", map[string]string{"io.payload.url": "https://a.example.com/in/other.pdf"}},
	} {
		if err := batch.Append(now.Add(-time.Minute+row.at), row.trace, row.span, row.name, row.svc, row.attrs, "payload-probe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	keys := []string{"io.payload.url", "archive.path"}
	from := now.Add(-time.Hour)

	got, err := s.PayloadLinkSpans(ctx, []string{tid}, keys, nil, from, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want pickup, deliver and store, got %+v", got)
	}
	if got[0].SpanID != "pickup" || got[0].Role != "input" || got[0].Values["io.payload.url"] != "https://a.example.com/in/INV_42.pdf" {
		t.Errorf("first: %+v", got[0])
	}
	if _, leaked := got[1].Values["other"]; leaked || got[1].Role != "output" {
		t.Errorf("deliver carries only the link values: %+v", got[1])
	}
	if got[2].ServiceName != arc || got[2].Values["archive.path"] != "2026/INV_42.pdf" {
		t.Errorf("store: %+v", got[2])
	}

	got, err = s.PayloadLinkSpans(ctx, []string{tid}, keys, []string{mft}, from, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("narrowed to %s: %+v", mft, got)
	}

	// A facet mapping gives the archiver's span a role it does not carry;
	// a span that carries io.role keeps its own.
	roleExpr, roleArgs := facetmappings.RoleExprByService([]facetmappings.Mapping{
		{ServiceName: arc, AttributeSource: facetmappings.AttrSourceSpan, AttributeKey: "archive.path", MatchOperator: facetmappings.OperatorExists, SetIORole: "output"},
		{ServiceName: mft, AttributeSource: facetmappings.AttrSourceSpan, AttributeKey: "io.payload.url", MatchOperator: facetmappings.OperatorExists, SetIORole: "output"},
	})
	got, err = s.PayloadLinkSpans(ctx, []string{tid}, keys, nil, from, roleExpr, roleArgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Role != "input" || got[1].Role != "output" || got[2].Role != "output" {
		t.Fatalf("roles with mappings: %+v", got)
	}
}
