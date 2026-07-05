// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The "since last visit" activity digest: per-user, RBAC-filtered. Two
// sections — services registered since the watermark (with a detected
// monitoring-template suggestion where their metrics identify a kind), and
// alert firings since the watermark (grouped target). The watermark is the
// user's digest_seen_at, bumped by POST /digest/seen.

package api

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
)

// integrationNameMap resolves integration id → name for the digest's failure
// rows (so an integration-bound firing reads with a name, not a UUID).
func (h *Handlers) integrationNameMap(ctx context.Context, orgID uuid.UUID) map[uuid.UUID]string {
	m := map[uuid.UUID]string{}
	if ints, err := h.Integrations.List(ctx, orgID); err == nil {
		for _, i := range ints {
			m[i.ID] = i.Name
		}
	}
	return m
}

// newServiceDigest is one newly-registered service, with an optional template
// suggestion when its emitted metrics identify a kind.
type newServiceDigest struct {
	ServiceName    string    `json:"service_name"`
	Namespace      string    `json:"namespace,omitempty"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	SuggestedKind  string    `json:"suggested_kind,omitempty"`
	SuggestedLabel string    `json:"suggested_label,omitempty"`
}

type failureDigest struct {
	ServiceName     string    `json:"service_name,omitempty"`
	IntegrationID   string    `json:"integration_id,omitempty"`
	IntegrationName string    `json:"integration_name,omitempty"`
	RuleName        string    `json:"rule_name"`
	Severity        string    `json:"severity"`
	State           string    `json:"state"`
	StartedAt       time.Time `json:"started_at"`
}

// newServiceScanCap bounds how many new services we run detection on, so a
// huge first-time backlog can't fan out into thousands of ClickHouse queries.
const newServiceScanCap = 50

// getDigest: GET /api/v1/digest
func (h *Handlers) getDigest(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	if p.UserID == nil {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"new_services": []any{}, "failures": []any{}})
		return
	}
	orgID := middleware.OrgID(r)
	ctx := r.Context()

	// Watermark: digest_seen_at, or a 14-day lookback the first time so the
	// digest isn't empty (or a flood) on a brand-new account.
	since := time.Now().Add(-14 * 24 * time.Hour)
	if seen, err := h.Identity.DigestSeenAt(ctx, *p.UserID); err == nil && seen != nil {
		since = *seen
	}

	// RBAC: resolve the caller's visible-service set once.
	allowed, hasFilter := h.visibleServiceFilter(r)
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, n := range allowed {
		allowedSet[n] = struct{}{}
	}
	canSeeSvc := func(name string) bool {
		if !hasFilter {
			return true
		}
		_, ok := allowedSet[name]
		return ok
	}

	// New services since the watermark, visible to the caller, with detection.
	newServices := []newServiceDigest{}
	if all, err := h.Catalog.AllServices(ctx, orgID); err == nil {
		detTo := time.Now()
		detFrom := detTo.Add(-24 * time.Hour)
		scanned := 0
		for _, s := range all {
			if !s.FirstSeenAt.After(since) || !canSeeSvc(s.ServiceName) {
				continue
			}
			d := newServiceDigest{ServiceName: s.ServiceName, Namespace: s.ServiceNamespace, FirstSeenAt: s.FirstSeenAt}
			if scanned < newServiceScanCap {
				scanned++
				if tmpls, derr := h.detectTemplates(ctx, orgID, s.ServiceName, detFrom, detTo); derr == nil && len(tmpls) > 0 {
					d.SuggestedKind = tmpls[0].Kind
					d.SuggestedLabel = tmpls[0].Label
				}
			}
			newServices = append(newServices, d)
		}
	} else {
		h.Logger.Warn("digest: list services failed", "err", err)
	}

	// Failures since the watermark, visibility-gated. Resolve integration
	// names once for the rows that need them.
	failures := []failureDigest{}
	if instances, err := h.Alerts.RecentInstances(ctx, orgID, 500); err == nil {
		intgNames := h.integrationNameMap(ctx, orgID)
		for _, inst := range instances {
			if !inst.StartedAt.After(since) {
				continue
			}
			if !h.canSeeAlertTarget(r, inst.ServiceName, inst.IntegrationID) {
				continue
			}
			f := failureDigest{
				ServiceName: inst.ServiceName,
				RuleName:    inst.RuleName,
				Severity:    string(inst.Severity),
				State:       inst.State,
				StartedAt:   inst.StartedAt,
			}
			if inst.IntegrationID != nil {
				f.IntegrationID = inst.IntegrationID.String()
				f.IntegrationName = intgNames[*inst.IntegrationID]
			}
			failures = append(failures, f)
		}
	} else {
		h.Logger.Warn("digest: recent instances failed", "err", err)
	}

	// Resources shared with the caller since the watermark (RBAC v2 §6).
	type sharedDigest struct {
		ResourceKind string    `json:"resource_kind"`
		ResourceID   string    `json:"resource_id"`
		ResourceName string    `json:"resource_name"`
		SharedBy     string    `json:"shared_by,omitempty"`
		SharedAt     time.Time `json:"shared_at"`
	}
	shared := []sharedDigest{}
	sharedIntgNames := h.integrationNameMap(ctx, orgID)
	if rows, err := h.Identity.SharedResourcesSince(ctx, *p.UserID, orgID, since); err == nil {
		for _, sr := range rows {
			sd := sharedDigest{ResourceKind: string(sr.ResourceKind), ResourceID: sr.ResourceID.String(), SharedBy: sr.SharedBy, SharedAt: sr.CreatedAt}
			switch sr.ResourceKind {
			case identity.ShareIntegration:
				sd.ResourceName = sharedIntgNames[sr.ResourceID]
			case identity.ShareSystem:
				if sy, ok, err := h.Catalog.GetSystem(ctx, orgID, sr.ResourceID); err == nil && ok {
					sd.ResourceName = sy.Name
				}
			}
			shared = append(shared, sd)
		}
	} else {
		h.Logger.Warn("digest: shared resources failed", "err", err)
	}

	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"since":        since,
		"new_services": newServices,
		"failures":     failures,
		"shared":       shared,
		"counts": map[string]int{
			"new_services": len(newServices),
			"failures":     len(failures),
			"shared":       len(shared),
		},
	})
}

// markDigestSeen: POST /api/v1/digest/seen
func (h *Handlers) markDigestSeen(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	if p.UserID == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.Identity.SetDigestSeen(r.Context(), *p.UserID); err != nil {
		h.Logger.Error("digest: set seen failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "failed to update")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
