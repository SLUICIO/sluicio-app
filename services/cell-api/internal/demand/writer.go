// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The demand ledger's write path (docs/telemetry-advisor-design.md §2):
// counters recording that some telemetry was CONSUMED, so the Telemetry
// Advisor can later contrast demand with ingest volume.
//
// Record() sits on read handlers, so it must never block, never error
// and never touch the network: it bumps an in-memory counter under a
// short mutex and returns. A ticker flushes the accumulated map to
// ClickHouse in one batch. Losing a flush costs a few counter bumps in
// an advisory feature — worth far less than a millisecond on every
// query, so nothing here is durable-on-write.
//
// PRIVACY IS STRUCTURAL, not a policy on top: the buffer's key has no
// room for a user id, and no call site can pass one. A ledger that
// cannot answer "who looked at this" cannot later be asked to.

package demand

import (
	"context"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/cellhealth"
	"log/slog"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// Signal is the telemetry family a demand row is about.
type Signal string

const (
	SignalTrace  Signal = "trace"
	SignalLog    Signal = "log"
	SignalMetric Signal = "metric"
	// SignalAlert is engagement with an ALERT rather than with
	// telemetry: someone followed a notification's deep link back into
	// the app. The Key is the rule id, which is what the Alert Fatigue
	// Advisor asks about — "did anyone act on this rule's pages" — and
	// it is the only demand signal that measures attention rather than
	// consumption.
	SignalAlert Signal = "alert"
)

// ConsumerKind is who consumed it. Beyond provenance this disambiguates
// the Key column's namespaces — a completion rule's Key is a span name,
// a template's is a metric name, a matcher's is an attribute key, and
// they may collide as strings without meaning the same thing.
type ConsumerKind string

const (
	KindHuman      ConsumerKind = "human"      // someone queried/charted it
	KindRule       ConsumerKind = "rule"       // an alert rule references it
	KindFacet      ConsumerKind = "facet"      // a facet mapping or key attribute
	KindMatcher    ConsumerKind = "matcher"    // an integration matcher
	KindDashboard  ConsumerKind = "dashboard"  // a dashboard widget reads it
	KindCompletion ConsumerKind = "completion" // a trace-completion stage
	KindTemplate   ConsumerKind = "template"   // monitoring template / system type check
	KindView       ConsumerKind = "view"       // a saved message view's filter
)

// bucket is one ledger row's identity. Every field is part of the
// ClickHouse ORDER BY, so the map key and the storage key agree.
type bucket struct {
	day     time.Time // UTC date
	org     string
	signal  Signal
	service string
	key     string
	kind    ConsumerKind
}

// maxBuckets caps the buffer so a pathological caller (a loop bumping a
// unique key per row) costs bounded memory rather than the process. At
// the cap we keep counting known buckets and drop new ones — degrading
// the ledger's completeness, never the cell's health.
const maxBuckets = 50_000

// Writer accumulates demand counters and flushes them in batches.
// The zero value is not usable; use NewWriter. A nil *Writer is safe to
// call — handlers can hold one unconditionally without nil checks.
type Writer struct {
	conn  driver.Conn
	log   *slog.Logger
	every time.Duration

	mu       sync.Mutex
	buf      map[bucket]uint64
	dropped  uint64
	warnedAt time.Time
}

// NewWriter builds a writer. every <= 0 defaults to 30s — long enough
// that a busy cell batches meaningfully, short enough that a restart
// loses little. conn may be nil (CH unavailable), which makes every
// Record a no-op.
func NewWriter(conn driver.Conn, logger *slog.Logger, every time.Duration) *Writer {
	if every <= 0 {
		every = 30 * time.Second
	}
	return &Writer{conn: conn, log: logger, every: every, buf: map[bucket]uint64{}}
}

// Record bumps the counter for one consumption. Safe on a nil receiver,
// safe from any goroutine, and never returns an error — a demand miss
// must never surface on a user's read.
func (w *Writer) Record(orgID uuid.UUID, signal Signal, service, key string, kind ConsumerKind) {
	w.RecordN(orgID, signal, service, key, kind, 1)
}

// RecordN is Record with an explicit count, for the sweep's one-shot
// "this config references this key today" rows.
func (w *Writer) RecordN(orgID uuid.UUID, signal Signal, service, key string, kind ConsumerKind, n uint64) {
	if w == nil || w.conn == nil || n == 0 || signal == "" || kind == "" || orgID == uuid.Nil {
		return
	}
	b := bucket{
		day:     time.Now().UTC().Truncate(24 * time.Hour),
		org:     orgID.String(),
		signal:  signal,
		service: service,
		key:     key,
		kind:    kind,
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, known := w.buf[b]; !known && len(w.buf) >= maxBuckets {
		w.dropped++
		return
	}
	w.buf[b] += n
}

// RecordKeys is the sweep's convenience for a slice of keys sharing a
// scope. Empty and duplicate keys are skipped — duplicates because the
// map would merge them anyway and a caller shouldn't have to dedupe.
func (w *Writer) RecordKeys(orgID uuid.UUID, signal Signal, service string, keys []string, kind ConsumerKind) {
	for _, k := range keys {
		if k == "" {
			continue
		}
		w.Record(orgID, signal, service, k, kind)
	}
}

// Run flushes on a ticker until ctx ends, then flushes once more so a
// clean shutdown doesn't discard the last window. The final flush gets
// its own short-lived context: ctx is already cancelled by then, and a
// cancelled context cannot issue the INSERT.
func (w *Writer) Run(ctx context.Context) {
	t := time.NewTicker(w.every)
	cellhealth.Register("demand-writer", w.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			w.flushLogged(final)
			return
		case <-t.C:
			w.flushLogged(ctx)
			// End of cycle, not start: a loop wedged inside its
			// own body is exactly what this catches.
			cellhealth.Beat("demand-writer")
		}
	}
}

func (w *Writer) flushLogged(ctx context.Context) {
	if err := w.Flush(ctx); err != nil {
		// Rate-limit the complaint: if the table is missing (cell-ingest
		// owns the ClickHouse migrations and may not have run yet) this
		// would otherwise log on every tick forever.
		if time.Since(w.warnedAt) > 5*time.Minute {
			w.warnedAt = time.Now()
			w.log.Warn("demand ledger flush failed; counters dropped", "err", err)
		}
	}
}

// Flush writes and clears the buffer. Exported for tests and for the
// sweep, which flushes explicitly at the end of its run rather than
// leaving a day's config demand sitting in memory until the next tick.
func (w *Writer) Flush(ctx context.Context) error {
	if w == nil || w.conn == nil {
		return nil
	}
	w.mu.Lock()
	if len(w.buf) == 0 {
		dropped := w.dropped
		w.dropped = 0
		w.mu.Unlock()
		if dropped > 0 {
			w.log.Warn("demand ledger buffer was full; some keys not counted", "dropped", dropped)
		}
		return nil
	}
	// Take the buffer and release the lock before touching the network —
	// Record() must not wait on ClickHouse.
	pending := w.buf
	dropped := w.dropped
	w.buf = map[bucket]uint64{}
	w.dropped = 0
	w.mu.Unlock()

	if dropped > 0 {
		w.log.Warn("demand ledger buffer was full; some keys not counted", "dropped", dropped)
	}

	batch, err := w.conn.PrepareBatch(ctx, `INSERT INTO telemetry_demand
		(Day, OrganizationId, Signal, ServiceName, Key, ConsumerKind, Hits)`)
	if err != nil {
		return err
	}
	for b, hits := range pending {
		if err := batch.Append(b.day, b.org, string(b.signal), b.service, b.key, string(b.kind), hits); err != nil {
			return err
		}
	}
	return batch.Send()
}

// Pending reports the number of buffered buckets. Tests only — the
// counter is not a health signal worth exposing.
func (w *Writer) Pending() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.buf)
}
