//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// #39 against a real ClickHouse: in "one trace through every service"
// mode, a member's "or" is one requirement, not one per alternative.
//
//	SUBTREE_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestMembersLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestMembersLive(t *testing.T) {
	addr := os.Getenv("SUBTREE_PROBE_CH")
	if addr == "" {
		t.Skip("set SUBTREE_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	s := New(conn)
	run := time.Now().UnixNano()
	a, b := fmt.Sprintf("members-a-%d", run), fmt.Sprintf("members-b-%d", run)
	now := time.Now().UTC()
	tid := fmt.Sprintf("trace%025d", run)
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, SpanAttributes, StatusCode, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	// One trace: a span of A with x = 1 (and none with x = 2), a span of B with b = 3.
	_ = batch.Append(now.Add(-2*time.Second), tid, "s1", "a", a, map[string]string{"x": "1"}, "Ok", "members-probe")
	_ = batch.Append(now.Add(-1*time.Second), tid, "s2", "b", b, map[string]string{"b": "3"}, "Ok", "members-probe")
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	m := func(n int) *int { return &n }
	groups := func(withMembers bool) [][]LogAttrFilter {
		mem := func(n int) *int {
			if withMembers {
				return m(n)
			}
			return nil
		}
		f := func(k, v string, member int) LogAttrFilter {
			return LogAttrFilter{Key: k, Op: AttrOpEq, Value: v, RequireAllGroups: true, Member: mem(member)}
		}
		return [][]LogAttrFilter{
			{f("service.name", a, 0), f("x", "1", 0)},
			{f("service.name", a, 0), f("x", "2", 0)},
			{f("service.name", b, 1), f("b", "3", 1)},
		}
	}
	from, to := now.Add(-time.Minute), now.Add(time.Minute)
	svcs := []string{a, b}
	before, _, err := s.DistinctTraceCounts(ctx, svcs, from, to, groups(false))
	if err != nil {
		t.Fatal(err)
	}
	after, _, err := s.DistinctTraceCounts(ctx, svcs, from, to, groups(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("every group required: %d trace(s); one group per member: %d trace(s)", before, after)
	if before != 0 {
		t.Errorf("the old reading should require x = 1 AND x = 2 and so find nothing, found %d", before)
	}
	if after != 1 {
		t.Errorf("with members the trace belongs: want 1, got %d", after)
	}
}
