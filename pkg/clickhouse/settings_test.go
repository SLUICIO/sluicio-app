// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package clickhouse

import "testing"

// The per-query ceiling was hardcoded at 2 GB, so a customer whose host
// had memory to spare still met "Memory limit (for query) exceeded ...
// maximum: 1.86 GiB" with no way to raise it. 1.86 GiB is 2e9 bytes
// rendered in GiB - the number in the error was ours all along.
func TestMaxMemoryUsageIsConfigurable(t *testing.T) {
	if got := ConfigFromEnv().MaxMemoryUsage; got != 2_000_000_000 {
		t.Fatalf("default = %d, want 2e9", got)
	}
	t.Setenv("CLICKHOUSE_MAX_MEMORY_USAGE", "8000000000")
	if got := ConfigFromEnv().MaxMemoryUsage; got != 8_000_000_000 {
		t.Fatalf("override = %d, want 8e9", got)
	}
}

// A bad value must not silently disable the ceiling - an unbounded query
// on a small host takes the whole server with it, which is worse than the
// 500 this replaces.
func TestABadCeilingFallsBackToTheDefault(t *testing.T) {
	for _, v := range []string{"", "0", "-1", "lots", "8GB"} {
		t.Setenv("CLICKHOUSE_MAX_MEMORY_USAGE", v)
		if got := ConfigFromEnv().MaxMemoryUsage; got != 2_000_000_000 {
			t.Errorf("%q gave %d, want the 2e9 default", v, got)
		}
	}
}
