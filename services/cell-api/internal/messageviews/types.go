// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package messageviews owns the saved-views model for the Messages
// page and the structured-filter search that backs it. The filter
// shape mirrors the frontend's FilterEditor 1:1 so the two ends never
// drift: each filter is a (field, operator, value) triple, with an
// optional fieldPath for nested attribute lookups under "payload".
package messageviews

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Field enumerates the columns the FilterEditor lets the user filter
// on. Adding a new field here means three coordinated edits: this
// constant, the ClickHouse predicate in search.go, and the picker on
// the frontend.
type Field string

const (
	FieldPayload     Field = "payload"
	FieldTime        Field = "time"
	FieldIntegration Field = "integration"
	FieldStatus      Field = "status"
	FieldService     Field = "service"
	FieldErrorType   Field = "errorType"
	FieldTraceID     Field = "traceId"
	FieldSpanID      Field = "spanId"
)

// AllFields is the ordered set the API exposes via /messages/fields.
var AllFields = []Field{
	FieldPayload, FieldTime, FieldIntegration,
	FieldStatus, FieldService, FieldErrorType,
	FieldTraceID, FieldSpanID,
}

// Operator enumerates the comparisons the picker offers. Not every
// combination of field+operator is valid; the API rejects invalid
// pairings with a 400 rather than silently dropping the filter.
type Operator string

const (
	OpEquals   Operator = "equals"
	OpContains Operator = "contains"
	OpIs       Operator = "is"
	OpIn       Operator = "in"
	OpMatches  Operator = "matches"
)

// Filter is one row in a saved view. FieldPath is only meaningful when
// Field == "payload" (it's the attribute key inside the merged span/
// resource attribute map). Removable, Locked and Optional are
// frontend-only hints kept on the wire so the round-trip stays
// lossless — Locked marks a row whose value is fixed by the page's
// scope (e.g. the integration filter on /integrations/:id/messages);
// Optional marks a row the user has muted but kept around as a
// reminder. The search engine treats Optional rows as a no-op.
type Filter struct {
	ID        string   `json:"id,omitempty"`
	Field     Field    `json:"field"`
	FieldPath string   `json:"fieldPath,omitempty"`
	Op        Operator `json:"op"`
	Value     string   `json:"value"`
	Removable bool     `json:"removable,omitempty"`
	Locked    bool     `json:"locked,omitempty"`
	Optional  bool     `json:"optional,omitempty"`
}

// Validate returns nil if the filter is well-formed. It does not check
// the value's *semantics* (e.g. that a time value is parseable) — the
// search engine does that when it builds the SQL.
func (f Filter) Validate() error {
	switch f.Field {
	case FieldPayload, FieldTime, FieldIntegration, FieldStatus, FieldService, FieldErrorType, FieldTraceID, FieldSpanID:
		// ok
	default:
		return fmt.Errorf("unknown field %q", f.Field)
	}
	switch f.Op {
	case OpEquals, OpContains, OpIs, OpIn, OpMatches:
		// ok
	default:
		return fmt.Errorf("unknown operator %q", f.Op)
	}
	if f.Field == FieldPayload && strings.TrimSpace(f.FieldPath) == "" {
		return errors.New("payload filter requires a fieldPath")
	}
	if len(f.Value) > 256 {
		return errors.New("filter value too long")
	}
	if len(f.FieldPath) > 128 {
		return errors.New("field path too long")
	}
	return nil
}

// Scope describes the entity a saved view is pinned to. A view is
// "scoped" if any of these fields is set; the same view then surfaces
// on the entity's Messages tab and on the global search page with an
// "in <entity>" badge. A nil/empty Scope means the view is global.
//
// The fields are intentionally optional so future scope kinds can be
// added without changing every call-site.
type Scope struct {
	IntegrationID string `json:"integrationId,omitempty"`
	ServiceID     string `json:"serviceId,omitempty"`
}

// IsZero reports whether the scope carries no pin.
func (s Scope) IsZero() bool {
	return s.IntegrationID == "" && s.ServiceID == ""
}

// View is one persisted saved view. JSON tags match the frontend's
// SavedView interface so the wire format stays a direct mirror of
// what the FilterEditor consumes.
type View struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"-"`
	OwnerUserID    *uuid.UUID `json:"-"`
	Name           string     `json:"name"`
	Description    string     `json:"description,omitempty"`
	Pinned         bool       `json:"pinned"`
	Shared         bool       `json:"shared"`
	Filters        []Filter   `json:"filters"`
	// Scope pins the view to a specific entity. The JSON object is
	// always emitted (even when empty) so the frontend doesn't have to
	// distinguish "field absent" from "no scope".
	Scope        Scope     `json:"scope"`
	Mine         bool      `json:"mine"`
	ResultCount  *int64    `json:"resultCount,omitempty"`
	LastEditedAt time.Time `json:"lastEditedAt"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// CreateRequest is the body of POST /api/v1/message-views.
type CreateRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Pinned      bool     `json:"pinned"`
	Shared      bool     `json:"shared"`
	Filters     []Filter `json:"filters"`
	// Scope, if any, pins the view to a specific entity. The caller —
	// typically the IntegrationMessages page — sets this so the saved
	// view surfaces in both the entity's tab and the global rail.
	Scope Scope `json:"scope,omitempty"`
}

// UpdateRequest is the body of PUT /api/v1/message-views/{id}. Every
// mutable field is required so the API stays a straightforward
// replace-with operation; partial updates can be added later if a
// real reason emerges.
type UpdateRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Pinned      bool     `json:"pinned"`
	Shared      bool     `json:"shared"`
	Filters     []Filter `json:"filters"`
	Scope       Scope    `json:"scope,omitempty"`
}

// SearchRequest is the body of POST /api/v1/messages/search. Range is
// the same format as the GET endpoints take in the `range` query
// param (e.g. "1h" or "ISO/ISO"); the time filter inside Filters can
// override it. Limit caps the matching trace count at 1000.
type SearchRequest struct {
	Range   string        `json:"range,omitempty"`
	Filters []Filter      `json:"filters"`
	Limit   int           `json:"limit,omitempty"`
	Cursor  *SearchCursor `json:"cursor,omitempty"`
}

// SearchCursor is the keyset position for the next page of results.
// Both fields are opaque strings to the client: TS is the last row's
// latest-match timestamp in unix nanoseconds (a string because it
// exceeds JS's safe-integer range) and ID is the last row's TraceId.
type SearchCursor struct {
	TS string `json:"ts"`
	ID string `json:"id"`
}

// ValidateAll returns the first validation error in the filter list,
// or nil. Callers should pre-check at the API boundary so the search
// engine can assume each filter is well-formed.
func ValidateAll(fs []Filter) error {
	for i, f := range fs {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("filter[%d]: %w", i, err)
		}
	}
	return nil
}

// SafeAttributeKey returns the input as-is if it is a safe identifier
// for use inside ClickHouse map lookups. Anything outside this charset
// is rejected — the value is interpolated directly into SQL via
// fmt.Sprintf because the driver can't bind a map key. Letters,
// digits, dots, underscores and dashes cover every OTel semantic
// convention attribute name (e.g. http.route, messaging.destination,
// payload.orderId, file-name).
var safeKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)

func SafeAttributeKey(k string) bool {
	return safeKeyRe.MatchString(k)
}
