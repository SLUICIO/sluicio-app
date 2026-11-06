// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package retention

import "testing"

// A trial licence keeps telemetry seven days (SaaS Agreement 3.7), even
// on an instance whose stored setting still says the default fourteen.
func TestTheLicenceCapsRetention(t *testing.T) {
	for _, c := range []struct{ days, max, want int }{
		{14, 7, 7},    // trial licence over the default setting
		{5, 7, 5},     // a shorter setting stands
		{400, 0, 400}, // no cap
	} {
		if got := Capped(c.days, c.max); got != c.want {
			t.Errorf("Capped(%d, %d) = %d, want %d", c.days, c.max, got, c.want)
		}
	}
}
