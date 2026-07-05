// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Boolean-expression access policies (kind='expression'). A policy of this
// kind carries an arbitrary AND/OR/NOT tree over leaves that match a
// service name or a resource attribute, giving groups a fully general
// visibility predicate:
//
//	(service prefix "ABC")
//	  AND ((attr "team" = "orders") OR (attr "team" = "payments"))
//	  AND NOT (attr "env" = "sandbox")
//
// Safety properties this file guarantees:
//   - Union-of-allows preserved: an expression resolves to a SET of
//     visible services that is UNIONed with the user's other policies
//     (see ResolveVisibleServiceSet). It only ever narrows what THIS
//     policy grants; it can never revoke another group's grant. There
//     are no deny rules.
//   - NOT is complemented against the ORG'S OWN service universe only —
//     never cross-org (the universe is supplied by the caller from the
//     org-scoped catalog).
//   - Fail closed: a malformed, empty, or too-deep tree resolves to the
//     empty set. Validation rejects such trees at write time; the
//     evaluator is defensive at read time regardless.

package identity

import (
	"fmt"
	"regexp"
	"strings"
)

// PolicyExpr is one node of an access-policy boolean expression. A node
// is either an operator (Op set, Children populated) or a leaf (Match
// set). A leaf whose Attr is "" matches the service NAME; otherwise it
// matches the resource attribute named by Attr.
type PolicyExpr struct {
	Op       string       `json:"op,omitempty"` // "and" | "or" | "not" (operator node)
	Children []PolicyExpr `json:"children,omitempty"`

	Attr   string   `json:"attr,omitempty"`   // leaf: "" = service name; else attribute key
	Match  string   `json:"match,omitempty"`  // leaf operator (see MatchOp constants)
	Value  string   `json:"value,omitempty"`  // scalar leaf value
	Values []string `json:"values,omitempty"` // leaf value list (for "in")
}

// Leaf match operators.
const (
	MatchEquals    = "equals"
	MatchNotEquals = "not_equals"
	MatchPrefix    = "prefix"
	MatchSuffix    = "suffix"
	MatchContains  = "contains"
	MatchRegex     = "regex"
	MatchIn        = "in"
	MatchExists    = "exists"     // attribute leaves only
	MatchNotExists = "not_exists" // attribute leaves only
)

// exprMaxDepth / exprMaxNodes bound a tree so a pathological policy can't
// blow the stack or the evaluator. Generous vs any real access rule.
const (
	exprMaxDepth = 24
	exprMaxNodes = 256
)

func (e *PolicyExpr) isOperator() bool { return e.Op != "" }

// ValidateExpr checks a tree is well-formed: known operators, correct
// child arity, known leaf ops with the right value shape, compilable
// regexes, and within the depth/size bounds. Returns a user-facing error.
func ValidateExpr(root *PolicyExpr) error {
	if root == nil {
		return fmt.Errorf("expression policy requires a conditions tree")
	}
	n := 0
	return validateExprNode(root, 1, &n)
}

func validateExprNode(e *PolicyExpr, depth int, count *int) error {
	*count++
	if depth > exprMaxDepth {
		return fmt.Errorf("expression nested too deep (max %d)", exprMaxDepth)
	}
	if *count > exprMaxNodes {
		return fmt.Errorf("expression too large (max %d nodes)", exprMaxNodes)
	}
	if e.isOperator() {
		op := strings.ToLower(e.Op)
		switch op {
		case "and", "or":
			if len(e.Children) == 0 {
				return fmt.Errorf("%q needs at least one child", op)
			}
		case "not":
			if len(e.Children) != 1 {
				return fmt.Errorf("\"not\" needs exactly one child")
			}
		default:
			return fmt.Errorf("unknown operator %q (want and/or/not)", e.Op)
		}
		// A leaf must not also carry operator fields.
		if e.Match != "" || e.Attr != "" || e.Value != "" || len(e.Values) > 0 {
			return fmt.Errorf("operator node must not set leaf fields")
		}
		for i := range e.Children {
			if err := validateExprNode(&e.Children[i], depth+1, count); err != nil {
				return err
			}
		}
		return nil
	}
	// Leaf.
	if len(e.Children) > 0 {
		return fmt.Errorf("leaf node must not have children")
	}
	isServiceLeaf := e.Attr == ""
	switch e.Match {
	case MatchEquals, MatchNotEquals, MatchPrefix, MatchSuffix, MatchContains:
		if e.Value == "" {
			return fmt.Errorf("%q requires a value", e.Match)
		}
	case MatchRegex:
		if e.Value == "" {
			return fmt.Errorf("regex requires a value")
		}
		if _, err := regexp.Compile(e.Value); err != nil {
			return fmt.Errorf("invalid regex: %v", err)
		}
	case MatchIn:
		if len(e.Values) == 0 {
			return fmt.Errorf("\"in\" requires a non-empty values list")
		}
	case MatchExists, MatchNotExists:
		if isServiceLeaf {
			return fmt.Errorf("%q applies to an attribute leaf, not the service name", e.Match)
		}
		if e.Value != "" || len(e.Values) > 0 {
			return fmt.Errorf("%q takes no value", e.Match)
		}
	case "":
		return fmt.Errorf("leaf requires a match operator")
	default:
		return fmt.Errorf("unknown match operator %q", e.Match)
	}
	return nil
}

// evalExpr resolves a validated tree to the set of service names it
// grants, given the org's service universe and the per-service resource
// attributes. Defensive: an unknown/invalid node yields the empty set
// (fail closed) rather than panicking or over-granting.
//
//	universe — every service name in the org (for NOT complement + service leaves)
//	attrs    — attrs[serviceName][attrKey] = attrValue (absent key ⇒ no such attr)
func evalExpr(e *PolicyExpr, universe []string, attrs map[string]map[string]string) map[string]struct{} {
	if e == nil {
		return map[string]struct{}{}
	}
	if e.isOperator() {
		switch strings.ToLower(e.Op) {
		case "and":
			// Intersection. Start from the first child, shrink by the rest.
			if len(e.Children) == 0 {
				return map[string]struct{}{}
			}
			acc := evalExpr(&e.Children[0], universe, attrs)
			for i := 1; i < len(e.Children); i++ {
				next := evalExpr(&e.Children[i], universe, attrs)
				for name := range acc {
					if _, ok := next[name]; !ok {
						delete(acc, name)
					}
				}
			}
			return acc
		case "or":
			acc := map[string]struct{}{}
			for i := range e.Children {
				for name := range evalExpr(&e.Children[i], universe, attrs) {
					acc[name] = struct{}{}
				}
			}
			return acc
		case "not":
			if len(e.Children) != 1 {
				return map[string]struct{}{}
			}
			inner := evalExpr(&e.Children[0], universe, attrs)
			out := make(map[string]struct{}, len(universe))
			for _, name := range universe {
				if _, ok := inner[name]; !ok {
					out[name] = struct{}{}
				}
			}
			return out
		default:
			return map[string]struct{}{}
		}
	}
	return evalLeaf(e, universe, attrs)
}

// evalLeaf resolves a single leaf against the universe. Iterates the
// universe (not the attrs map) so services with no resource attributes
// are still considered — important for not_equals / not_exists.
func evalLeaf(e *PolicyExpr, universe []string, attrs map[string]map[string]string) map[string]struct{} {
	out := map[string]struct{}{}
	var re *regexp.Regexp
	if e.Match == MatchRegex {
		// Validated at write time; if it somehow fails here, match nothing.
		var err error
		if re, err = regexp.Compile(e.Value); err != nil {
			return out
		}
	}
	serviceLeaf := e.Attr == ""
	for _, name := range universe {
		var subject string
		present := true
		if serviceLeaf {
			subject = name
		} else {
			subject, present = attrs[name][e.Attr]
		}
		if matchLeaf(e, subject, present, re) {
			out[name] = struct{}{}
		}
	}
	return out
}

// matchLeaf applies one leaf's operator to one subject value. `present`
// reports whether the attribute exists on the service (always true for a
// service-name leaf). Absence semantics are deliberate and documented:
//   - not_equals matches when the value differs OR is absent (i.e. NOT (a=X))
//   - not_exists matches only when absent
//   - every positive op (equals/prefix/…/regex/in) requires presence
func matchLeaf(e *PolicyExpr, subject string, present bool, re *regexp.Regexp) bool {
	switch e.Match {
	case MatchExists:
		return present
	case MatchNotExists:
		return !present
	case MatchNotEquals:
		return !present || subject != e.Value
	}
	// Positive operators require the attribute (or service name) to exist.
	if !present {
		return false
	}
	switch e.Match {
	case MatchEquals:
		return subject == e.Value
	case MatchPrefix:
		return strings.HasPrefix(subject, e.Value)
	case MatchSuffix:
		return strings.HasSuffix(subject, e.Value)
	case MatchContains:
		return strings.Contains(subject, e.Value)
	case MatchRegex:
		return re != nil && re.MatchString(subject)
	case MatchIn:
		for _, v := range e.Values {
			if subject == v {
				return true
			}
		}
		return false
	default:
		return false
	}
}
