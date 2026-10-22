//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// #42 against a real ClickHouse: an exception recorded as a span event
// is what the trace, the message list, the error filters, the error
// breakdown and alert notifications name.
//
//	EVENTS_PROBE_CH=localhost:9000 go test -tags liveprobe \
//	  ./services/cell-api/internal/store/ -run TestExceptionsLive -v

package store

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestExceptionsLive(t *testing.T) {
	addr := os.Getenv("EVENTS_PROBE_CH")
	if addr == "" {
		t.Skip("set EVENTS_PROBE_CH")
	}
	ctx := context.Background()
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	s := New(conn)
	run := time.Now().UnixNano()
	svc := fmt.Sprintf("exc-svc-%d", run)
	now := time.Now().UTC()
	tEvent := fmt.Sprintf("evt%029d", run)
	tAttr := fmt.Sprintf("att%029d", run)
	tPlain := fmt.Sprintf("pln%029d", run)
	const exType = "System.InvalidOperationException"
	const exMsg = "Payment provider returned 503"
	const exStack = "System.InvalidOperationException: Payment provider returned 503\n   at Checkout.PaymentClient.ChargeAsync()"

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO traces (Timestamp, TraceId, SpanId, SpanName, ServiceName, SpanAttributes, StatusCode, StatusMessage,
		EventsTimestamp, EventsName, EventsAttributes, EventsTotal, OrganizationId)`)
	if err != nil {
		t.Fatal(err)
	}
	noTimes, noNames, noAttrs := []time.Time{}, []string{}, []map[string]string{}
	// A span that recorded the exception as an event (Activity.AddException),
	// after a non-exception event.
	if err := batch.Append(now.Add(-10*time.Second), tEvent, "e1", "charge", svc, map[string]string{}, "Error", "",
		[]time.Time{now.Add(-10 * time.Second), now.Add(-10 * time.Second)},
		[]string{"retry", "exception"},
		[]map[string]string{{"attempt": "2"}, {"exception.type": exType, "exception.message": exMsg, "exception.stacktrace": exStack}},
		uint32(2), "exc-probe"); err != nil {
		t.Fatal(err)
	}
	// A span that set exception.* attributes instead.
	if err := batch.Append(now.Add(-5*time.Second), tAttr, "a1", "charge", svc,
		map[string]string{"exception.type": "TimeoutException", "exception.message": "timed out"}, "Error", "",
		noTimes, noNames, noAttrs, uint32(0), "exc-probe"); err != nil {
		t.Fatal(err)
	}
	// A span that failed with only a status.
	if err := batch.Append(now.Add(-1*time.Second), tPlain, "p1", "charge", svc, map[string]string{}, "Error", "boom",
		noTimes, noNames, noAttrs, uint32(0), "exc-probe"); err != nil {
		t.Fatal(err)
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	from, to := now.Add(-time.Minute), now.Add(time.Minute)

	t.Run("the trace carries the events", func(t *testing.T) {
		spans, err := s.SpansForTrace(ctx, tEvent, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(spans) != 1 {
			t.Fatalf("want 1 span, got %d", len(spans))
		}
		sp := spans[0]
		if !slices.Equal(sp.EventsName, []string{"retry", "exception"}) || sp.EventsTotal != 2 {
			t.Fatalf("events: %v total %d", sp.EventsName, sp.EventsTotal)
		}
		if sp.EventsAttributes[1]["exception.stacktrace"] != exStack {
			t.Errorf("stack trace lost: %q", sp.EventsAttributes[1]["exception.stacktrace"])
		}
	})

	t.Run("error types include the recorded exception", func(t *testing.T) {
		types, err := s.DistinctErrorTypes(ctx, from, to, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{exType, "TimeoutException", "boom"} {
			if !slices.Contains(types, want) {
				t.Errorf("%q missing from %v", want, types)
			}
		}
	})

	t.Run("messages name what failed", func(t *testing.T) {
		rows, err := s.SearchMessages(ctx, MessagesSearchParams{From: from, To: to, Limit: 10, ServiceFilter: []string{svc}, OnlyFailed: true})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string][2]string{}
		for _, r := range rows {
			got[r.TraceID] = [2]string{r.ExceptionType, r.ExceptionMessage}
		}
		if got[tEvent] != [2]string{exType, exMsg} {
			t.Errorf("event trace: %v", got[tEvent])
		}
		if got[tAttr] != [2]string{"TimeoutException", "timed out"} {
			t.Errorf("attribute trace: %v", got[tAttr])
		}
		if got[tPlain] != [2]string{} {
			t.Errorf("status-only trace should name no exception: %v", got[tPlain])
		}
	})

	t.Run("an error-type filter matches the event", func(t *testing.T) {
		rows, err := s.SearchMessages(ctx, MessagesSearchParams{
			From: from, To: to, Limit: 10, ServiceFilter: []string{svc},
			Clauses: []string{"(StatusMessage = ? OR ExceptionType = ? OR SpanAttributes['exception.type'] = ?)"},
			Args:    []any{exType, exType, exType},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].TraceID != tEvent {
			t.Errorf("want only %s, got %d rows", tEvent, len(rows))
		}
	})

	t.Run("search finds the exception message", func(t *testing.T) {
		rows, err := s.SearchTraces(ctx, "provider returned", from, to, 10, []string{svc}, false, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].TraceID != tEvent {
			t.Errorf("want %s, got %d rows", tEvent, len(rows))
		}
	})

	t.Run("the breakdown splits by exception", func(t *testing.T) {
		b, err := s.ErrorTracesByDimension(ctx, []string{svc}, from, to, nil, ErrorDimension{Exception: true}, 10)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]uint64{}
		for _, x := range b {
			got[x.Value] = x.Errors
		}
		if got[exType] != 1 || got["TimeoutException"] != 1 || len(got) != 2 {
			t.Errorf("buckets: %v", got)
		}
	})

	t.Run("the latest exception is the newest that recorded one", func(t *testing.T) {
		ex, ok, err := s.LatestException(ctx, []string{svc}, from, to, nil)
		if err != nil {
			t.Fatal(err)
		}
		// The status-only span is newer but recorded nothing.
		if !ok || ex.Type != "TimeoutException" || ex.TraceID != tAttr {
			t.Errorf("got %+v ok=%v", ex, ok)
		}
		// Bounds bind at second precision, hence the gap between spans.
		ex, ok, err = s.LatestException(ctx, []string{svc}, from, now.Add(-7*time.Second), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !ok || ex.Type != exType || ex.Message != exMsg || ex.Stacktrace != exStack {
			t.Errorf("event exception: %+v ok=%v", ex, ok)
		}
	})
}
