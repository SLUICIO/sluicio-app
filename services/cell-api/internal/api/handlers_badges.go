// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Public status badges — a shields-style SVG at
//
//	GET /api/v1/badges/{integration|system|service}/{id}[.svg]
//
// reachable WITHOUT a session (added to the auth skip-list in main.go), but
// only for an entity whose owner flipped `badge_public = true`. Everything
// else returns 404 — identical for "doesn't exist" and "not opted in", so the
// endpoint never reveals which entities exist. The toggle endpoints below are
// the opposite: authenticated + write-gated, one per entity kind.
//
// Self-hosted cells are single-org (DefaultOrgID), so the public lookup keys
// off that. A future multi-tenant deployment would encode the org in the URL.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/integrations"
)

// ── status → badge ────────────────────────────────────────────────────────

// badgeStatus computes a coarse public health for an entity from its member
// services. unhealthy (a health check is firing — the app's own definition of
// unhealthy) wins; else errors (error traces in the last 24h — raw, so a
// coarser signal than the acked in-app view); else ok; else "quiet" (no
// members). No principal needed — it's an org-scoped read.
func (h *Handlers) badgeStatus(ctx context.Context, orgID uuid.UUID, members []string) string {
	if len(members) == 0 {
		return "quiet"
	}
	if h.Alerts != nil {
		if firing, err := h.Alerts.FiringHealthServices(ctx, orgID); err == nil {
			for _, m := range members {
				if firing[m] {
					return "unhealthy"
				}
			}
		} else {
			h.Logger.Warn("badge: firing services lookup failed", "err", err)
		}
	}
	if h.Store != nil {
		to := time.Now().UTC()
		from := to.Add(-24 * time.Hour)
		if n, err := h.Store.CountErrorTracesForServices(ctx, members, from, to, nil); err == nil && n > 0 {
			return "errors"
		}
	}
	return "ok"
}

func badgeColor(status string) string {
	switch status {
	case "ok":
		return "#3fb950" // green
	case "errors":
		return "#d29922" // amber
	case "unhealthy":
		return "#e5534b" // red
	default:
		return "#8b949e" // grey (quiet / unknown)
	}
}

func badgeMessage(status string) string {
	switch status {
	case "ok":
		return "healthy"
	case "errors":
		return "errors"
	case "unhealthy":
		return "unhealthy"
	default:
		return "no data"
	}
}

// textWidth approximates the rendered px width of s in the badge's ~11px font.
// Deliberately generous (7px/char) so a name never clips.
func badgeTextWidth(s string) int { return len([]rune(s))*7 + 12 }

func badgeTruncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// renderBadge builds a flat shields-style SVG: a grey label segment and a
// status-coloured message segment, each with a subtle drop shadow.
func renderBadge(label, message, color string) []byte {
	label = badgeTruncate(label, 40)
	lw := badgeTextWidth(label)
	mw := badgeTextWidth(message)
	total := lw + mw
	lx := lw / 2
	mx := lw + mw/2
	esc := html.EscapeString
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">`+
		`<title>%s: %s</title>`+
		`<linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>`+
		`<clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>`+
		`<g clip-path="url(#r)">`+
		`<rect width="%d" height="20" fill="#555"/>`+
		`<rect x="%d" width="%d" height="20" fill="%s"/>`+
		`<rect width="%d" height="20" fill="url(#s)"/>`+
		`</g>`+
		`<g fill="#fff" text-anchor="middle" font-family="Verdana,DejaVu Sans,Geneva,sans-serif" font-size="11">`+
		`<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text><text x="%d" y="14">%s</text>`+
		`<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text><text x="%d" y="14">%s</text>`+
		`</g></svg>`,
		total, esc(label), esc(message),
		esc(label), esc(message),
		total,
		lw,
		lw, mw, color,
		total,
		lx, esc(label), lx, esc(label),
		mx, esc(message), mx, esc(message),
	)
	return []byte(svg)
}

func writeBadge(w http.ResponseWriter, label, status string) {
	w.Header().Set("Content-Type", "image/svg+xml;charset=utf-8")
	// Short cache so embeds reflect status within ~a minute without hammering
	// the DB on every README render.
	w.Header().Set("Cache-Control", "max-age=60")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(renderBadge(label, badgeMessage(status), badgeColor(status)))
}

// ── public endpoint ───────────────────────────────────────────────────────

// badgeSVG: GET /api/v1/badges/{kind}/{id} — public, no session. Renders only
// for opted-in entities; 404 otherwise (indistinguishable from not-found).
func (h *Handlers) badgeSVG(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	id := strings.TrimSuffix(r.PathValue("id"), ".svg")
	orgID := integrations.DefaultOrgID // single-org self-hosted

	switch kind {
	case "integration":
		iid, err := uuid.Parse(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		integ, err := h.Integrations.Get(r.Context(), orgID, iid)
		if err != nil || !integ.BadgePublic {
			http.NotFound(w, r)
			return
		}
		members, _ := h.Catalog.IntegrationServices(r.Context(), iid)
		writeBadge(w, integ.Name, h.badgeStatus(r.Context(), orgID, members))
	case "system":
		sid, err := uuid.Parse(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		sy, ok, err := h.Catalog.GetSystem(r.Context(), orgID, sid)
		if err != nil || !ok || !sy.BadgePublic {
			http.NotFound(w, r)
			return
		}
		writeBadge(w, sy.Name, h.badgeStatus(r.Context(), orgID, sy.Members))
	case "service":
		svc, ok, err := h.Catalog.GetService(r.Context(), orgID, id)
		if err != nil || !ok || !svc.BadgePublic {
			http.NotFound(w, r)
			return
		}
		writeBadge(w, svc.ServiceName, h.badgeStatus(r.Context(), orgID, []string{svc.ServiceName}))
	default:
		http.NotFound(w, r)
	}
}

// ── authed toggles (one per kind) ─────────────────────────────────────────

type badgeToggleRequest struct {
	Public bool `json:"public"`
}

func (h *Handlers) decodeBadgeToggle(w http.ResponseWriter, r *http.Request) (bool, bool) {
	var req badgeToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return false, false
	}
	return req.Public, true
}

// putIntegrationBadge: PUT /api/v1/integrations/{id}/badge {public: bool}.
func (h *Handlers) putIntegrationBadge(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	public, ok := h.decodeBadgeToggle(w, r)
	if !ok {
		return
	}
	if err := h.Integrations.SetBadgePublic(r.Context(), middleware.OrgID(r), id, public); err != nil {
		if err == integrations.ErrNotFound {
			httpserver.WriteError(w, http.StatusNotFound, "integration not found")
			return
		}
		h.Logger.Error("set integration badge failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "badge.visibility_set", "integration", id.String(), map[string]any{"public": public, "kind": "integration"})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"badge_public": public})
}

// putSystemBadge: PUT /api/v1/systems/{id}/badge {public: bool}.
func (h *Handlers) putSystemBadge(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	public, ok := h.decodeBadgeToggle(w, r)
	if !ok {
		return
	}
	if err := h.Catalog.SetSystemBadgePublic(r.Context(), middleware.OrgID(r), id, public); err != nil {
		h.Logger.Error("set system badge failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "badge.visibility_set", "system", id.String(), map[string]any{"public": public, "kind": "system"})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"badge_public": public})
}

// putServiceBadge: PUT /api/v1/services/{name}/badge {public: bool}.
func (h *Handlers) putServiceBadge(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	public, ok := h.decodeBadgeToggle(w, r)
	if !ok {
		return
	}
	found, err := h.Catalog.SetServiceBadgePublic(r.Context(), middleware.OrgID(r), name, public)
	if err != nil {
		h.Logger.Error("set service badge failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !found {
		httpserver.WriteError(w, http.StatusNotFound, "service not found")
		return
	}
	h.recordAudit(r, "badge.visibility_set", "service", name, map[string]any{"public": public, "kind": "service"})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"badge_public": public})
}
