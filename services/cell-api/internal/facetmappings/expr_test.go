// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package facetmappings

import (
	"reflect"
	"strings"
	"testing"
)

// TestBuildResolverEmpty covers the identity case explicitly — when a
// service has no mappings, the resolver returns plain SpanAttributes
// lookups with empty arg slices so call sites can append unconditionally.
func TestBuildResolverEmpty(t *testing.T) {
	t.Parallel()
	got := BuildResolver(nil)
	if got.KindExpr != "SpanAttributes['io.kind']" {
		t.Errorf("KindExpr = %q, want raw SpanAttributes lookup", got.KindExpr)
	}
	if got.RoleExpr != "SpanAttributes['io.role']" {
		t.Errorf("RoleExpr = %q, want raw SpanAttributes lookup", got.RoleExpr)
	}
	if len(got.KindArgs) != 0 || len(got.RoleArgs) != 0 {
		t.Errorf("args should be empty, got kind=%v role=%v", got.KindArgs, got.RoleArgs)
	}
}

// TestBuildResolverSingleEquals checks one common case end-to-end: an
// equals rule on a span attribute that should set both io.kind and
// io.role.
func TestBuildResolverSingleEquals(t *testing.T) {
	t.Parallel()
	got := BuildResolver([]Mapping{
		{
			AttributeSource: AttrSourceSpan,
			AttributeKey:    "peer.service",
			MatchOperator:   OperatorEquals,
			MatchValue:      "sftp.bank.com",
			SetIOKind:       "file",
			SetIORole:       "input",
		},
	})
	// The expression should fall back to the raw lookup first, then
	// the CASE cascade, then ''.
	wantContains := []string{
		"coalesce(nullIf(SpanAttributes['io.kind'], ''),",
		"WHEN SpanAttributes[?] = ? THEN ?",
		"ELSE ''",
	}
	for _, w := range wantContains {
		if !strings.Contains(got.KindExpr, w) {
			t.Errorf("KindExpr missing %q\n got: %s", w, got.KindExpr)
		}
	}
	wantArgs := []any{"peer.service", "sftp.bank.com", "file"}
	if !reflect.DeepEqual(got.KindArgs, wantArgs) {
		t.Errorf("KindArgs = %#v, want %#v", got.KindArgs, wantArgs)
	}
	wantRoleArgs := []any{"peer.service", "sftp.bank.com", "input"}
	if !reflect.DeepEqual(got.RoleArgs, wantRoleArgs) {
		t.Errorf("RoleArgs = %#v, want %#v", got.RoleArgs, wantRoleArgs)
	}
}

// TestBuildResolverPreservesOrder ensures multiple mappings are applied
// in slice order in the cascade so a user's "first rule wins" intuition
// holds across edits.
func TestBuildResolverPreservesOrder(t *testing.T) {
	t.Parallel()
	got := BuildResolver([]Mapping{
		{
			AttributeSource: AttrSourceSpan,
			AttributeKey:    "messaging.system",
			MatchOperator:   OperatorExists,
			SetIOKind:       "queue", SetIORole: "input",
		},
		{
			AttributeSource: AttrSourceSpan,
			AttributeKey:    "http.route",
			MatchOperator:   OperatorExists,
			SetIOKind:       "http", SetIORole: "input",
		},
	})
	// Attribute keys are parameterized in the SQL (SpanAttributes[?]), so
	// the names live in KindArgs in cascade order rather than inlined in
	// the expression. The first rule's key must precede the second's there
	// so the user's "first rule wins" intuition holds.
	idx := func(args []any, want string) int {
		for i, a := range args {
			if s, ok := a.(string); ok && s == want {
				return i
			}
		}
		return -1
	}
	qfirst := idx(got.KindArgs, "messaging.system")
	qsecond := idx(got.KindArgs, "http.route")
	if qfirst < 0 || qsecond < 0 {
		t.Fatalf("KindArgs missing one of the attribute keys:\n%#v\nexpr: %s", got.KindArgs, got.KindExpr)
	}
	if qfirst > qsecond {
		t.Errorf("rule order not preserved: messaging.system at %d, http.route at %d", qfirst, qsecond)
	}
	// Each "exists" rule contributes only the attribute key + THEN value;
	// no MatchValue arg should leak in.
	wantKindArgs := []any{
		"messaging.system", "queue",
		"http.route", "http",
	}
	if !reflect.DeepEqual(got.KindArgs, wantKindArgs) {
		t.Errorf("KindArgs = %#v, want %#v", got.KindArgs, wantKindArgs)
	}
}

// TestConditionSQLOperators iterates every operator and the
// resource-source variant so a renaming of e.g. startsWith doesn't slip
// past as a silent test gap.
func TestConditionSQLOperators(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		mapping  Mapping
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "equals on span",
			mapping:  Mapping{AttributeSource: AttrSourceSpan, AttributeKey: "k", MatchOperator: OperatorEquals, MatchValue: "v"},
			wantSQL:  "SpanAttributes[?] = ?",
			wantArgs: []any{"k", "v"},
		},
		{
			name:     "equals on resource",
			mapping:  Mapping{AttributeSource: AttrSourceResource, AttributeKey: "k", MatchOperator: OperatorEquals, MatchValue: "v"},
			wantSQL:  "ResourceAttributes[?] = ?",
			wantArgs: []any{"k", "v"},
		},
		{
			name:     "prefix",
			mapping:  Mapping{AttributeSource: AttrSourceSpan, AttributeKey: "k", MatchOperator: OperatorPrefix, MatchValue: "v"},
			wantSQL:  "startsWith(SpanAttributes[?], ?)",
			wantArgs: []any{"k", "v"},
		},
		{
			name:     "suffix",
			mapping:  Mapping{AttributeSource: AttrSourceSpan, AttributeKey: "k", MatchOperator: OperatorSuffix, MatchValue: "v"},
			wantSQL:  "endsWith(SpanAttributes[?], ?)",
			wantArgs: []any{"k", "v"},
		},
		{
			name:     "contains",
			mapping:  Mapping{AttributeSource: AttrSourceSpan, AttributeKey: "k", MatchOperator: OperatorContains, MatchValue: "v"},
			wantSQL:  "position(SpanAttributes[?], ?) > 0",
			wantArgs: []any{"k", "v"},
		},
		{
			name:     "exists has no value arg",
			mapping:  Mapping{AttributeSource: AttrSourceSpan, AttributeKey: "k", MatchOperator: OperatorExists},
			wantSQL:  "SpanAttributes[?] != ''",
			wantArgs: []any{"k"},
		},
		{
			name:     "unknown operator fails closed",
			mapping:  Mapping{AttributeSource: AttrSourceSpan, AttributeKey: "k", MatchOperator: Operator("bogus"), MatchValue: "v"},
			wantSQL:  "1=0",
			wantArgs: nil,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotSQL, gotArgs := conditionSQL(tc.mapping)
			if gotSQL != tc.wantSQL {
				t.Errorf("sql = %q, want %q", gotSQL, tc.wantSQL)
			}
			if !reflect.DeepEqual(gotArgs, tc.wantArgs) {
				t.Errorf("args = %#v, want %#v", gotArgs, tc.wantArgs)
			}
		})
	}
}
