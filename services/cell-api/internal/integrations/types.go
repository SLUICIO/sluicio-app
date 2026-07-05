// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package integrations holds the cell-local integration model:
// integrations themselves, the matcher rules that classify services
// into them, and the in-process resolver used by API handlers to
// enrich service responses.
package integrations

import (
	"time"

	"github.com/google/uuid"
)

// Operator is the kind of comparison a matcher performs. The values
// must match the Postgres `matcher_operator` enum.
type Operator string

const (
	OperatorEquals   Operator = "equals"
	OperatorPrefix   Operator = "prefix"
	OperatorSuffix   Operator = "suffix"
	OperatorContains Operator = "contains"
	OperatorRegex    Operator = "regex"
)

// AllOperators enumerates the operators for validation and UI hints.
var AllOperators = []Operator{
	OperatorEquals, OperatorPrefix, OperatorSuffix, OperatorContains, OperatorRegex,
}

// Integration is a user-defined logical grouping of services.
type Integration struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Slug           string    `json:"slug"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	// BadgePublic opts this integration into a public (unauthenticated) status
	// badge at /api/v1/badges/integration/<id>. Only populated by Get.
	BadgePublic bool      `json:"badge_public"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Matcher classifies a service into an integration by attribute pattern.
// In v0.1 only the "service.name" attribute is supported; the column
// is left in the schema for forward compatibility.
type Matcher struct {
	ID            uuid.UUID `json:"id"`
	IntegrationID uuid.UUID `json:"integration_id"`
	Attribute     string    `json:"attribute"`
	Operator      Operator  `json:"operator"`
	Value         string    `json:"value"`
	// MatchGroup groups ATTRIBUTE matchers for OR matching: matchers with
	// the same group are AND-ed, and the groups are OR-ed (DNF). Ignored
	// for service.name matchers (membership). 0 = the default AND-group.
	MatchGroup int       `json:"match_group"`
	CreatedAt  time.Time `json:"created_at"`
}

// IntegrationWithMatchers is the full read-model used by the detail API.
type IntegrationWithMatchers struct {
	Integration
	Matchers []Matcher `json:"matchers"`
}

// DefaultOrgID is the placeholder organization ID used while auth and
// multi-tenancy are not yet wired up. It is stable so that the same
// rows persist across restarts and can be replaced with real org IDs
// without a data migration once we add tenancy.
var DefaultOrgID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
