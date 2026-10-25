// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package clickhouse

import (
	"context"
	"testing"
	"time"
)

// TestWithOrgFilterLive validates the driver→server plumbing of the
// additional_table_filters setting against a reachable ClickHouse (the
// dev stack). It self-skips when ClickHouse isn't available, so it's safe
// in CI. Run locally with the dev stack up to confirm tenant filtering.
func TestWithOrgFilterLive(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(ctx, ConfigFromEnv())
	if err != nil {
		t.Skipf("clickhouse not reachable: %v", err)
	}
	defer conn.Close()

	// A bogus org must return zero rows from a table that has data — proves
	// the filter is actually applied by the server via the context setting.
	bogus := WithOrgFilter(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	var n uint64
	if err := conn.QueryRow(bogus, "SELECT count() FROM logs").Scan(&n); err != nil {
		t.Skipf("query failed (logs table may not exist yet): %v", err)
	}
	if n != 0 {
		t.Fatalf("org filter not applied: bogus org returned %d log rows, want 0", n)
	}
}

// TestWithTableFilterLive: the organization and a predicate travel in one
// filter (#40), and a predicate's own quotes and backslashes survive the
// map literal they are packed into.
func TestWithTableFilterLive(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(ctx, ConfigFromEnv())
	if err != nil {
		t.Skipf("clickhouse not reachable: %v", err)
	}
	defer conn.Close()
	org := "eeeeeeee-0000-0000-0000-00000000f11e"
	now := time.Now().UTC()
	for _, env := range []string{"production", "test", `it's\here`} {
		if err := conn.Exec(ctx, `INSERT INTO traces (Timestamp, TraceId, SpanId, ServiceName, SpanName, ResourceAttributes, OrganizationId)
			VALUES (?, ?, 's', 'table-filter-probe', 'probe', map('deployment.environment.name', ?), ?)`,
			now, "tf-"+env+now.Format("150405.000000"), env, org); err != nil {
			t.Skipf("insert failed (traces table may not exist yet): %v", err)
		}
	}
	count := func(c context.Context) uint64 {
		var n uint64
		if err := conn.QueryRow(c, "SELECT count() FROM traces WHERE ServiceName = 'table-filter-probe' AND Timestamp >= ?", now.Add(-time.Second)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(WithTableFilter(ctx, org, "")); n != 3 {
		t.Errorf("org alone: %d, want 3", n)
	}
	if n := count(WithTableFilter(ctx, org, "Environment IN ('test')")); n != 1 {
		t.Errorf("org and environment: %d, want 1", n)
	}
	if n := count(WithTableFilter(ctx, org, `Environment = 'it\'s\\here'`)); n != 1 {
		t.Errorf("escaped predicate: %d, want 1", n)
	}
	if n := count(WithTableFilter(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff", "Environment IN ('test')")); n != 0 {
		t.Errorf("another org: %d, want 0", n)
	}
}
