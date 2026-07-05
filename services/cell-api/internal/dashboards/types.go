// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package dashboards owns the per-user, named-dashboard model behind
// the Home/Health page. A dashboard is a saved layout of integration
// cards plus the widget choice for each card. The shape mirrors the
// frontend's Dashboard / DashboardItem types 1:1 so the wire format
// is a direct round-trip of the editor's in-memory state.
package dashboards

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// WidgetType enumerates the per-card widgets the dashboard editor can
// pick. Adding a new value here is a four-place change:
//   - this constant + AllWidgetTypes
//   - the CHECK constraint in the 0006_dashboards migration (or a new
//     migration that ALTERs the constraint)
//   - the WidgetType union in frontend/src/api/types.ts
//   - the renderer in frontend/src/pages/Health.tsx
type WidgetType string

const (
	// WidgetTrafficSparkline is the default — the small line chart of
	// message volume that ships in the legacy "show everything" view.
	WidgetTrafficSparkline WidgetType = "traffic_sparkline"
	// WidgetErrorCount renders the error count and (when room) the
	// error rate. Suits users who care about reliability over volume.
	WidgetErrorCount WidgetType = "error_count"
	// WidgetLatencyP95 shows the integration's p95 latency. Useful for
	// teams whose pager fires on slowness rather than failure count.
	WidgetLatencyP95 WidgetType = "latency_p95"
	// WidgetSystemHealth is the only widget for a system item: status +
	// error count + kind badge. Not offered for integration items.
	WidgetSystemHealth WidgetType = "system_health"
)

// AllWidgetTypes is the integration-card picker set (a system item always
// uses system_health). Keep this sorted by the order the picker presents them.
var AllWidgetTypes = []WidgetType{
	WidgetTrafficSparkline,
	WidgetErrorCount,
	WidgetLatencyP95,
}

// EntityKind discriminates what a dashboard item points at.
type EntityKind string

const (
	EntityIntegration EntityKind = "integration"
	EntitySystem      EntityKind = "system"
)

// IsValidWidgetType reports whether s is a known integration widget.
func IsValidWidgetType(s string) bool {
	for _, w := range AllWidgetTypes {
		if string(w) == s {
			return true
		}
	}
	return false
}

// Dashboard is one named, per-user layout. AutoIncludeAll = true means
// "render every integration in the org and use Items only as widget-
// type overrides"; false means "render only the integrations explicitly
// listed in Items". Mine is computed per request from the caller's user
// id, matching the message_views convention.
type Dashboard struct {
	ID                uuid.UUID  `json:"id"`
	OrganizationID    uuid.UUID  `json:"-"`
	OwnerUserID       *uuid.UUID `json:"-"`
	Name              string     `json:"name"`
	IsDefault         bool       `json:"isDefault"`
	AutoIncludeAll    bool       `json:"autoIncludeAll"`
	DefaultWidgetType WidgetType `json:"defaultWidgetType"`
	Position          int        `json:"position"`
	// GroupID scopes the dashboard to one team (RBAC v2 A'): nil =
	// org-wide. Immutable after create — moving between teams is a
	// delete + recreate, which keeps the manage gate simple and honest.
	GroupID *uuid.UUID `json:"groupId,omitempty"`
	// CanManage is computed per caller by the handler layer; the store
	// never sets it.
	CanManage bool `json:"canManage"`
	Mine      bool `json:"mine"`
	// Items is always emitted (possibly empty) so the frontend never
	// has to distinguish "field absent" from "no overrides".
	Items     []Item    `json:"items"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Item pins an integration or a system to a dashboard with a chosen widget
// type. EntityKind discriminates: "integration" uses IntegrationID, "system"
// uses SystemName (+ the system_health widget). Position orders the card;
// ties are broken by CreatedAt at query time.
type Item struct {
	ID            uuid.UUID  `json:"id"`
	EntityKind    EntityKind `json:"entityKind"`
	IntegrationID uuid.UUID  `json:"integrationId"`
	SystemName    string     `json:"systemName,omitempty"`
	WidgetType    WidgetType `json:"widgetType"`
	Position      int        `json:"position"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// CreateRequest is the body of POST /api/v1/dashboards.
type CreateRequest struct {
	Name              string     `json:"name"`
	IsDefault         bool       `json:"isDefault,omitempty"`
	AutoIncludeAll    bool       `json:"autoIncludeAll,omitempty"`
	DefaultWidgetType WidgetType `json:"defaultWidgetType,omitempty"`
	Position          int        `json:"position,omitempty"`
	// GroupID scopes the new dashboard to a team; nil = org-wide.
	GroupID *uuid.UUID    `json:"groupId,omitempty"`
	Items   []ItemRequest `json:"items,omitempty"`
}

// UpdateRequest is the body of PUT /api/v1/dashboards/{id}. Like
// message_views, this is a full replace — every mutable field is
// required so the API stays a simple swap-with operation. Items are
// replaced atomically: any row not in the new list is dropped, rows
// present are upserted by (dashboard, integration).
type UpdateRequest struct {
	Name              string     `json:"name"`
	IsDefault         bool       `json:"isDefault"`
	AutoIncludeAll    bool       `json:"autoIncludeAll"`
	DefaultWidgetType WidgetType `json:"defaultWidgetType"`
	Position          int        `json:"position"`
	// GroupID is accepted for shape parity with CreateRequest but
	// IGNORED on update — a dashboard's team is immutable (see
	// Dashboard.GroupID).
	GroupID *uuid.UUID    `json:"groupId,omitempty"`
	Items   []ItemRequest `json:"items"`
}

// ItemRequest is one row in a Create/Update payload. ID is ignored on
// write. EntityKind defaults to "integration" for back-compat; "system"
// items carry SystemName instead of IntegrationID.
type ItemRequest struct {
	EntityKind    EntityKind `json:"entityKind"`
	IntegrationID string     `json:"integrationId"`
	SystemName    string     `json:"systemName"`
	WidgetType    WidgetType `json:"widgetType"`
	Position      int        `json:"position"`
}

// Validate checks shape and vocabulary. Semantic checks that need the
// DB (integration exists in this org) live in the store / handler.
func (r CreateRequest) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return errInvalid("name is required")
	}
	if len(r.Name) > 200 {
		return errInvalid("name must be 200 characters or fewer")
	}
	dwt := r.DefaultWidgetType
	if dwt == "" {
		dwt = WidgetTrafficSparkline
	}
	if !IsValidWidgetType(string(dwt)) {
		return errInvalid(fmt.Sprintf("unknown defaultWidgetType %q", r.DefaultWidgetType))
	}
	for i, it := range r.Items {
		if err := it.Validate(); err != nil {
			return fmt.Errorf("items[%d]: %w", i, err)
		}
	}
	return nil
}

// Validate enforces the same rules as CreateRequest.Validate (update is
// a full replace, so both endpoints accept the same shape).
func (r UpdateRequest) Validate() error {
	return CreateRequest(r).Validate()
}

// Validate checks the item is well-formed in isolation. EntityKind defaults
// to "integration"; a system item needs a non-empty systemName and uses the
// system_health widget.
func (i ItemRequest) Validate() error {
	kind := i.EntityKind
	if kind == "" {
		kind = EntityIntegration
	}
	switch kind {
	case EntitySystem:
		if strings.TrimSpace(i.SystemName) == "" {
			return errInvalid("system item requires systemName")
		}
		if i.IntegrationID != "" {
			return errInvalid("system item must not set integrationId")
		}
		if i.WidgetType != "" && i.WidgetType != WidgetSystemHealth {
			return errInvalid("system item must use the system_health widget")
		}
	case EntityIntegration:
		if _, err := uuid.Parse(i.IntegrationID); err != nil {
			return errInvalid("integrationId must be a UUID")
		}
		wt := i.WidgetType
		if wt == "" {
			wt = WidgetTrafficSparkline
		}
		if !IsValidWidgetType(string(wt)) {
			return errInvalid(fmt.Sprintf("unknown widgetType %q", i.WidgetType))
		}
	default:
		return errInvalid(fmt.Sprintf("unknown entityKind %q", i.EntityKind))
	}
	return nil
}

// Validation error plumbing — same pattern as the tags package so
// callers can use IsValidationError(err) to map to a 400.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func errInvalid(s string) error          { return &validationError{msg: s} }

// IsValidationError reports whether err originated from a Validate call.
func IsValidationError(err error) bool {
	var v *validationError
	return errors.As(err, &v)
}
