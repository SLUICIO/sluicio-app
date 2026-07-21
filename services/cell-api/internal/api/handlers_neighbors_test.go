// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"reflect"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// TestGroupNeighborRows checks the pure transformation from
// store-layer ServiceNeighborRow into the two-list NeighborsResponse
// shape. The function is the only spot in the dependency-suggestion
// path that does any real bookkeeping outside of SQL, so it carries
// the bulk of the unit-test coverage for the feature.
//
// Tests are table-driven; each case names the property under test so
// failures point at the specific invariant that broke.
func TestGroupNeighborRows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		rows     []store.ServiceNeighborRow
		wantUp   []ServiceNeighbor
		wantDown []ServiceNeighbor
	}{
		{
			name:     "empty input produces empty (non-nil) arrays",
			rows:     nil,
			wantUp:   []ServiceNeighbor{},
			wantDown: []ServiceNeighbor{},
		},
		{
			name: "splits by direction",
			rows: []store.ServiceNeighborRow{
				{Direction: "upstream", ServiceName: "api-gateway", TraceCount: 50, ErrorCount: 2},
				{Direction: "downstream", ServiceName: "ocr", TraceCount: 40, ErrorCount: 1},
				{Direction: "downstream", ServiceName: "classifier", TraceCount: 30, ErrorCount: 0},
			},
			wantUp: []ServiceNeighbor{
				{ServiceName: "api-gateway", TraceCount: 50, ErrorCount: 2},
			},
			wantDown: []ServiceNeighbor{
				{ServiceName: "ocr", TraceCount: 40, ErrorCount: 1},
				{ServiceName: "classifier", TraceCount: 30, ErrorCount: 0},
			},
		},
		{
			name: "preserves SQL ordering within a direction",
			rows: []store.ServiceNeighborRow{
				// SQL orders by trace_count DESC inside each direction.
				{Direction: "upstream", ServiceName: "a", TraceCount: 100, ErrorCount: 0},
				{Direction: "upstream", ServiceName: "b", TraceCount: 10, ErrorCount: 0},
				{Direction: "upstream", ServiceName: "c", TraceCount: 1, ErrorCount: 0},
			},
			wantUp: []ServiceNeighbor{
				{ServiceName: "a", TraceCount: 100, ErrorCount: 0},
				{ServiceName: "b", TraceCount: 10, ErrorCount: 0},
				{ServiceName: "c", TraceCount: 1, ErrorCount: 0},
			},
			wantDown: []ServiceNeighbor{},
		},
		{
			name: "same service in both directions appears in both lists",
			rows: []store.ServiceNeighborRow{
				// This is a real case: a worker service that pulls work
				// from a queue (upstream of the focal service) and also
				// reports completion back to it (downstream).
				{Direction: "upstream", ServiceName: "worker", TraceCount: 12, ErrorCount: 0},
				{Direction: "downstream", ServiceName: "worker", TraceCount: 12, ErrorCount: 0},
			},
			wantUp: []ServiceNeighbor{
				{ServiceName: "worker", TraceCount: 12, ErrorCount: 0},
			},
			wantDown: []ServiceNeighbor{
				{ServiceName: "worker", TraceCount: 12, ErrorCount: 0},
			},
		},
		{
			name: "defensive dedup sums duplicates within a direction",
			rows: []store.ServiceNeighborRow{
				// Shouldn't happen given the SQL GROUP BY but the
				// helper is defensive — duplicate rows are merged
				// rather than producing duplicate suggestions.
				{Direction: "downstream", ServiceName: "storage", TraceCount: 10, ErrorCount: 1},
				{Direction: "downstream", ServiceName: "storage", TraceCount: 5, ErrorCount: 2},
			},
			wantUp: []ServiceNeighbor{},
			wantDown: []ServiceNeighbor{
				{ServiceName: "storage", TraceCount: 15, ErrorCount: 3},
			},
		},
		{
			name: "unknown direction is silently dropped",
			rows: []store.ServiceNeighborRow{
				{Direction: "sideways", ServiceName: "weird", TraceCount: 9},
				{Direction: "downstream", ServiceName: "ok", TraceCount: 1},
			},
			wantUp: []ServiceNeighbor{},
			wantDown: []ServiceNeighbor{
				{ServiceName: "ok", TraceCount: 1, ErrorCount: 0},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotUp, gotDown := groupNeighborRows(tc.rows)
			if !reflect.DeepEqual(gotUp, tc.wantUp) {
				t.Errorf("upstream mismatch\n got: %#v\nwant: %#v", gotUp, tc.wantUp)
			}
			if !reflect.DeepEqual(gotDown, tc.wantDown) {
				t.Errorf("downstream mismatch\n got: %#v\nwant: %#v", gotDown, tc.wantDown)
			}
		})
	}
}

// TestFilterVisibleNeighbors — invisible services must vanish from the
// adjacency list entirely (RBAC deny-by-default: no names, no counts).
func TestFilterVisibleNeighbors(t *testing.T) {
	t.Parallel()
	in := []ServiceNeighbor{
		{ServiceName: "a", TraceCount: 3},
		{ServiceName: "b", TraceCount: 5},
		{ServiceName: "c", TraceCount: 1},
	}
	visible := map[string]struct{}{"a": {}, "c": {}}
	got := filterVisibleNeighbors(in, visible)
	want := []ServiceNeighbor{{ServiceName: "a", TraceCount: 3}, {ServiceName: "c", TraceCount: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if got := filterVisibleNeighbors(in, map[string]struct{}{}); len(got) != 0 {
		t.Fatalf("empty visible set must yield empty list, got %+v", got)
	}
}
