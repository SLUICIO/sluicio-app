// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"strings"
	"testing"
	"time"
)

// The batch is placeholders read in the order the SQL asks for them, four
// copies of the open-error condition among them. Pin that they line up.
func TestSliceTraceStatsSQL(t *testing.T) {
	from := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	dag := func(id string) [][]LogAttrFilter {
		return [][]LogAttrFilter{{
			{Key: "service.name", Op: AttrOpEq, Value: "airflow-scheduler"},
			{Key: "airflow.dag_id", Op: AttrOpEq, Value: id, Descendants: true},
		}}
	}
	slices := []IntegrationSlice{
		{Key: "gl", Services: []string{"airflow-scheduler"}, Groups: dag("gl_posting")},
		{Key: "orders", Services: []string{"airflow-scheduler"}, Groups: dag("orders_export")},
		{Key: "empty"}, // no members: no branch
	}
	q := SliceQuery{From: from, To: to, AckedUntil: map[string]time.Time{
		"airflow-scheduler": from.Add(10 * time.Minute), // inside the window: applies
		"elsewhere":         from.Add(10 * time.Minute), // not a member: ignored
	}}

	sql, args := sliceTraceStatsSQL(slices, q)
	if got := strings.Count(sql, "UNION ALL"); got != 1 {
		t.Fatalf("want two branches, got %d UNION ALLs:\n%s", got, sql)
	}
	if got, want := strings.Count(sql, "?"), len(args); got != want {
		t.Fatalf("%d placeholders, %d args", got, want)
	}
	if args[0] != "gl" {
		t.Errorf("first arg should be the first slice's key, got %v", args[0])
	}
	if got := strings.Count(sql, "NOT (ServiceName = ? AND Timestamp <= ?)"); got != 8 {
		t.Errorf("the member's watermark should appear in each of 4 open-error reads per branch, got %d", got)
	}
	if strings.Contains(sql, "StatusCode = 'Error' AND ServiceName IN") || strings.Contains(sql, "toUInt64(0) AS traces") {
		t.Errorf("a full read must count healthy traffic too:\n%s", sql)
	}

	// No watermark inside the window: the condition is the plain error test.
	sql, args = sliceTraceStatsSQL(slices[:1], SliceQuery{From: from, To: to, AckedUntil: map[string]time.Time{"airflow-scheduler": from.Add(-time.Hour)}})
	if strings.Contains(sql, "NOT (ServiceName") {
		t.Errorf("a watermark before the window changes nothing, yet it was rendered:\n%s", sql)
	}
	if got, want := strings.Count(sql, "?"), len(args); got != want {
		t.Fatalf("%d placeholders, %d args", got, want)
	}

	// Errors only: the healthy traffic is not read.
	sql, args = sliceTraceStatsSQL(slices[:1], SliceQuery{From: from, To: to, ErrorsOnly: true})
	if !strings.Contains(sql, "AND StatusCode = 'Error'") || !strings.Contains(sql, "toUInt64(0) AS traces") {
		t.Errorf("errors-only should filter to error spans and skip the traffic count:\n%s", sql)
	}
	if got, want := strings.Count(sql, "?"), len(args); got != want {
		t.Fatalf("%d placeholders, %d args", got, want)
	}

	if sql, _ := sliceTraceStatsSQL([]IntegrationSlice{{Key: "empty"}}, q); sql != "" {
		t.Errorf("no members, no statement; got:\n%s", sql)
	}
}
