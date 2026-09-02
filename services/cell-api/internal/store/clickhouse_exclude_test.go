// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A negation over a MESSAGE is universal: "no step has rpc.service =
// abc". Applied to spans the way a positive filter is, it would match
// any message with at least one span lacking the attribute - nearly
// every message, for nearly every negation - and the filter would look
// applied while sifting out almost nothing.

package store

import (
	"strings"
	"testing"
	"time"
)

func excludeParams() MessagesSearchParams {
	return MessagesSearchParams{
		From:  time.Unix(1000, 0).UTC(),
		To:    time.Unix(2000, 0).UTC(),
		Limit: 25,
	}
}

func TestNoAntiJoinWithoutNegatedRows(t *testing.T) {
	sql, _ := buildMessagesSearchSQL(excludeParams())
	if strings.Contains(sql, "NOT IN (SELECT TraceId") {
		t.Fatalf("an anti-join appeared with nothing to exclude:\n%s", sql)
	}
}

// The subquery must carry the scope and NOTHING else. Conjoining the
// positive rows would only exclude traces where ONE span satisfied both,
// so a message with a matching step and a separate excluded step would
// survive - the exact case the universal reading exists to catch.
func TestTheAntiJoinIsNotConstrainedByThePositiveRows(t *testing.T) {
	p := excludeParams()
	p.Clauses = []string{"SpanName = ?"}
	p.Args = []any{"pickup_file"}
	p.ExcludeClauses = []string{"SpanAttributes['rpc.service'] = ?"}
	p.ExcludeArgs = []any{"abc"}
	sql, args := buildMessagesSearchSQL(p)

	sub := between(t, sql, "NOT IN (SELECT TraceId FROM traces WHERE", "))")
	if strings.Contains(sub, "SpanName") {
		t.Errorf("the positive row leaked into the exclusion:\n%s", sub)
	}
	if !strings.Contains(sub, "Timestamp >=") {
		t.Errorf("the exclusion lost the window:\n%s", sub)
	}

	// from, to | span name | from, to (the subquery's own scope) | abc
	want := []any{p.From, p.To, "pickup_file", p.From, p.To, "abc"}
	if len(args) < len(want) {
		t.Fatalf("got %d binds %v, want at least %d", len(args), args, len(want))
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("bind %d = %v, want %v (full: %v)", i, args[i], want[i], args)
		}
	}
}

// The scope DOES belong in the subquery: a message must not vanish
// because of a step outside what the reader can see on the page.
func TestTheAntiJoinCarriesTheServiceScope(t *testing.T) {
	p := excludeParams()
	p.ServiceFilter = []string{"checkout-api"}
	p.ExcludeClauses = []string{"SpanAttributes['rpc.service'] = ?"}
	p.ExcludeArgs = []any{"abc"}
	sql, args := buildMessagesSearchSQL(p)

	sub := between(t, sql, "NOT IN (SELECT TraceId FROM traces WHERE", "))")
	if !strings.Contains(sub, "ServiceName IN (?)") {
		t.Errorf("the exclusion is not service-scoped:\n%s", sub)
	}
	// from, to, svc | from, to, svc, abc
	want := []any{p.From, p.To, "checkout-api", p.From, p.To, "checkout-api", "abc"}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("bind %d = %v, want %v (full: %v)", i, args[i], want[i], args)
		}
	}
}

// Several negated rows are one subquery with an OR: excluding traces
// that have a span matching A or B is exactly "no span has A" AND "no
// span has B", at one pass instead of one per row.
func TestSeveralNegationsShareOneAntiJoin(t *testing.T) {
	p := excludeParams()
	p.ExcludeClauses = []string{"a = ?", "b = ?"}
	p.ExcludeArgs = []any{"1", "2"}
	sql, _ := buildMessagesSearchSQL(p)

	if n := strings.Count(sql, "NOT IN (SELECT TraceId"); n != 1 {
		t.Fatalf("got %d anti-joins, want 1", n)
	}
	sub := between(t, sql, "NOT IN (SELECT TraceId FROM traces WHERE", "))")
	if !strings.Contains(sub, "a = ? OR b = ?") {
		t.Errorf("the negated rows are not OR-ed inside the exclusion:\n%s", sub)
	}
}
