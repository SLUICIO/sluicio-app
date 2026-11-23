// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"strings"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// The predicate a request read in some environments narrows every
// ClickHouse read with (#40).
func TestEnvironmentPredicate(t *testing.T) {
	t.Cleanup(func() { store.SetEnvironmentConfig(store.EnvironmentConfig{}) })
	store.SetEnvironmentConfig(store.EnvironmentConfig{})
	plain := "coalesce(nullIf(Environment, ''), nullIf(LogAttributes['deployment.environment.name'], ''), nullIf(LogAttributes['deployment.environment'], ''), '')"
	if got := environmentPredicate("logs", []string{"production", ""}); got != "("+plain+") IN ('production', '')" {
		t.Errorf("plain: %s", got)
	}
	if got := environmentPredicate("logs", []string{`it's\x`}); got != "("+plain+`) IN ('it\'s\\x')` {
		t.Errorf("escaped: %s", got)
	}
	// Each table reads its own record attributes: the request filter is
	// set per table.
	if got := environmentPredicate("metrics", []string{"prod"}); !strings.Contains(got, "MetricAttributes['deployment.environment']") {
		t.Errorf("metrics: %s", got)
	}
}
