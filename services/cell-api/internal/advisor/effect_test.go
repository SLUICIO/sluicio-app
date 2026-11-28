// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package advisor

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Too early to measure: no query is made (the connection is nil), and the
// windows still say what will be compared.
func TestMeasureMetricWindows(t *testing.T) {
	applied := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	e, err := MeasureMetric(context.Background(), nil, uuid.New(), "m", applied, applied.Add(10*time.Minute))
	if err != nil || e.Ready {
		t.Fatalf("ten minutes after applying: ready=%v err=%v", e.Ready, err)
	}
	// Ten minutes less the ingest margin: five minutes on each side.
	if got := e.After.To.Sub(e.After.From); got != 5*time.Minute {
		t.Fatalf("after window %s, want 5m", got)
	}
	if e.Before.To != applied || e.Before.To.Sub(e.Before.From) != e.After.To.Sub(e.After.From) {
		t.Fatalf("windows differ: before %v-%v after %v-%v", e.Before.From, e.Before.To, e.After.From, e.After.To)
	}
	// Right after applying, the recent edge never goes before the change.
	e, _ = MeasureMetric(context.Background(), nil, uuid.New(), "m", applied, applied.Add(time.Minute))
	if e.After.To != applied || e.Before.From != applied {
		t.Fatalf("a minute after applying: %+v", e)
	}
}
