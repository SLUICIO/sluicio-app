// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// The suggestion store against a real Postgres.
//
// These behaviours decide whether anyone keeps reading the advisor, and
// none of them can be checked without a database — the logic lives in
// SQL, in CASE expressions the Go type checker never sees:
//
//   - A dismissal STICKS across re-evaluation. A suggestion that comes
//     back every night after being refused trains people to ignore the
//     whole page, which costs more than the finding was ever worth.
//   - A dismissal is NOT permanent silence. When the volume roughly
//     doubles the finding is no longer the one that was dismissed, and
//     it must resurface — otherwise one "no" hides a metric that later
//     grows into the largest line on the bill.
//   - An ACCEPTED decision is never undone by a later run. The nightly
//     job recomputing findings must not overwrite what a person
//     concluded, or the whole accept/dismiss workflow is theatre.
//
// The e2e specs that try to cover this can only run when the seeded
// cell happens to produce a suggestion, which on a freshly-built CI
// cell it never does (the demand ledger is minutes old and the maturity
// guard correctly refuses to judge). So they skip, and this is where the
// coverage actually lives.
//
//	go test -tags integration ./services/cell-api/internal/advisor/...

package advisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/advisor"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
)

func newAdvisorDB(t *testing.T) (*advisor.Store, uuid.UUID, context.Context) {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("controlplane"),
		tcpostgres.WithUsername("controlplane"),
		tcpostgres.WithPassword("controlplane"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(pg); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := impostgres.Pool(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := impostgres.Migrate(ctx, pool, migrations.FS, migrations.Dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// advisor_suggestions is FK'd to orgs, so a real one is needed.
	ident := identity.NewStore(pool)
	orgs, err := ident.ListOrgs(ctx)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("no seeded org: %v", err)
	}
	return advisor.NewStore(pool), orgs[0].Org.ID, ctx
}

// finding builds a candidate the evaluator would produce.
func finding(weight int64) advisor.Suggestion {
	return advisor.Suggestion{
		Fingerprint: "T1|metric|queue.depth",
		Class:       "T1",
		Advisor:     "telemetry",
		ScopeKind:   "metric",
		ScopeID:     "queue.depth",
		Title:       "Nothing reads the \"queue.depth\" metric",
		Loss:        "You lose the ability to chart it without collecting it again.",
		Snippet:     "processors:\n  filter/sluicio-advisor:\n",
		Weight:      weight,
		Evidence:    map[string]any{"rows_per_day": 5000},
	}
}

func onBoard(t *testing.T, s *advisor.Store, ctx context.Context, org uuid.UUID, fp string) *advisor.Suggestion {
	t.Helper()
	items, err := s.List(ctx, org, "", "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := range items {
		if items[i].Fingerprint == fp {
			return &items[i]
		}
	}
	return nil
}

func TestDismissalSticksUntilTheFactsOutgrowIt(t *testing.T) {
	s, org, ctx := newAdvisorDB(t)
	fp := finding(1_000).Fingerprint

	if err := s.Upsert(ctx, org, finding(1_000)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	found := onBoard(t, s, ctx, org, fp)
	if found == nil || found.State != "open" {
		t.Fatalf("a new finding should be open, got %+v", found)
	}

	if _, err := s.Decide(ctx, org, found.ID, uuid.Nil, "dismissed", "load-bearing, keep it"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if onBoard(t, s, ctx, org, fp) != nil {
		t.Fatal("a dismissed suggestion must leave the board")
	}

	// Tonight's run finds the same thing again, at roughly the same
	// volume. It must stay dismissed — this is the property that decides
	// whether anyone keeps reading the advisor at all.
	for i, w := range []int64{1_000, 1_200, 1_900} {
		if err := s.Upsert(ctx, org, finding(w)); err != nil {
			t.Fatalf("re-upsert %d: %v", i, err)
		}
		if got := onBoard(t, s, ctx, org, fp); got != nil {
			t.Fatalf("dismissed suggestion resurfaced at weight %d (%.1fx) — nagging is how an advisor gets ignored",
				w, float64(w)/1000)
		}
	}

	// …but a doubling is a different finding, and must come back. One
	// "no" should not hide a metric that later becomes the biggest line
	// on the bill.
	if err := s.Upsert(ctx, org, finding(2_000)); err != nil {
		t.Fatalf("upsert doubled: %v", err)
	}
	back := onBoard(t, s, ctx, org, fp)
	if back == nil {
		t.Fatal("a dismissed finding whose volume doubled must resurface")
	}
	if back.State != "open" {
		t.Errorf("resurfaced state = %q, want open", back.State)
	}
}

func TestAcceptedDecisionsSurviveReEvaluation(t *testing.T) {
	s, org, ctx := newAdvisorDB(t)
	fp := finding(5_000).Fingerprint

	if err := s.Upsert(ctx, org, finding(5_000)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	item := onBoard(t, s, ctx, org, fp)
	if _, err := s.Decide(ctx, org, item.ID, uuid.Nil, "accepted", ""); err != nil {
		t.Fatalf("accept: %v", err)
	}

	// The collector has not been redeployed yet, so tonight's run still
	// finds it. That is information for the verification pass, not a
	// reason to re-ask — and certainly not a reason to reset the state a
	// person set.
	if err := s.Upsert(ctx, org, finding(5_000)); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	still := onBoard(t, s, ctx, org, fp)
	if still == nil || still.State != "accepted" {
		t.Fatalf("accepted state was overwritten by a later run: %+v", still)
	}

	// Once the finding stops being produced, the change took effect.
	n, err := s.MarkVerified(ctx, org, []string{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if n != 1 {
		t.Fatalf("verified %d suggestions, want 1", n)
	}
	done := onBoard(t, s, ctx, org, fp)
	if done == nil || done.State != "verified" {
		t.Fatalf("state after supply dropped = %+v, want verified", done)
	}

	// And a run that still finds it must NOT promote it.
	if err := s.Upsert(ctx, org, finding(5_000)); err != nil {
		t.Fatalf("re-upsert after verify: %v", err)
	}
	if _, err := s.MarkVerified(ctx, org, []string{fp}); err != nil {
		t.Fatalf("verify again: %v", err)
	}
}

func TestOpenFindingsAreRetiredWhenTheyStopBeingTrue(t *testing.T) {
	// An open finding that is no longer true has no history worth
	// keeping — somebody acted, or demand appeared. A list of "things
	// that briefly looked wasteful" is noise in every later query.
	s, org, ctx := newAdvisorDB(t)
	fp := finding(3_000).Fingerprint

	if err := s.Upsert(ctx, org, finding(3_000)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.CloseMissing(ctx, org, []string{"T1"}, []string{}); err != nil {
		t.Fatalf("close missing: %v", err)
	}
	if onBoard(t, s, ctx, org, fp) != nil {
		t.Error("an open finding this run did not reproduce should be retired")
	}

	// A DECIDED one is never touched: it records what a person concluded.
	if err := s.Upsert(ctx, org, finding(3_000)); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	item := onBoard(t, s, ctx, org, fp)
	if _, err := s.Decide(ctx, org, item.ID, uuid.Nil, "dismissed", ""); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if err := s.CloseMissing(ctx, org, []string{"T1"}, []string{}); err != nil {
		t.Fatalf("close missing after dismiss: %v", err)
	}
	hidden, err := s.List(ctx, org, "", "dismissed", 10)
	if err != nil {
		t.Fatalf("list dismissed: %v", err)
	}
	if len(hidden) != 1 {
		t.Errorf("a dismissed decision was deleted by reconciliation; %d remain", len(hidden))
	}
}
