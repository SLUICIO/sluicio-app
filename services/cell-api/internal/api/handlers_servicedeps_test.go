// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The services list used to compute per-service dependency degrees on
// every render: a self-join of the traces table across the whole window,
// measured on a customer cell at three seconds and 833 MiB over two days,
// four times in half an hour. The two numbers it produced are never
// displayed - the frontend reads them in exactly one place, inside a
// filter that defaults to off.

package api

import (
	"net/http/httptest"
	"testing"
)

// wantsDependencies mirrors the gate in enrichServiceListExtras. Kept as
// a test on the parameter contract because the frontend and the handler
// have to agree on the spelling, and the cost of disagreeing is silent:
// the page just gets slow again, or the filter silently stops working.
func wantsDependencies(target string) bool {
	r := httptest.NewRequest("GET", target, nil)
	return r.URL.Query().Get("dependencies") == "1"
}

func TestDependencyDegreesAreOptIn(t *testing.T) {
	for _, tc := range []struct {
		target string
		want   bool
	}{
		{"/api/v1/services?range=2d", false},
		{"/api/v1/services", false},
		{"/api/v1/services?range=2d&dependencies=1", true},
		{"/api/v1/services?dependencies=1&range=2d", true},
		// Anything other than the exact opt-in stays off. A typo must
		// cost a missing filter, not three seconds a page load.
		{"/api/v1/services?dependencies=0", false},
		{"/api/v1/services?dependencies=true", false},
		{"/api/v1/services?dependencies=", false},
	} {
		if got := wantsDependencies(tc.target); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.target, got, tc.want)
		}
	}
}
