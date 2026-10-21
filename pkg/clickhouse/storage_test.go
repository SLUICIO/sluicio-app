// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package clickhouse

import "testing"

func TestStorageLimitStopsAtTheLimitAndResumesBelowIt(t *testing.T) {
	l := StorageLimitFromGB(10)
	cases := []struct {
		usage      uint64
		wasStopped bool
		want       bool
		why        string
	}{
		{9_000_000_000, false, false, "under the limit, accepting"},
		{10_000_000_000, false, true, "at the limit, stops"},
		{11_000_000_000, false, true, "over the limit, stops"},
		{9_900_000_000, true, true, "just under the limit after stopping: stays stopped, no flapping"},
		{9_799_000_000, true, false, "below 98%: resumes"},
		{9_900_000_000, false, false, "same usage while accepting: keeps accepting"},
	}
	for _, c := range cases {
		if got := l.Stopped(c.usage, c.wasStopped); got != c.want {
			t.Errorf("%s: Stopped(%d, %v) = %v", c.why, c.usage, c.wasStopped, got)
		}
	}
	if (StorageLimit{}).Stopped(1<<62, true) {
		t.Error("no limit must never stop ingest")
	}
	if StorageLimitFromGB(0).Bytes != 0 || StorageLimitFromGB(-1).Bytes != 0 {
		t.Error("zero or negative GB must mean no limit")
	}
}
