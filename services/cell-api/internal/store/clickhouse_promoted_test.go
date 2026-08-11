// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Promoted columns (issue #23) resolve an attribute against every span
// of a trace. The expression is built by string assembly with positional
// binds, and the two ways that goes wrong are silent: a mismatched bind
// count shifts every later argument onto the wrong placeholder, and a
// lookup that reads only span attributes quietly returns blank for a
// resource attribute the user could see in the drawer.

package store

import (
	"strings"
	"testing"
)

func TestPromotedColumnSQLIsInertWithoutKeys(t *testing.T) {
	// No keys must add no SQL and no args — the statement has to stay
	// byte-identical to what every un-configured integration runs today.
	sql, args := promotedColumnSQL(nil)
	if sql != "" || len(args) != 0 {
		t.Fatalf("want empty, got sql=%q args=%v", sql, args)
	}
	if sql, args := promotedColumnSQL([]string{}); sql != "" || len(args) != 0 {
		t.Fatalf("want empty for an empty slice, got sql=%q args=%v", sql, args)
	}
}

func TestPromotedColumnSQLBindsMatchPlaceholders(t *testing.T) {
	// The failure this catches shifts EVERY later bind by one, so the
	// symptom is a wrong time window or a wrong cursor rather than a
	// wrong column — nothing points at this function.
	for _, keys := range [][]string{
		{"documents.exported"},
		{"a", "b"},
		{"a", "b", "c", "d", "e"},
	} {
		sql, args := promotedColumnSQL(keys)
		if got, want := strings.Count(sql, "?"), len(args); got != want {
			t.Errorf("%d keys: %d placeholders but %d args", len(keys), got, want)
		}
	}
}

func TestPromotedColumnSQLNamesColumnsByIndex(t *testing.T) {
	// The scan addresses results positionally, so column i must be the
	// i-th requested key.
	sql, _ := promotedColumnSQL([]string{"a", "b", "c"})
	for i, want := range []string{"promoted_0", "promoted_1", "promoted_2"} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %s: %q", want, sql)
		}
		if i > 0 {
			prev := strings.Index(sql, "promoted_"+string(rune('0'+i-1)))
			cur := strings.Index(sql, want)
			if prev > cur {
				t.Errorf("columns are out of order: promoted_%d appears after promoted_%d", i-1, i)
			}
		}
	}
}

func TestPromotedColumnSQLRepeatsEachKeyForEveryPlaceholder(t *testing.T) {
	// Each key appears in the expression five times (two lookups, one
	// existence test each, plus the span test in the value branch), so
	// the args must repeat it exactly that many times in a row.
	_, args := promotedColumnSQL([]string{"k"})
	for i, a := range args {
		if a != "k" {
			t.Fatalf("arg %d = %v, want every bind for one key to be that key", i, a)
		}
	}
	_, two := promotedColumnSQL([]string{"first", "second"})
	half := len(two) / 2
	for i, a := range two {
		want := "first"
		if i >= half {
			want = "second"
		}
		if a != want {
			t.Errorf("arg %d = %v, want %q — keys must not interleave", i, a, want)
		}
	}
}

func TestPromotedColumnSQLFallsBackToResourceAttributes(t *testing.T) {
	// A key the user picked off the drawer's "Resource attributes" table
	// has to resolve, or the picker offers keys that render blank.
	sql, _ := promotedColumnSQL([]string{"service.namespace"})
	if !strings.Contains(sql, "ResourceAttributes") {
		t.Error("resource attributes are never consulted")
	}
	// Span first: it is the operation-specific value, matching
	// mergeAttributes in the API layer.
	if strings.Index(sql, "SpanAttributes") > strings.Index(sql, "ResourceAttributes") {
		t.Error("resource attributes must be the fallback, not the first choice")
	}
}

func TestPromotedColumnSQLTakesTheLastSpanThatCarriesTheKey(t *testing.T) {
	// argMaxIf on Timestamp, not any(): with a key that changes through
	// a flow, an arbitrary pick is the same defect this release fixed in
	// health checks.
	sql, _ := promotedColumnSQL([]string{"k"})
	if !strings.Contains(sql, "argMaxIf") {
		t.Errorf("want argMaxIf so the newest value wins deterministically: %q", sql)
	}
	// And the condition must be the existence test, so a trace where
	// only some spans carry the key still resolves.
	if !strings.Contains(sql, "has(SpanAttributes, ?) OR has(ResourceAttributes, ?)") {
		t.Errorf("want an existence-gated aggregate: %q", sql)
	}
}
