// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package settings

import (
	"context"
	"testing"
)

// The deployment's value is the answer for every reader, alert mail and
// metrics export included, and needs no database to give.
func TestGetEnvironmentPrefersTheDeployment(t *testing.T) {
	t.Setenv("SLUICIO_ENVIRONMENT", "  Trial ")
	got, err := (&Store{}).GetEnvironment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Trial" {
		t.Fatalf("GetEnvironment = %q, want Trial", got)
	}
}
