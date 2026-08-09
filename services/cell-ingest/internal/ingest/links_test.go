// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Links are how asynchronous hand-offs are expressed. Getting the cap
// wrong is not recoverable: links we discard are gone, with no backfill.

package ingest

import (
	"testing"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func link(trace, span byte) *tracepb.Span_Link {
	return &tracepb.Span_Link{
		TraceId: []byte{trace, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:  []byte{span, 1, 2, 3, 4, 5, 6, 7},
	}
}

func TestASpanWithNoLinksStoresNothing(t *testing.T) {
	// The median span. It must not cost an allocation or a row of empty
	// arrays' worth of meaning.
	tr, sp, total := convertLinks(nil)
	if tr != nil || sp != nil || total != 0 {
		t.Fatalf("got %v %v %d", tr, sp, total)
	}
}

func TestTheCommonCaseIsOneLink(t *testing.T) {
	// A queue consumer pointing back at its producer: the hand-off shape
	// the whole feature exists for.
	tr, sp, total := convertLinks([]*tracepb.Span_Link{link(0xaa, 0xbb)})
	if len(tr) != 1 || len(sp) != 1 || total != 1 {
		t.Fatalf("got %v %v %d", tr, sp, total)
	}
	if tr[0][:2] != "aa" || sp[0][:2] != "bb" {
		t.Errorf("ids not hex-encoded: %q %q", tr[0], sp[0])
	}
}

func TestABatchIsTruncatedButItsSizeIsKept(t *testing.T) {
	// The second mode: a consumer processing 500 messages links to 500
	// producers. Truncating silently would let a "where is my message"
	// feature present 32 as the whole story.
	links := make([]*tracepb.Span_Link, 500)
	for i := range links {
		links[i] = link(byte(i%251), byte(i%241))
	}
	tr, sp, total := convertLinks(links)
	if len(tr) != MaxSpanLinks || len(sp) != MaxSpanLinks {
		t.Fatalf("got %d/%d stored, want %d", len(tr), len(sp), MaxSpanLinks)
	}
	if total != 500 {
		t.Fatalf("the TRUE count must survive truncation: got %d", total)
	}
}

func TestExactlyTheCapIsNotTruncated(t *testing.T) {
	links := make([]*tracepb.Span_Link, MaxSpanLinks)
	for i := range links {
		links[i] = link(byte(i), byte(i))
	}
	tr, _, total := convertLinks(links)
	if len(tr) != MaxSpanLinks || total != MaxSpanLinks {
		t.Fatalf("got %d stored, total %d", len(tr), total)
	}
}

func TestANilLinkInTheArrayIsSkippedNotCounted(t *testing.T) {
	// Malformed input from an exporter must not produce an empty string
	// masquerading as a trace id.
	tr, sp, total := convertLinks([]*tracepb.Span_Link{link(0x01, 0x02), nil, link(0x03, 0x04)})
	if len(tr) != 2 || len(sp) != 2 {
		t.Fatalf("got %v %v", tr, sp)
	}
	// The total counts what arrived, including the malformed entry: it
	// is what the exporter claimed to send.
	if total != 3 {
		t.Errorf("got total %d, want 3", total)
	}
	for _, id := range tr {
		if id == "" {
			t.Error("an empty trace id was stored")
		}
	}
}

func TestLinkOrderIsPreserved(t *testing.T) {
	// Truncation keeps the FIRST links rather than an arbitrary subset,
	// so "showing 32 of 500" means a prefix a reader can reason about.
	tr, _, _ := convertLinks([]*tracepb.Span_Link{link(0x11, 1), link(0x22, 2), link(0x33, 3)})
	if tr[0][:2] != "11" || tr[1][:2] != "22" || tr[2][:2] != "33" {
		t.Fatalf("order changed: %v", tr)
	}
}
