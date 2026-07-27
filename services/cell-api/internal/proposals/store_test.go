// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Drift detection is the part of the proposal loop that can silently do
// harm: a false negative means approving a stale proposal reverts a
// human's edit without anyone noticing. These tests pin the semantics
// that matter — value equality rather than byte equality, and "I can't
// see the field" counting as drift rather than as agreement.

package proposals

import (
	"encoding/json"
	"testing"
)

func raw(v string) json.RawMessage { return json.RawMessage(v) }

func TestCheckDriftCleanWhenTargetUnchanged(t *testing.T) {
	changes := []Change{
		{Field: "threshold", Before: raw(`5`), After: raw(`8`)},
		{Field: "severity", Before: raw(`"warning"`), After: raw(`"critical"`)},
	}
	current := map[string]json.RawMessage{
		"threshold": raw(`5`),
		"severity":  raw(`"warning"`),
	}
	if got := CheckDrift(changes, current); len(got) != 0 {
		t.Fatalf("expected no drift, got %v", got)
	}
}

func TestCheckDriftFlagsChangedField(t *testing.T) {
	changes := []Change{
		{Field: "threshold", Before: raw(`5`), After: raw(`8`)},
		{Field: "severity", Before: raw(`"warning"`), After: raw(`"critical"`)},
	}
	// Somebody raised the threshold by hand while the proposal sat.
	current := map[string]json.RawMessage{
		"threshold": raw(`6`),
		"severity":  raw(`"warning"`),
	}
	got := CheckDrift(changes, current)
	if len(got) != 1 || got[0] != "threshold" {
		t.Fatalf("expected threshold to drift, got %v", got)
	}
}

func TestCheckDriftTreatsMissingFieldAsDrift(t *testing.T) {
	// The target no longer exposes the field the proposal targets — the
	// caller cannot establish the basis for approval, so approving would
	// be acting on an unknown. Must NOT read as "unchanged".
	changes := []Change{{Field: "threshold", Before: raw(`5`), After: raw(`8`)}}
	if got := CheckDrift(changes, map[string]json.RawMessage{}); len(got) != 1 {
		t.Fatalf("missing field must count as drift, got %v", got)
	}
}

func TestCheckDriftIgnoresFormattingDifferences(t *testing.T) {
	// Re-serialisation must not masquerade as a human edit: key order and
	// whitespace differ constantly between what an agent read and what
	// the store returns later. Crying drift here would make the whole
	// mechanism unusable.
	changes := []Change{
		{Field: "labels", Before: raw(`{"team":"payments","env":"prod"}`), After: raw(`{"team":"payments","env":"staging"}`)},
		{Field: "window", Before: raw(`  "5m"  `), After: raw(`"10m"`)},
	}
	current := map[string]json.RawMessage{
		"labels": raw(`{"env":"prod","team":"payments"}`), // same value, different key order
		"window": raw(`"5m"`),
	}
	if got := CheckDrift(changes, current); len(got) != 0 {
		t.Fatalf("formatting differences must not read as drift, got %v", got)
	}
}

func TestPendingOnlyForPendingState(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  bool
	}{
		{"pending", true},
		{"approved", false},
		{"rejected", false},
		{"expired", false},
		{"superseded", false},
	} {
		if got := (Proposal{State: tc.state}).Pending(); got != tc.want {
			t.Errorf("state %q: Pending()=%v want %v", tc.state, got, tc.want)
		}
	}
}
