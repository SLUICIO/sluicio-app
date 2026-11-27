// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Collector management over OpAMP, stage 1 (read only, Beta).
//
//   GET    /api/v1/collectors                                  collectors the caller may read
//   GET    /api/v1/collectors/{id}                             one collector and its history
//   GET    /api/v1/collectors/{id}/configs/{version}           one version, masked ("latest" for the newest)
//   DELETE /api/v1/collectors/{id}                             remove an offline collector and its history
//   GET    /api/v1/collector-teams                             teams the caller sees here, and which it manages
//   GET    /api/v1/settings/groups/{id}/collector-tokens                a team's tokens
//   POST   /api/v1/settings/groups/{id}/collector-tokens                mint (plaintext returned once)
//   POST   /api/v1/settings/groups/{id}/collector-tokens/{token}/regenerate
//   DELETE /api/v1/settings/groups/{id}/collector-tokens/{token}       revoke
//
// Only mounted when the cell turns the feature on
// (SLUICIO_COLLECTORS_ENABLED); otherwise none of this exists. The OpAMP
// endpoint itself is mounted in cmd/cell-api, outside the session stack.
//
// Who sees what: a team's members read its collectors; an org admin reads
// every team's. A collector's configuration can carry the customer's
// internal endpoints, so this is a secrecy boundary and fails closed: a
// membership lookup that fails reads as no teams. Tokens are managed by
// the team's admins and by org admins, because a token decides which team
// a collector lands in.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/license"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// collectorHistoryLimit bounds the history in a detail response.
const collectorHistoryLimit = 100

// otelflowURL is the embed base when collector management is on and the
// cell serves OTelFlow; empty otherwise.
func (h *Handlers) otelflowURL() string {
	if h.Collectors == nil {
		return ""
	}
	return h.OtelflowURL
}

// collectorScope is what the caller may read.
func (h *Handlers) collectorScope(r *http.Request) collectors.Scope {
	p := middleware.Principal(r)
	scope := collectors.Scope{OrgID: p.OrgID}
	if p.ReadRole().CanAdmin() {
		scope.All = true
		return scope
	}
	for id := range h.callerGroupRoles(r) {
		scope.Groups = append(scope.Groups, id)
	}
	return scope
}

// canManageCollectorTokens: the team's admins and org admins. A token
// scoped to read cannot mint, whatever its owner's team role: a minted
// collector token is a write into the team.
//
// roles is the caller's team roles when the caller already has them.
func (h *Handlers) canManageCollectorTokens(r *http.Request, groupID uuid.UUID, roles map[uuid.UUID]identity.Role) bool {
	p := middleware.Principal(r)
	if p.TokenScope.IsValid() && !p.TokenScope.CanWrite() {
		return false
	}
	if p.Role.CanAdmin() {
		return true
	}
	if roles == nil {
		roles = h.callerGroupRoles(r)
	}
	return roles[groupID] == identity.RoleAdmin
}

type collectorsListResponse struct {
	Collectors []collectors.Collector `json:"collectors"`
	// ConfigStorage is false when the cell has no
	// SLUICIO_COLLECTOR_CONFIG_KEY: collectors register, configurations
	// are not stored.
	ConfigStorage bool `json:"config_storage"`
}

// listCollectors: GET /api/v1/collectors
func (h *Handlers) listCollectors(w http.ResponseWriter, r *http.Request) {
	list, err := h.Collectors.List(r.Context(), h.collectorScope(r))
	if err != nil {
		h.Logger.Error("list collectors failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, collectorsListResponse{Collectors: list, ConfigStorage: h.Collectors.ConfigStorage()})
}

type collectorDetailResponse struct {
	Collector     collectors.Collector       `json:"collector"`
	History       []collectors.ConfigVersion `json:"history"`
	ConfigStorage bool                       `json:"config_storage"`
	// CanManage: the caller may remove this collector (its team's admins
	// and org admins). A hint for the UI; the DELETE route checks again.
	CanManage bool `json:"can_manage"`
	// CanPush: the caller may send this collector a configuration (owner
	// team editors and admins, org admins) and the collector takes one.
	CanPush bool `json:"can_push"`
	// RequireApproval: a change to this collector waits for approval
	// before it is sent (Enterprise, the team's setting).
	RequireApproval bool `json:"require_approval"`
}

func (h *Handlers) collectorFromPath(w http.ResponseWriter, r *http.Request) (collectors.Collector, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid collector id")
		return collectors.Collector{}, false
	}
	c, err := h.Collectors.Get(r.Context(), h.collectorScope(r), id)
	if errors.Is(err, collectors.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "collector not found")
		return collectors.Collector{}, false
	}
	if err != nil {
		h.Logger.Error("get collector failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return collectors.Collector{}, false
	}
	return c, true
}

// getCollector: GET /api/v1/collectors/{id}
func (h *Handlers) getCollector(w http.ResponseWriter, r *http.Request) {
	c, ok := h.collectorFromPath(w, r)
	if !ok {
		return
	}
	hist, err := h.Collectors.History(r.Context(), c.ID, collectorHistoryLimit)
	if err != nil {
		h.Logger.Error("collector history failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, collectorDetailResponse{
		Collector: c, History: hist, ConfigStorage: h.Collectors.ConfigStorage(),
		CanManage: h.canManageCollectorTokens(r, c.GroupID, nil),
		CanPush:   c.Kind == collectors.KindSupervisor && h.canPushCollector(r, c.GroupID),
		RequireApproval: func() bool {
			if !h.featureEntitled(license.FeatureAdvisor) {
				return false
			}
			v, _ := h.Collectors.RequireApproval(r.Context(), c.GroupID)
			return v
		}(),
	})
}

// deleteCollector: DELETE /api/v1/collectors/{id}
//
// Removes a collector the agent is no longer behind (a host gone, an
// extension restarted without a fixed instance_uid) and its configuration
// history, so a team can be cleaned up and, once empty, deleted. Managed
// like the team's tokens. An online collector is refused: it would be
// back with its next message; revoke its token first.
func (h *Handlers) deleteCollector(w http.ResponseWriter, r *http.Request) {
	c, ok := h.collectorFromPath(w, r)
	if !ok {
		return
	}
	if !h.canManageCollectorTokens(r, c.GroupID, nil) {
		httpserver.WriteError(w, http.StatusForbidden, "only the team's admins and org admins can remove a collector")
		return
	}
	if c.Online {
		httpserver.WriteError(w, http.StatusConflict, "the collector is online and would register again; stop it or revoke its token first")
		return
	}
	if err := h.Collectors.Delete(r.Context(), h.collectorScope(r), c.ID); err != nil {
		if errors.Is(err, collectors.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "collector not found")
			return
		}
		h.Logger.Error("delete collector failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "collector.deleted", "collector", c.ID.String(), map[string]any{"group_id": c.GroupID.String(), "instance_uid": c.InstanceUID})
	w.WriteHeader(http.StatusNoContent)
}

type collectorConfigResponse struct {
	Version collectors.ConfigVersion `json:"version"`
	Files   []collectors.MaskedFile  `json:"files"`
}

// getCollectorConfig: GET /api/v1/collectors/{id}/configs/{version}
//
// Always masked. There is no unmasked read in stage 1: nothing needs the
// plaintext until a configuration is pushed back to the collector.
func (h *Handlers) getCollectorConfig(w http.ResponseWriter, r *http.Request) {
	c, ok := h.collectorFromPath(w, r)
	if !ok {
		return
	}
	var version *uuid.UUID
	if v := r.PathValue("version"); v != "latest" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid version id")
			return
		}
		version = &id
	}
	v, files, err := h.Collectors.MaskedConfig(r.Context(), c.ID, version)
	switch {
	case errors.Is(err, collectors.ErrNoKey):
		httpserver.WriteError(w, http.StatusConflict, "configurations are not stored on this cell: SLUICIO_COLLECTOR_CONFIG_KEY is not set")
		return
	case errors.Is(err, collectors.ErrNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "no stored configuration")
		return
	case err != nil:
		h.Logger.Error("collector config failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not read the stored configuration")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, collectorConfigResponse{Version: v, Files: files})
}

type collectorTeam struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CanManage bool      `json:"can_manage"`
}

// listCollectorTeams: GET /api/v1/collector-teams. The teams the page
// groups collectors under, including teams that have none yet, so a team
// can mint its first token.
func (h *Handlers) listCollectorTeams(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	groups, err := h.Identity.ListGroups(r.Context(), p.OrgID)
	if err != nil {
		h.Logger.Error("list teams failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	all := p.ReadRole().CanAdmin()
	roles := h.callerGroupRoles(r)
	out := []collectorTeam{}
	for _, g := range groups {
		if _, member := roles[g.ID]; !all && !member {
			continue
		}
		out = append(out, collectorTeam{ID: g.ID, Name: g.Name, CanManage: h.canManageCollectorTokens(r, g.ID, roles)})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"teams": out})
}

// teamForTokens parses the team and checks the caller manages it. A team
// the caller cannot manage is a 404, like a team that does not exist.
func (h *Handlers) teamForTokens(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid team id")
		return uuid.Nil, false
	}
	if !h.canManageCollectorTokens(r, id, nil) {
		httpserver.WriteError(w, http.StatusNotFound, "team not found")
		return uuid.Nil, false
	}
	return id, true
}

// listCollectorTokens: GET /api/v1/settings/groups/{id}/collector-tokens
func (h *Handlers) listCollectorTokens(w http.ResponseWriter, r *http.Request) {
	groupID, ok := h.teamForTokens(w, r)
	if !ok {
		return
	}
	tokens, err := h.Collectors.ListTokens(r.Context(), middleware.OrgID(r), groupID)
	if err != nil {
		h.Logger.Error("list collector tokens failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

// createCollectorToken: POST /api/v1/settings/groups/{id}/collector-tokens
func (h *Handlers) createCollectorToken(w http.ResponseWriter, r *http.Request) {
	groupID, ok := h.teamForTokens(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	var createdBy *uuid.UUID
	if p, ok := middleware.PrincipalFromContext(r.Context()); ok {
		createdBy = p.UserID
	}
	full, t, err := h.Collectors.CreateToken(r.Context(), middleware.OrgID(r), groupID, body.Name, createdBy)
	if errors.Is(err, collectors.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "team not found")
		return
	}
	if err != nil {
		h.Logger.Error("create collector token failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.recordAudit(r, "collector_token.created", "collector_token", t.ID.String(), map[string]any{"name": t.Name, "group_id": groupID.String()})
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{"token": full, "meta": t})
}

func tokenIDFromPath(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("token"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid token id")
		return uuid.Nil, false
	}
	return id, true
}

// regenerateCollectorToken: POST .../collector-tokens/{token}/regenerate.
// The old secret stops working now, and connections made with it close.
func (h *Handlers) regenerateCollectorToken(w http.ResponseWriter, r *http.Request) {
	groupID, ok := h.teamForTokens(w, r)
	if !ok {
		return
	}
	id, ok := tokenIDFromPath(w, r)
	if !ok {
		return
	}
	full, t, err := h.Collectors.RegenerateToken(r.Context(), middleware.OrgID(r), groupID, id)
	if errors.Is(err, collectors.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "token not found")
		return
	}
	if err != nil {
		h.Logger.Error("regenerate collector token failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "regenerate failed")
		return
	}
	if h.OpAMP != nil {
		h.OpAMP.DisconnectToken(id)
	}
	h.recordAudit(r, "collector_token.regenerated", "collector_token", id.String(), map[string]any{"group_id": groupID.String()})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"token": full, "meta": t})
}

// revokeCollectorToken: DELETE .../collector-tokens/{token}
func (h *Handlers) revokeCollectorToken(w http.ResponseWriter, r *http.Request) {
	groupID, ok := h.teamForTokens(w, r)
	if !ok {
		return
	}
	id, ok := tokenIDFromPath(w, r)
	if !ok {
		return
	}
	if err := h.Collectors.RevokeToken(r.Context(), middleware.OrgID(r), groupID, id); err != nil {
		if errors.Is(err, collectors.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "token not found")
			return
		}
		h.Logger.Error("revoke collector token failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	if h.OpAMP != nil {
		h.OpAMP.DisconnectToken(id)
	}
	h.recordAudit(r, "collector_token.revoked", "collector_token", id.String(), map[string]any{"group_id": groupID.String()})
	w.WriteHeader(http.StatusNoContent)
}
