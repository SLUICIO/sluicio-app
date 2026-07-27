// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A proposal that reports "approved" while changing nothing is worse
// than one that fails: the audit trail then records a change that never
// happened, and the reviewer believes they acted.
//
// That is not hypothetical. evaluation_seconds was originally listed as
// tunable because it sits on AlertRule and reads back from the database
// — but Alerts.UpdateRule's SET clause omits it, so approving silently
// no-opped. The guard below is source-level rather than behavioural
// because the failure lives in SQL that a unit test can't reach without
// a database: every tunable must appear in UpdateRule's UPDATE
// statement, or it cannot possibly persist.

package api

import (
	"os"
	"strings"
	"testing"
)

func TestEveryAlertRuleTunableIsPersistedByUpdateRule(t *testing.T) {
	src, err := os.ReadFile("../alerting/store.go")
	if err != nil {
		t.Fatalf("read alerting store: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "func (s *Store) UpdateRule(")
	if start < 0 {
		t.Fatal("UpdateRule not found — this guard needs updating")
	}
	// The UPDATE runs early in the function; bound the window generously
	// rather than parsing Go.
	end := start + 2000
	if end > len(body) {
		end = len(body)
	}
	stmt := body[start:end]
	set := stmt[strings.Index(stmt, "SET "):]

	// Spec-carried fields are persisted inside the rule_spec JSON blob,
	// not as their own columns, so they're satisfied by rule_spec.
	specCarried := map[string]bool{"threshold": true, "for_window": true}

	for field := range alertRuleTunables {
		column := field
		if specCarried[field] {
			column = "rule_spec"
		}
		if !strings.Contains(set, column) {
			t.Errorf("tunable %q maps to column %q, which UpdateRule does not SET — "+
				"approving a proposal for it would report success and change nothing",
				field, column)
		}
	}
}

// The tunables are the "too noisy / too quiet" dials. Retargeting a rule
// is authoring, not tuning, and must stay a human action — an agent that
// could repoint a check at another service or metric could quietly
// disable monitoring while appearing to tune it.
func TestAlertRuleTunablesExcludeRetargeting(t *testing.T) {
	for _, forbidden := range []string{
		"metric_name", "service_name", "integration_id", "signal",
		"group_id", "name", "channel_ids", "aggregation", "operator",
	} {
		if alertRuleTunables[forbidden] {
			t.Errorf("%q is retargeting/authoring, not tuning — it must not be agent-proposable", forbidden)
		}
	}
}

// Each supported target kind must declare its fields, a snapshot and an
// apply. A kind missing snapshot would nil-panic on create; one missing
// apply would accept proposals it can never honour.
func TestProposalTargetsAreComplete(t *testing.T) {
	if len(proposalTargets) == 0 {
		t.Fatal("no proposal targets registered")
	}
	for kind, target := range proposalTargets {
		if len(target.fields) == 0 {
			t.Errorf("%s: no proposable fields declared", kind)
		}
		if target.snapshot == nil {
			t.Errorf("%s: no snapshot func — the drift check has nothing to compare against", kind)
		}
		if target.apply == nil {
			t.Errorf("%s: no apply func — proposals could be filed but never honoured", kind)
		}
	}
}
