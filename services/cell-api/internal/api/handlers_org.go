// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
)

// Org-level CRUD for the Organization settings tab:
//
//   GET    /api/v1/orgs/{id}        — read the org row (any member)
//   PATCH  /api/v1/orgs/{id}        — rename / re-slug (admin)
//   DELETE /api/v1/orgs/{id}        — wipe the org (admin, with safety)
//
// All three are gated to the org named in the URL: the resolved
// principal's OrgID must match {id}, mirroring how the rest of the
// surface treats cross-org access. We don't yet have a "switch active
// org" flow that would let an admin operate on org B from a session
// pinned to A — that's a separate slice.

// orgSlugRe matches the same shape the create-group form enforces:
// lowercase letters, digits, hyphens. Strict enough that URLs and
// org-prefixed IDs stay readable.
var orgSlugRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// getOrg: GET /api/v1/orgs/{id}
//
// Any member of the org can read its profile (name, slug, timestamps).
// Use the path id rather than the principal's active OrgID so a future
// "switch org" call can hit the new one explicitly.
func (h *Handlers) getOrg(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathOrgID(w, r)
	if !ok {
		return
	}
	if !sameAsActiveOrg(r, orgID) {
		httpserver.WriteError(w, http.StatusForbidden, "not a member of this org")
		return
	}
	o, err := h.Identity.GetOrgByID(r.Context(), orgID)
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "org not found")
			return
		}
		h.Logger.Error("getOrg failed", "err", err, "org_id", orgID)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, o)
}

type updateOrgBody struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// updateOrg: PATCH /api/v1/orgs/{id}
//
// Renames or re-slugs the org. Either field can be omitted to leave it
// alone. Slug must match orgSlugRe — short, URL-safe, lowercase. Gated
// to org admin (RequireRole at the mux level), and the path id must
// also be the principal's active org.
func (h *Handlers) updateOrg(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathOrgID(w, r)
	if !ok {
		return
	}
	if !sameAsActiveOrg(r, orgID) {
		httpserver.WriteError(w, http.StatusForbidden, "not a member of this org")
		return
	}
	var body updateOrgBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	slug := strings.TrimSpace(body.Slug)
	if slug != "" && !orgSlugRe.MatchString(slug) {
		httpserver.WriteError(w, http.StatusBadRequest, "slug must be lowercase letters, digits, and hyphens (1-64 chars)")
		return
	}
	if err := h.Identity.UpdateOrg(r.Context(), orgID, name, slug); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "org not found")
			return
		}
		if errors.Is(err, identity.ErrSlugTaken) {
			httpserver.WriteError(w, http.StatusConflict, "an org with that slug already exists")
			return
		}
		h.Logger.Error("updateOrg failed", "err", err, "org_id", orgID)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "org.updated", "org", orgID.String(), map[string]any{"name": name, "slug": slug})
	o, err := h.Identity.GetOrgByID(r.Context(), orgID)
	if err != nil {
		h.Logger.Warn("updateOrg: re-read failed", "err", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, o)
}

// Org deletion is operator-only: DELETE /api/v1/operator/orgs/{id}
// (handlers_operator.go). The former org-admin delete was removed —
// removing an organization is cell lifecycle, not org self-service.

// pathOrgID extracts the {id} from /api/v1/orgs/{id}, writing a 400
// response and returning false if it's missing or malformed.
func pathOrgID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "missing org id")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid org id")
		return uuid.Nil, false
	}
	return id, true
}

// sameAsActiveOrg returns true if the path's org id matches the
// principal's active OrgID. The handlers use this as a per-request
// authorization check on top of the mux-level role gate.
func sameAsActiveOrg(r *http.Request, orgID uuid.UUID) bool {
	p, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		return false
	}
	return p.OrgID == orgID
}
