// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// matchedSpanIDsExpr decides whether a search reports which spans it
// matched. The distinction it encodes is easy to lose in a refactor and
// expensive to get wrong: with no predicate every span in the window
// satisfies the query, so reporting them all would make the UI mark an
// entire trace as "this is what you searched for".

package store

import (
	"strings"
	"testing"
)

func TestMatchedSpanIDsExprIsInertWithoutAPredicate(t *testing.T) {
	got := matchedSpanIDsExpr(false)
	if !strings.Contains(got, "CAST([]") {
		t.Fatalf("without a predicate the expression must be an empty array, got %q", got)
	}
	// It must not aggregate: doing the work would also assert that every
	// span in the window was a deliberate match.
	if strings.Contains(got, "groupArray") {
		t.Errorf("no-predicate expression still aggregates span ids: %q", got)
	}
}

func TestMatchedSpanIDsExprIsOrderedAndDoublyCapped(t *testing.T) {
	got := matchedSpanIDsExpr(true)
	for _, want := range []string{
		"groupArray(1000)", // bounds what one trace can pull into memory
		"arraySort",        // oldest first — the UI selects [0]
		"arrayDistinct",    // `traces` holds one row per write, not per span
		"arraySlice",       // bounds what crosses the wire
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expression is missing %s: %q", want, got)
		}
	}
	// Sorting has to happen between the two caps, or the 50 that reach
	// the UI are an arbitrary 50 rather than the oldest matches — and
	// "open on the first match" would land on a different span run to run.
	sliceAt := strings.Index(got, "arraySlice")
	distinctAt := strings.Index(got, "arrayDistinct")
	sortAt := strings.Index(got, "arraySort")
	groupAt := strings.Index(got, "groupArray")
	if !(sliceAt < distinctAt && distinctAt < sortAt && sortAt < groupAt) {
		t.Errorf("expected arraySlice(arrayDistinct(...arraySort(groupArray(...)))) nesting, got %q", got)
	}
	// Dedupe must come BEFORE the slice. The other way round, 50 rows of
	// the same handful of repeatedly-written spans would collapse to a
	// near-empty list and the later real matches would already be gone.
	if distinctAt < sliceAt {
		t.Error("arrayDistinct must be inside arraySlice, or duplicates consume the cap")
	}
}
