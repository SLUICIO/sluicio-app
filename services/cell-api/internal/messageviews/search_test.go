// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Filter → ClickHouse translation, pinned where the UI depends on the
// exact behaviour: id-fragment matching, the incomplete-payload no-op,
// and the error-type clause shapes.
package messageviews

import (
	"strings"
	"testing"
)

func TestTraceAndSpanIDOperators(t *testing.T) {
	// Exact (is/equals) — case-normalised comparison.
	sql, err := Build([]Filter{{Field: FieldTraceID, Op: OpIs, Value: "ABCDEF012345"}})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(sql.Clauses) != 1 || sql.Clauses[0] != "lower(TraceId) = ?" {
		t.Fatalf("exact trace clause = %v", sql.Clauses)
	}
	if sql.Args[0] != "abcdef012345" {
		t.Fatalf("trace id not lowercased: %v", sql.Args)
	}

	// Fragment (contains) — for partial ids out of log lines.
	sql, err = Build([]Filter{{Field: FieldTraceID, Op: OpContains, Value: "CDEF01"}})
	if err != nil {
		t.Fatalf("build contains: %v", err)
	}
	if len(sql.Clauses) != 1 || !strings.Contains(sql.Clauses[0], "positionCaseInsensitive(TraceId") {
		t.Fatalf("contains trace clause = %v", sql.Clauses)
	}

	sql, err = Build([]Filter{{Field: FieldSpanID, Op: OpContains, Value: "beef"}})
	if err != nil {
		t.Fatalf("build span contains: %v", err)
	}
	if len(sql.Clauses) != 1 || !strings.Contains(sql.Clauses[0], "positionCaseInsensitive(SpanId") {
		t.Fatalf("contains span clause = %v", sql.Clauses)
	}
}

func TestIncompletePayloadRowIsANoOp(t *testing.T) {
	// The FilterEditor's freshly added row: field=payload, no fieldPath.
	// It must neither fail validation nor contribute a clause — adding
	// a filter row must never 400 the search before the user finishes it.
	f := Filter{Field: FieldPayload, Op: OpEquals, Value: ""}
	if err := f.Validate(); err != nil {
		t.Fatalf("incomplete payload row must validate, got %v", err)
	}
	sql, err := Build([]Filter{f, {Field: FieldService, Op: OpIs, Value: "order-api"}})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(sql.Clauses) != 0 {
		t.Fatalf("incomplete payload row leaked a clause: %v", sql.Clauses)
	}
	if len(sql.ServiceNameLiterals) != 1 {
		t.Fatalf("sibling filters must still apply: %+v", sql)
	}

	// A COMPLETE payload row still validates its key and builds.
	sql, err = Build([]Filter{{Field: FieldPayload, FieldPath: "http.route", Op: OpEquals, Value: "/x"}})
	if err != nil || len(sql.Clauses) != 1 {
		t.Fatalf("complete payload row: clauses=%v err=%v", sql.Clauses, err)
	}
	if _, err := Build([]Filter{{Field: FieldPayload, FieldPath: "bad'key", Op: OpEquals, Value: "x"}}); err == nil {
		t.Fatal("unsafe payload key must still be rejected")
	}
}

func TestErrorTypeClauses(t *testing.T) {
	for _, op := range []Operator{OpEquals, OpContains, OpMatches, OpIn} {
		sql, err := Build([]Filter{{Field: FieldErrorType, Op: op, Value: "TimeoutException"}})
		if err != nil {
			t.Fatalf("op %s: %v", op, err)
		}
		if len(sql.Clauses) != 1 || !strings.Contains(sql.Clauses[0], "exception.type") {
			t.Fatalf("op %s clause = %v", op, sql.Clauses)
		}
	}
}
