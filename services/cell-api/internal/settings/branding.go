// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The cell's mark, for partners who run Sluicio under their own brand
// (issue #29).
//
// Cell-level rather than per-organisation, because a partner's brand is
// a property of the deployment: every org in their cell belongs to them,
// and asking them to set it once per org would be a chore whose only
// possible outcome is an inconsistency.
//
// Gated on the white_label ENTITLEMENT, not a setting. The product is
// self-hosted, so a setting is something anyone running a cell can turn
// on, and for a feature whose subject is the attribution of the product
// that is the difference between a boundary and a request. Storage is
// ungated — an expired licence must leave the values intact so renewing
// restores the brand rather than asking for it again — and the READ
// applies the gate, which is the only place it can be enforced.

package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// MaxBrandingAssetBytes caps one asset's encoded size.
//
// A mark is a small vector or a modest PNG. The cap exists because these
// ride in the cell settings row and are served on every page load: an
// unbounded field here becomes a slow first paint for everybody, and the
// person who pasted a 4MB screenshot would never connect the two.
const MaxBrandingAssetBytes = 256 * 1024

// MaxWordmarkLen bounds the text beside the mark.
const MaxWordmarkLen = 40

// ErrInvalidBranding is returned for an asset that is too large, not a
// data URI, or of a type we will not serve.
var ErrInvalidBranding = errors.New("settings: invalid branding")

// Branding is what replaces the Sluicio lockup.
//
// Every field is optional and falls back to the Sluicio default
// independently, so a partner with only a mark does not have to invent
// a wordmark or a favicon to use it.
type Branding struct {
	// Logo is the mark shown in the app shell, as a data URI.
	Logo string `json:"logo,omitempty"`
	// LogoDark is used when the chrome is dark. Optional: a mark that
	// reads on both is common, and requiring two would make the simple
	// case harder for no gain. Falls back to Logo.
	LogoDark string `json:"logo_dark,omitempty"`
	// Wordmark is the text beside the mark, and the product name in the
	// browser tab title. Empty keeps "Sluicio".
	Wordmark string `json:"wordmark,omitempty"`
	// Favicon is the browser tab icon, as a data URI. Empty keeps the
	// Sluicio favicon.
	Favicon string `json:"favicon,omitempty"`
}

// Empty reports whether nothing has been set, so callers can skip the
// entitlement lookup entirely for the overwhelmingly common case.
func (b Branding) Empty() bool {
	return b.Logo == "" && b.LogoDark == "" && b.Wordmark == "" && b.Favicon == ""
}

// allowedBrandingPrefixes are the data-URI types we will serve back into
// an <img>. Restricted deliberately: the value is echoed into the app
// shell for every user in the cell, so the set of things it may be is
// worth keeping small and boring.
//
// SVG is included because a mark is usually a vector, and excluded types
// are rejected rather than sanitised — we are not in the business of
// scrubbing scripts out of somebody's logo, and an operator who cannot
// upload a PNG has a clearer problem than one whose SVG was silently
// altered.
var allowedBrandingPrefixes = []string{
	"data:image/svg+xml;base64,",
	"data:image/png;base64,",
	"data:image/jpeg;base64,",
	"data:image/webp;base64,",
	"data:image/x-icon;base64,",
}

func validateAsset(field, v string) error {
	if v == "" {
		return nil
	}
	if len(v) > MaxBrandingAssetBytes {
		return fmt.Errorf("%w: %s is over %d KB", ErrInvalidBranding, field, MaxBrandingAssetBytes/1024)
	}
	for _, p := range allowedBrandingPrefixes {
		if strings.HasPrefix(v, p) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s must be a base64 data URI of type svg, png, jpeg, webp or ico", ErrInvalidBranding, field)
}

// GetBranding reads the stored branding. Absent or unparseable reads as
// empty, which means the Sluicio default.
func (s *Store) GetBranding(ctx context.Context) (Branding, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM cell_settings WHERE key = 'system.branding'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Branding{}, nil
	}
	if err != nil {
		return Branding{}, fmt.Errorf("settings: get branding: %w", err)
	}
	var b Branding
	if err := json.Unmarshal(raw, &b); err != nil {
		// A malformed row falls back to the Sluicio mark rather than
		// failing the page. The shell has to render something.
		return Branding{}, nil
	}
	return b, nil
}

// SetBranding replaces the stored branding. Validates every asset before
// writing so a bad value is rejected at the point somebody can still fix
// it, rather than discovered as a broken image by every user at once.
func (s *Store) SetBranding(ctx context.Context, b Branding) error {
	b.Wordmark = strings.TrimSpace(b.Wordmark)
	if len(b.Wordmark) > MaxWordmarkLen {
		return fmt.Errorf("%w: wordmark is over %d characters", ErrInvalidBranding, MaxWordmarkLen)
	}
	for field, v := range map[string]string{
		"logo": b.Logo, "logo_dark": b.LogoDark, "favicon": b.Favicon,
	} {
		if err := validateAsset(field, v); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("settings: encode branding: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO cell_settings (key, value) VALUES ('system.branding', $1)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, raw); err != nil {
		return fmt.Errorf("settings: set branding: %w", err)
	}
	return nil
}
