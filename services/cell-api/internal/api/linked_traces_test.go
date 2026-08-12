// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The visibility rule for a hand-off's far side (issue #24).
//
// This is a leak boundary, not a formatting choice. A linked trace can
// belong to services the caller is not allowed to see, and naming one —
// even just its service — discloses exactly what the policy exists to
// withhold. The opposite failure is as bad in a different way: a chain
// that is quietly short looks complete, so a withheld hand-off has to be
// counted out loud.

package api

import "testing"

func TestAnyVisibleRequiresAnOverlap(t *testing.T) {
	allowed := map[string]struct{}{"a": {}, "b": {}}

	t.Run("visible when the caller can see any service in the trace", func(t *testing.T) {
		// ANY, not all: a trace that crosses a boundary is partly
		// visible, and the hand-off itself is a fact about the edge the
		// caller can already see.
		if !anyVisible([]string{"z", "b"}, allowed) {
			t.Error("a trace containing a visible service must be visible")
		}
	})

	t.Run("hidden when the caller can see none of them", func(t *testing.T) {
		if anyVisible([]string{"y", "z"}, allowed) {
			t.Error("a trace with no visible service must be withheld")
		}
	})

	t.Run("a trace with no services is not visible", func(t *testing.T) {
		// Defensive: an empty service list must not read as "nothing to
		// check, therefore allowed".
		if anyVisible(nil, allowed) {
			t.Error("an empty service list must not pass the filter")
		}
	})

	t.Run("an empty allowlist admits nothing", func(t *testing.T) {
		// A caller whose policy resolves to no services sees no
		// hand-offs. The caller of this helper only applies it when a
		// filter is in force, but the helper must not invert on empty.
		if anyVisible([]string{"a"}, map[string]struct{}{}) {
			t.Error("an empty allowlist must admit nothing")
		}
	})
}
