// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package retention keeps ClickHouse's per-table TTL in sync with the
// retention policy stored in Postgres (cell_settings).
//
// Two paths drive ClickHouse from this package:
//
//	ApplyOnce(ctx) - synchronous, called by the PATCH handler so the
//	                 user sees their change reflected in CH before the
//	                 response returns. Updates last_applied_at.
//
//	Run(ctx)       - safety net. Periodic loop (default 1h) that
//	                 unconditionally re-applies the configured TTL.
//	                 Repairs drift if someone hand-edited the CH TTL,
//	                 or if a PATCH succeeded against Postgres but
//	                 failed mid-flight against CH. Does NOT touch
//	                 last_applied_at — that's "when the user changed
//	                 this", not "when we last checked".
//
// Why not run DELETE WHERE Timestamp < X ourselves? ClickHouse's TTL
// drops parts at merge time — effectively free. A DELETE creates a
// long-running mutation that competes with ingest. ALTER TABLE …
// MODIFY TTL is the right primitive: instant metadata change, async
// part eviction at the engine's pace.
//
// ALTER TABLE … MODIFY TTL is idempotent at the metadata level: if
// you set the same TTL twice, the second call is a no-op. So the
// "always apply" model in Run() has zero steady-state cost.

package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/sluicio/sluicio-app/pkg/audit"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/settings"
)

// Enforcer is the periodic-loop owner.
type Enforcer struct {
	settings *settings.Store
	ch       driver.Conn
	logger   *slog.Logger
	interval time.Duration

	// Audit, when wired (Enterprise builds), lets the same loop prune
	// audit_log rows past the configured audit retention. Postgres
	// DELETE, not a CH TTL — the audit store owns the chain-safe
	// mechanics (pruning anchor); this loop just decides the cutoff.
	Audit audit.Recorder
}

// New wires the dependencies. interval=0 picks a sane default (1h).
func New(s *settings.Store, ch driver.Conn, logger *slog.Logger, interval time.Duration) *Enforcer {
	if interval <= 0 {
		interval = time.Hour
	}
	return &Enforcer{settings: s, ch: ch, logger: logger, interval: interval}
}

// Run loops until ctx is cancelled. Applies once immediately on start
// (so a fresh deployment converges before any user-driven change
// arrives), then on every tick.
func (e *Enforcer) Run(ctx context.Context) {
	if err := e.applyAll(ctx, false); err != nil {
		e.logger.Warn("retention initial apply failed", "err", err)
	}
	t := time.NewTicker(e.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.applyAll(ctx, false); err != nil {
				e.logger.Warn("retention apply failed", "err", err)
			}
		}
	}
}

// ApplyOnce is the synchronous "do it now" call the API handler uses
// after a successful PATCH. Updates last_applied_at on success.
func (e *Enforcer) ApplyOnce(ctx context.Context) error {
	return e.applyAll(ctx, true)
}

// applyAll reads the configured retention for each telemetry type and
// pushes it into the matching ClickHouse table. When recordApplied is
// true (the PATCH-handler path), we update cell_settings.
// last_applied so the UI shows when the user-visible change landed.
// When false (the periodic-loop path), we don't — the enforcer is a
// safety net, not a user-facing event.
//
// Tries all three types even if one fails; returns the first error.
func (e *Enforcer) applyAll(ctx context.Context, recordApplied bool) error {
	policy, err := e.settings.GetRetention(ctx)
	if err != nil {
		return fmt.Errorf("retention: load policy: %w", err)
	}
	var firstErr error
	for _, t := range settings.AllTelemetryTypes {
		days := daysFor(policy, t)
		if err := e.applyOne(ctx, t, days, recordApplied); err != nil {
			e.logger.Warn("retention apply per-type failed", "type", t, "days", days, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := e.pruneAudit(ctx); err != nil {
		e.logger.Warn("audit retention prune failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// pruneAudit deletes audit entries older than the configured audit
// retention. No-op when no audit recorder is wired (community builds).
func (e *Enforcer) pruneAudit(ctx context.Context) error {
	if e.Audit == nil {
		return nil
	}
	days, err := e.settings.GetAuditRetentionDays(ctx)
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	n, err := e.Audit.Prune(ctx, cutoff)
	if err != nil {
		return err
	}
	if n > 0 {
		e.logger.Info("audit retention pruned", "rows", n, "older_than_days", days)
	}
	return nil
}

// daysFor maps the policy struct into a per-type day count.
func daysFor(p settings.RetentionPolicy, t settings.TelemetryType) int {
	switch t {
	case settings.TelemetryTraces:
		return p.Traces.Days
	case settings.TelemetryLogs:
		return p.Logs.Days
	case settings.TelemetryMetrics:
		return p.Metrics.Days
	}
	return 14
}

// tableFor maps a telemetry type to its ClickHouse table name.
func tableFor(t settings.TelemetryType) string {
	switch t {
	case settings.TelemetryTraces:
		return "traces"
	case settings.TelemetryLogs:
		return "logs"
	case settings.TelemetryMetrics:
		return "metrics"
	}
	return ""
}

// applyOne issues the ALTER on one table. The ALTER is idempotent —
// if the table's TTL already matches, ClickHouse treats it as a no-op
// at the storage level. So we don't bother reading the current TTL
// first; that read costs more than the would-be redundant ALTER.
func (e *Enforcer) applyOne(ctx context.Context, t settings.TelemetryType, days int, recordApplied bool) error {
	table := tableFor(t)
	if table == "" {
		return fmt.Errorf("retention: no table mapping for %q", t)
	}
	if days < settings.RetentionMinDays || days > settings.RetentionMaxDays {
		// Defensive: the API layer validates, but if a hand-edited
		// row in cell_settings is out of range we'd otherwise issue
		// a syntactically-valid but semantically-broken ALTER.
		return fmt.Errorf("retention: %s days=%d out of bounds", t, days)
	}
	// MODIFY TTL is metadata-only. The actual data drop happens during
	// the next merge cycle on each partition.
	alter := fmt.Sprintf(
		"ALTER TABLE %s MODIFY TTL toDate(Timestamp) + INTERVAL %d DAY",
		table, days,
	)
	if err := e.ch.Exec(ctx, alter); err != nil {
		return fmt.Errorf("retention: alter %s TTL: %w", table, err)
	}
	if recordApplied {
		if err := e.settings.RecordRetentionApplied(ctx, t, time.Now().UTC()); err != nil {
			// Non-fatal — the CH-side change succeeded. The UI's
			// "last applied at" will lag until the next user-driven
			// update.
			e.logger.Warn("retention: record applied failed", "err", err, "table", table)
		}
	}
	return nil
}
