// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package facetmappings

import (
	"fmt"
	"strings"
)

// Resolver is a compiled set of SQL expressions for one service's
// effective io.kind and io.role. The cell-api interpolates these
// into widget queries and the service-profile query, replacing the
// raw SpanAttributes lookups so user-defined mappings drive
// classification and per-facet filtering.
//
// When there are no mappings for the service, KindExpr/RoleExpr are
// the raw SpanAttributes lookups and Args slices are empty — so
// callers can use the Resolver unconditionally without branching on
// "are there rules" at every query site.
//
// Each expression carries its own argument slice because ClickHouse
// driver placeholders are positional and the same Resolver may be
// referenced multiple times in a single query (once per WHERE
// predicate, once in a SELECT projection, etc.). Callers append the
// relevant Args slice each time the expression is interpolated.
type Resolver struct {
	KindExpr string
	KindArgs []any
	RoleExpr string
	RoleArgs []any
}

// IdentityResolver returns a Resolver that performs no substitution —
// effective io.kind / io.role are just the raw SpanAttributes lookups.
// Used when a service has no user-defined mappings, or as a safe
// default in callers that don't have a Resolver wired up yet.
func IdentityResolver() Resolver {
	return Resolver{
		KindExpr: "SpanAttributes['io.kind']",
		RoleExpr: "SpanAttributes['io.role']",
	}
}

// BuildResolver compiles a slice of mappings into a Resolver. The
// mappings are applied as a CASE-WHEN cascade in slice order — the
// store returns them sorted by created_at so the user's "first rule
// added wins" intuition holds. The raw SpanAttributes lookup takes
// precedence over any rule, so a service that *does* emit io.kind /
// io.role directly is unaffected by stale or aspirational rules.
func BuildResolver(mappings []Mapping) Resolver {
	if len(mappings) == 0 {
		return IdentityResolver()
	}
	var kindWhens, roleWhens []string
	var kindArgs, roleArgs []any
	for _, m := range mappings {
		cond, condArgs := conditionSQL(m)
		kindWhens = append(kindWhens, fmt.Sprintf("WHEN %s THEN ?", cond))
		roleWhens = append(roleWhens, fmt.Sprintf("WHEN %s THEN ?", cond))
		// Each WHEN re-emits the condition's args, then appends the
		// THEN value's arg. ClickHouse fills placeholders strictly
		// left-to-right so the order here must mirror the WHEN /
		// THEN order in the SQL.
		kindArgs = append(kindArgs, condArgs...)
		kindArgs = append(kindArgs, m.SetIOKind)
		roleArgs = append(roleArgs, condArgs...)
		roleArgs = append(roleArgs, m.SetIORole)
	}
	kindExpr := fmt.Sprintf(
		"coalesce(nullIf(SpanAttributes['io.kind'], ''), CASE %s ELSE '' END)",
		strings.Join(kindWhens, " "),
	)
	roleExpr := fmt.Sprintf(
		"coalesce(nullIf(SpanAttributes['io.role'], ''), CASE %s ELSE '' END)",
		strings.Join(roleWhens, " "),
	)
	return Resolver{
		KindExpr: kindExpr,
		KindArgs: kindArgs,
		RoleExpr: roleExpr,
		RoleArgs: roleArgs,
	}
}

// conditionSQL renders one mapping's WHEN condition. The attribute
// source picks SpanAttributes vs ResourceAttributes; the operator
// chooses the ClickHouse string function. The returned args list
// covers every "?" in the returned fragment, in left-to-right order.
func conditionSQL(m Mapping) (string, []any) {
	var attrExpr string
	switch m.AttributeSource {
	case AttrSourceResource:
		attrExpr = "ResourceAttributes[?]"
	default:
		attrExpr = "SpanAttributes[?]"
	}
	switch m.MatchOperator {
	case OperatorEquals:
		return fmt.Sprintf("%s = ?", attrExpr), []any{m.AttributeKey, m.MatchValue}
	case OperatorPrefix:
		return fmt.Sprintf("startsWith(%s, ?)", attrExpr), []any{m.AttributeKey, m.MatchValue}
	case OperatorSuffix:
		return fmt.Sprintf("endsWith(%s, ?)", attrExpr), []any{m.AttributeKey, m.MatchValue}
	case OperatorContains:
		return fmt.Sprintf("position(%s, ?) > 0", attrExpr), []any{m.AttributeKey, m.MatchValue}
	case OperatorExists:
		// Presence is "attribute set and non-empty" — matches how the
		// raw SpanAttributes lookups elsewhere treat empty strings as
		// "not set".
		return fmt.Sprintf("%s != ''", attrExpr), []any{m.AttributeKey}
	}
	// Unknown operator — fail closed so the cascade never matches.
	// Validate() should keep us out of this branch in practice.
	return "1=0", nil
}
