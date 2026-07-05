// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Scoped-manage helpers (RBAC v2 phase 2, docs/rbac-v2-design.md §5).
//
// The capability model: org editors/admins manage everything (the outer
// ceiling, unchanged); an org-viewer who is EDITOR in a group manages
// exactly the services that group's policies grant — nothing else.
// Scope-capped tokens never escalate through group roles (same invariant
// as RequireWriteAnywhere).
//
// Containment (spec §5.3): a resource that spans services (integration,
// system) is manageable only when ALL its current member services are in
// the caller's managed set — someone who can't see a resource's full
// blast radius can't change it. Integrations are matcher-defined, so the
// check is against the services the matchers resolve to right now; if a
// pattern later swallows an out-of-scope service, manage is lost until
// an org admin intervenes (fail toward less power).

package api

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/pkg/license"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/integrations"
)

// managedServiceFilter resolves the caller's MANAGED set. Returns
// (nil, false) when unrestricted (org editor/admin — manage everything),
// (set, true) when group-scoped. Scope-capped tokens and service
// accounts never gain from group roles: capped → role decides alone.
func (h *Handlers) managedServiceFilter(r *http.Request) (map[string]struct{}, bool) {
	p := middleware.Principal(r)
	if p.UserID == nil {
		// Service accounts / no-auth: org role is the whole story.
		return nil, false
	}
	if p.Role.CanWrite() {
		return nil, false // org editor/admin — unrestricted manage
	}
	if p.ScopeCapped() {
		// A capped token must not escalate via group-editor roles.
		return map[string]struct{}{}, true
	}
	if !h.featureEntitled(license.FeatureRBACAdvanced) {
		// Scoped manage is Enterprise (spec §3): in CE a group role above
		// viewer has no effect — org role is the whole story.
		return map[string]struct{}{}, true
	}
	sets, err := h.Identity.ResolveAccessSets(r.Context(), *p.UserID, p.OrgID, h.integrationExpander, h.systemExpander, h.serviceUniverse)
	if err != nil {
		// Fail toward less power on resolution errors.
		h.Logger.Warn("managed set resolve failed; denying manage", "err", err)
		return map[string]struct{}{}, true
	}
	if sets.ManagedAll {
		return nil, false
	}
	return sets.Managed, true
}

// canManageService reports whether the caller may mutate the named
// service's configuration.
func (h *Handlers) canManageService(r *http.Request, serviceName string) bool {
	if h.AuthMW == nil {
		return true // no-auth dev mode
	}
	managed, restricted := h.managedServiceFilter(r)
	if !restricted {
		return true
	}
	_, ok := managed[serviceName]
	return ok
}

// canManageAllServices reports whether every name is in the caller's
// managed set (vacuously true for an empty list — creation starts empty,
// and an empty resource has no blast radius).
func (h *Handlers) canManageAllServices(r *http.Request, names []string) bool {
	if h.AuthMW == nil {
		return true
	}
	managed, restricted := h.managedServiceFilter(r)
	if !restricted {
		return true
	}
	for _, n := range names {
		if _, ok := managed[n]; !ok {
			return false
		}
	}
	return true
}

// requireManageService wraps a per-service {name} write handler:
// 403 unless the caller manages the service. Layered UNDER
// gateServiceRoute (visibility 404s first, so out-of-scope names stay
// indistinguishable from nonexistent ones).
func (h *Handlers) requireManageService(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name != "" && !h.canManageService(r, name) {
			httpserver.WriteError(w, http.StatusForbidden, "you don't manage this service")
			return
		}
		next.ServeHTTP(w, r)
	}
}

// integrationMemberServices resolves the services an integration's
// matchers currently match (the containment subject).
func (h *Handlers) integrationMemberServices(r *http.Request, integrationID uuid.UUID) ([]string, error) {
	return h.integrationExpander(r.Context(), middleware.OrgID(r), integrationID)
}

// canManageIntegration: org write role, or ALL currently-matched member
// services in the managed set.
func (h *Handlers) canManageIntegration(r *http.Request, integrationID uuid.UUID) bool {
	if h.AuthMW == nil {
		return true
	}
	managed, restricted := h.managedServiceFilter(r)
	if !restricted {
		return true
	}
	members, err := h.integrationMemberServices(r, integrationID)
	if err != nil {
		h.Logger.Warn("integration containment resolve failed; denying", "err", err)
		return false
	}
	for _, m := range members {
		if _, ok := managed[m]; !ok {
			return false
		}
	}
	return true
}

// canManageSystem: org write role, or ALL member services in the managed set.
func (h *Handlers) canManageSystem(r *http.Request, systemID uuid.UUID) bool {
	if h.AuthMW == nil {
		return true
	}
	managed, restricted := h.managedServiceFilter(r)
	if !restricted {
		return true
	}
	members, err := h.Catalog.SystemMemberNames(r.Context(), middleware.OrgID(r), systemID)
	if err != nil {
		h.Logger.Warn("system containment resolve failed; denying", "err", err)
		return false
	}
	for _, m := range members {
		if _, ok := managed[m]; !ok {
			return false
		}
	}
	return true
}

// requireManageIntegration / requireManageSystem wrap {id}-routed write
// handlers with the containment check. Both assume RequireWriteAnywhere
// already ran (caller is SOME kind of editor); they add the "which
// scope" narrowing.
func (h *Handlers) requireManageIntegration(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err == nil && !h.canManageIntegration(r, id) {
			httpserver.WriteError(w, http.StatusForbidden, "this integration spans services outside your managed scope")
			return
		}
		next.ServeHTTP(w, r)
	}
}

func (h *Handlers) requireManageSystem(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err == nil && !h.canManageSystem(r, id) {
			httpserver.WriteError(w, http.StatusForbidden, "this system spans services outside your managed scope")
			return
		}
		next.ServeHTTP(w, r)
	}
}

// matcherContainmentOK vets a prospective matcher set for a restricted
// caller (spec §5.3): service-name matchers must resolve — against the
// org's current catalog — only to managed services. Attribute matchers
// (non-service.name) can't be cheaply resolved before ingest, so a
// group-scoped editor may not create them at all: fail toward less
// power, org editors unaffected.
func (h *Handlers) matcherContainmentOK(r *http.Request, ms []integrations.Matcher) (bool, string) {
	if h.AuthMW == nil {
		return true, ""
	}
	managed, restricted := h.managedServiceFilter(r)
	if !restricted {
		return true, ""
	}
	names, err := h.serviceUniverse(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("matcher containment: universe resolve failed; denying", "err", err)
		return false, "could not resolve your managed scope"
	}
	for _, m := range ms {
		if !m.IsServiceMatcher() {
			return false, "attribute matchers require an org-wide editor role"
		}
		for _, n := range names {
			if m.Match(n) {
				if _, ok := managed[n]; !ok {
					return false, fmt.Sprintf("matcher would include service %q, which is outside your managed scope", n)
				}
			}
		}
	}
	return true, ""
}

// meAccess: GET /api/v1/me/access — the frontend's scoped-capability
// mirror: what may this session manage? Read-only convenience; the
// server-side gates are authoritative.
func (h *Handlers) meAccess(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	type editorGroup struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	type resp struct {
		WriteAnywhere   bool          `json:"write_anywhere"`
		ManageAll       bool          `json:"manage_all"`
		ManagedServices []string      `json:"managed_services"`
		EditorGroups    []editorGroup `json:"editor_groups"`
	}
	out := resp{ManagedServices: []string{}, EditorGroups: []editorGroup{}}
	fillEditorGroups := func() {
		if p.UserID == nil {
			return
		}
		groups, err := h.Identity.ListUserGroups(r.Context(), *p.UserID, p.OrgID)
		if err != nil {
			return
		}
		for _, g := range groups {
			if !g.Role.CanWrite() {
				continue
			}
			if full, err := h.Identity.GetGroup(r.Context(), p.OrgID, g.GroupID); err == nil {
				out.EditorGroups = append(out.EditorGroups, editorGroup{ID: full.ID.String(), Slug: full.Slug, Name: full.Name})
			}
		}
	}
	if p.Role.CanWrite() {
		out.WriteAnywhere, out.ManageAll = true, true
		if h.featureEntitled(license.FeatureRBACAdvanced) {
			fillEditorGroups()
		}
		httpserver.WriteJSON(w, http.StatusOK, out)
		return
	}
	if p.UserID == nil || p.ScopeCapped() || !h.featureEntitled(license.FeatureRBACAdvanced) {
		// CE: group roles grant no capability — nothing beyond the org role.
		httpserver.WriteJSON(w, http.StatusOK, out)
		return
	}
	fillEditorGroups()
	sets, err := h.Identity.ResolveAccessSets(r.Context(), *p.UserID, p.OrgID, h.integrationExpander, h.systemExpander, h.serviceUniverse)
	if err != nil {
		h.Logger.Warn("me/access resolve failed", "err", err)
		httpserver.WriteJSON(w, http.StatusOK, out)
		return
	}
	out.ManageAll = sets.ManagedAll
	for name := range sets.Managed {
		out.ManagedServices = append(out.ManagedServices, name)
	}
	// write_anywhere = may create scoped resources at all (org write OR
	// editor in any group — mirrors RequireWriteAnywhere).
	if ok, err := h.Identity.CanUserWriteAnywhere(r.Context(), *p.UserID, p.OrgID); err == nil {
		out.WriteAnywhere = ok
	}
	httpserver.WriteJSON(w, http.StatusOK, out)
}
