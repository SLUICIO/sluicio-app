// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package identity

import (
	"testing"

	"github.com/google/uuid"
)

// Unit tests for policyMatchesInput — the equality test behind
// CreatePolicy's ErrPolicyExists. The end-to-end duplicate rejection
// (including that a duplicate on ANOTHER group is fine) lives in
// policies_dedup_integration_test.go.

func strp(s string) *string { return &s }

func TestPolicyMatchesInput(t *testing.T) {
	integA := uuid.New()
	integB := uuid.New()
	sysA := uuid.New()

	expr := &PolicyExpr{Op: "and", Children: []PolicyExpr{
		{Match: MatchPrefix, Value: "ABC"},
		{Attr: "env", Match: MatchEquals, Value: "prod"},
	}}
	exprOther := &PolicyExpr{Op: "and", Children: []PolicyExpr{
		{Match: MatchPrefix, Value: "ABC"},
		{Attr: "env", Match: MatchEquals, Value: "staging"},
	}}

	cases := []struct {
		name     string
		existing AccessPolicy
		in       AccessPolicyInput
		integID  *uuid.UUID
		sysID    *uuid.UUID
		want     bool
	}{
		{
			name:     "service policy identical",
			existing: AccessPolicy{Kind: PolicyService, TargetServiceName: strp("checkout"), AttributeMatch: map[string]string{}},
			in:       AccessPolicyInput{Kind: PolicyService, TargetServiceName: "checkout", AttributeMatch: map[string]string{}},
			want:     true,
		},
		{
			name:     "different service name",
			existing: AccessPolicy{Kind: PolicyService, TargetServiceName: strp("checkout"), AttributeMatch: map[string]string{}},
			in:       AccessPolicyInput{Kind: PolicyService, TargetServiceName: "billing", AttributeMatch: map[string]string{}},
			want:     false,
		},
		{
			name:     "different kind, same emptiness",
			existing: AccessPolicy{Kind: PolicyAllOrg, AttributeMatch: map[string]string{}},
			in:       AccessPolicyInput{Kind: PolicySystem, AttributeMatch: map[string]string{}},
			want:     false,
		},
		{
			name:     "integration policy identical",
			existing: AccessPolicy{Kind: PolicyIntegration, TargetIntegrationID: &integA, AttributeMatch: map[string]string{}},
			in:       AccessPolicyInput{Kind: PolicyIntegration, AttributeMatch: map[string]string{}},
			integID:  &integA,
			want:     true,
		},
		{
			name:     "different integration target",
			existing: AccessPolicy{Kind: PolicyIntegration, TargetIntegrationID: &integA, AttributeMatch: map[string]string{}},
			in:       AccessPolicyInput{Kind: PolicyIntegration, AttributeMatch: map[string]string{}},
			integID:  &integB,
			want:     false,
		},
		{
			name:     "system policy: nil vs set system id",
			existing: AccessPolicy{Kind: PolicySystem, AttributeMatch: map[string]string{}},
			in:       AccessPolicyInput{Kind: PolicySystem, AttributeMatch: map[string]string{}},
			sysID:    &sysA,
			want:     false,
		},
		{
			name:     "system policy: kind narrowing differs",
			existing: AccessPolicy{Kind: PolicySystem, TargetSystemKind: strp("database"), AttributeMatch: map[string]string{}},
			in:       AccessPolicyInput{Kind: PolicySystem, TargetSystemKind: "queue", AttributeMatch: map[string]string{}},
			want:     false,
		},
		{
			name:     "attributes identical regardless of map construction",
			existing: AccessPolicy{Kind: PolicyAttributes, AttributeMatch: map[string]string{"env": "prod", "team": "orders"}},
			in:       AccessPolicyInput{Kind: PolicyAttributes, AttributeMatch: map[string]string{"team": "orders", "env": "prod"}},
			want:     true,
		},
		{
			name:     "attributes differ by one value",
			existing: AccessPolicy{Kind: PolicyAttributes, AttributeMatch: map[string]string{"env": "prod"}},
			in:       AccessPolicyInput{Kind: PolicyAttributes, AttributeMatch: map[string]string{"env": "staging"}},
			want:     false,
		},
		{
			name:     "expression identical tree",
			existing: AccessPolicy{Kind: PolicyExpression, AttributeMatch: map[string]string{}, Conditions: expr},
			in:       AccessPolicyInput{Kind: PolicyExpression, AttributeMatch: map[string]string{}, Conditions: expr},
			want:     true,
		},
		{
			name:     "expression differing leaf value",
			existing: AccessPolicy{Kind: PolicyExpression, AttributeMatch: map[string]string{}, Conditions: expr},
			in:       AccessPolicyInput{Kind: PolicyExpression, AttributeMatch: map[string]string{}, Conditions: exprOther},
			want:     false,
		},
		{
			name:     "signals: same set, different order",
			existing: AccessPolicy{Kind: PolicyService, TargetServiceName: strp("checkout"), AttributeMatch: map[string]string{}, Signals: []Signal{SignalTraces, SignalLogs}},
			in:       AccessPolicyInput{Kind: PolicyService, TargetServiceName: "checkout", AttributeMatch: map[string]string{}, Signals: []Signal{SignalLogs, SignalTraces}},
			want:     true,
		},
		{
			name:     "signals: narrowed vs all-signal",
			existing: AccessPolicy{Kind: PolicyService, TargetServiceName: strp("checkout"), AttributeMatch: map[string]string{}, Signals: []Signal{SignalTraces}},
			in:       AccessPolicyInput{Kind: PolicyService, TargetServiceName: "checkout", AttributeMatch: map[string]string{}},
			want:     false,
		},
		{
			name:     "signals: disjoint sets of equal size",
			existing: AccessPolicy{Kind: PolicyService, TargetServiceName: strp("checkout"), AttributeMatch: map[string]string{}, Signals: []Signal{SignalTraces}},
			in:       AccessPolicyInput{Kind: PolicyService, TargetServiceName: "checkout", AttributeMatch: map[string]string{}, Signals: []Signal{SignalLogs}},
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policyMatchesInput(tc.existing, tc.in, tc.integID, tc.sysID); got != tc.want {
				t.Errorf("policyMatchesInput = %v, want %v", got, tc.want)
			}
		})
	}
}
