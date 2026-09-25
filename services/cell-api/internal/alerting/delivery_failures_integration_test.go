// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// The half of issue #33 that lives in SQL: counting what the cell gave
// up on, putting one back by hand, and dropping a notification that has
// been overtaken by events.
//
// Run with `make test-integration`.
package alerting_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
)

// seedOrgJob is seedJob with the org handed back, since a failure count
// is per organisation.
func seedOrgJob(t *testing.T, ctx context.Context, pool *pgxpool.Pool, org uuid.UUID, channel, state string, age time.Duration) uuid.UUID {
	t.Helper()
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
		 VALUES ($1, 'firing', $2, 'boom') RETURNING id`, ruleID, uuid.NewString(),
	).Scan(&instID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	// One channel per (org, name): several jobs against the same channel
	// is the case being measured.
	var chID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO notification_channels (organization_id, name, kind, config)
		 VALUES ($1, $2, 'webhook', '{"url":"https://hook.example"}'::jsonb)
		 ON CONFLICT (organization_id, name) DO UPDATE SET updated_at = now()
		 RETURNING id`, org, channel,
	).Scan(&chID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var jobID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO notification_jobs (alert_instance_id, channel_id, state, attempts, last_error, updated_at)
		 VALUES ($1, $2, $3::notification_job_state, 5, 'receiver said 500', now() - $4::interval) RETURNING id`,
		instID, chID, state, age,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	return jobID
}

// The point of the count: an operator learns that a channel stopped
// working without opening the delivery history to look for it.
func TestRecentDeliveryFailuresAreCountedPerChannel(t *testing.T) {
	store, pool, ctx := newAlertingDB(t)
	org := uuid.New()

	seedOrgJob(t, ctx, pool, org, "slack-ops", "failed", time.Minute)
	seedOrgJob(t, ctx, pool, org, "slack-ops", "failed", 2*time.Minute)
	seedOrgJob(t, ctx, pool, org, "pager", "failed", 3*time.Minute)
	// Not failures: one that worked, one that was dropped as overtaken,
	// and one that failed yesterday.
	seedOrgJob(t, ctx, pool, org, "slack-ops", "succeeded", time.Minute)
	seedOrgJob(t, ctx, pool, org, "slack-ops", "dropped", time.Minute)
	seedOrgJob(t, ctx, pool, org, "slack-ops", "failed", 26*time.Hour)
	// Another organisation's outage is not this one's business.
	seedOrgJob(t, ctx, pool, uuid.New(), "slack-ops", "failed", time.Minute)

	got, err := store.RecentDeliveryFailures(ctx, org, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("recent failures: %v", err)
	}
	counts := map[string]int{}
	for _, f := range got {
		counts[f.ChannelName] = f.Count
	}
	if counts["slack-ops"] != 2 {
		t.Errorf("slack-ops: counted %d, want 2 (a success, a drop and yesterday do not count)", counts["slack-ops"])
	}
	if counts["pager"] != 1 {
		t.Errorf("pager: counted %d, want 1", counts["pager"])
	}
	if len(got) != 2 {
		// Seen failing once with every count at zero, on a loaded
		// machine, and not reproducible afterwards (five consecutive
		// runs, clocks within a second). Zero rows and no error can only
		// mean the query looked somewhere the seeds are not, so the
		// state it looked at is what the next occurrence has to show -
		// otherwise this is a mystery again rather than a diagnosis.
		t.Errorf("counted %d channels, want 2: %+v", len(got), got)
		dumpDeliveryState(t, ctx, pool, org)
	}
	// Worst first, so the health line names the channel that matters.
	//
	// Guarded: the assertions above already say what went wrong, and
	// indexing an empty slice replaces that report with a panic and a
	// stack trace that names the test rather than the cause.
	if len(got) == 0 {
		return
	}
	if got[0].ChannelName != "slack-ops" {
		t.Errorf("ordered by count descending, got %s first", got[0].ChannelName)
	}
	if got[0].LastError == "" {
		t.Error("the count says nothing about what went wrong")
	}
}

// dumpDeliveryState prints what the count query had to work with: the
// rows, and both clocks, since the window is compared across them.
func dumpDeliveryState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, org uuid.UUID) {
	t.Helper()
	var dbNow string
	if err := pool.QueryRow(ctx, `SELECT now()::text`).Scan(&dbNow); err != nil {
		t.Logf("db clock unreadable: %v", err)
	}
	t.Logf("clocks: db=%s host=%s", dbNow, time.Now().UTC().Format("2006-01-02 15:04:05-07"))
	rows, err := pool.Query(ctx, `
		SELECT c.organization_id::text, c.name, j.state::text, j.updated_at::text
		FROM notification_jobs j JOIN notification_channels c ON c.id = j.channel_id
		ORDER BY j.updated_at DESC`)
	if err != nil {
		t.Logf("state unreadable: %v", err)
		return
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var o, name, state, updated string
		if err := rows.Scan(&o, &name, &state, &updated); err != nil {
			t.Logf("scan: %v", err)
			return
		}
		mine := ""
		if o == org.String() {
			mine = "  <- this org"
		}
		t.Logf("  %s %-10s %-9s %s%s", o, name, state, updated, mine)
		seen++
	}
	if seen == 0 {
		t.Log("  no jobs at all: the seeds are not in the database this query read")
	}
}

// Fixing the receiver should not mean waiting for a policy that has
// already given up.
func TestRetryJobRequeuesAGivenUpDelivery(t *testing.T) {
	store, pool, ctx := newAlertingDB(t)
	org := uuid.New()
	id := seedOrgJob(t, ctx, pool, org, "slack-ops", "failed", time.Hour)

	if err := store.RetryJob(ctx, org, id, 6*time.Hour); err != nil {
		t.Fatalf("retry: %v", err)
	}
	state, attempts := jobState(t, ctx, pool, id)
	if state != "pending" || attempts != 0 {
		t.Fatalf("after retry: state=%s attempts=%d, want pending/0", state, attempts)
	}
	// The window has to move with it, or the first attempt gives up on a
	// deadline that passed while the receiver was broken.
	var expires time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM notification_jobs WHERE id = $1`, id).Scan(&expires); err != nil {
		t.Fatalf("read expires_at: %v", err)
	}
	if time.Until(expires) < 5*time.Hour {
		t.Errorf("window after retry is %s, want about six hours", time.Until(expires))
	}
}

func TestRetryJobRefusesWhatItShouldNotTouch(t *testing.T) {
	store, pool, ctx := newAlertingDB(t)
	org := uuid.New()

	done := seedOrgJob(t, ctx, pool, org, "slack-ops", "succeeded", time.Minute)
	if err := store.RetryJob(ctx, org, done, time.Hour); err != alerting.ErrNotFound {
		t.Errorf("re-queued a delivery that already arrived: %v", err)
	}
	// Another organisation's delivery is not reachable by id.
	theirs := seedOrgJob(t, ctx, pool, uuid.New(), "slack-ops", "failed", time.Minute)
	if err := store.RetryJob(ctx, org, theirs, time.Hour); err != alerting.ErrNotFound {
		t.Errorf("re-queued another org's delivery: %v", err)
	}
}

// A notification queued to say something started, delivered after it
// stopped, is worse than silence. The job records which transition it is
// for so the decision can be made at all.
func TestEnqueueRecordsTheTransitionAndTheWindow(t *testing.T) {
	store, pool, ctx := newAlertingDB(t)
	org := uuid.New()
	id := seedOrgJob(t, ctx, pool, org, "slack-ops", "failed", time.Minute)

	var instID, chID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT alert_instance_id, channel_id FROM notification_jobs WHERE id = $1`, id,
	).Scan(&instID, &chID); err != nil {
		t.Fatalf("read seed job: %v", err)
	}
	if err := store.EnqueueJobs(ctx, instID, []uuid.UUID{chID}, 6*time.Hour); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	var forState string
	var expires time.Time
	if err := pool.QueryRow(ctx,
		`SELECT for_state::text, expires_at FROM notification_jobs
		  WHERE alert_instance_id = $1 AND state = 'pending' ORDER BY created_at DESC LIMIT 1`, instID,
	).Scan(&forState, &expires); err != nil {
		t.Fatalf("read queued job: %v", err)
	}
	if forState != "firing" {
		t.Errorf("queued for %q, want firing (the instance's state when it was queued)", forState)
	}
	if d := time.Until(expires); d < 5*time.Hour || d > 7*time.Hour {
		t.Errorf("window is %s, want about six hours", d)
	}
}
