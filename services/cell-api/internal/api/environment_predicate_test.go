// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// The predicate a request read in some environments narrows every
// ClickHouse read with (#40).
func TestEnvironmentPredicate(t *testing.T) {
	t.Cleanup(func() { store.SetEnvironmentConfig(store.EnvironmentConfig{}) })
	store.SetEnvironmentConfig(store.EnvironmentConfig{})
	if got := environmentPredicate([]string{"production", ""}); got != `(Environment) IN ('production', '')` {
		t.Errorf("plain: %s", got)
	}
	if got := environmentPredicate([]string{`it's\x`}); got != `(Environment) IN ('it\'s\\x')` {
		t.Errorf("escaped: %s", got)
	}
}
