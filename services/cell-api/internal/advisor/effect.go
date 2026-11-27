// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Measuring what a change did (collector management, stage 3): the
// ingest of what it drops (a metric, or a service's logs below a
// severity) in the window before a collector applied a change and in the
// window after. Counted the way the supply queries count the finding, so
// the number that justified the change and the number that measures it
// are the same kind of number.

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

// Effect is the volume of what a change drops, around the moment it
// applied.
type Effect struct {
	AppliedAt time.Time `json:"applied_at"`
	// Ready is false until EffectMinAfter has passed since AppliedAt.
	Ready  bool   `json:"ready"`
	Before Window `json:"before"`
	After  Window `json:"after"`
	// ChangePct is the change in rows per hour, -100 when it stopped
	// arriving. Nil when nothing arrived before.
	ChangePct *float64 `json:"change_pct,omitempty"`
}

// MeasureMetric measures metric for one org around appliedAt, as of now.
func MeasureMetric(ctx context.Context, conn driver.Conn, orgID uuid.UUID, metric string, appliedAt, now time.Time) (Effect, error) {
	return measure(ctx, conn, appliedAt, now, `
		SELECT countIf(Timestamp < ?), countIf(Timestamp >= ?)
		FROM metrics
		WHERE OrganizationId = ? AND MetricName = ? AND Timestamp >= ? AND Timestamp < ?`, orgID.String(), metric)
}

// MeasureLogs measures one service's log records with a severity below
// below (a severity number: 9 INFO, 13 WARN), records without a severity
// left out as the change leaves them.
func MeasureLogs(ctx context.Context, conn driver.Conn, orgID uuid.UUID, service string, below int32, appliedAt, now time.Time) (Effect, error) {
	return measure(ctx, conn, appliedAt, now, `
		SELECT countIf(Timestamp < ?), countIf(Timestamp >= ?)
		FROM logs
		WHERE OrganizationId = ? AND ServiceName = ? AND SeverityNumber > 0 AND SeverityNumber < ?
		  AND Timestamp >= ? AND Timestamp < ?`, orgID.String(), service, below)
}

// measure runs query, which counts rows before and after its first two
// arguments (appliedAt) within its last two (the windows' outer bounds),
// with args between.
func measure(ctx context.Context, conn driver.Conn, appliedAt, now time.Time, query string, args ...any) (Effect, error) {
	e := Effect{AppliedAt: appliedAt}
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
	all := append(append([]any{appliedAt, appliedAt}, args...), e.Before.From, e.After.To)
	if err := conn.QueryRow(ctx, query, all...).Scan(&before, &after); err != nil {
		return Effect{}, err
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
