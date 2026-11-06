// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	imclickhouse "github.com/sluicio/sluicio-app/pkg/clickhouse"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
)

// StorageRetryAfter is what a refused sender is told to wait. Long enough
// not to be hammered by every collector at once; short enough that once
// retention has freed space, or the limit was raised, data flows again
// within minutes rather than after the sender has given up.
const StorageRetryAfter = 5 * time.Minute

// StorageGate keeps the instance within its storage limit
// (SLUICIO_STORAGE_LIMIT_GB). By default it deletes the oldest day of
// telemetry when the limit is reached, and refuses telemetry only when
// there is no earlier day left to delete. With
// SLUICIO_STORAGE_LIMIT_ACTION=pause it refuses telemetry instead and
// deletes nothing.
//
// The size is measured in the background, not per request: a request
// only reads an atomic. Refusal is OTLP's back-off answer, 429 with a
// Retry-After, so a collector keeps the data in its retry queue for a
// while instead of dropping it at once - a limit raised in time loses
// nothing. Ingest resumes on its own when usage falls back below the
// limit, which retention does as data ages out.
//
// If a measurement fails the previous answer stands. With no measurement
// yet the gate is open: an instance that cannot read its own size must
// not stop receiving telemetry because of it.
type StorageGate struct {
	limit  imclickhouse.StorageLimit
	logger *slog.Logger

	// measureFn and deleteOldestFn are the database calls, replaceable so
	// the state machine is testable without one.
	measureFn      func(context.Context) (uint64, error)
	deleteOldestFn func(context.Context) (string, error)

	stopped  atomic.Bool
	usage    atomic.Uint64
	measured atomic.Int64 // unix seconds of the last good measurement
}

// NewStorageGate returns a gate for limit. A zero limit makes a gate that
// never refuses and never measures.
func NewStorageGate(conn driver.Conn, limit imclickhouse.StorageLimit, logger *slog.Logger) *StorageGate {
	return &StorageGate{
		limit:  limit,
		logger: logger,
		measureFn: func(ctx context.Context) (uint64, error) {
			u, err := imclickhouse.MeasureStorage(ctx, conn)
			return u.Bytes, err
		},
		deleteOldestFn: func(ctx context.Context) (string, error) {
			return imclickhouse.DeleteOldestDay(ctx, conn)
		},
	}
}

// maxDaysPerMeasure bounds how many days one measurement may delete, so
// a limit lowered far below usage is worked down over a few minutes
// rather than in one burst of drops.
const maxDaysPerMeasure = 30

// Run measures now and then every interval until ctx ends.
func (g *StorageGate) Run(ctx context.Context, interval time.Duration) {
	if g.limit.Bytes == 0 {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	g.measure(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.measure(ctx)
		}
	}
}

func (g *StorageGate) measure(ctx context.Context) {
	mctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	bytes, err := g.measureFn(mctx)
	if err != nil {
		g.logger.Warn("storage measurement failed; keeping the last state", "err", err, "stopped", g.stopped.Load())
		return
	}
	if g.limit.Action != imclickhouse.StoragePause {
		bytes = g.makeRoom(mctx, bytes)
	}
	g.observe(bytes)
}

// makeRoom deletes the oldest days of telemetry while usage is at the
// limit and an earlier day than today is left, and returns the usage it
// leaves. When it cannot get under the limit, observe refuses telemetry,
// which protects the disk when today's data alone fills it.
func (g *StorageGate) makeRoom(ctx context.Context, bytes uint64) uint64 {
	for i := 0; bytes >= g.limit.Bytes && i < maxDaysPerMeasure; i++ {
		day, err := g.deleteOldestFn(ctx)
		if err != nil {
			g.logger.Warn("storage limit reached; deleting the oldest telemetry failed", "err", err)
			return bytes
		}
		if day == "" {
			return bytes
		}
		after, err := g.measureFn(ctx)
		if err != nil {
			g.logger.Warn("storage measurement after deleting failed", "err", err)
			return bytes
		}
		g.logger.Warn("storage limit reached: deleted the oldest day of telemetry",
			"day", day, "used_bytes_before", bytes, "used_bytes_after", after, "limit_bytes", g.limit.Bytes)
		bytes = after
	}
	return bytes
}

// observe applies one measurement. Split from measure so the state
// machine is testable without a database.
func (g *StorageGate) observe(bytes uint64) {
	was := g.stopped.Load()
	now := g.limit.Stopped(bytes, was)
	g.usage.Store(bytes)
	g.measured.Store(time.Now().Unix())
	g.stopped.Store(now)
	switch {
	case now && !was:
		g.logger.Warn("storage limit reached: refusing telemetry until usage falls below the limit",
			"used_bytes", bytes, "limit_bytes", g.limit.Bytes)
	case !now && was:
		g.logger.Info("storage back under the limit: accepting telemetry again",
			"used_bytes", bytes, "limit_bytes", g.limit.Bytes)
	}
}

// Wrap refuses requests for signal while the limit is reached.
func (g *StorageGate) Wrap(signal string, h http.Handler) http.Handler {
	if g == nil || g.limit.Bytes == 0 {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.stopped.Load() {
			h.ServeHTTP(w, r)
			return
		}
		if c := countersFor(signal); c != nil {
			c.storageRefused.Add(1)
		}
		w.Header().Set("Retry-After", strconv.Itoa(int(StorageRetryAfter.Seconds())))
		httpserver.WriteError(w, http.StatusTooManyRequests,
			"this instance has reached its storage limit and is not accepting telemetry; it resumes once usage falls below the limit")
	})
}

// Snapshot is the gate's state for /healthz.
func (g *StorageGate) Snapshot() map[string]any {
	if g == nil || g.limit.Bytes == 0 {
		return nil
	}
	out := map[string]any{
		"limit_bytes": g.limit.Bytes,
		"action":      g.limit.Action,
		"stopped":     g.stopped.Load(),
	}
	if at := g.measured.Load(); at > 0 {
		out["used_bytes"] = g.usage.Load()
		out["measured_at"] = time.Unix(at, 0).UTC()
	}
	return out
}
