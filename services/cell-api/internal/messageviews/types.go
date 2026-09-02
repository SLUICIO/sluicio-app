// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package messageviews owns the saved-views model for the Messages
// page and the structured-filter search that backs it. The filter
// shape mirrors the frontend's FilterEditor 1:1 so the two ends never
// drift: each filter is a (field, operator, value) triple, with an
// optional fieldPath for nested attribute lookups under "payload".
package messageviews

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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

	// The negations, and the two existence checks.
	//
	// Over a MESSAGE these read universally: "no step in this message
	// satisfies the positive form". A message is a trace of many spans,
	// and applying a negation span-by-span the way a positive filter is
	// applied would return any message with at least one span lacking
	// the attribute - nearly every message, for nearly every negation.
	//
	// OpExists stays existential, which is the reading that matches the
	// word: some step carries it. Its counterpart is universal for the
	// same reason as the rest - "no step carries it".
	OpNotEquals   Operator = "not_equals"
	OpNotContains Operator = "not_contains"
	OpExists      Operator = "exists"
	OpNotExists   Operator = "not_exists"
)

// Negated reports whether the operator excludes messages rather than
// selecting them, which decides whether its predicate goes in the
// span-level WHERE or in the anti-join.
func (o Operator) Negated() bool {
	return o == OpNotEquals || o == OpNotContains || o == OpNotExists
}

// Valueless reports whether the operator asks about the key rather than
// its value.
func (o Operator) Valueless() bool {
	return o == OpExists || o == OpNotExists
}

// positiveForm is the operator whose matches a negated row excludes.
// not_equals excludes the messages that equals would have selected.
func (o Operator) positiveForm() Operator {
	switch o {
	case OpNotEquals:
		return OpEquals
	case OpNotContains:
		return OpContains
	case OpNotExists:
		return OpExists
	}
	return o
}

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
	case OpEquals, OpContains, OpIs, OpIn, OpMatches,
		OpNotEquals, OpNotContains, OpExists, OpNotExists:
		// ok
	default:
		return fmt.Errorf("unknown operator %q", f.Op)
	}
	// The existence checks only make sense against an attribute: every
	// other field is a column that is always there, so "status exists"
	// is not a question.
	if f.Op.Valueless() && f.Field != FieldPayload {
		return fmt.Errorf("operator %q applies to payload attributes only, not %q", f.Op, f.Field)
	}
	// A payload filter without a fieldPath is INCOMPLETE, not invalid:
	// it's the FilterEditor's default freshly-added row, it round-trips
	// through saved views, and the search engine treats it as a no-op
	// (search.go). Rejecting it here 400'd the whole search the moment
	// a user added a filter row before picking an attribute.
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
	Scope Scope `json:"scope"`
	// MessageColumns is this view's own column set, in column order.
	//
	// A POINTER because nil and empty mean different things, and the
	// difference is the feature: nil is "this view has no opinion, use
	// the integration's columns", empty is "this view deliberately shows
	// none". Collapsing them would turn every existing view into one
	// that shows nothing.
	//
	// Typed as a raw message rather than the integrations.MessageColumn
	// slice to keep this package free of that dependency — it is
	// validated and normalised by the API layer, which owns both.
	MessageColumns *json.RawMessage `json:"messageColumns,omitempty"`
	Mine           bool             `json:"mine"`
	ResultCount    *int64           `json:"resultCount,omitempty"`
	LastEditedAt   time.Time        `json:"lastEditedAt"`
	CreatedAt      time.Time        `json:"createdAt"`
	UpdatedAt      time.Time        `json:"updatedAt"`
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
	// MessageColumns is this view's own column set. Omit the field to
	// leave the view inheriting the integration's columns; send `[]` to
	// say the view shows none. See View.MessageColumns.
	MessageColumns *json.RawMessage `json:"messageColumns,omitempty"`
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
	// MessageColumns is this view's own column set. Omit the field to
	// leave the view inheriting the integration's columns; send `[]` to
	// say the view shows none. See View.MessageColumns.
	MessageColumns *json.RawMessage `json:"messageColumns,omitempty"`
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
	// ViewID, when set, names the saved view being run. It does not
	// affect WHICH messages come back — the filters already say that —
	// only which columns. Sent so the server resolves the column set
	// from stored state rather than trusting the client to echo it: the
	// query and the headers then come from one answer, and a client
	// holding a stale view cannot label a column it did not query.
	ViewID string `json:"viewId,omitempty"`
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
