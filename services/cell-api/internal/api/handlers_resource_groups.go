// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Resource ⇄ group attachment endpoints (RBAC v2 phase 1):
//
//   GET /api/v1/integrations/{id}/groups   — groups that can view it
//   PUT /api/v1/integrations/{id}/groups   — replace the set (admin)
//   GET /api/v1/systems/{id}/groups
//   PUT /api/v1/systems/{id}/groups
//
// This is the CE-facing visibility grant: attaching a group makes the
// resource and its member services viewable by the group's members.
// Deliberately NOT gated on the rbac_advanced entitlement — it's the
// Community way to grant visibility; the richer policy kinds stay EE.
// Both endpoints are a restricted façade over single-resource policies
// (see identity/resource_groups.go) and cannot touch other kinds.

package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

type setResourceGroupsBody struct {
	GroupIDs []string `json:"group_ids"`
}

func parseGroupIDs(raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	seen := map[uuid.UUID]struct{}{}
	for _, r := range raw {
		id, err := uuid.Parse(r)
		if err != nil {
			return nil, errors.New("group_ids must be UUIDs")
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

// listIntegrationGroups: GET /api/v1/integrations/{id}/groups
func (h *Handlers) listIntegrationGroups(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	if !h.integrationInOrg(w, r, id) {
		return
	}
	groups, err := h.Identity.ListGroupsForIntegration(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		h.Logger.Error("list integration groups failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// putIntegrationGroups: PUT /api/v1/integrations/{id}/groups  (admin)
func (h *Handlers) putIntegrationGroups(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	if !h.integrationInOrg(w, r, id) {
		return
	}
	gids, ok := h.decodeGroupIDs(w, r)
	if !ok {
		return
	}
	if err := h.Identity.SetIntegrationGroups(r.Context(), middleware.OrgID(r), id, gids); err != nil {
		h.writeSetGroupsError(w, err)
		return
	}
	h.recordAudit(r, "integration_groups.updated", "integration", id.String(),
		map[string]any{"group_count": len(gids)})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"group_ids": gids})
}

// listSystemGroups: GET /api/v1/systems/{id}/groups
func (h *Handlers) listSystemGroups(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	if !h.systemInOrg(w, r, id) {
		return
	}
	groups, err := h.Identity.ListGroupsForSystem(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		h.Logger.Error("list system groups failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// putSystemGroups: PUT /api/v1/systems/{id}/groups  (admin)
func (h *Handlers) putSystemGroups(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	if !h.systemInOrg(w, r, id) {
		return
	}
	gids, ok := h.decodeGroupIDs(w, r)
	if !ok {
		return
	}
	if err := h.Identity.SetSystemGroups(r.Context(), middleware.OrgID(r), id, gids); err != nil {
		h.writeSetGroupsError(w, err)
		return
	}
	h.recordAudit(r, "system_groups.updated", "system", id.String(),
		map[string]any{"group_count": len(gids)})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"group_ids": gids})
}

// ── shared helpers ─────────────────────────────────────────────────

func (h *Handlers) decodeGroupIDs(w http.ResponseWriter, r *http.Request) ([]uuid.UUID, bool) {
	var body setResourceGroupsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return nil, false
	}
	gids, err := parseGroupIDs(body.GroupIDs)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return gids, true
}

func (h *Handlers) writeSetGroupsError(w http.ResponseWriter, err error) {
	if errors.Is(err, identity.ErrNotFound) {
		httpserver.WriteError(w, http.StatusBadRequest, "one or more groups do not exist in this organization")
		return
	}
	h.Logger.Error("set resource groups failed", "err", err)
	httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
}

// integrationInOrg 404s (and returns false) unless the integration
// belongs to the caller's org.
func (h *Handlers) integrationInOrg(w http.ResponseWriter, r *http.Request, id uuid.UUID) bool {
	if _, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id); err != nil {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return false
	}
	return true
}

// systemInOrg 404s (and returns false) unless the system belongs to the
// caller's org.
func (h *Handlers) systemInOrg(w http.ResponseWriter, r *http.Request, id uuid.UUID) bool {
	_, ok, err := h.Catalog.GetSystem(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		h.Logger.Error("system lookup failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return false
	}
	if !ok {
		httpserver.WriteError(w, http.StatusNotFound, "system not found")
		return false
	}
	return true
}
