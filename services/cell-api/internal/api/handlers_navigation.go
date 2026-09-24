// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What the caller can actually reach, for the navigation (issue #30).
//
// A viewer scoped to one integration used to be offered Services,
// Systems, Topology, Metrics and Logs alongside it. Every one of them
// was empty or 404, so five of nine entries led nowhere. The navigation
// should offer what is there.
//
// # This is a hint, not a gate
//
// The point governs the whole design, so it is worth saying plainly:
// the backend remains the access boundary. #28 made it correct; this
// only decides what to offer. Hiding Logs must never be the reason
// somebody cannot read logs, and showing it must never be the reason
// they can. Every field below is derived from the same resolution the
// gates use, so the two cannot drift — but if they ever did, the gate
// wins and the navigation is merely wrong rather than unsafe.
//
// # Why "is there anything here" and not "may you write"
//
// The Configure section already gates on write, and Admin on role.
// Those are questions about permission. This answers a different one:
// whether the surface holds anything for you at all. A viewer with
// read access to metrics sees Metrics; a viewer with none does not,
// even though neither of them may change anything.

package api

import (
	"net/http"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// NavigationResponse says which monitor surfaces hold something for the
// caller. Every field is "is there anything here for you".
type NavigationResponse struct {
	Integrations bool `json:"integrations"`
	Services     bool `json:"services"`
	Systems      bool `json:"systems"`
	Topology     bool `json:"topology"`
	Messages     bool `json:"messages"`
	Metrics      bool `json:"metrics"`
	Logs         bool `json:"logs"`
	Errors       bool `json:"errors"`
	// Unrestricted is an admin or a wildcard policy. Sent so the UI can
	// skip the per-entry reasoning entirely rather than inferring it
	// from eight trues.
	Unrestricted bool `json:"unrestricted"`
}

// getNavigation: GET /api/v1/me/navigation
func (h *Handlers) getNavigation(w http.ResponseWriter, r *http.Request) {
	if _, restricted := h.visibilityMember(r); !restricted {
		httpserver.WriteJSON(w, http.StatusOK, NavigationResponse{
			Integrations: true, Services: true, Systems: true, Topology: true,
			Messages: true, Metrics: true, Logs: true, Errors: true,
			Unrestricted: true,
		})
		return
	}

	// Resolved through the same helpers the GATES use, not through
	// ResolveEffectiveAccessMember directly. Those two are not the same
	// answer: resource shares are a supplement applied during resolution
	// and never appear in the raw policy composition, so reading it
	// directly showed an empty navigation to a user whose only access is
	// a shared integration - every entry hidden, on a cell where they
	// could open the thing that was shared with them.
	//
	// This is the drift the file header warns about, and it is why the
	// hint must be derived from the gate rather than from the same
	// inputs.
	allowed, restricted := h.visibleServiceFilter(r)
	if !restricted {
		// Wildcard, or a resolver error that failed open. A database
		// blip must not empty somebody's navigation; the gates still
		// hold, so the worst case is an entry leading to an empty page.
		httpserver.WriteJSON(w, http.StatusOK, NavigationResponse{
			Integrations: true, Services: true, Systems: true, Topology: true,
			Messages: true, Metrics: true, Logs: true, Errors: true,
			Unrestricted: true,
		})
		return
	}
	granted, integRestricted := h.grantedIntegrations(r)
	if !integRestricted {
		httpserver.WriteJSON(w, http.StatusOK, NavigationResponse{
			Integrations: true, Services: true, Systems: true, Topology: true,
			Messages: true, Metrics: true, Logs: true, Errors: true,
			Unrestricted: true,
		})
		return
	}

	// Services, Systems and Topology are all lists of service-shaped
	// things, so they stand or fall on the service set. An
	// integration-only grant contributes nothing here, which is the
	// whole point of #28: the services stay invisible as objects.
	hasServices := len(allowed) > 0

	// Integrations needs either route in. A plain service grant implies
	// its integrations, and an integration grant is now first-class.
	hasIntegrations := hasServices || len(granted) > 0

	signalReaches := func(sig identity.Signal) bool {
		names, filtered := h.signalServiceFilter(r, sig)
		return !filtered || len(names) > 0
	}

	httpserver.WriteJSON(w, http.StatusOK, NavigationResponse{
		Integrations: hasIntegrations,
		Services:     hasServices,
		Systems:      hasServices,
		Topology:     hasServices,
		// Messages follows the span scope rather than the service set,
		// because an integration-only grant reaches its own slice even
		// with no service of its own (#28 phase 2).
		Messages: !h.visibleSpanScope(r, identity.SignalMessages, time.Time{}, time.Time{}).Empty,
		Metrics:  signalReaches(identity.SignalMetrics),
		Logs:     signalReaches(identity.SignalLogs),
		// Errors is attributed per service today, so it follows the
		// service set. When error attribution becomes integration-aware
		// it should follow Integrations too.
		Errors: hasServices,
	})
}
