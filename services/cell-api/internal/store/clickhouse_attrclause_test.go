// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"strings"
	"testing"
)

// span.name and service.name address span COLUMNS, not attribute-map
// entries. If either regresses to a map lookup the predicate still
// compiles, still saves, and silently matches nothing forever — so these
// assert the compiled SQL rather than any behaviour a caller could see.

func TestSpanNameCompilesToTheColumn(t *testing.T) {
	sql, args := attrClauseIn("SpanAttributes", LogAttrFilter{
		Key: "span.name", Op: AttrOpEq, Value: "Error Detected",
	})
	if !strings.Contains(sql, "SpanName") {
		t.Fatalf("span.name did not reach the SpanName column: %q", sql)
	}
	if strings.Contains(sql, "mapContains") || strings.Contains(sql, "SpanAttributes[") {
		t.Fatalf("span.name compiled to an attribute lookup: %q", sql)
	}
	if len(args) != 1 || args[0] != "Error Detected" {
		t.Fatalf("value not bound: %v", args)
	}
}

func TestSpanNameExistsCompilesToTheColumn(t *testing.T) {
	sql, _ := attrClauseIn("SpanAttributes", LogAttrFilter{Key: "span.name", Op: AttrOpExists})
	if !strings.Contains(sql, "SpanName != ''") {
		t.Fatalf("span.name exists did not reach the column: %q", sql)
	}
}

// The logs table has no SpanName column. On any non-span table the key
// must stay an ordinary attribute lookup, or the query fails outright.
func TestSpanNameStaysAnAttributeOnLogs(t *testing.T) {
	sql, _ := attrClauseIn("LogAttributes", LogAttrFilter{
		Key: "span.name", Op: AttrOpEq, Value: "x",
	})
	if strings.Contains(sql, "SpanName") {
		t.Fatalf("span.name leaked the SpanName column onto the logs table: %q", sql)
	}
	if !strings.Contains(sql, "LogAttributes") {
		t.Fatalf("span.name did not compile to a log attribute lookup: %q", sql)
	}
}

func TestServiceNameStillCompilesToTheColumn(t *testing.T) {
	for _, m := range []string{"SpanAttributes", "LogAttributes"} {
		sql, _ := attrClauseIn(m, LogAttrFilter{Key: "service.name", Op: AttrOpEq, Value: "svc"})
		if !strings.Contains(sql, "ServiceName") {
			t.Fatalf("service.name did not reach the column on %s: %q", m, sql)
		}
	}
}
