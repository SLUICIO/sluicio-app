// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package cellhealth reports whether this cell is doing its job
// (issue #14).
//
// The part that matters, and the part most likely to be skipped: a cell
// runs about ten background loops — alert evaluation, the service
// reconciler, notification delivery, trace-completion evaluation,
// retention, the demand writer and sweeper, the advisor, event
// subscriptions, proposal expiry. Every one of them can stop without a
// single request failing and without an error being logged. A cell whose
// alert evaluation quietly died looks perfectly healthy and notifies
// nobody about anything, forever.
//
// That is Sluicio's own core insight turned on itself. We sell a
// low-traffic check to customers for exactly this shape of failure,
// because SILENCE LOOKS LIKE HEALTH. So each loop records when it last
// completed a cycle, and a loop that has not completed within a
// generous multiple of its own interval is reported as a fault rather
// than as an absence.
//
// Two rules this package holds to, both settled on the issue:
//
//   - AGGREGATE ONLY. A cell operator and an organisation's admins can
//     be different parties. Health broken down per org would let the
//     operator read each tenant's activity level — how much they ingest,
//     when they are busy — which is customer information wearing
//     operational clothing. Nothing here is keyed by organisation.
//
//   - CAPACITY IS REPORTED, NOT JUDGED. What counts as "too full"
//     differs per customer, and a red state we invented on their behalf
//     is a red state they learn to ignore. A fact becomes a fault only
//     when it has a measurable consequence: a disk at 85% is an
//     observation; writes failing because a disk is full is a dependency
//     that has stopped working, which is a different question.

package cellhealth

import (
	"sort"
	"sync"
	"time"
)

// Status is a loop's or a dependency's verdict.
type Status string

const (
	// StatusOK means observed working.
	StatusOK Status = "ok"
	// StatusStale means a loop has not completed a cycle in far longer
	// than its own interval. It has not necessarily died, but nothing
	// has confirmed it is alive either, and for a loop that nobody
	// calls that is the strongest signal available.
	StatusStale Status = "stale"
	// StatusUnknown means the loop is registered but has not completed a
	// first cycle yet. Distinct from stale: a cell that started ninety
	// seconds ago is not broken, and saying so would train operators to
	// ignore the page during exactly the window when it matters.
	StatusUnknown Status = "unknown"
)

// staleFactor is how many of its own intervals a loop may miss before
// it is called stale. Deliberately generous: a loop that runs every
// minute and is briefly blocked on a slow query is not a fault, and an
// alarm that cries wolf is worse than none. Three consecutive misses is
// a pattern rather than a hiccup.
const staleFactor = 3

// minGrace floors the staleness window for very frequent loops. A loop
// on a five-second interval would otherwise be called stale after
// fifteen seconds, which one slow ClickHouse query can cause.
const minGrace = 2 * time.Minute

// LoopHealth is one background loop's state.
type LoopHealth struct {
	Name string `json:"name"`
	// Interval is how often the loop intends to run.
	Interval string `json:"interval"`
	// LastCompleted is when it last finished a cycle. Nil until the
	// first one completes.
	LastCompleted *time.Time `json:"last_completed,omitempty"`
	// Overdue is how far past its staleness window it is. Only set when
	// Status is stale, and present so an operator can tell "just tipped
	// over" from "dead since yesterday" without doing arithmetic.
	Overdue string `json:"overdue,omitempty"`
	Status  Status `json:"status"`
}

// Registry tracks the cell's background loops.
//
// Registration is separate from beating so a loop that has never run
// still appears. A loop that vanished from the report entirely would be
// indistinguishable from one that was never wired up, and the whole
// point is to notice absence.
type Registry struct {
	mu    sync.RWMutex
	loops map[string]*loopState
	now   func() time.Time
}

type loopState struct {
	interval time.Duration
	last     time.Time
}

// NewRegistry returns an empty registry using the wall clock.
func NewRegistry() *Registry {
	return &Registry{loops: map[string]*loopState{}, now: time.Now}
}

// WithClock returns a registry with a fixed clock, for tests.
func WithClock(now func() time.Time) *Registry {
	return &Registry{loops: map[string]*loopState{}, now: now}
}

// Register declares a loop and how often it intends to run. Safe to
// call more than once for the same name; the latest interval wins.
func (r *Registry) Register(name string, interval time.Duration) {
	if r == nil || name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.loops[name]; ok {
		s.interval = interval
		return
	}
	r.loops[name] = &loopState{interval: interval}
}

// Beat records that a loop finished a cycle.
//
// Called at the END of a cycle rather than the start: a loop wedged
// inside its own body is exactly the failure this exists to catch, and
// beating on entry would report it as healthy forever.
//
// Never blocks on anything but a short mutex, and is safe on a nil
// registry so a caller does not need to check.
func (r *Registry) Beat(name string) {
	if r == nil || name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.loops[name]
	if !ok {
		s = &loopState{}
		r.loops[name] = s
	}
	s.last = r.now()
}

// grace is how long a loop may go without completing before it is
// called stale.
func grace(interval time.Duration) time.Duration {
	g := interval * staleFactor
	if g < minGrace {
		return minGrace
	}
	return g
}

// Loops returns every registered loop's state, ordered by name so the
// response is stable between calls.
func (r *Registry) Loops() []LoopHealth {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := r.now()
	out := make([]LoopHealth, 0, len(r.loops))
	for name, s := range r.loops {
		h := LoopHealth{Name: name, Interval: s.interval.String(), Status: StatusUnknown}
		if !s.last.IsZero() {
			last := s.last
			h.LastCompleted = &last
			if over := now.Sub(last) - grace(s.interval); over > 0 {
				h.Status = StatusStale
				h.Overdue = over.Round(time.Second).String()
			} else {
				h.Status = StatusOK
			}
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// StaleLoops returns the names of loops that have missed their window.
// Never includes loops that have not run a first cycle: not-yet is not
// the same claim as no-longer.
func (r *Registry) StaleLoops() []string {
	var out []string
	for _, l := range r.Loops() {
		if l.Status == StatusStale {
			out = append(out, l.Name)
		}
	}
	return out
}

// ── Process-wide default ─────────────────────────────────────────────
//
// A package-level registry, which is a global and therefore normally
// something to avoid. It earns its place here because the alternative
// is threading a registry through ten constructors that otherwise have
// no interest in health reporting, and because the cost of getting it
// wrong is asymmetric: a loop nobody remembered to wire up is a loop
// this feature silently fails to watch, which is the exact failure it
// exists to prevent. Making the call site one line makes forgetting
// harder.
//
// Tests use the Registry type directly and never touch this.

var defaultRegistry = NewRegistry()

// Default returns the process-wide registry the API reads from.
func Default() *Registry { return defaultRegistry }

// Register declares a loop on the default registry.
func Register(name string, interval time.Duration) { defaultRegistry.Register(name, interval) }

// Beat records a completed cycle on the default registry. Call it at
// the END of the cycle: a loop wedged inside its own body is what this
// catches, and beating on entry would report it healthy forever.
func Beat(name string) { defaultRegistry.Beat(name) }
