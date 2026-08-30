// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package identity

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func lister(names ...string) entityLister {
	return func(context.Context, uuid.UUID) ([]namedEntity, error) {
		out := make([]namedEntity, 0, len(names))
		for _, n := range names {
			out = append(out, namedEntity{ID: uuid.New(), Name: n})
		}
		return out, nil
	}
}

func matched(t *testing.T, expr *PolicyExpr, names ...string) []string {
	t.Helper()
	got, err := matchEntities(context.Background(), uuid.New(), expr, lister(names...))
	if err != nil {
		t.Fatalf("matchEntities: %v", err)
	}
	out := make([]string, 0, len(got))
	for _, e := range got {
		out = append(out, e.Name)
	}
	return out
}

// The three shapes the feature was asked for.
func TestNamePatterns(t *testing.T) {
	universe := []string{"abc-orders-at", "abc-orders-se", "xyz-orders-at", "abcdef", "zzdef"}

	t.Run("prefix", func(t *testing.T) {
		got := matched(t, &PolicyExpr{Match: MatchPrefix, Value: "abc"}, universe...)
		if len(got) != 3 {
			t.Fatalf("prefix abc = %v, want the three abc* names", got)
		}
	})

	t.Run("suffix", func(t *testing.T) {
		got := matched(t, &PolicyExpr{Match: MatchSuffix, Value: "def"}, universe...)
		if len(got) != 2 {
			t.Fatalf("suffix def = %v, want abcdef and zzdef", got)
		}
	})

	// "starting with abc AND ending with -at" — the combination that
	// motivated the feature.
	t.Run("prefix and suffix", func(t *testing.T) {
		got := matched(t, &PolicyExpr{Op: "and", Children: []PolicyExpr{
			{Match: MatchPrefix, Value: "abc"},
			{Match: MatchSuffix, Value: "-at"},
		}}, universe...)
		if len(got) != 1 || got[0] != "abc-orders-at" {
			t.Fatalf("abc* AND *-at = %v, want [abc-orders-at]", got)
		}
	})
}

// A nil lister is an unwired dependency, not a wildcard. On the grant
// path the safe direction is nothing.
func TestNilListerGrantsNothing(t *testing.T) {
	got, err := matchEntities(context.Background(), uuid.New(), &PolicyExpr{Match: MatchPrefix, Value: "abc"}, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("nil lister matched %d entities, want 0", len(got))
	}
}

// An attribute leaf against a universe of names matches nothing forever,
// in a policy that reads as correct. It has to be refused at write time.
func TestAttributeLeafRefused(t *testing.T) {
	err := validateNameOnlyExpr(&PolicyExpr{Attr: "team", Match: MatchEquals, Value: "orders"})
	if err == nil {
		t.Fatal("attribute leaf accepted on a name-matching policy")
	}
	if err := validateNameOnlyExpr(&PolicyExpr{Match: MatchPrefix, Value: "abc"}); err != nil {
		t.Fatalf("name leaf rejected: %v", err)
	}
}

func TestIntegrationPolicyTakesIdOrPattern(t *testing.T) {
	both := AccessPolicyInput{
		Kind:                PolicyIntegration,
		TargetIntegrationID: uuid.New().String(),
		Conditions:          &PolicyExpr{Match: MatchPrefix, Value: "abc"},
	}
	if err := validatePolicyInput(&both); err == nil {
		t.Fatal("accepted an integration policy naming one AND describing a set")
	}
	neither := AccessPolicyInput{Kind: PolicyIntegration}
	if err := validatePolicyInput(&neither); err == nil {
		t.Fatal("accepted an integration policy that targets nothing")
	}
	pattern := AccessPolicyInput{Kind: PolicyIntegration, Conditions: &PolicyExpr{Match: MatchPrefix, Value: "abc"}}
	if err := validatePolicyInput(&pattern); err != nil {
		t.Fatalf("rejected a pattern-only integration policy: %v", err)
	}
}

// Resolution, not just matching: a pattern policy has to reach
// EffectiveAccess.Integrations the same way naming one id does.
func TestIntegrationPatternResolvesToGrants(t *testing.T) {
	orgID := uuid.New()
	daily1, daily2, other := uuid.New(), uuid.New(), uuid.New()
	s := &Store{
		listIntegrations: func(context.Context, uuid.UUID) ([]namedEntity, error) {
			return []namedEntity{
				{ID: daily1, Name: "Daily invoices → Finance"},
				{ID: daily2, Name: "Daily PIM Updates"},
				{ID: other, Name: "Nightly price file → Retailer X"},
			}, nil
		},
	}
	pols := []AccessPolicy{{
		Kind:       PolicyIntegration,
		Conditions: &PolicyExpr{Match: MatchPrefix, Value: "Daily"},
	}}
	acc, err := s.composeAccess(context.Background(), orgID, pols, nil, nil)
	if err != nil {
		t.Fatalf("composeAccess: %v", err)
	}
	if len(acc.Integrations) != 2 {
		t.Fatalf("granted %d integrations, want the two Daily ones", len(acc.Integrations))
	}
	if _, ok := acc.Integrations[other]; ok {
		t.Fatal("granted an integration the pattern does not match")
	}
	// grant_services is false, so no services ride along — the same
	// least-privilege default as a single-id policy.
	if len(acc.Services) != 0 {
		t.Fatalf("granted %d services without grant_services", len(acc.Services))
	}
}

// grant_services on a pattern applies to EVERY match, which is the part
// worth being deliberate about: one checkbox, N integrations' services.
func TestPatternGrantServicesAppliesToEveryMatch(t *testing.T) {
	orgID := uuid.New()
	a, b := uuid.New(), uuid.New()
	s := &Store{
		listIntegrations: func(context.Context, uuid.UUID) ([]namedEntity, error) {
			return []namedEntity{{ID: a, Name: "abc-one"}, {ID: b, Name: "abc-two"}}, nil
		},
	}
	expand := func(_ context.Context, _ uuid.UUID, id uuid.UUID) ([]string, error) {
		if id == a {
			return []string{"svc-a"}, nil
		}
		return []string{"svc-b"}, nil
	}
	pols := []AccessPolicy{{
		Kind:          PolicyIntegration,
		Conditions:    &PolicyExpr{Match: MatchPrefix, Value: "abc"},
		GrantServices: true,
	}}
	acc, err := s.composeAccess(context.Background(), orgID, pols, expand, nil)
	if err != nil {
		t.Fatalf("composeAccess: %v", err)
	}
	if len(acc.Services) != 2 {
		t.Fatalf("services = %v, want both matches' services", acc.Services)
	}
}
