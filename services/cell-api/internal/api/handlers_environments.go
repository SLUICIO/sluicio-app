// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Environments (issue #40): where an integration's telemetry ran, read
// per environment, the environments an integration declares, and the
// cell setting that says where a row's environment comes from.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/envrole"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/settings"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// IntegrationEnvironment is one environment of one integration: its
// status and traffic, and whether logs or metrics came from it too.
type IntegrationEnvironment struct {
	EnvironmentStatus
	LogRecords uint64 `json:"log_records"`
	MetricRows uint64 `json:"metric_rows"`
}

// integrationEnvironments: GET /api/v1/integrations/{id}/environments
//
// Every environment the integration's own slice has telemetry from in
// the window (traces, logs or metrics), plus the ones it declares, each
// with its status, worst first. The Overview's strip of tiles.
func (h *Handlers) integrationEnvironments(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	tr := ParseRange(r, time.Hour)
	ctx := r.Context()
	orgID := middleware.OrgID(r)
	full, err := h.Integrations.Get(ctx, orgID, id)
	if errors.Is(err, integrations.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	members, _ := h.Catalog.IntegrationServices(ctx, id)
	visible, anyVisible := h.visibleIntegrationMembers(r, id, members)
	if !anyVisible {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return
	}
	// The integration's whole slice, not narrowed by ?env: this lists
	// the environments, so it must see all of them.
	groups := AttrGroupsFromMatchers(full.Matchers, full.Integration.RuleMatch)
	slice := store.IntegrationSlice{Key: id.String(), Services: visible, Groups: groups}

	firingScopes, err := h.Alerts.FiringHealthServiceScopes(ctx, orgID)
	if err != nil {
		firingScopes = map[string]alerting.ServiceFiring{}
	}
	firingByEnv, err := h.Alerts.FiringHealthIntegrationEnvironments(ctx, orgID)
	if err != nil {
		h.Logger.Warn("firing health integration environments failed", "err", err)
		firingByEnv = map[uuid.UUID]map[string]bool{}
	}
	sum := []IntegrationSummary{{Integration: full.Integration}}
	h.fillEnvironmentStatuses(ctx, sum, []store.IntegrationSlice{slice}, firingByEnv,
		map[uuid.UUID]alerting.ServiceFiring{id: memberFiring(members, firingScopes)},
		map[uuid.UUID]bool{id: integrations.SelectsSlice(full.Matchers, full.Integration.RuleMatch)},
		tr, h.errorAcks(ctx, orgID))

	// Logs and metrics can come from an environment no trace did.
	volumes, err := h.Store.EnvironmentsIn(ctx, visible, groups, tr.From, tr.To)
	if err != nil {
		h.Logger.Warn("integration environments: volumes failed", "err", err)
	}
	byEnv := map[string]*IntegrationEnvironment{}
	out := []*IntegrationEnvironment{}
	for _, es := range sum[0].Environments {
		e := &IntegrationEnvironment{EnvironmentStatus: es}
		byEnv[es.Environment] = e
		out = append(out, e)
	}
	declared := map[string]bool{}
	for _, d := range full.Integration.DeclaredEnvironments {
		declared[d] = true
	}
	for _, v := range volumes {
		e, ok := byEnv[v.Environment]
		if !ok {
			// Metrics or logs only: active, read by the same rule as any
			// other environment.
			status := integrationRollupStatus(integrationHealth{
				Slice:             integrations.SelectsSlice(full.Matchers, full.Integration.RuleMatch),
				Active:            true,
				MemberFiring:      memberFiring(members, firingScopes),
				IntegrationFiring: alerting.FiringIn(firingByEnv, id, v.Environment),
			})
			e = &IntegrationEnvironment{EnvironmentStatus: EnvironmentStatus{
				Environment: v.Environment, Status: status, Declared: declared[v.Environment],
				Production: envrole.IsProduction(v.Environment),
			}}
			byEnv[v.Environment] = e
			out = append(out, e)
		}
		e.LogRecords, e.MetricRows = v.LogRecords, v.MetricRows
	}
	sort.SliceStable(out, func(a, b int) bool {
		if ra, rb := envStatusOrder[out[a].Status], envStatusOrder[out[b].Status]; ra != rb {
			return ra < rb
		}
		return out[a].Environment < out[b].Environment
	})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"window":       tr.Window(),
		"environments": out,
		"declared":     nonNilStrings(full.Integration.DeclaredEnvironments),
		"open_on":      h.openOn(ctx),
	})
}

// openOn is what an integration's page opens on: "production" (its
// production environments) or "all".
func (h *Handlers) openOn(ctx context.Context) string {
	v, err := h.Settings.GetTelemetryEnvironment(ctx)
	if err != nil {
		h.Logger.Warn("telemetry environment setting failed", "err", err)
	}
	return v.OpenOn
}

// EnvironmentRole is one environment's role, for the settings list and
// the pickers that offer "production environments".
type EnvironmentRole struct {
	Environment string `json:"environment"`
	Role        string `json:"role"`
	// Confirmed: an operator decided the role. Otherwise it is what the
	// name suggests, and the settings list asks for a decision.
	Confirmed bool `json:"confirmed"`
	// Seen: telemetry came from it in the window (else it is only
	// configured or declared).
	Seen bool `json:"seen"`
}

func environmentRoles(names []string, seen map[string]bool) []EnvironmentRole {
	out := make([]EnvironmentRole, 0, len(names))
	for _, n := range names {
		if n == "" {
			continue
		}
		_, confirmed := envrole.Confirmed(n)
		out = append(out, EnvironmentRole{Environment: n, Role: envrole.Of(n), Confirmed: confirmed, Seen: seen[n]})
	}
	return out
}

// putDeclaredEnvironments: PUT /api/v1/integrations/{id}/declared-environments
func (h *Handlers) putDeclaredEnvironments(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	var body struct {
		Environments []string `json:"environments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Environments) > 50 {
		httpserver.WriteError(w, http.StatusBadRequest, "at most 50 declared environments")
		return
	}
	if err := h.Integrations.SetDeclaredEnvironments(r.Context(), middleware.OrgID(r), id, body.Environments); err != nil {
		if errors.Is(err, integrations.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "integration not found")
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	full, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	h.recordAudit(r, "integration.declared_environments_updated", "integration", id.String(),
		map[string]any{"environments": full.Integration.DeclaredEnvironments})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"declared": nonNilStrings(full.Integration.DeclaredEnvironments)})
}

// listEnvironments: GET /api/v1/environments
//
// The environments the caller's visible services sent telemetry from in
// the window, for the list-wide picker. Declared environments of any
// integration are included, so a picker can select a silent one.
func (h *Handlers) listEnvironments(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, 24*time.Hour)
	ctx := r.Context()
	all, err := h.Store.ListServices(ctx, tr.From, tr.To)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	names := make([]string, 0, len(all))
	for _, s := range all {
		names = append(names, s.ServiceName)
	}
	visible, ok := h.filterVisibleMembers(r, names)
	if !ok {
		visible = nil
	}
	seen := map[string]bool{}
	sent := map[string]bool{}
	if len(visible) > 0 {
		vols, err := h.Store.EnvironmentsIn(ctx, visible, nil, tr.From, tr.To)
		if err != nil {
			h.Logger.Warn("list environments failed", "err", err)
		}
		for _, v := range vols {
			seen[v.Environment] = true
			sent[v.Environment] = true
		}
	}
	if integs, err := h.Integrations.List(ctx, middleware.OrgID(r)); err == nil {
		for _, i := range integs {
			for _, d := range i.DeclaredEnvironments {
				seen[d] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for e := range seen {
		out = append(out, e)
	}
	sort.Strings(out)
	var production []string
	for _, e := range out {
		if envrole.IsProduction(e) {
			production = append(production, e)
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"environments":     out,
		"production":       nonNilStrings(production),
		"roles":            environmentRoles(out, sent),
		"none_param":       NoEnvironmentParam,
		"production_token": envrole.ProductionToken,
		"open_on":          h.openOn(ctx),
		"window":           tr.Window(),
	})
}

// envKeyRe bounds the setting's attribute key to what attribute keys
// look like. The expression quotes it anyway; this keeps typos out.
var envKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.\-/]{1,128}$`)

// getTelemetryEnvironment: GET /api/v1/cell-settings/telemetry-environment
func (h *Handlers) getTelemetryEnvironment(w http.ResponseWriter, r *http.Request) {
	v, err := h.Settings.GetTelemetryEnvironment(r.Context())
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not load setting")
		return
	}
	if v.Aliases == nil {
		v.Aliases = map[string]string{}
	}
	if v.Roles == nil {
		v.Roles = map[string]string{}
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"key":          v.Key,
		"aliases":      v.Aliases,
		"roles":        v.Roles,
		"open_on":      v.OpenOn,
		"default_keys": []string{"deployment.environment.name", "deployment.environment"},
	})
}

// putTelemetryEnvironment: PUT /api/v1/cell-settings/telemetry-environment
// (operator). Applied at read time, so it changes what every page and
// check reads from now on, and rewrites nothing.
func (h *Handlers) putTelemetryEnvironment(w http.ResponseWriter, r *http.Request) {
	var body settings.TelemetryEnvironment
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Key = strings.TrimSpace(body.Key)
	if body.Key != "" && !envKeyRe.MatchString(body.Key) {
		httpserver.WriteError(w, http.StatusBadRequest, "key must be an attribute key: letters, digits and . _ - /")
		return
	}
	if len(body.Aliases) > 100 {
		httpserver.WriteError(w, http.StatusBadRequest, "at most 100 aliases")
		return
	}
	clean := map[string]string{}
	for from, to := range body.Aliases {
		from, to = strings.ToLower(strings.TrimSpace(from)), strings.TrimSpace(to)
		if from == "" || to == "" || len(from) > 128 || len(to) > 128 {
			httpserver.WriteError(w, http.StatusBadRequest, "an alias needs a value and a name, each at most 128 characters")
			return
		}
		clean[from] = to
	}
	body.Aliases = clean
	// Roles and open_on left out keep what is stored, so a client that
	// only sets the aliases does not reset them. v0.11.180's default
	// carries over only when neither is given.
	stored, err := h.Settings.GetTelemetryEnvironment(r.Context())
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not load setting")
		return
	}
	if body.Roles != nil || body.OpenOn != "" {
		body.Default = nil
	}
	if body.Roles == nil {
		body.Roles = stored.Roles
	}
	if body.OpenOn == "" && body.Default == nil {
		body.OpenOn = stored.OpenOn
	}
	if len(body.Roles) > 200 {
		httpserver.WriteError(w, http.StatusBadRequest, "at most 200 environment roles")
		return
	}
	roles := map[string]string{}
	for env, role := range body.Roles {
		env = strings.TrimSpace(env)
		if env == "" || len(env) > 128 || (role != envrole.Production && role != envrole.NonProduction) {
			httpserver.WriteError(w, http.StatusBadRequest, "a role is production or non-production, for an environment of at most 128 characters")
			return
		}
		roles[env] = role
	}
	body.Roles = roles
	if body.OpenOn != "" && body.OpenOn != settings.OpenOnProduction && body.OpenOn != settings.OpenOnAll {
		httpserver.WriteError(w, http.StatusBadRequest, "open_on is production or all")
		return
	}
	body = body.Normalized()
	if err := h.Settings.SetTelemetryEnvironment(r.Context(), body); err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not save setting")
		return
	}
	store.SetEnvironmentConfig(store.EnvironmentConfig{Key: body.Key, Aliases: body.Aliases})
	envrole.Set(envrole.Config{Roles: body.Roles})
	h.recordAudit(r, "cell_settings.telemetry_environment_updated", "cell_settings", "telemetry_environment",
		map[string]any{"key": body.Key, "aliases": len(body.Aliases), "roles": body.Roles, "open_on": body.OpenOn})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"key": body.Key, "aliases": body.Aliases, "roles": body.Roles, "open_on": body.OpenOn})
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
