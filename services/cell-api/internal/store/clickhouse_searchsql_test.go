// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The message search is compiled, not written, and the thing that broke
// in production was its SHAPE: a single aggregation pass carried both
// attribute maps in the state of every trace in the window and only then
// applied the LIMIT. On a busy service that is millions of groups each
// holding a copy of two maps, which ClickHouse answers with "Memory
// limit (for query) exceeded" rather than a slow page.
//
// These tests pin the two-pass shape and the bind order, because both are
// invisible at the call site and expensive to get wrong.

package store

import (
	"strings"
	"testing"
	"time"
)

func baseParams() MessagesSearchParams {
	return MessagesSearchParams{
		From:  time.Unix(1000, 0).UTC(),
		To:    time.Unix(2000, 0).UTC(),
		Limit: 25,
	}
}

// The candidate pass must aggregate only what the ordering and filters
// need. An attribute map in here is the defect that caused the outage.
func TestCandidatePassCarriesNoAttributeMaps(t *testing.T) {
	sql, _ := buildMessagesSearchSQL(baseParams())

	cand := between(t, sql, "WITH candidates AS (", "matching AS (")
	for _, forbidden := range []string{"ResourceAttributes", "SpanAttributes", "groupArray"} {
		if strings.Contains(cand, forbidden) {
			t.Errorf("the candidate pass aggregates %s; that state is held for every trace in the window", forbidden)
		}
	}
	for _, want := range []string{"max(Timestamp)", "countIf(StatusCode = 'Error')", "LIMIT ?"} {
		if !strings.Contains(cand, want) {
			t.Errorf("candidate pass is missing %q:\n%s", want, cand)
		}
	}
}

// ...and the expensive pass must be restricted to the survivors, or
// splitting it changed nothing.
func TestAttributesAreReadOnlyForCandidates(t *testing.T) {
	sql, _ := buildMessagesSearchSQL(baseParams())
	match := between(t, sql, "matching AS (", "summary AS (")

	if !strings.Contains(match, "TraceId IN (SELECT TraceId FROM candidates)") {
		t.Fatalf("the attribute pass is not restricted to candidates:\n%s", match)
	}
	for _, want := range []string{"argMax(ResourceAttributes", "argMax(SpanAttributes"} {
		if !strings.Contains(match, want) {
			t.Errorf("attribute pass lost %q", want)
		}
	}
	if strings.Contains(match, "LIMIT ?") {
		t.Error("the attribute pass has its own LIMIT; the candidate pass already bounded the set")
	}
}

// matched_* must describe the spans that MATCHED, not every span in the
// trace, so the predicates have to be applied in both passes. Dropping
// them from the second is a silent correctness change: a search for one
// span name would start reporting some other span's attributes.
func TestPredicatesApplyToBothPasses(t *testing.T) {
	p := baseParams()
	p.Clauses = []string{"SpanName = ?"}
	p.Args = []any{"Error Detected"}
	sql, args := buildMessagesSearchSQL(p)

	if n := strings.Count(sql, "SpanName = ?"); n != 2 {
		t.Fatalf("predicate appears %d times, want 2 (candidates and matching)", n)
	}
	// from, to, span name | limit | from, to, span name
	want := []any{p.From, p.To, "Error Detected", 25, p.From, p.To, "Error Detected"}
	if len(args) != len(want) {
		t.Fatalf("got %d binds %v, want %d %v", len(args), args, len(want), want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("bind %d = %v, want %v", i, args[i], want[i])
		}
	}
}

// The cursor and status filters must be evaluated before the LIMIT, or a
// page comes back short and pagination stops early.
func TestCursorAndStatusFilterTheCandidatePass(t *testing.T) {
	p := baseParams()
	p.OnlyFailed = true
	p.Before = &MessageCursor{LatestMatchNano: 42, TraceID: "abc"}
	sql, args := buildMessagesSearchSQL(p)

	cand := between(t, sql, "WITH candidates AS (", "matching AS (")
	if !strings.Contains(cand, "HAVING has_error = 1") {
		t.Errorf("status filter is not in the candidate HAVING:\n%s", cand)
	}
	if !strings.Contains(cand, "toUnixTimestamp64Nano(latest_match) < ?") {
		t.Errorf("cursor is not in the candidate HAVING:\n%s", cand)
	}
	// from, to | cursor x3 | limit | from, to
	if len(args) != 8 || args[2] != int64(42) || args[4] != "abc" || args[5] != 25 {
		t.Fatalf("cursor binds are out of order: %v", args)
	}
}

// The service filter is part of the predicate, so it must be bound twice
// like every other clause - once per pass, in the same order.
func TestServiceFilterIsBoundInBothPasses(t *testing.T) {
	p := baseParams()
	p.ServiceFilter = []string{"checkout-api", "orders"}
	sql, args := buildMessagesSearchSQL(p)

	if n := strings.Count(sql, "ServiceName IN (?,?)"); n != 2 {
		t.Fatalf("service filter appears %d times, want 2", n)
	}
	// from, to, svc, svc | limit | from, to, svc, svc
	want := []any{p.From, p.To, "checkout-api", "orders", 25, p.From, p.To, "checkout-api", "orders"}
	if len(args) != len(want) {
		t.Fatalf("got %d binds %v, want %d", len(args), args, len(want))
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("bind %d = %v, want %v", i, args[i], want[i])
		}
	}
}

// Promoted columns are computed over the whole trace in the summary pass,
// and their binds come last.
func TestPromotedBindsComeLast(t *testing.T) {
	p := baseParams()
	p.PromotedKeys = []string{"customer.id"}
	sql, args := buildMessagesSearchSQL(p)

	if !strings.Contains(sql, "AS promoted_0") || !strings.Contains(sql, "s.promoted_0") {
		t.Fatal("promoted column is missing from the statement or the outer select")
	}
	// from, to | limit | from, to | five binds for one promoted key
	if len(args) != 10 {
		t.Fatalf("got %d binds, want 10: %v", len(args), args)
	}
	for i := 5; i < 10; i++ {
		if args[i] != "customer.id" {
			t.Fatalf("bind %d = %v, want the promoted key", i, args[i])
		}
	}
}

// The displayed status and timestamp come from the pass that filtered on
// them, so the row's pill cannot disagree with the filter that selected it.
func TestOuterSelectReadsStatusFromTheCandidatePass(t *testing.T) {
	sql, _ := buildMessagesSearchSQL(baseParams())
	for _, want := range []string{"c.has_error", "c.latest_match", "ORDER BY c.latest_match DESC, c.TraceId DESC"} {
		if !strings.Contains(sql, want) {
			t.Errorf("outer select is missing %q", want)
		}
	}
}

func between(t *testing.T, s, from, to string) string {
	t.Helper()
	i := strings.Index(s, from)
	if i < 0 {
		t.Fatalf("marker %q not found in:\n%s", from, s)
	}
	j := strings.Index(s[i:], to)
	if j < 0 {
		t.Fatalf("marker %q not found after %q", to, from)
	}
	return s[i : i+j]
}
