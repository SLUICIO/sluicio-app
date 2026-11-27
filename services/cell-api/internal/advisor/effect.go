// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Measuring what a change did (collector management, stage 3): a metric's
// ingest in the window before a collector applied a change and in the
// window after. Counted the way MetricsSupply counts the finding, so the
// number that justified the change and the number that measures it are
// the same kind of number.

package advisor

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// EffectWindow is how long each side of a measurement is at most. Both
// sides are always the same length, the time since the change applied up
// to a day: traffic is not flat across a day, and a day before against an
// hour after compares a daily average with whatever hour it is now.
const EffectWindow = 24 * time.Hour

// IngestLag is left off the recent end of a measurement: the last
// minutes are still arriving (batches, scrape intervals, retries), and
// counting them would read as a drop the change did not cause.
const IngestLag = 5 * time.Minute

// EffectMinAfter is how long after the change is applied before a
// measurement is reported: shorter, and a scrape interval or a batch
// still in flight decides the answer.
const EffectMinAfter = 15 * time.Minute

// Window is one side of a measurement.
type Window struct {
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Rows    uint64    `json:"rows"`
	PerHour float64   `json:"per_hour"`
}

// MetricEffect is a metric's volume around the moment a change applied.
type MetricEffect struct {
	Metric    string    `json:"metric"`
	AppliedAt time.Time `json:"applied_at"`
	// Ready is false until EffectMinAfter has passed since AppliedAt.
	Ready  bool   `json:"ready"`
	Before Window `json:"before"`
	After  Window `json:"after"`
	// ChangePct is the change in rows per hour, -100 when the metric
	// stopped arriving. Nil when nothing arrived before.
	ChangePct *float64 `json:"change_pct,omitempty"`
}

// MeasureMetric measures metric for one org around appliedAt, as of now.
func MeasureMetric(ctx context.Context, conn driver.Conn, orgID uuid.UUID, metric string, appliedAt, now time.Time) (MetricEffect, error) {
	e := MetricEffect{Metric: metric, AppliedAt: appliedAt}
	end := appliedAt.Add(EffectWindow)
	if settled := now.Add(-IngestLag); settled.Before(end) {
		end = settled
	}
	if end.Before(appliedAt) {
		end = appliedAt
	}
	e.Before = Window{From: appliedAt.Add(-end.Sub(appliedAt)), To: appliedAt}
	e.After = Window{From: appliedAt, To: end}
	if end.Sub(appliedAt) < EffectMinAfter {
		return e, nil
	}
	e.Ready = true
	var before, after uint64
	if err := conn.QueryRow(ctx, `
		SELECT countIf(Timestamp < ?), countIf(Timestamp >= ?)
		FROM metrics
		WHERE OrganizationId = ? AND MetricName = ? AND Timestamp >= ? AND Timestamp < ?`,
		appliedAt, appliedAt, orgID.String(), metric, e.Before.From, e.After.To).Scan(&before, &after); err != nil {
		return MetricEffect{}, err
	}
	e.Before.Rows, e.After.Rows = before, after
	e.Before.PerHour = perHour(before, e.Before)
	e.After.PerHour = perHour(after, e.After)
	if e.Before.PerHour > 0 {
		pct := (e.After.PerHour - e.Before.PerHour) / e.Before.PerHour * 100
		e.ChangePct = &pct
	}
	return e, nil
}

func perHour(rows uint64, w Window) float64 {
	h := w.To.Sub(w.From).Hours()
	if h <= 0 {
		return 0
	}
	return float64(rows) / h
}
