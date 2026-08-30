// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Integration test for the delivery queue's crash recovery, against a
// real Postgres (testcontainers).
//
// ClaimDueJobs flips a job to 'running' before the send is attempted, and
// only MarkJobSucceeded / MarkJobFailed move it out. A worker that dies in
// between - a crash, an OOM kill, or `up -d --force-recreate` during an
// upgrade - leaves the job stranded, and the claim query only ever looks
// at 'pending'. The notification is dropped and nothing says so.
//
// Run with:
//
//	go test -tags integration ./services/cell-api/internal/alerting/...
//
// or `make test-integration`.
package alerting_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/migrations"
)

func newAlertingDB(t *testing.T) (*alerting.Store, *pgxpool.Pool, context.Context) {
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
	return alerting.NewStore(pool), pool, ctx
}

// seedJob creates the rule → instance → channel → job chain and returns
// the job id, with `state` and an `updated_at` the given age.
func seedJob(t *testing.T, ctx context.Context, pool *pgxpool.Pool, state string, age time.Duration) uuid.UUID {
	t.Helper()
	org := uuid.New()

	var ruleID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO alert_rules (organization_id, name, signal, rule_spec)
		 VALUES ($1, 'boom', 'metric', '{}'::jsonb) RETURNING id`, org,
	).Scan(&ruleID); err != nil {
		t.Fatalf("seed rule: %v", err)
	}

	var instID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO alert_instances (alert_rule_id, state, fingerprint, summary)
		 VALUES ($1, 'firing', 'fp', 'boom') RETURNING id`, ruleID,
	).Scan(&instID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	var chID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO notification_channels (organization_id, name, kind, config)
		 VALUES ($1, 'probe', 'webhook', '{"url":"https://hook.example"}'::jsonb) RETURNING id`, org,
	).Scan(&chID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO notification_jobs (alert_instance_id, channel_id, state, attempts, updated_at)
		 VALUES ($1, $2, $3::notification_job_state, 0, now() - $4::interval) RETURNING id`,
		instID, chID, state, age,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	return jobID
}

func jobState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (string, int) {
	t.Helper()
	var state string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT state::text, attempts FROM notification_jobs WHERE id = $1`, id,
	).Scan(&state, &attempts); err != nil {
		t.Fatalf("read job: %v", err)
	}
	return state, attempts
}

// The job a dead worker left behind must come back, and be deliverable:
// 'pending' alone is not enough if next_attempt_at is in the future.
func TestReclaimRequeuesAStrandedJob(t *testing.T) {
	store, pool, ctx := newAlertingDB(t)
	id := seedJob(t, ctx, pool, "running", 10*time.Minute)

	n, err := store.ReclaimStuckJobs(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed %d jobs, want 1", n)
	}

	state, attempts := jobState(t, ctx, pool, id)
	if state != "pending" {
		t.Fatalf("state is %q, want pending", state)
	}
	// Counts as an attempt, so a delivery that reliably kills the worker
	// is still bounded by maxAttempts instead of looping for ever.
	if attempts != 1 {
		t.Fatalf("attempts is %d, want 1", attempts)
	}

	var due bool
	if err := pool.QueryRow(ctx,
		`SELECT next_attempt_at <= now() FROM notification_jobs WHERE id = $1`, id,
	).Scan(&due); err != nil {
		t.Fatalf("read next_attempt_at: %v", err)
	}
	if !due {
		t.Fatal("re-queued job is not due, so nothing will pick it up")
	}
}

// A delivery in flight right now must not be swept out from under the
// worker that owns it - that would send the notification twice for no
// reason.
func TestReclaimLeavesAFreshClaimAlone(t *testing.T) {
	store, pool, ctx := newAlertingDB(t)
	id := seedJob(t, ctx, pool, "running", 10*time.Second)

	n, err := store.ReclaimStuckJobs(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 0 {
		t.Fatalf("reclaimed %d jobs, want 0", n)
	}
	if state, _ := jobState(t, ctx, pool, id); state != "running" {
		t.Fatalf("state is %q, want running", state)
	}
}

// Only 'running' is ambiguous. A job that already reached a terminal
// state, or one waiting on its backoff, must be left exactly as it is -
// reviving a 'failed' job would resurrect an alert somebody gave up on,
// and touching 'pending' would reset its backoff.
func TestReclaimIgnoresEveryOtherState(t *testing.T) {
	store, pool, ctx := newAlertingDB(t)
	ids := map[string]uuid.UUID{}
	for _, state := range []string{"pending", "succeeded", "failed"} {
		ids[state] = seedJob(t, ctx, pool, state, time.Hour)
	}

	n, err := store.ReclaimStuckJobs(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 0 {
		t.Fatalf("reclaimed %d jobs, want 0", n)
	}
	for want, id := range ids {
		if got, attempts := jobState(t, ctx, pool, id); got != want || attempts != 0 {
			t.Errorf("%s job became %s with %d attempts", want, got, attempts)
		}
	}
}
