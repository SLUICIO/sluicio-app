// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package deploymentlimits

import "testing"

func TestThePlanURLMustBeALinkSomeoneCanFollow(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want string
		bad  bool
	}{
		{"", "", false},
		{" https://portal.example.com/plan ", "https://portal.example.com/plan", false},
		{"portal.example.com/plan", "", true},
		{"javascript:alert(1)", "", true},
	} {
		t.Setenv(PlanURLEnv, c.raw)
		got, err := PlanURLFromEnv()
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%q: got %q, %v; want %q (error %v)", c.raw, got, err, c.want, c.bad)
		}
	}
}
