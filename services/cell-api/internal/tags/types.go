// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package tags holds the cell-local tag model. A tag is a flat,
// org-scoped label that can be attached to integrations and to
// individual services. The vocabulary is intentionally minimal — a
// stable slug, a display name, and a color — so the UI can render
// chips today and we can add richer fields (category, description)
// later without a data migration.
package tags

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Tag is one row in the org's tag vocabulary.
type Tag struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Slug           string    `json:"slug"`
	Name           string    `json:"name"`
	// Color is a lowercase CSS hex color, "#rgb" or "#rrggbb". The
	// frontend uses it as the chip background.
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TagWithUsage extends Tag with the count of integrations and
// services it's currently attached to. Returned by the management
// list endpoint so the UI can show informed delete confirmations
// ("This will untag 4 integrations and 12 services") without N+1.
type TagWithUsage struct {
	Tag
	IntegrationCount int `json:"integration_count"`
	ServiceCount     int `json:"service_count"`
}

// IntegrationTagLink is an attachment of a tag to an integration.
type IntegrationTagLink struct {
	IntegrationID uuid.UUID `json:"integration_id"`
	TagID         uuid.UUID `json:"tag_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// ServiceTagLink is an attachment of a tag to a service. Services are
// not first-class rows in this DB (they live in ClickHouse), so the
// link is keyed by the service's name within the org.
type ServiceTagLink struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	ServiceName    string    `json:"service_name"`
	TagID          uuid.UUID `json:"tag_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// validation

// colorRe enforces the same shape as the DB CHECK: "#rgb" or "#rrggbb"
// in lowercase hex. The handler normalises before validating.
var colorRe = regexp.MustCompile(`^#([0-9a-f]{3}|[0-9a-f]{6})$`)

// slugRe enforces a URL-safe slug: lowercase alphanumerics with single
// hyphens, no leading/trailing hyphen, 1-64 chars.
var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// Validate checks that a tag has a usable slug, name, and color.
// Callers should normalise (TrimSpace, lowercase color) before calling.
func (t Tag) Validate() error {
	if t.Slug == "" || !slugRe.MatchString(t.Slug) {
		return errInvalid("slug must be lowercase alphanumeric with optional hyphens (1-64 chars)")
	}
	if strings.TrimSpace(t.Name) == "" {
		return errInvalid("name must not be empty")
	}
	if len(t.Name) > 128 {
		return errInvalid("name must be 128 characters or fewer")
	}
	if !colorRe.MatchString(t.Color) {
		return errInvalid("color must be a lowercase hex like #3b82f6 or #fff")
	}
	return nil
}

// NormalizeColor lowercases and trims a color string for storage. It
// does not validate — callers should run Validate afterwards.
func NormalizeColor(c string) string {
	return strings.ToLower(strings.TrimSpace(c))
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func errInvalid(s string) error { return &validationError{msg: s} }

// IsValidationError reports whether err originated from Tag.Validate.
func IsValidationError(err error) bool {
	var v *validationError
	return errors.As(err, &v)
}
