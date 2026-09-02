// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// not_exists asks whether a row CARRIES an attribute, which is a question
// no value comparison can answer: a ClickHouse Map returns the empty
// string for a key it does not hold, so an absent attribute and one set
// to "" are identical through every other operator - and every negation
// already matches the absent ones.

package store

import (
	"strings"
	"testing"
)

func TestNotExistsChecksBothMaps(t *testing.T) {
	sql, args := attrClauseIn("SpanAttributes", LogAttrFilter{Key: "rpc.service", Op: AttrOpNotExists})
	if len(args) != 0 {
		t.Fatalf("not_exists takes no value, got binds %v", args)
	}
	for _, want := range []string{
		"NOT mapContains(SpanAttributes, 'rpc.service')",
		"NOT mapContains(ResourceAttributes, 'rpc.service')",
		" AND ",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q in:\n%s", want, sql)
		}
	}
	// The distinction the operator exists for.
	if strings.Contains(sql, "= ''") {
		t.Errorf("compiled to a value comparison, which cannot tell absent from empty:\n%s", sql)
	}
}

// Absent means absent from BOTH maps, exactly as the value expression
// falls back from the primary map to the resource attributes. Checking
// only one would call an attribute absent while a lookup would find it.
func TestNotExistsIsTheInverseOfExists(t *testing.T) {
	ex, _ := attrClauseIn("SpanAttributes", LogAttrFilter{Key: "k", Op: AttrOpExists})
	nx, _ := attrClauseIn("SpanAttributes", LogAttrFilter{Key: "k", Op: AttrOpNotExists})
	if strings.Count(ex, "mapContains") != strings.Count(nx, "mapContains") {
		t.Fatalf("exists and not_exists consult different maps:\n%s\n%s", ex, nx)
	}
	if !strings.Contains(ex, " OR ") || !strings.Contains(nx, " AND ") {
		t.Errorf("expected exists to be an OR and not_exists an AND (De Morgan):\nexists:     %s\nnot_exists: %s", ex, nx)
	}
}

// service.name and span.name address indexed columns rather than a map,
// so they need the column form here too - exists already had it.
func TestNotExistsOnTheIndexedColumns(t *testing.T) {
	svc, _ := attrClauseIn("SpanAttributes", LogAttrFilter{Key: "service.name", Op: AttrOpNotExists})
	if svc != "ServiceName = ''" {
		t.Errorf("service.name: got %q", svc)
	}
	span, _ := attrClauseIn("SpanAttributes", LogAttrFilter{Key: "span.name", Op: AttrOpNotExists})
	if span != "SpanName = ''" {
		t.Errorf("span.name: got %q", span)
	}
	// span.name is only a column on the span tables.
	logSpan, _ := attrClauseIn("LogAttributes", LogAttrFilter{Key: "span.name", Op: AttrOpNotExists})
	if strings.Contains(logSpan, "SpanName") {
		t.Errorf("span.name treated as a column on the logs table: %q", logSpan)
	}
}

// The behaviour that makes not_exists necessary, pinned so nobody
// "simplifies" negation later and changes what live matchers mean.
func TestNegationStillMatchesAbsentAttributes(t *testing.T) {
	neq, _ := attrClauseIn("SpanAttributes", LogAttrFilter{Key: "rpc.service", Op: AttrOpNeq, Value: "abc"})
	if !strings.Contains(neq, "!=") {
		t.Fatalf("neq did not compile to a value comparison: %s", neq)
	}
	// It reads the value expression, which yields '' for an absent key -
	// so '' != 'abc' is true and the row matches. That is the documented
	// behaviour, and the reason not_exists is a separate operator rather
	// than a redefinition of this one.
	if !strings.Contains(neq, "mapContains") {
		t.Fatalf("neq no longer goes through the fallback value expression: %s", neq)
	}
}
