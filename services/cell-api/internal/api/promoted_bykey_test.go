// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// promotedByKey joins two lists that are indexed differently: the column
// set contains built-ins AND attributes, while the value slice contains
// only the attributes (MessageColumnKeys skips built-ins).
//
// Getting that wrong does not fail. It renders the right values under
// the wrong headings — a document count in a column labelled "From" —
// which reads as real data and survives every check except someone
// looking at the table. Hence a test that walks the exact shape.

package api

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
)

func builtin(key string) integrations.MessageColumn {
	return integrations.MessageColumn{Kind: integrations.ColumnKindBuiltin, Key: key, Label: key}
}
func attr(key string) integrations.MessageColumn {
	return integrations.MessageColumn{Kind: integrations.ColumnKindAttribute, Key: key, Label: key}
}

func TestPromotedByKeySkipsBuiltinsWhenAligning(t *testing.T) {
	// A built-in FIRST is the case that shifts everything: with a naive
	// zip, "step" would consume the document count and each attribute
	// would take its neighbour's value.
	cols := []integrations.MessageColumn{
		builtin(integrations.BuiltinStep),
		attr("documents.exported"),
		attr("archive.month_from"),
		attr("archive.month_to"),
		builtin(integrations.BuiltinDuration),
	}
	got := promotedByKey(cols, []string{"7", "2026-07-01", "2026-07-31"})

	want := map[string]string{
		"documents.exported": "7",
		"archive.month_from": "2026-07-01",
		"archive.month_to":   "2026-07-31",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	// And a built-in must never appear as a promoted value.
	if _, ok := got[integrations.BuiltinStep]; ok {
		t.Error("a built-in leaked into the promoted map")
	}
}

func TestPromotedByKeyHandlesBuiltinsInterleaved(t *testing.T) {
	cols := []integrations.MessageColumn{
		attr("a"), builtin(integrations.BuiltinService), attr("b"), builtin(integrations.BuiltinDuration), attr("c"),
	}
	got := promotedByKey(cols, []string{"1", "2", "3"})
	for k, want := range map[string]string{"a": "1", "b": "2", "c": "3"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

func TestPromotedByKeyOmitsBlanks(t *testing.T) {
	// A key no span carried is absent, not "". The column still renders;
	// the cell shows the "not set" dash.
	got := promotedByKey([]integrations.MessageColumn{attr("a"), attr("b")}, []string{"1", ""})
	if _, ok := got["b"]; ok {
		t.Errorf("blank value was sent: %v", got)
	}
	if got["a"] != "1" {
		t.Errorf("a = %q", got["a"])
	}
}

func TestPromotedByKeyIsNilWhenNothingToSay(t *testing.T) {
	if got := promotedByKey(nil, []string{"1"}); got != nil {
		t.Errorf("got %v", got)
	}
	if got := promotedByKey([]integrations.MessageColumn{attr("a")}, nil); got != nil {
		t.Errorf("got %v", got)
	}
	// Only built-ins configured: no attribute was queried, so there is
	// no promoted map at all rather than an empty object per row.
	if got := promotedByKey([]integrations.MessageColumn{builtin(integrations.BuiltinService)}, []string{"x"}); got != nil {
		t.Errorf("got %v", got)
	}
}

func TestPromotedByKeyToleratesAShortValueSlice(t *testing.T) {
	// Defensive: a mismatch here should drop columns, never panic.
	got := promotedByKey([]integrations.MessageColumn{attr("a"), attr("b")}, []string{"1"})
	if got["a"] != "1" {
		t.Errorf("a = %q", got["a"])
	}
	if _, ok := got["b"]; ok {
		t.Error("invented a value for b")
	}
}
