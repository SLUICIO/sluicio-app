// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"strings"
	"testing"
	"unicode/utf8"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func str(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

// What .NET's Activity.AddException (and every SDK's recordException)
// sends: an event named "exception" with type, message and stack trace.
func TestAnExceptionEventIsKept(t *testing.T) {
	span := &tracepb.Span{
		TraceId: make([]byte, 16), SpanId: make([]byte, 8), Name: "Process invoice",
		Events: []*tracepb.Span_Event{{
			Name: "exception", TimeUnixNano: 1_700_000_000_000_000_000,
			Attributes: []*commonpb.KeyValue{
				str("exception.type", "System.TimeoutException"),
				str("exception.message", "The operation has timed out."),
				str("exception.stacktrace", "at Invoices.Send()\n   at Worker.Run()"),
			},
		}},
	}
	row := convertSpan(span, map[string]string{}, "invoices", "")
	if row.EventsTotal != 1 || len(row.EventsName) != 1 || row.EventsName[0] != "exception" {
		t.Fatalf("event not kept: %+v", row.EventsName)
	}
	if a := row.EventsAttributes[0]; a["exception.type"] != "System.TimeoutException" || !strings.Contains(a["exception.stacktrace"], "Worker.Run") {
		t.Errorf("exception attributes not kept: %v", a)
	}
	if row.EventsTimestamp[0].UnixNano() != 1_700_000_000_000_000_000 {
		t.Errorf("event time: %v", row.EventsTimestamp[0])
	}
}

func TestEventsAreCappedWithTheTrueCountKept(t *testing.T) {
	evs := make([]*tracepb.Span_Event, MaxSpanEvents+10)
	for i := range evs {
		evs[i] = &tracepb.Span_Event{Name: "item"}
	}
	_, names, _, total := convertEvents(evs)
	if len(names) != MaxSpanEvents || total != uint32(MaxSpanEvents+10) {
		t.Errorf("want %d stored of %d, got %d of %d", MaxSpanEvents, MaxSpanEvents+10, len(names), total)
	}
}

func TestALongStackTraceIsCutAndSaysSo(t *testing.T) {
	long := strings.Repeat("at Frame.Ä()\n", MaxEventValueBytes/8)
	_, _, attrs, _ := convertEvents([]*tracepb.Span_Event{{Name: "exception", Attributes: []*commonpb.KeyValue{str("exception.stacktrace", long)}}})
	got := attrs[0]["exception.stacktrace"]
	if !strings.HasSuffix(got, TruncatedMarker) || len(got) > MaxEventValueBytes+len(TruncatedMarker) {
		t.Errorf("want a cut value ending in the marker, got %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Error("the cut split a character")
	}
}

func TestASpanWithoutEventsStoresNone(t *testing.T) {
	times, names, attrs, total := convertEvents(nil)
	if times != nil || names != nil || attrs != nil || total != 0 {
		t.Errorf("no events should store nothing: %v %v %v %d", times, names, attrs, total)
	}
}
