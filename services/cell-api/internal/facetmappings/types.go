// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package facetmappings holds the cell-local model for user-defined
// facet attribute mappings. A mapping says "for this service, treat
// spans whose attribute X satisfies condition Y as carrying io.kind=K
// and io.role=R" — a way to bring services into the built-in facet
// classification without re-instrumenting them to emit io.kind /
// io.role directly.
//
// At query time the cell-api compiles a service's mappings into SQL
// expressions for effective_io_kind and effective_io_role; those
// expressions are interpolated into widget queries and the service
// profile query in place of the raw SpanAttributes lookups. See
// `expr.go` for the compilation step.
package facetmappings

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AttributeSource is where the cell-api should pull the matched
// attribute from. Mirrors the OTel split between span and resource
// attributes — both maps are populated on every span row in
// ClickHouse, so either is cheap to read.
type AttributeSource string

const (
	AttrSourceSpan     AttributeSource = "span"
	AttrSourceResource AttributeSource = "resource"
)

// Operator is the comparison performed against the attribute value.
// "exists" matches whenever the attribute is present and non-empty —
// useful for rules like "any span with messaging.system set is a
// queue interaction" where the value varies but the presence is the
// signal.
//
// Regex is intentionally NOT in the v1 vocabulary; the additional
// query-injection surface (regex literals interpolated into SQL)
// isn't worth the small UX gain.
type Operator string

const (
	OperatorEquals   Operator = "equals"
	OperatorPrefix   Operator = "prefix"
	OperatorSuffix   Operator = "suffix"
	OperatorContains Operator = "contains"
	OperatorExists   Operator = "exists"
)

// AllOperators lists the operators for UI hints and validation.
var AllOperators = []Operator{
	OperatorEquals, OperatorPrefix, OperatorSuffix, OperatorContains, OperatorExists,
}

// Mapping is one user-defined rule. When the When* fields are
// satisfied on a span, the cell-api treats that span as carrying
// SetIOKind and SetIORole for the purposes of facet classification
// and widget filtering.
type Mapping struct {
	ID              uuid.UUID       `json:"id"`
	OrganizationID  uuid.UUID       `json:"organization_id"`
	ServiceName     string          `json:"service_name"`
	AttributeSource AttributeSource `json:"attribute_source"`
	AttributeKey    string          `json:"attribute_key"`
	MatchOperator   Operator        `json:"match_operator"`
	// MatchValue is empty when MatchOperator == "exists".
	MatchValue string    `json:"match_value"`
	SetIOKind  string    `json:"set_io_kind"`
	SetIORole  string    `json:"set_io_role"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// validKinds / validRoles are the closed sets the built-in facets
// recognize. The API layer rejects values outside these so a user
// can't write a mapping that classifies a service into a non-
// existent facet.
var validKinds = map[string]bool{
	"file": true, "queue": true, "stream": true, "http": true, "db": true, "email": true,
}
var validRoles = map[string]bool{
	"input": true, "output": true,
}

// Validate checks the mapping's structure. The caller should trim
// whitespace and lowercase enum-like fields before calling.
func (m Mapping) Validate() error {
	switch m.AttributeSource {
	case AttrSourceSpan, AttrSourceResource:
	default:
		return errInvalid("attribute_source must be 'span' or 'resource'")
	}
	if strings.TrimSpace(m.AttributeKey) == "" {
		return errInvalid("attribute_key must not be empty")
	}
	switch m.MatchOperator {
	case OperatorEquals, OperatorPrefix, OperatorSuffix, OperatorContains:
		if m.MatchValue == "" {
			return errInvalid("match_value must not be empty for this operator")
		}
	case OperatorExists:
		// match_value is ignored; the store layer also writes "" so the
		// DB CHECK stays happy.
	default:
		return errInvalid("unknown match_operator")
	}
	if !validKinds[m.SetIOKind] {
		return errInvalid("set_io_kind must be one of: file, queue, stream, http, db, email")
	}
	if !validRoles[m.SetIORole] {
		return errInvalid("set_io_role must be 'input' or 'output'")
	}
	if strings.TrimSpace(m.ServiceName) == "" {
		return errInvalid("service_name must not be empty")
	}
	return nil
}

// ErrNotFound is returned when a mapping ID is not in the store.
var ErrNotFound = errors.New("facet mapping not found")

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func errInvalid(s string) error          { return &validationError{msg: s} }

// IsValidationError reports whether err came from Mapping.Validate.
func IsValidationError(err error) bool {
	var v *validationError
	return errors.As(err, &v)
}
