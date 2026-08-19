// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The collector a generated snippet targets (issue #16).
//
// Read by anyone who can see a snippet, because the snippet is useless
// without knowing what it is for. Written by an admin, because it
// changes what every generated config in the org says.
//
// Two levels, because a snippet always targets ONE service's pipeline.
// An estate running a newer collector on one host than another would
// otherwise get correct YAML for some services and YAML that refuses to
// start for others, with no way to say so. The org default is what
// almost everyone needs; the per-service override is what makes it
// honest.

package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectorversion"
)

// CollectorTargetResponse is the effective target plus enough context to
// explain it.
type CollectorTargetResponse struct {
	Version      string `json:"version"`
	Distribution string `json:"distribution"`
	// Configured is false when nothing is set and the newest known
	// version is being assumed. The UI has to be able to say which,
	// because a snippet generated against an ASSUMPTION needs a
	// different sentence next to it than one generated against a
	// customer's stated version.
	Configured bool `json:"configured"`
	// Newest is what this build of Sluicio knows about, so a UI can
	// offer it and can explain the ceiling.
	Newest string `json:"newest_known"`
	// BeyondKnown is true when the configured version is newer than this
	// release can reason about. Presented as a limit of Sluicio, never
	// as a problem with the customer's collector.
	BeyondKnown bool `json:"beyond_known,omitempty"`

	// Service-scoped reads only. Overridden says the value comes from
	// this service rather than the org, so the UI can offer "back to the
	// org default" as a distinct action from "set to the same value" —
	// they look identical on screen and behave differently the next time
	// the org default moves.
	Service    string `json:"service,omitempty"`
	Overridden bool   `json:"overridden,omitempty"`
	// OrgVersion is the default this service would fall back to.
	OrgVersion string `json:"org_version,omitempty"`
}

// orgCollectorTarget reads the org's configured target, if any.
//
// A read failure returns nil rather than an error: a missing setting is
// not a reason to refuse to show a snippet, and the resolver treats nil
// as "fall through to the default".
func (h *Handlers) orgCollectorTarget(ctx context.Context, orgID uuid.UUID) *collectorversion.Target {
	var version, dist *string
	err := h.PGPool.QueryRow(ctx,
		`SELECT collector_version, collector_distribution FROM orgs WHERE id = $1`,
		orgID).Scan(&version, &dist)
	if err != nil {
		return nil
	}
	return targetFrom(version, dist)
}

// serviceCollectorTarget reads one service's override, if any.
func (h *Handlers) serviceCollectorTarget(ctx context.Context, orgID uuid.UUID, serviceName string) *collectorversion.Target {
	if serviceName == "" {
		return nil
	}
	var version, dist *string
	err := h.PGPool.QueryRow(ctx,
		`SELECT collector_version, collector_distribution
		   FROM services
		  WHERE organization_id = $1 AND service_name = $2`,
		orgID, serviceName).Scan(&version, &dist)
	if err != nil {
		return nil
	}
	return targetFrom(version, dist)
}

// targetFrom builds a Target from two nullable columns, returning nil
// when neither is set so Resolve skips the level entirely.
func targetFrom(version, dist *string) *collectorversion.Target {
	if version == nil && dist == nil {
		return nil
	}
	t := collectorversion.Target{}
	if version != nil {
		t.Version = *version
	}
	if dist != nil {
		t.Distribution = collectorversion.Distribution(*dist)
	}
	return &t
}

// CollectorTargetFor is the effective target for a service: its own
// override over the org default over the built-in newest.
//
// Exported because snippet generation is the whole point of the setting
// and lives outside this file — a suggestion about one service must be
// written for the collector that service runs.
//
// An empty serviceName resolves the org default, which is right for a
// snippet that is not about any one service (the metric-level
// suggestions, and the onboarding config).
func (h *Handlers) CollectorTargetFor(ctx context.Context, orgID uuid.UUID, serviceName string) collectorversion.Target {
	return collectorversion.Resolve(
		h.orgCollectorTarget(ctx, orgID),
		h.serviceCollectorTarget(ctx, orgID, serviceName),
	)
}

// getCollectorTarget: GET /api/v1/collector-target
func (h *Handlers) getCollectorTarget(w http.ResponseWriter, r *http.Request) {
	org := h.orgCollectorTarget(r.Context(), middleware.OrgID(r))
	eff := collectorversion.Resolve(org, nil)
	httpserver.WriteJSON(w, http.StatusOK, CollectorTargetResponse{
		Version:      eff.Version,
		Distribution: string(eff.Distribution),
		Configured:   org != nil && org.Version != "",
		Newest:       collectorversion.Newest,
		BeyondKnown:  collectorversion.NewerThanKnown(eff.Version),
	})
}

// patchCollectorTarget: PATCH /api/v1/collector-target
func (h *Handlers) patchCollectorTarget(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeTargetBody(w, r)
	if !ok {
		return
	}
	if _, err := h.PGPool.Exec(r.Context(),
		`UPDATE orgs
		    SET collector_version = COALESCE($2, collector_version),
		        collector_distribution = COALESCE($3, collector_distribution)
		  WHERE id = $1`,
		middleware.OrgID(r), body.Version, body.Distribution); err != nil {
		h.Logger.Error("save collector target failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "saving failed")
		return
	}
	h.recordAudit(r, "collector_target.updated", "organization", middleware.OrgID(r).String(), map[string]any{
		"version":      body.Version,
		"distribution": body.Distribution,
	})
	h.getCollectorTarget(w, r)
}

// getServiceCollectorTarget: GET /api/v1/services/{name}/collector-target
func (h *Handlers) getServiceCollectorTarget(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	orgID := middleware.OrgID(r)
	org := h.orgCollectorTarget(r.Context(), orgID)
	svc := h.serviceCollectorTarget(r.Context(), orgID, name)
	eff := collectorversion.Resolve(org, svc)
	orgOnly := collectorversion.Resolve(org, nil)
	httpserver.WriteJSON(w, http.StatusOK, CollectorTargetResponse{
		Version:      eff.Version,
		Distribution: string(eff.Distribution),
		Configured:   (org != nil && org.Version != "") || (svc != nil && svc.Version != ""),
		Newest:       collectorversion.Newest,
		BeyondKnown:  collectorversion.NewerThanKnown(eff.Version),
		Service:      name,
		Overridden:   svc != nil && (svc.Version != "" || svc.Distribution != ""),
		OrgVersion:   orgOnly.Version,
	})
}

// patchServiceCollectorTarget: PATCH /api/v1/services/{name}/collector-target
//
// A null version AND a null distribution clears the override, which is
// how a service goes back to following the org default. That is a
// different intent from setting it to the org's current value, and the
// difference shows the next time the org default moves.
func (h *Handlers) patchServiceCollectorTarget(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	body, ok := decodeTargetBody(w, r)
	if !ok {
		return
	}
	orgID := middleware.OrgID(r)

	var err error
	if body.Clear {
		_, err = h.PGPool.Exec(r.Context(),
			`UPDATE services
			    SET collector_version = NULL, collector_distribution = NULL
			  WHERE organization_id = $1 AND service_name = $2`,
			orgID, name)
	} else {
		_, err = h.PGPool.Exec(r.Context(),
			`UPDATE services
			    SET collector_version = COALESCE($3, collector_version),
			        collector_distribution = COALESCE($4, collector_distribution)
			  WHERE organization_id = $1 AND service_name = $2`,
			orgID, name, body.Version, body.Distribution)
	}
	if err != nil {
		h.Logger.Error("save service collector target failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "saving failed")
		return
	}
	h.recordAudit(r, "collector_target.updated", "service", name, map[string]any{
		"version":      body.Version,
		"distribution": body.Distribution,
		"cleared":      body.Clear,
	})
	h.getServiceCollectorTarget(w, r)
}

type targetBody struct {
	Version      *string
	Distribution *string
	// Clear is the explicit "follow the org default again" intent, sent
	// as {"version": null, "distribution": null} on a service.
	Clear bool
}

// decodeTargetBody parses and validates a target payload, writing the
// error response itself and reporting whether the caller should carry on.
//
// Validation happens at save time rather than at snippet generation: an
// unparseable version would otherwise silently select the older,
// wider-compatible spelling and nobody would find out why their config
// looked dated.
func decodeTargetBody(w http.ResponseWriter, r *http.Request) (targetBody, bool) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return targetBody{}, false
	}
	var out targetBody
	get := func(key string) (*string, bool) {
		msg, present := raw[key]
		if !present {
			return nil, false
		}
		var s *string
		if err := json.Unmarshal(msg, &s); err != nil {
			return nil, true
		}
		return s, true
	}
	version, versionPresent := get("version")
	dist, distPresent := get("distribution")
	out.Version, out.Distribution = version, dist
	// Both keys present and both null: clear the override.
	out.Clear = versionPresent && distPresent && version == nil && dist == nil

	if version != nil && *version != "" && !collectorversion.Valid(*version) {
		httpserver.WriteError(w, http.StatusBadRequest, "version must look like 0.157.0")
		return targetBody{}, false
	}
	if dist != nil && *dist != "" {
		d := collectorversion.Distribution(*dist)
		if d != collectorversion.DistributionContrib && d != collectorversion.DistributionCore {
			httpserver.WriteError(w, http.StatusBadRequest, "distribution must be contrib or core")
			return targetBody{}, false
		}
	}
	return out, true
}
