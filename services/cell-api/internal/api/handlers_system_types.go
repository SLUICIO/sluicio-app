// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The system-types catalog: the managed list of system kinds (rabbitmq, kafka,
// otel-collector, …) and the detection prefixes + starter checks each owns.
// Built-in types stay code-defined (the monitoringTemplates slice) and
// read-only; an org may add CUSTOM types and OVERRIDE a built-in by reusing its
// key. The "effective" catalog an org sees — and that detection / apply read —
// is the built-ins with org rows merged on top.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/monitoringtemplates"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/systemtypes"
	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation reports whether err is a Postgres unique-constraint error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// systemTypeToTemplate maps a stored system type to the in-memory
// monitoringTemplate the detection + apply paths consume.
func systemTypeToTemplate(st systemtypes.SystemType) monitoringTemplate {
	checks := make([]systemCheck, len(st.Checks))
	for i, c := range st.Checks {
		checks[i] = customCheckToSystemCheck(c)
	}
	prefixes := st.DetectPrefixes
	if prefixes == nil {
		prefixes = []string{}
	}
	return monitoringTemplate{
		Kind:           st.Key,
		Label:          st.Label,
		System:         st.IsSystem,
		DetectPrefixes: prefixes,
		Checks:         checks,
	}
}

// effectiveType is one entry in the merged catalog: the resolved template plus
// whether it's a read-only built-in and (for org rows) the editable row id.
type effectiveType struct {
	Template monitoringTemplate
	BuiltIn  bool
	ID       uuid.UUID // zero for a pure built-in
}

// mergedSystemTypes returns the built-in catalog with the org's custom types
// and overrides merged on top (an org row with a built-in's key replaces it).
func (h *Handlers) mergedSystemTypes(ctx context.Context, orgID uuid.UUID) ([]effectiveType, error) {
	byKey := make(map[string]effectiveType)
	order := make([]string, 0, len(monitoringTemplates))
	for _, t := range monitoringTemplates {
		byKey[t.Kind] = effectiveType{Template: t, BuiltIn: true}
		order = append(order, t.Kind)
	}
	if h.SystemTypes != nil {
		rows, err := h.SystemTypes.List(ctx, orgID)
		if err != nil {
			return nil, err
		}
		for _, st := range rows {
			if _, exists := byKey[st.Key]; !exists {
				order = append(order, st.Key)
			}
			byKey[st.Key] = effectiveType{Template: systemTypeToTemplate(st), BuiltIn: false, ID: st.ID}
		}
	}
	out := make([]effectiveType, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out, nil
}

// effectiveTemplates is mergedSystemTypes reduced to the templates the engine
// needs (detection + apply).
func (h *Handlers) effectiveTemplates(ctx context.Context, orgID uuid.UUID) ([]monitoringTemplate, error) {
	merged, err := h.mergedSystemTypes(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]monitoringTemplate, len(merged))
	for i, m := range merged {
		out[i] = m.Template
	}
	return out, nil
}

// templateByKind resolves one kind from the org's effective catalog.
func (h *Handlers) templateByKind(ctx context.Context, orgID uuid.UUID, kind string) (monitoringTemplate, bool, error) {
	tmpls, err := h.effectiveTemplates(ctx, orgID)
	if err != nil {
		return monitoringTemplate{}, false, err
	}
	for _, t := range tmpls {
		if t.Kind == kind {
			return t, true, nil
		}
	}
	return monitoringTemplate{}, false, nil
}

// ── DTO ──────────────────────────────────────────────────────────────

type systemTypeDTO struct {
	ID             string                      `json:"id,omitempty"` // "" = pure built-in (read-only)
	Key            string                      `json:"key"`
	Label          string                      `json:"label"`
	IsSystem       bool                        `json:"is_system"`
	DetectPrefixes []string                    `json:"detect_prefixes"`
	Checks         []monitoringtemplates.Check `json:"checks"`
	BuiltIn        bool                        `json:"built_in"`
}

func effectiveToDTO(e effectiveType) systemTypeDTO {
	checks := make([]monitoringtemplates.Check, len(e.Template.Checks))
	for i, c := range e.Template.Checks {
		checks[i] = systemCheckToCustom(c)
	}
	prefixes := e.Template.DetectPrefixes
	if prefixes == nil {
		prefixes = []string{}
	}
	id := ""
	if e.ID != uuid.Nil {
		id = e.ID.String()
	}
	return systemTypeDTO{
		ID:             id,
		Key:            e.Template.Kind,
		Label:          e.Template.Label,
		IsSystem:       e.Template.System,
		DetectPrefixes: prefixes,
		Checks:         checks,
		BuiltIn:        e.BuiltIn,
	}
}

// ── handlers ─────────────────────────────────────────────────────────

// listSystemTypes: GET /api/v1/system-types — the effective catalog (built-ins
// + org custom/overrides), each flagged built_in (read-only) or with an id.
func (h *Handlers) listSystemTypes(w http.ResponseWriter, r *http.Request) {
	merged, err := h.mergedSystemTypes(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list system types failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]systemTypeDTO, len(merged))
	for i, e := range merged {
		out[i] = effectiveToDTO(e)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"system_types": out})
}

type systemTypeInput struct {
	Key            string                      `json:"key"`
	Label          string                      `json:"label"`
	IsSystem       bool                        `json:"is_system"`
	DetectPrefixes []string                    `json:"detect_prefixes"`
	Checks         []monitoringtemplates.Check `json:"checks"`
}

func cleanPrefixes(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// createSystemType: POST /api/v1/system-types (writer+)
func (h *Handlers) createSystemType(w http.ResponseWriter, r *http.Request) {
	var in systemTypeInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	key := strings.ToLower(strings.TrimSpace(in.Key))
	label := strings.TrimSpace(in.Label)
	if key == "" || label == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "key and label are required")
		return
	}
	st, err := h.SystemTypes.Create(r.Context(), middleware.OrgID(r), key, label, in.IsSystem, cleanPrefixes(in.DetectPrefixes), in.Checks)
	if err != nil {
		if isUniqueViolation(err) {
			httpserver.WriteError(w, http.StatusConflict, "a system type with that key already exists")
			return
		}
		h.Logger.Error("create system type failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.recordAudit(r, "system_type.created", "system_type", st.ID.String(), map[string]any{"key": st.Key})
	httpserver.WriteJSON(w, http.StatusCreated, effectiveToDTO(effectiveType{Template: systemTypeToTemplate(st), ID: st.ID}))
}

// updateSystemType: PUT /api/v1/system-types/{id} (writer+)
func (h *Handlers) updateSystemType(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in systemTypeInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "label is required")
		return
	}
	st, ok, err := h.SystemTypes.Update(r.Context(), middleware.OrgID(r), id, label, in.IsSystem, cleanPrefixes(in.DetectPrefixes), in.Checks)
	if err != nil {
		h.Logger.Error("update system type failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !ok {
		httpserver.WriteError(w, http.StatusNotFound, "system type not found")
		return
	}
	h.recordAudit(r, "system_type.updated", "system_type", st.ID.String(), map[string]any{"key": st.Key})
	httpserver.WriteJSON(w, http.StatusOK, effectiveToDTO(effectiveType{Template: systemTypeToTemplate(st), ID: st.ID}))
}

// deleteSystemType: DELETE /api/v1/system-types/{id} (writer+)
func (h *Handlers) deleteSystemType(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.SystemTypes.Delete(r.Context(), middleware.OrgID(r), id); err != nil {
		if err == systemtypes.ErrNotFound {
			httpserver.WriteError(w, http.StatusNotFound, "system type not found")
			return
		}
		h.Logger.Error("delete system type failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "system_type.deleted", "system_type", id.String(), nil)
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
