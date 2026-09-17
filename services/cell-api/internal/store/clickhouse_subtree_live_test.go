//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Runs the subtree SQL against a real ClickHouse, because arrayFold over
// tuples is exactly the kind of thing that reads right and does not run.
// Writes one synthetic trace under its own organisation id, so a real
// organisation's reads never see it.
//
//	SUBTREE_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestSubtreeLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestSubtreeLive(t *testing.T) {
	addr := os.Getenv("SUBTREE_PROBE_CH")
	if addr == "" {
		t.Skip("set SUBTREE_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	trace := fmt.Sprintf("subtreeprobe%020d", time.Now().UnixNano())
	now := time.Now().UTC()

	//   root
	//   ├── a            abc=123  <- anchor
	//   │   └── a1
	//   │       └── a11
	//   ├── b            (sibling)
	//   └── c            abc=999
	spans := []struct{ id, parent, abc string }{
		{"root", "", ""}, {"a", "root", "123"}, {"a1", "a", ""}, {"a11", "a1", ""}, {"b", "root", ""}, {"c", "root", "999"},
	}
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO traces (Timestamp, TraceId, SpanId, ParentSpanId, SpanName, ServiceName, SpanAttributes, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range spans {
		attrs := map[string]string{}
		if s.abc != "" {
			attrs["abc"] = s.abc
		}
		if err := batch.Append(now.Add(time.Duration(i)*time.Millisecond), trace, s.id, s.parent, s.id, "subtree-probe", attrs, "subtree-probe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}

	from, to := now.Add(-time.Minute), now.Add(time.Minute)
	query := func(groups [][]LogAttrFilter) []string {
		clause, args := attrGroupsClause("SpanAttributes", groups, from, to)
		rows, err := conn.Query(ctx, "SELECT SpanId FROM traces WHERE TraceId = ? AND "+clause, append([]any{trace}, args...)...)
		if err != nil {
			t.Fatalf("%v\n%s", err, clause)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
		sort.Strings(out)
		return out
	}

	if got := strings.Join(query(abc123(false)), ","); got != "a" {
		t.Errorf("plain: got %s, want a", got)
	}
	if got := strings.Join(query(abc123(true)), ","); got != "a,a1,a11" {
		t.Errorf("descendants: got %s, want a,a1,a11", got)
	}
	// Another condition in the group chooses the anchor; the children
	// follow although none of them is named "a".
	anchorByName := [][]LogAttrFilter{{
		{Key: "abc", Op: AttrOpEq, Value: "123", Descendants: true},
		{Key: "span.name", Op: AttrOpEq, Value: "a"},
	}}
	if got := strings.Join(query(anchorByName), ","); got != "a,a1,a11" {
		t.Errorf("anchor with two conditions: got %s, want a,a1,a11", got)
	}
	// A subtree OR-ed with a plain group.
	either := [][]LogAttrFilter{
		{{Key: "abc", Op: AttrOpEq, Value: "123", Descendants: true}},
		{{Key: "abc", Op: AttrOpEq, Value: "999"}},
	}
	if got := strings.Join(query(either), ","); got != "a,a1,a11,c" {
		t.Errorf("subtree OR plain: got %s, want a,a1,a11,c", got)
	}

	// Logs: a log written inside a1 carries no abc of its own and is
	// reached only through its span. One inside b is not reached.
	logBatch, err := conn.PrepareBatch(ctx, "INSERT INTO logs (Timestamp, TraceId, SpanId, ServiceName, Body, OrganizationId)")
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range []string{"a1", "b"} {
		if err := logBatch.Append(now, trace, sp, "subtree-probe", "log in "+sp, "subtree-probe"); err != nil {
			t.Fatal(err)
		}
	}
	if err := logBatch.Send(); err != nil {
		t.Fatal(err)
	}
	clause, args := attrGroupsClause("LogAttributes", abc123(true), from, to)
	rows, err := conn.Query(ctx, "SELECT SpanId FROM logs WHERE TraceId = ? AND "+clause, append([]any{trace}, args...)...)
	if err != nil {
		t.Fatalf("logs: %v\n%s", err, clause)
	}
	defer rows.Close()
	var logSpans []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		logSpans = append(logSpans, id)
	}
	if got := strings.Join(logSpans, ","); got != "a1" {
		t.Errorf("logs: got %s, want a1", got)
	}
}
