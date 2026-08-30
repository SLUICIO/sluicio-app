// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The cell's mark (issue #29).
//
// Read by every authenticated user, because the app shell cannot render
// without knowing what to draw, and by nobody at all on the LOGIN page —
// see publicBranding, which exists because the sign-in screen is the one
// page a partner's visitor is guaranteed to see and the only one that
// could not be branded.
//
// Written by a cell OPERATOR, because the brand is a property of the
// deployment rather than of any one organisation in it — which is also
// what makes it a single setting instead of one per org.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/license"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/settings"
)

// BrandingResponse is what the shell draws.
type BrandingResponse struct {
	settings.Branding
	// Entitled says whether white-labelling is licensed. Sent even when
	// no branding is stored, so the operator view can explain why the
	// form is inert rather than leaving somebody to guess.
	Entitled bool `json:"entitled"`
}

// getBranding: GET /api/v1/cell-settings/branding
//
// Returns the Sluicio default (an empty Branding) whenever the cell is
// not entitled, WHATEVER is stored. The gate lives on the read because
// that is the only place it can be enforced: storage stays untouched so
// a lapsed licence loses the brand rather than the configuration, and
// renewing restores it without asking the partner to upload anything
// again.
func (h *Handlers) getBranding(w http.ResponseWriter, r *http.Request) {
	entitled := h.featureEntitled(license.FeatureWhiteLabel)
	if !entitled {
		httpserver.WriteJSON(w, http.StatusOK, BrandingResponse{Entitled: false})
		return
	}
	b, err := h.Settings.GetBranding(r.Context())
	if err != nil {
		// The shell has to render something. Falling back to the Sluicio
		// mark is the honest failure: it is ours, and it is never wrong
		// in the way a half-loaded partner brand would be.
		h.Logger.Warn("read branding failed; using the default mark", "err", err)
		httpserver.WriteJSON(w, http.StatusOK, BrandingResponse{Entitled: true})
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, BrandingResponse{Branding: b, Entitled: true})
}

// publicBranding: GET /api/v1/branding/login — no session required.
//
// The sign-in screen drew the Sluicio mark and said "Sign in to Sluicio"
// on a cell branded as somebody else, because the only branding read
// needed a session and the login page by definition has none. A partner's
// brand appearing only AFTER sign-in inverts the point of having one: the
// visitor who is not yet a customer sees exactly one page, and it was
// ours.
//
// Deliberately a separate route with a narrower body rather than opening
// the authenticated one. It carries the three things needed to paint a
// login screen and NOT the `entitled` flag: whether this deployment holds
// a white-label licence is nobody's business before sign-in, and it is
// only ever consumed by the operator form, which has a session anyway.
//
// Mirrors /api/v1/announcements/login, which is public for the same
// reason and returns a similarly minimal payload.
func (h *Handlers) publicBranding(w http.ResponseWriter, r *http.Request) {
	// An unentitled cell answers with the empty object, which the client
	// reads as "draw the Sluicio lockup" — the same fallback it uses when
	// the request fails outright.
	if !h.featureEntitled(license.FeatureWhiteLabel) {
		httpserver.WriteJSON(w, http.StatusOK, settings.Branding{})
		return
	}
	b, err := h.Settings.GetBranding(r.Context())
	if err != nil {
		h.Logger.Warn("read branding for login failed; using the default mark", "err", err)
		httpserver.WriteJSON(w, http.StatusOK, settings.Branding{})
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, b)
}

// ProductName resolves what this deployment calls itself, for the code
// that has no HTTP request to hang it off — the alert delivery worker and
// the error notifier, whose mails are the only place the product's name
// leaves the building.
//
// Wired into the alerting package at startup (SetProductNameResolver), the
// same shape as ResolveAlertContext and DefaultEmailTemplate: alerting owns
// no stores, so the store-backed half lives here.
//
// Carries the same white_label gate as getBranding, and for the same
// reason — the read is the only place it can be enforced, so an expired
// licence returns "Sluicio" while leaving the stored wordmark untouched.
// A failed read falls back too: our own name in a partner's email is a
// blemish, a nameless one is a broken email.
func (h *Handlers) ProductName(ctx context.Context) string {
	if !h.featureEntitled(license.FeatureWhiteLabel) {
		return ""
	}
	b, err := h.Settings.GetBranding(ctx)
	if err != nil {
		h.Logger.Warn("read branding for notifications failed; using the default name", "err", err)
		return ""
	}
	return b.Wordmark
}

// putBranding: PUT /api/v1/cell-settings/branding  (operator)
func (h *Handlers) putBranding(w http.ResponseWriter, r *http.Request) {
	if !h.featureEntitled(license.FeatureWhiteLabel) {
		httpserver.WriteJSON(w, http.StatusPaymentRequired, map[string]any{
			"error":   "enterprise_feature",
			"feature": string(license.FeatureWhiteLabel),
			"message": "Replacing the Sluicio mark requires a Sluicio Enterprise license with the white_label entitlement.",
		})
		return
	}
	var body settings.Branding
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.Settings.SetBranding(r.Context(), body); err != nil {
		// An oversized or wrongly-typed asset is the caller's mistake and
		// is reported verbatim: "logo is over 256 KB" is actionable where
		// "failed" sends somebody hunting.
		if errors.Is(err, settings.ErrInvalidBranding) {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("save branding failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "saving failed")
		return
	}
	h.recordAudit(r, "branding.updated", "cell", "", map[string]any{
		"wordmark":    body.Wordmark,
		"has_logo":    body.Logo != "",
		"has_dark":    body.LogoDark != "",
		"has_favicon": body.Favicon != "",
	})
	h.getBranding(w, r)
}
