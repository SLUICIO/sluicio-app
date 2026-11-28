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

// Selection is what a change acts on, as rows of one table: a metric, a
// service's logs below a severity, a service's spans carrying an
// attribute. The measurement and the count per collector read the same
// rows.
type Selection struct {
	table string
	cond  string
	args  []any
}

// MetricSelection is one metric.
func MetricSelection(metric string) Selection {
	return Selection{table: "metrics", cond: "MetricName = ?", args: []any{metric}}
}

// LogSelection is one service's records with a severity below below (a
// severity number: 9 INFO, 13 WARN), records without a severity left out
// as the change leaves them.
func LogSelection(service string, below int32) Selection {
	return Selection{table: "logs", cond: "ServiceName = ? AND SeverityNumber > 0 AND SeverityNumber < ?", args: []any{service, below}}
}

// SpanSelection is one service's spans that carry what a change removes:
// the attribute key, every key starting with it when prefix is set, or
// with hash set the key holding a value that is not a SHA-256 hash yet.
func SpanSelection(service, key string, prefix, hash bool) Selection {
	cond, args := `mapContains(SpanAttributes, ?)`, []any{key}
	switch {
	case prefix:
		cond = `arrayExists(k -> startsWith(k, ?), mapKeys(SpanAttributes))`
	case hash:
		cond, args = `mapContains(SpanAttributes, ?) AND NOT match(SpanAttributes[?], '^[0-9a-f]{64}$')`, []any{key, key}
	}
	return Selection{table: "traces", cond: "ServiceName = ? AND " + cond, args: append([]any{service}, args...)}
}

// SpanNameSelection is one service's internal spans of one name that did
// not fail: what a T7 change drops.
func SpanNameSelection(service, name string) Selection {
	return Selection{table: "traces", cond: "ServiceName = ? AND SpanName = ? AND SpanKind = 'Internal' AND StatusCode != 'Error'", args: []any{service, name}}
}

// MeasureMetric measures metric for one org around appliedAt, as of now.
func MeasureMetric(ctx context.Context, conn driver.Conn, orgID uuid.UUID, metric string, appliedAt, now time.Time) (Effect, error) {
	return Measure(ctx, conn, orgID, MetricSelection(metric), "", appliedAt, now)
}

// MeasureLogs measures LogSelection around appliedAt.
func MeasureLogs(ctx context.Context, conn driver.Conn, orgID uuid.UUID, service string, below int32, appliedAt, now time.Time) (Effect, error) {
	return Measure(ctx, conn, orgID, LogSelection(service, below), "", appliedAt, now)
}

// MeasureSpans measures SpanSelection around appliedAt.
func MeasureSpans(ctx context.Context, conn driver.Conn, orgID uuid.UUID, service, key string, prefix, hash bool, appliedAt, now time.Time) (Effect, error) {
	return Measure(ctx, conn, orgID, SpanSelection(service, key, prefix, hash), "", appliedAt, now)
}

// Measure counts sel before and after appliedAt for one org; with
// collector set, only the rows stamped by that collector (its instance
// id, the CollectorId column).
func Measure(ctx context.Context, conn driver.Conn, orgID uuid.UUID, sel Selection, collector string, appliedAt, now time.Time) (Effect, error) {
	where, args := "OrganizationId = ? AND "+sel.cond, append([]any{orgID.String()}, sel.args...)
	if collector != "" {
		where += " AND CollectorId = ?"
		args = append(args, collector)
	}
	return measure(ctx, conn, appliedAt, now, `
		SELECT countIf(Timestamp < ?), countIf(Timestamp >= ?)
		FROM `+sel.table+`
		WHERE `+where+` AND Timestamp >= ? AND Timestamp < ?`, args...)
}

// CarriedBy counts sel per collector stamp in [from, to): which collector
// the rows came through. Rows nobody stamped count under "".
func CarriedBy(ctx context.Context, conn driver.Conn, orgID uuid.UUID, sel Selection, from, to time.Time) (map[string]uint64, error) {
	rows, err := conn.Query(ctx, `
		SELECT CollectorId, count() FROM `+sel.table+`
		WHERE OrganizationId = ? AND `+sel.cond+` AND Timestamp >= ? AND Timestamp < ?
		GROUP BY CollectorId`, append(append([]any{orgID.String()}, sel.args...), from, to)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uint64{}
	for rows.Next() {
		var id string
		var n uint64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// CollectorVolume is what one collector stamped in [from, to), per signal:
// spans, metric data points, log records.
type CollectorVolume struct {
	Spans        uint64 `json:"spans"`
	MetricPoints uint64 `json:"metric_points"`
	LogRecords   uint64 `json:"log_records"`
}

// VolumeOf counts the rows one collector stamped in [from, to).
func VolumeOf(ctx context.Context, conn driver.Conn, orgID uuid.UUID, collector string, from, to time.Time) (CollectorVolume, error) {
	var v CollectorVolume
	for _, q := range []struct {
		table string
		into  *uint64
	}{{"traces", &v.Spans}, {"metrics", &v.MetricPoints}, {"logs", &v.LogRecords}} {
		if err := conn.QueryRow(ctx, `SELECT count() FROM `+q.table+`
			WHERE OrganizationId = ? AND CollectorId = ? AND Timestamp >= ? AND Timestamp < ?`,
			orgID.String(), collector, from, to).Scan(q.into); err != nil {
			return CollectorVolume{}, err
		}
	}
	return v, nil
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
