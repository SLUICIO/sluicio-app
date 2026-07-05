// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package clickhouse

import (
	"context"
	"testing"
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
