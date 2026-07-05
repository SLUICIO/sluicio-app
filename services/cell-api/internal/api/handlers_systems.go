// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Systems — first-class entities (docs/systems.md phase 2). A system is an
// instance of a system type that spans member services. The "mark as system"
// flag on a service attaches it to a system for its kind (find-or-create), so
// the flag flow keeps the entity layer populated. Visibility: a user sees a
// system if they can see at least one of its members (or it has none).

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/catalog"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
)

// putServiceSystem: PUT /api/v1/services/{name}/system  (writer+)
// Body: { "is_system": bool, "system_kind": "rabbitmq" }. Marks/unmarks the
// service as a system; marking attaches it to a system entity for its kind.
func (h *Handlers) putServiceSystem(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	var req struct {
		IsSystem   bool   `json:"is_system"`
		SystemKind string `json:"system_kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.SystemKind))
	if !req.IsSystem {
		kind = ""
	}
	if err := h.Catalog.SetServiceSystem(r.Context(), middleware.OrgID(r), name, req.IsSystem, kind); err != nil {
		h.Logger.Error("set service system failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "service_system.set", "service", name, map[string]any{"is_system": req.IsSystem, "system_kind": kind})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"service_name": name,
		"is_system":    req.IsSystem,
		"system_kind":  kind,
	})
}

// visibleServiceChecker resolves the request's visible-service predicate once
// (admins / wildcard see all).
func (h *Handlers) visibleServiceChecker(r *http.Request) (func(string) bool, bool, error) {
	p := middleware.Principal(r)
	if p.UserID == nil || p.ReadRole().CanAdmin() {
		return func(string) bool { return true }, true, nil
	}
	visible, wildcard, err := h.Identity.ResolveVisibleServiceSet(r.Context(), *p.UserID, p.OrgID, h.integrationExpander, h.systemExpander, h.serviceUniverse)
	if err != nil {
		return nil, false, err
	}
	if wildcard {
		return func(string) bool { return true }, true, nil
	}
	return func(n string) bool { _, ok := visible[n]; return ok }, false, nil
}

type systemDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	TypeKey     string   `json:"type_key"`
	Description string   `json:"description"`
	Members     []string `json:"members"`
	MemberCount int      `json:"member_count"`
	// Status is the rollup of member health ("ok"/"errors"/"unhealthy"/"quiet").
	Status string `json:"status,omitempty"`
	// BadgePublic: this system exposes a public status badge.
	BadgePublic bool `json:"badge_public"`
}

func systemToDTO(sy catalog.System) systemDTO {
	if sy.Members == nil {
		sy.Members = []string{}
	}
	return systemDTO{
		ID: sy.ID.String(), Name: sy.Name, TypeKey: sy.TypeKey, Description: sy.Description,
		Members: sy.Members, MemberCount: len(sy.Members),
	}
}

// listSystems: GET /api/v1/systems — system entities, visibility-filtered.
func (h *Handlers) listSystems(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgID(r)
	systems, err := h.Catalog.ListSystems(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("list systems failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	canSee, all, err := h.visibleServiceChecker(r)
	if err != nil {
		h.Logger.Error("list systems: visibility failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Member health for the rollup status (serviceSummaries is visibility-safe).
	statusByName := map[string]string{}
	if summaries, serr := h.serviceSummaries(r, ParseRange(r, time.Hour)); serr == nil {
		for _, s := range summaries {
			statusByName[s.ServiceName] = s.Status
		}
	}
	out := make([]systemDTO, 0, len(systems))
	for _, sy := range systems {
		vis := make([]string, 0, len(sy.Members))
		statuses := make([]string, 0, len(sy.Members))
		for _, m := range sy.Members {
			if canSee(m) {
				vis = append(vis, m)
				if st, ok := statusByName[m]; ok {
					statuses = append(statuses, st)
				}
			}
		}
		// Hide systems whose members are all invisible (but show empty systems).
		if !all && len(sy.Members) > 0 && len(vis) == 0 {
			continue
		}
		out = append(out, systemDTO{
			ID: sy.ID.String(), Name: sy.Name, TypeKey: sy.TypeKey, Description: sy.Description,
			Members: vis, MemberCount: len(vis), Status: aggregateStatus(statuses),
		})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"systems": out})
}

// getSystem: GET /api/v1/systems/{id} — the system + its member services as
// summaries (with health), visibility-filtered.
func (h *Handlers) getSystem(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	orgID := middleware.OrgID(r)
	sy, ok, err := h.Catalog.GetSystem(r.Context(), orgID, id)
	if err != nil {
		h.Logger.Error("get system failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !ok {
		httpserver.WriteError(w, http.StatusNotFound, "system not found")
		return
	}
	canSee, all, err := h.visibleServiceChecker(r)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	memberSet := make(map[string]struct{}, len(sy.Members))
	visibleMembers := 0
	for _, m := range sy.Members {
		memberSet[m] = struct{}{}
		if canSee(m) {
			visibleMembers++
		}
	}
	if !all && len(sy.Members) > 0 && visibleMembers == 0 {
		httpserver.WriteError(w, http.StatusNotFound, "system not found")
		return
	}
	// Member health: serviceSummaries is already visibility-filtered.
	tr := ParseRange(r, time.Hour)
	summaries, err := h.serviceSummaries(r, tr)
	if err != nil {
		h.Logger.Error("get system: summaries failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	members := make([]ServiceSummary, 0, len(sy.Members))
	for _, s := range summaries {
		if _, ok := memberSet[s.ServiceName]; ok {
			members = append(members, s)
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"window":     tr.Window(),
		"can_manage": h.canManageSystem(r, id),
		"system":     systemDTO{ID: sy.ID.String(), Name: sy.Name, TypeKey: sy.TypeKey, Description: sy.Description, BadgePublic: sy.BadgePublic},
		"members":    members,
	})
}

type systemInput struct {
	Name        string `json:"name"`
	TypeKey     string `json:"type_key"`
	Description string `json:"description"`
}

// createSystem: POST /api/v1/systems  (writer+)
func (h *Handlers) createSystem(w http.ResponseWriter, r *http.Request) {
	var in systemInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	sy, err := h.Catalog.CreateSystem(r.Context(), middleware.OrgID(r), name, strings.ToLower(strings.TrimSpace(in.TypeKey)), strings.TrimSpace(in.Description))
	if err != nil {
		if isUniqueViolation(err) {
			httpserver.WriteError(w, http.StatusConflict, "a system with that name already exists")
			return
		}
		h.Logger.Error("create system failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.recordAudit(r, "system.created", "system", sy.ID.String(), map[string]any{"name": sy.Name, "type_key": sy.TypeKey})
	httpserver.WriteJSON(w, http.StatusCreated, systemToDTO(sy))
}

// updateSystem: PUT /api/v1/systems/{id}  (writer+)
func (h *Handlers) updateSystem(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in systemInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	sy, ok, err := h.Catalog.UpdateSystem(r.Context(), middleware.OrgID(r), id, name, strings.ToLower(strings.TrimSpace(in.TypeKey)), strings.TrimSpace(in.Description))
	if err != nil {
		if isUniqueViolation(err) {
			httpserver.WriteError(w, http.StatusConflict, "a system with that name already exists")
			return
		}
		h.Logger.Error("update system failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !ok {
		httpserver.WriteError(w, http.StatusNotFound, "system not found")
		return
	}
	h.recordAudit(r, "system.updated", "system", sy.ID.String(), map[string]any{"name": sy.Name})
	httpserver.WriteJSON(w, http.StatusOK, systemToDTO(sy))
}

// deleteSystem: DELETE /api/v1/systems/{id}  (writer+)
func (h *Handlers) deleteSystem(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Catalog.DeleteSystem(r.Context(), middleware.OrgID(r), id); err != nil {
		if err == catalog.ErrSystemNotFound {
			httpserver.WriteError(w, http.StatusNotFound, "system not found")
			return
		}
		h.Logger.Error("delete system failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	// Shares point at this system without an FK — clear them.
	if err := h.Identity.DeleteSharesForResource(r.Context(), middleware.OrgID(r), identity.ShareSystem, id); err != nil {
		h.Logger.Warn("delete system: clear shares failed", "err", err)
	}
	h.recordAudit(r, "system.deleted", "system", id.String(), nil)
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// attachSystemService: POST /api/v1/systems/{id}/services  (writer+)
// Body: { "service_name": "..." }
func (h *Handlers) attachSystemService(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		ServiceName string `json:"service_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	name := strings.TrimSpace(req.ServiceName)
	// Scoped manage: attaching pulls a service into this system's blast
	// radius — the service itself must be in the caller's managed set.
	if name != "" && !h.canManageService(r, name) {
		httpserver.WriteError(w, http.StatusForbidden, "that service is outside your managed scope")
		return
	}
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service_name is required")
		return
	}
	if !h.canSeeService(r, name) {
		httpserver.WriteError(w, http.StatusForbidden, "not allowed")
		return
	}
	ok, err := h.Catalog.AttachService(r.Context(), middleware.OrgID(r), id, name)
	if err != nil {
		h.Logger.Error("attach service failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "attach failed")
		return
	}
	if !ok {
		httpserver.WriteError(w, http.StatusNotFound, "system or service not found")
		return
	}
	h.recordAudit(r, "system_service.attached", "system", id.String(), map[string]any{"service_name": name})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"attached": true})
}

// detachSystemService: DELETE /api/v1/systems/{id}/services/{name}  (writer+)
func (h *Handlers) detachSystemService(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	ok, err := h.Catalog.DetachService(r.Context(), middleware.OrgID(r), id, name)
	if err != nil {
		h.Logger.Error("detach service failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "detach failed")
		return
	}
	if !ok {
		httpserver.WriteError(w, http.StatusNotFound, "service not a member of this system")
		return
	}
	h.recordAudit(r, "system_service.detached", "system", id.String(), map[string]any{"service_name": name})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"detached": true})
}

// canSeeSystem: a user can see a system if it has a visible member (or none).
func (h *Handlers) canSeeSystem(r *http.Request, sy catalog.System) bool {
	canSee, all, err := h.visibleServiceChecker(r)
	if err != nil {
		return false
	}
	if all || len(sy.Members) == 0 {
		return true
	}
	for _, m := range sy.Members {
		if canSee(m) {
			return true
		}
	}
	return false
}

// getSystemMetadata: GET /api/v1/systems/{id}/metadata — the fields that apply
// to this system's type + its saved values (keyed by field key).
func (h *Handlers) getSystemMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	orgID := middleware.OrgID(r)
	sy, ok, err := h.Catalog.GetSystem(r.Context(), orgID, id)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !ok || !h.canSeeSystem(r, sy) {
		httpserver.WriteError(w, http.StatusNotFound, "system not found")
		return
	}
	fields, err := h.Metadata.SystemFields(r.Context(), orgID, sy.TypeKey)
	if err != nil {
		h.Logger.Error("system metadata: fields failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	vals, err := h.Metadata.SystemValues(r.Context(), id)
	if err != nil {
		h.Logger.Error("system metadata: values failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"fields":          fields,
		"metadata_values": remapByKey(fields, vals),
	})
}

// putSystemMetadata: PUT /api/v1/systems/{id}/metadata  (writer+)
func (h *Handlers) putSystemMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	orgID := middleware.OrgID(r)
	sy, ok, err := h.Catalog.GetSystem(r.Context(), orgID, id)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !ok || !h.canSeeSystem(r, sy) {
		httpserver.WriteError(w, http.StatusNotFound, "system not found")
		return
	}
	values, err := decodeValueMap(r)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Metadata.SetSystemValues(r.Context(), orgID, id, sy.TypeKey, values); err != nil {
		if writeMetadataValidationError(w, err) {
			return
		}
		h.Logger.Error("set system metadata failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "system_metadata.updated", "system", id.String(), nil)
	fields, _ := h.Metadata.SystemFields(r.Context(), orgID, sy.TypeKey)
	vals, _ := h.Metadata.SystemValues(r.Context(), id)
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"fields":          fields,
		"metadata_values": remapByKey(fields, vals),
	})
}

// applySystemTemplateAll: POST /api/v1/systems/{id}/apply-template  (writer+)
// Applies the system type's starter checks to every member service. Body:
// { channel_ids?: [] }. A system-level convenience over the per-service apply.
func (h *Handlers) applySystemTemplateAll(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	orgID := middleware.OrgID(r)
	sy, ok, err := h.Catalog.GetSystem(r.Context(), orgID, id)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !ok || !h.canSeeSystem(r, sy) {
		httpserver.WriteError(w, http.StatusNotFound, "system not found")
		return
	}
	var body struct {
		ChannelIDs []string `json:"channel_ids"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	channels := parseChannelIDList(body.ChannelIDs)

	tmpl, has, terr := h.templateByKind(r.Context(), orgID, sy.TypeKey)
	if terr != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !has || len(tmpl.Checks) == 0 {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{
			"type_key": sy.TypeKey, "members": 0, "created": 0, "updated": 0, "skipped": 0,
			"message": "no template for this system's type",
		})
		return
	}
	created, updated, skipped, applied := 0, 0, 0, 0
	for _, m := range sy.Members {
		if !h.canSeeService(r, m) {
			continue
		}
		c, u, s, aerr := h.createTemplateChecks(r, orgID, m, tmpl.Checks, channels)
		if aerr != nil {
			h.Logger.Error("apply system template: member failed", "err", aerr, "service", m)
			continue
		}
		created, updated, skipped, applied = created+c, updated+u, skipped+s, applied+1
	}
	h.recordAudit(r, "system_template.applied", "system", id.String(), map[string]any{"type_key": sy.TypeKey})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"type_key": sy.TypeKey, "members": applied,
		"created": created, "updated": updated, "skipped": skipped,
	})
}
