// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// License *gating* lives in the core (FSL) so the whole app can ask "is this
// feature entitled?" — the *verification* logic and the Enterprise features
// themselves live under ee/ (separate license). This split is intentional:
// the gate is open-source and inspectable; only the EE code it guards is
// proprietary.

package api

import (
	"net/http"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/license"
)

// featureEntitled is the single nil-safe entitlement check. No license
// manager wired, or no/expired key, ⇒ false (feature off) — never a panic,
// never a block on core flows.
func (h *Handlers) featureEntitled(f license.Feature) bool {
	return h.License != nil && h.License.Entitled(f)
}

// requireFeature gates an HTTP handler behind an Enterprise entitlement.
// Unentitled callers get 402 Payment Required with a machine-readable body
// the frontend turns into an upgrade prompt. Use on EE-only routes.
func (h *Handlers) requireFeature(f license.Feature, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.featureEntitled(f) {
			httpserver.WriteJSON(w, http.StatusPaymentRequired, map[string]any{
				"error":   "enterprise_feature",
				"feature": string(f),
				"message": "This is a Sluicio Enterprise feature. A valid license key is required to enable it.",
			})
			return
		}
		next(w, r)
	}
}

// licenseStatusResponse is the license read model plus the live integration
// usage (count vs the licensed cap) so the frontend can drive both feature
// gating and the "integration limit reached" admin notice from one fetch.
type licenseStatusResponse struct {
	license.Status
	IntegrationUsage integrationUsage `json:"integration_usage"`
}

// licenseStatus: GET /api/v1/license — the read model that drives the
// frontend's gating + upsell. Auth'd (any signed-in user may see what's
// licensed); never returns the raw key. Nil-safe: an unwired manager reports
// an unlicensed status with all features off.
func (h *Handlers) licenseStatus(w http.ResponseWriter, r *http.Request) {
	usage := h.integrationUsage(r)
	if h.License == nil {
		st := license.Status{Features: map[string]bool{}}
		for _, f := range license.AllFeatures {
			st.Features[string(f)] = false
		}
		st.Entitlements = []string{}
		httpserver.WriteJSON(w, http.StatusOK, licenseStatusResponse{Status: st, IntegrationUsage: usage})
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, licenseStatusResponse{Status: h.License.Status(), IntegrationUsage: usage})
}
