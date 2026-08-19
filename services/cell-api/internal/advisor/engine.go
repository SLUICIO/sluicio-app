// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The evaluation run: gather supply, gather demand, evaluate, reconcile
// with what a human already decided.
//
// Findings are recomputed from scratch every time rather than
// incrementally updated. That is not laziness — it is the property that
// makes the advisor safe to trust. An incremental advisor accumulates
// stale conclusions: a suggestion filed when a metric was unused
// survives long after somebody built a dashboard on it, because nothing
// ever goes back to check. Recomputing means a finding exists exactly as
// long as the facts behind it do, and disappears the moment they change.
//
// Reconciliation is therefore the interesting part, and it has one
// asymmetry worth stating: findings are disposable, decisions are not.
// An open suggestion that stops being true is deleted without ceremony.
// An accepted or dismissed one is kept forever, because it records what
// a person concluded — and the whole accept/dismiss workflow is
// worthless if a nightly job can quietly undo it.
package advisor

import (
	"context"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/cellhealth"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectorversion"
)

// ObservationWindow is how far back an evaluation looks when deciding
// whether something is unused (design §8.3: 30 days, fixed in v1).
//
// It is also the quarantine: telemetry first seen inside this window is
// never judged, because it has not yet had a fair chance to be consumed.
const ObservationWindow = 30 * 24 * time.Hour

// evidenceHorizon is how far back the demand ledger is READ. Longer than
// the window on purpose — demand older than the window does not stop a
// suggestion, but "last consumed 47 days ago" is the most useful line in
// the evidence block, and it cannot be written without looking further
// back than the verdict requires.
const evidenceHorizon = 400 * 24 * time.Hour

// telemetryClasses and alertingClasses enumerate what a full run
// produces, so reconciliation knows which open findings this run was
// responsible for and can retire the ones it did not reproduce.
var (
	telemetryClasses = []string{"T1", "T2", "T3", "T4", "T5", "T6"}
	alertingClasses  = []string{"F1", "F2", "F3", "F4", "F5"}
)

// Engine evaluates one cell's orgs on a schedule.
type Engine struct {
	Store *Store
	Pool  *pgxpool.Pool
	CH    driver.Conn
	Log   *slog.Logger
	// Entitled reports whether an org may use the advisor. Evaluation is
	// skipped entirely for orgs that cannot see the result — there is no
	// point spending the cell's largest analytical query on a report
	// nobody is allowed to read.
	Entitled func() bool
	// Orgs lists the orgs to evaluate.
	Orgs func(ctx context.Context) ([]uuid.UUID, error)
	// IntegrationServices reports which services belong to an
	// integration, so the completeness promise can be enforced.
	IntegrationServices func(ctx context.Context, orgID uuid.UUID) (map[string]bool, error)
	// CollectorTarget resolves which collector a service runs, so a
	// snippet is written in that version's syntax (issue #16). The empty
	// service name asks for the org default. Optional; nil means the
	// newest version this build knows.
	CollectorTarget func(ctx context.Context, orgID uuid.UUID, service string) collectorversion.Target
	// Every defaults to 24h.
	Every time.Duration
	// Window overrides the observation window. Zero means the shipped
	// 30 days.
	//
	// It exists so the feature can be exercised before a cell has a
	// month of history — testing, demos, screenshots. Shortening it
	// makes the advisor MORE likely to call something unused, so it is
	// an env knob on the cell rather than an org setting: a customer
	// should never be able to make their own advisor jumpier without
	// realising that is what they did.
	Window time.Duration
	// Now is swappable for tests.
	Now func() time.Time
}

// window is the effective observation window.
func (e *Engine) window() time.Duration {
	if e.Window > 0 {
		return e.Window
	}
	return ObservationWindow
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Run evaluates on a ticker until the context ends.
//
// Deliberately NOT run at startup, unlike the demand sweep. Evaluation
// is the most expensive thing this service does — a full attribute scan
// across a month of spans — and doing it during boot would make every
// restart slow at exactly the moment an operator is watching. There is
// no urgency: the findings were true yesterday and will be true tonight.
func (e *Engine) Run(ctx context.Context) {
	every := e.Every
	if every <= 0 {
		every = 24 * time.Hour
	}
	t := time.NewTicker(every)
	cellhealth.Register("advisor", every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.RunAll(ctx)
			// End of cycle, not start: a loop wedged inside its
			// own body is exactly what this catches.
			cellhealth.Beat("advisor")
		}
	}
}

// RunAll evaluates every org, tolerating per-org failures.
func (e *Engine) RunAll(ctx context.Context) {
	if e.Entitled != nil && !e.Entitled() {
		return
	}
	orgs, err := e.Orgs(ctx)
	if err != nil {
		e.Log.Warn("advisor: listing orgs failed", "err", err)
		return
	}
	for _, orgID := range orgs {
		if err := e.RunOrg(ctx, orgID); err != nil {
			// One org's bad data must not stop the others from being
			// evaluated — a cell is multi-tenant and a broken tenant is
			// not a reason to stop advising the rest.
			e.Log.Warn("advisor: org evaluation failed", "org", orgID, "err", err)
		}
	}
}

// LedgerStatus reports how much demand history an org has, and whether
// it is yet enough to advise from.
//
// Days alone cannot carry the message, because "0 days" has two causes
// that call for opposite advice. A ledger that is FILLING will be ready
// on a knowable date, so "check back later" is true. A ledger that is
// EMPTY — nobody has opened a single view — will read zero forever, and
// telling that org to wait 30 days is a promise the product cannot
// keep. Empty separates them so the UI can say the useful thing.
type LedgerStatus struct {
	Ready     bool `json:"ready"`
	Days      int  `json:"days"`
	NeedsDays int  `json:"needs_days"`
	// Empty means the ledger holds no consumption at all for this org.
	// Waiting will not change it; someone has to use the product.
	Empty bool `json:"empty"`
	// Unavailable means the ledger could not be read (ClickHouse down,
	// query failed). Previously this returned zero days, so an outage
	// was reported to the user as "you have no history" — a wrong and
	// unactionable claim about their data.
	Unavailable bool `json:"unavailable"`
}

// Ledger reports the org's demand-history status. The UI needs this to
// explain an empty advisor: "nothing to suggest" and "not watching long
// enough to say" look identical on screen and mean opposite things.
func (e *Engine) Ledger(ctx context.Context, orgID uuid.UUID) LedgerStatus {
	now := e.now()
	window := int(e.window().Hours() / 24)
	dem, err := LoadDemand(ctx, e.CH, orgID, now.Add(-evidenceHorizon))
	if err != nil {
		return LedgerStatus{Ready: false, Days: 0, NeedsDays: window, Unavailable: true}
	}
	if dem.Earliest.IsZero() {
		return LedgerStatus{Ready: false, Days: 0, NeedsDays: window, Empty: true}
	}
	days := int(now.Sub(dem.Earliest).Hours() / 24)
	return LedgerStatus{
		Ready:     dem.Mature(now.Add(-e.window())),
		Days:      days,
		NeedsDays: window,
	}
}

// Result reports what a run produced, for the manual-trigger response.
type Result struct {
	Telemetry int `json:"telemetry_findings"`
	Alerting  int `json:"alerting_findings"`
	Retired   int `json:"retired"`
	Verified  int `json:"verified"`
}

// RunOrg evaluates one org and reconciles the result.
func (e *Engine) RunOrg(ctx context.Context, orgID uuid.UUID) error {
	now := e.now()
	from := now.Add(-e.window())

	dem, err := LoadDemand(ctx, e.CH, orgID, now.Add(-evidenceHorizon))
	if err != nil {
		return err
	}

	var intSvcs map[string]bool
	if e.IntegrationServices != nil {
		if intSvcs, err = e.IntegrationServices(ctx, orgID); err != nil {
			e.Log.Warn("advisor: integration membership unavailable", "org", orgID, "err", err)
			intSvcs = map[string]bool{}
		}
	}

	res := Result{}
	var found []Suggestion

	if e.CH != nil {
		var target func(string) collectorversion.Target
		if e.CollectorTarget != nil {
			target = func(service string) collectorversion.Target {
				return e.CollectorTarget(ctx, orgID, service)
			}
		}
		tele, err := EvaluateTelemetry(ctx, TelemetryInput{
			OrgID: orgID, Conn: e.CH, Demand: dem,
			From: from, To: now, IntegrationServices: intSvcs,
			Target: target,
		})
		if err != nil {
			// A ClickHouse hiccup must not wipe the alerting findings
			// too: report it, keep the telemetry classes untouched (no
			// CloseMissing for them) and carry on.
			e.Log.Warn("advisor: telemetry evaluation failed", "org", orgID, "err", err)
		} else {
			found = append(found, tele...)
			res.Telemetry = len(tele)
			if err := e.reconcile(ctx, orgID, telemetryClasses, tele); err != nil {
				return err
			}
		}
	}

	fat, err := EvaluateFatigue(ctx, FatigueInput{
		OrgID: orgID, Pool: e.Pool, Demand: dem, From: from, To: now,
	})
	if err != nil {
		e.Log.Warn("advisor: alerting evaluation failed", "org", orgID, "err", err)
	} else {
		found = append(found, fat...)
		res.Alerting = len(fat)
		if err := e.reconcile(ctx, orgID, alertingClasses, fat); err != nil {
			return err
		}
	}

	// Anything previously ACCEPTED that this run no longer finds has
	// actually taken effect — the collector really did change, or the
	// rule really was retuned. This is the only honest confirmation v1
	// can offer, and it costs one extra statement.
	fps := fingerprints(found)
	if n, err := e.Store.MarkVerified(ctx, orgID, fps); err != nil {
		e.Log.Warn("advisor: verification pass failed", "org", orgID, "err", err)
	} else {
		res.Verified = int(n)
	}

	e.Log.Info("advisor evaluation complete", "org", orgID,
		"telemetry", res.Telemetry, "alerting", res.Alerting, "verified", res.Verified)
	return nil
}

// reconcile writes this run's findings and retires the open ones it did
// not reproduce.
func (e *Engine) reconcile(ctx context.Context, orgID uuid.UUID, classes []string, found []Suggestion) error {
	for _, f := range found {
		if err := e.Store.Upsert(ctx, orgID, f); err != nil {
			return err
		}
	}
	return e.Store.CloseMissing(ctx, orgID, classes, fingerprints(found))
}

func fingerprints(in []Suggestion) []string {
	// Never nil: `NOT (fingerprint = ANY($3))` with a NULL array matches
	// nothing in Postgres, which would silently skip every retirement.
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.Fingerprint)
	}
	return out
}
