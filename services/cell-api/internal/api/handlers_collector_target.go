// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The collector a generated snippet targets (issue #16).
//
// Read by anyone who can see a snippet, because the snippet is useless
// without knowing what it is for. Written by an admin, because it
// changes what every generated config in the org says.

package api

import (
	"encoding/json"
	"net/http"

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
}

// orgCollectorTarget reads the org's configured target, if any.
func (h *Handlers) orgCollectorTarget(r *http.Request) *collectorversion.Target {
	var version, dist *string
	err := h.PGPool.QueryRow(r.Context(),
		`SELECT collector_version, collector_distribution FROM orgs WHERE id = $1`,
		middleware.OrgID(r)).Scan(&version, &dist)
	if err != nil {
		// A cell that has not migrated yet, or a read failure. Falling
		// back to the default is right: a missing setting is not a
		// reason to refuse to show a snippet.
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

// getCollectorTarget: GET /api/v1/collector-target
func (h *Handlers) getCollectorTarget(w http.ResponseWriter, r *http.Request) {
	org := h.orgCollectorTarget(r)
	eff := collectorversion.Resolve(org, nil)
	configured := org != nil && org.Version != ""
	httpserver.WriteJSON(w, http.StatusOK, CollectorTargetResponse{
		Version:      eff.Version,
		Distribution: string(eff.Distribution),
		Configured:   configured,
		Newest:       collectorversion.Newest,
		BeyondKnown:  collectorversion.NewerThanKnown(eff.Version),
	})
}

// patchCollectorTarget: PATCH /api/v1/collector-target
func (h *Handlers) patchCollectorTarget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version      *string `json:"version"`
		Distribution *string `json:"distribution"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Rejected at save time rather than discovered when a snippet is
	// generated: a version we cannot parse would silently select the
	// older, wider-compatible spelling and nobody would know why.
	if body.Version != nil && *body.Version != "" && !collectorversion.Valid(*body.Version) {
		httpserver.WriteError(w, http.StatusBadRequest,
			"version must look like 0.157.0")
		return
	}
	if body.Distribution != nil && *body.Distribution != "" {
		d := collectorversion.Distribution(*body.Distribution)
		if d != collectorversion.DistributionContrib && d != collectorversion.DistributionCore {
			httpserver.WriteError(w, http.StatusBadRequest,
				"distribution must be contrib or core")
			return
		}
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
