// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Trace demand is the half of the ledger that was missing (issue #1).
// Demand was recorded from the log views and the metrics explorer only,
// so a cell whose people live in Messages recorded nothing at all and
// the advisor reported "0 days of consumption history" forever.
//
// The recording itself needs a live request and a writer; what is
// testable in isolation is the two decisions that shape WHAT gets
// recorded, and those are the parts that would go quietly wrong.

package api

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/messageviews"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

func TestMessageAttrKeys(t *testing.T) {
	t.Run("takes payload paths", func(t *testing.T) {
		got := messageAttrKeys([]messageviews.Filter{
			{Field: messageviews.FieldPayload, FieldPath: "order.id", Value: "7"},
			{Field: messageviews.FieldPayload, FieldPath: "customer.vat", Value: "SE1"},
		})
		want := []string{"order.id", "customer.vat"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("ignores structural fields", func(t *testing.T) {
		// service/status/time are properties of every span. Recording
		// them as attribute demand would make each look load-bearing
		// while saying nothing about which payload fields earn their
		// cost, which is the only question T3 asks.
		got := messageAttrKeys([]messageviews.Filter{
			{Field: messageviews.FieldService, Value: "billing"},
			{Field: messageviews.FieldStatus, Value: "error"},
			{Field: messageviews.FieldTime, Value: "24h"},
			{Field: messageviews.FieldIntegration, Value: "paperless"},
			{Field: messageviews.FieldTraceID, Value: "abc"},
		})
		if len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})

	t.Run("skips a payload filter with no path", func(t *testing.T) {
		// Free-text payload search names no key; recording "" would
		// register whole-signal demand twice.
		got := messageAttrKeys([]messageviews.Filter{
			{Field: messageviews.FieldPayload, Value: "invoice"},
		})
		if len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})

	t.Run("survives an empty filter set", func(t *testing.T) {
		if got := messageAttrKeys(nil); len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})
}

func TestSpanServices(t *testing.T) {
	rows := []store.SpanRow{
		{ServiceName: "gateway"},
		{ServiceName: "billing"},
		{ServiceName: "gateway"},
		{ServiceName: ""},
		{ServiceName: "ledger"},
		{ServiceName: "billing"},
	}
	got := spanServices(rows)

	// Every service the trace crosses, once each: opening a trace is
	// demand on the whole flow, and crediting only the entry point would
	// have the advisor recommend dropping the middle of every hop chain
	// anyone reads.
	want := []string{"gateway", "billing", "ledger"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (first-seen order)", got, want)
		}
	}

	if got := spanServices(nil); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}
