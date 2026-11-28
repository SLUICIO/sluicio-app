// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Collector management over OpAMP, stage 2 (sending a configuration) and
// the Enterprise governance of stage 3 (approval, rollback).
//
//   POST /api/v1/collectors/{id}/validate                     check a configuration with the cell's OTelFlow
//   GET  /api/v1/collectors/{id}/deployments                  what was sent and what the collector said
//   POST /api/v1/collectors/{id}/deployments                  send a configuration (or propose it, when the team requires approval)
//   GET  /api/v1/collectors/{id}/deployments/{dep}/diff       the current configuration and the deployment's, masked
//   POST /api/v1/collectors/{id}/deployments/{dep}/rollback   send an applied configuration again (Enterprise)
//   GET  /api/v1/settings/groups/{id}/collector-settings      the team's setting
//   PUT  /api/v1/settings/groups/{id}/collector-settings      require approval before sending (Enterprise)
//
// Sending is open core: the owner team's editors and admins and org
// admins send a configuration directly. Enterprise adds what makes it
// governed: a team can require that a change is approved by someone else
// before it is sent, and an applied configuration can be sent again in
// one step. The CI path is the same POST with a service account token
// (via "api"), with the same rights and checks.
//
// Editing happens on the masked configuration; the server restores the
// stored secrets (collectors.RestoreSecrets), so nobody reads one to send
// a change. Only the supervisor type accepts a configuration.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/license"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/advisor"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
)

const proposalCollectorConfig = "collector_config"

// canPushCollector: the owner team's editors and admins, and org admins.
// Never a token scoped to read.
func (h *Handlers) canPushCollector(r *http.Request, groupID uuid.UUID) bool {
	p := middleware.Principal(r)
	if p.TokenScope.IsValid() && !p.TokenScope.CanWrite() {
		return false
	}
	if p.Role.CanAdmin() {
		return true
	}
	role := h.callerGroupRoles(r)[groupID]
	return role == identity.RoleAdmin || role == identity.RoleEditor
}

// pushableCollector loads the collector and checks the caller may send it
// a configuration and that it takes one.
func (h *Handlers) pushableCollector(w http.ResponseWriter, r *http.Request) (collectors.Collector, bool) {
	c, ok := h.collectorFromPath(w, r)
	if !ok {
		return c, false
	}
	if !h.canPushCollector(r, c.GroupID) {
		httpserver.WriteError(w, http.StatusForbidden, "only the owner team's editors and admins, and org admins, can change this collector's configuration")
		return c, false
	}
	if c.Kind != collectors.KindSupervisor {
		httpserver.WriteError(w, http.StatusConflict, "this collector does not accept a configuration (it is read only: the OpAMP extension, or a supervisor without accepts_remote_config)")
		return c, false
	}
	return c, true
}

type collectorConfigBody struct {
	Config  string `json:"config"`
	Message string `json:"message"`
	// Force sends despite errors OTelFlow found. OTelFlow's registry can
	// lag a collector release, so its errors are not the last word; the
	// collector's own check when it applies the configuration is.
	Force bool `json:"force"`
	// SuggestionID marks the change as an Advisor suggestion applied to
	// this collector (Enterprise, org admins). It always waits for the
	// owner team's approval.
	SuggestionID string `json:"suggestion_id"`
	// SuggestionHash hashes a personal-data value (T6) instead of
	// deleting the attribute, as the preview did with ?hash=true.
	SuggestionHash bool `json:"suggestion_hash"`
	// DurationMinutes makes the change temporary (Enterprise): the
	// collector is sent its base again this long after it applied the
	// change. Zero is permanent.
	DurationMinutes int `json:"duration_minutes"`
	// Stamp marks the change as adding the collector stamp (Enterprise):
	// always proposed to the owner team.
	Stamp bool `json:"stamp"`
}

// plainSecretsOf is where config, as typed, writes a secret in plain text
// instead of an ${env:...} reference; a masked value is the stored one
// and does not count. Never nil, so the answer always has the list.
func plainSecretsOf(config string) []string {
	if p := collectors.PlainSecrets(config); p != nil {
		return p
	}
	return []string{}
}

func (h *Handlers) validateWithOtelflow(ctx context.Context, c collectors.Collector, config string) (*collectors.Validation, error) {
	if h.OtelflowValidator == nil {
		return nil, nil
	}
	v, err := h.OtelflowValidator.Validate(ctx, config, c.Description["service.version"])
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// validateCollectorConfig: POST /api/v1/collectors/{id}/validate
func (h *Handlers) validateCollectorConfig(w http.ResponseWriter, r *http.Request) {
	c, ok := h.collectorFromPath(w, r)
	if !ok {
		return
	}
	var body collectorConfigBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	v, err := h.validateWithOtelflow(r.Context(), c, body.Config)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadGateway, "OTelFlow could not check the configuration: "+err.Error())
		return
	}
	out := map[string]any{"checked": v != nil, "kept_by_local": h.keptByLocal(r, c, body.Config),
		"plain_secrets": plainSecretsOf(body.Config)}
	if v != nil {
		out["validation"] = v
	}
	httpserver.WriteJSON(w, http.StatusOK, out)
}

// keptByLocal lists what the change removes that the collector's local
// files set and so keep in effect (see collectors.KeptByLocal). Empty when
// it cannot be told: no configuration as written, or not YAML.
func (h *Handlers) keptByLocal(r *http.Request, c collectors.Collector, change string) []string {
	current, err := h.Collectors.LatestRawOnly(r.Context(), c.ID)
	if err != nil {
		return []string{}
	}
	sent, err := h.Collectors.LastSent(r.Context(), c.ID)
	if err != nil {
		return []string{}
	}
	paths, err := collectors.KeptByLocal(current, sent, change)
	if err != nil {
		return []string{}
	}
	return paths
}

// listCollectorDeployments: GET /api/v1/collectors/{id}/deployments
func (h *Handlers) listCollectorDeployments(w http.ResponseWriter, r *http.Request) {
	c, ok := h.collectorFromPath(w, r)
	if !ok {
		return
	}
	list, err := h.Collectors.Deployments(r.Context(), c.ID, 50)
	if err != nil {
		h.Logger.Error("list deployments failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"deployments": list})
}

// createCollectorDeployment: POST /api/v1/collectors/{id}/deployments
func (h *Handlers) createCollectorDeployment(w http.ResponseWriter, r *http.Request) {
	c, ok := h.pushableCollector(w, r)
	if !ok || h.refusePipelineOwned(w, r, c) {
		return
	}
	var body collectorConfigBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Config) == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "config is required")
		return
	}
	duration := time.Duration(body.DurationMinutes) * time.Minute
	if duration != 0 {
		if !h.featureEntitled(license.FeatureAdvisor) {
			httpserver.WriteError(w, http.StatusPaymentRequired, "time-limited changes are a Sluicio Enterprise feature")
			return
		}
		if duration < collectors.MinTemporary || duration > collectors.MaxTemporary {
			httpserver.WriteError(w, http.StatusBadRequest, "a temporary change lasts from 5 minutes to 7 days")
			return
		}
		if body.SuggestionID != "" {
			httpserver.WriteError(w, http.StatusBadRequest, "an Advisor suggestion is applied permanently")
			return
		}
		if active, err := h.Collectors.ActiveTemporary(r.Context(), c.ID); err != nil {
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		} else if active != nil {
			httpserver.WriteError(w, http.StatusConflict, errTemporaryActive)
			return
		}
	}
	if body.Stamp {
		if !h.featureEntitled(license.FeatureAdvisor) {
			httpserver.WriteError(w, http.StatusPaymentRequired, "the collector stamp is a Sluicio Enterprise feature")
			return
		}
		if body.SuggestionID != "" || duration != 0 {
			httpserver.WriteError(w, http.StatusBadRequest, "the collector stamp is a change of its own, sent permanently")
			return
		}
	}
	var suggestion *advisor.Suggestion
	if body.SuggestionID != "" {
		if !h.featureEntitled(license.FeatureAdvisor) {
			httpserver.WriteError(w, http.StatusPaymentRequired, "Advisor suggestions are a Sluicio Enterprise feature")
			return
		}
		if !middleware.Principal(r).Role.CanAdmin() {
			httpserver.WriteError(w, http.StatusForbidden, "only org admins apply Advisor suggestions")
			return
		}
		s, ok := h.collectorSuggestion(w, r, body.SuggestionID)
		if !ok {
			return
		}
		suggestion = &s
	}
	// Secrets: the edited text is the masked one; put the stored values
	// back, from the configuration as written only. The effective one
	// leaves fields out and is never a base for a change, so a collector
	// that reports only that (or nothing) has no secrets to restore, and
	// a masked value in what is sent is refused. A complete file sent from
	// CI carries no masked values and needs none.
	original, err := h.Collectors.LatestRawOnly(r.Context(), c.ID)
	if errors.Is(err, collectors.ErrNotFound) {
		if strings.Contains(body.Config, collectors.Masked) {
			httpserver.WriteError(w, http.StatusUnprocessableEntity, "this collector does not report its configuration as written (reports_raw_config), so a masked value cannot be restored; type the real values")
			return
		}
		original, err = "", nil
	}
	if err != nil {
		h.Logger.Error("read stored configuration failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not read the stored configuration")
		return
	}
	// A value the collector hides is restored from what Sluicio last sent.
	applied, err := h.Collectors.AppliedConfig(r.Context(), c.ID)
	if err != nil {
		h.Logger.Error("read applied configuration failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not read the stored configuration")
		return
	}
	restored, err := collectors.RestoreSecrets(body.Config, original, applied)
	if err != nil {
		httpserver.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	v, err := h.validateWithOtelflow(r.Context(), c, body.Config)
	if err != nil && !body.Force {
		httpserver.WriteError(w, http.StatusBadGateway, "OTelFlow could not check the configuration; send with force to rely on the collector's own check")
		return
	}
	if v != nil && v.Errors() > 0 && !body.Force {
		httpserver.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":      map[string]any{"message": fmt.Sprintf("OTelFlow found %d error(s); fix them, or send with force", v.Errors()), "status": http.StatusUnprocessableEntity},
			"validation": v,
		})
		return
	}
	payload, err := collectors.PrepareRemote(restored)
	if err != nil {
		httpserver.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	p := middleware.Principal(r)
	via := "ui"
	if viaAPI(r) {
		via = "api"
	}
	message := strings.TrimSpace(body.Message)
	approval := false
	if suggestion != nil {
		// The team that owns the collector answers for it, so a change the
		// Advisor proposes is theirs to approve, whatever their setting.
		approval, via = true, "advisor"
		if message == "" {
			message = "Advisor: " + suggestion.Title
		}
	} else if body.Stamp {
		// It changes what the collector sends, so the team that runs it
		// approves it, whatever its setting.
		approval, via = true, "stamp"
		if message == "" {
			message = "Stamp the data this collector carries with its id"
		}
	} else if h.featureEntitled(license.FeatureAdvisor) {
		// Fails closed: a team's setting that cannot be read must not
		// send straight to the collector what it would have held back.
		req, err := h.Collectors.RequireApproval(r.Context(), c.GroupID)
		if err != nil {
			h.Logger.Error("read collector approval setting failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "could not read whether this team requires approval")
			return
		}
		approval = req
	}
	nd := collectors.NewDeployment{
		CollectorID: c.ID, Body: payload, Message: message,
		CreatedBy: p.UserID, CreatedByName: h.actorLabel(r), Via: via, Proposed: approval, Duration: duration,
	}
	if suggestion != nil {
		t, _ := adviceFor(*suggestion)
		t = withHash(*suggestion, t, body.SuggestionHash)
		nd.AdvisorSuggestionID, nd.AdvisorClass, nd.AdvisorTarget = &suggestion.ID, suggestion.Class, &t
	}
	d, err := h.Collectors.CreateDeployment(r.Context(), nd)
	if errors.Is(err, collectors.ErrTemporaryActive) {
		httpserver.WriteError(w, http.StatusConflict, errTemporaryActive)
		return
	}
	if err != nil {
		h.Logger.Error("create deployment failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not store the configuration")
		return
	}

	if approval {
		prop, status, err := h.proposeCollectorConfig(r, c, d, message)
		if err != nil {
			// Without its proposal the row would read as waiting for an
			// approval nobody can give.
			if dErr := h.Collectors.DiscardProposed(r.Context(), c.ID, d.ID); dErr != nil {
				h.Logger.Error("discard unproposed deployment failed", "err", dErr, "deployment", d.ID)
			}
			httpserver.WriteError(w, status, err.Error())
			return
		}
		meta := map[string]any{"deployment_id": d.ID.String(), "proposal_id": prop.ID.String()}
		if suggestion != nil {
			meta["suggestion_id"] = suggestion.ID.String()
		}
		h.recordAudit(r, "collector.config_proposed", "collector", c.ID.String(), meta)
		httpserver.WriteJSON(w, http.StatusAccepted, map[string]any{"deployment": d, "proposal": prop, "validation": v, "plain_secrets": plainSecretsOf(body.Config)})
		return
	}
	delivered := h.OpAMP != nil && h.OpAMP.Deliver(r.Context(), c.ID)
	meta := map[string]any{"deployment_id": d.ID.String(), "forced": body.Force && v != nil && v.Errors() > 0}
	if duration > 0 {
		meta["duration_minutes"] = body.DurationMinutes
	}
	if d.Status == collectors.DeploymentHeld {
		meta["held"] = true
	}
	h.recordAudit(r, "collector.config_sent", "collector", c.ID.String(), meta)
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{"deployment": d, "delivered": delivered, "validation": v, "plain_secrets": plainSecretsOf(body.Config)})
}

// acceptCollectorDrift: POST /api/v1/collectors/{id}/drift/accept
//
// Takes the configuration the collector reports now as the baseline its
// drift is measured from: changes made on the host that are meant to
// stay. Nothing is sent to the collector. Open to whoever may change the
// collector's configuration.
func (h *Handlers) acceptCollectorDrift(w http.ResponseWriter, r *http.Request) {
	c, ok := h.pushableCollector(w, r)
	if !ok {
		return
	}
	d, err := h.Collectors.AcceptDrift(r.Context(), c.ID, h.actorLabel(r))
	if errors.Is(err, collectors.ErrNothingToAccept) {
		httpserver.WriteError(w, http.StatusConflict, "there is no drift to accept on this collector")
		return
	}
	if err != nil {
		h.Logger.Error("accept drift failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not accept the configuration")
		return
	}
	meta := map[string]any{}
	if c.Drift != nil {
		paths := make([]string, 0, len(c.Drift.Paths))
		for _, p := range c.Drift.Paths {
			paths = append(paths, p.Kind+" "+p.Path)
		}
		meta["accepted_paths"] = paths
	}
	h.recordAudit(r, "collector.drift_accepted", "collector", c.ID.String(), meta)
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"drift": d})
}

const errPipelineOwned = "the team's pipeline owns this collector's configuration, so Sluicio sends it nothing from here; apply the change in the pipeline, or send it through the API as the pipeline does"

// viaAPI says whether the request carries an API token rather than a
// browser session: how a pipeline that deploys through Sluicio sends.
func viaAPI(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// refusePipelineOwned refuses a send from the UI to a collector whose
// configuration the team's pipeline owns. The API stays open: that is
// how a pipeline deploying through Sluicio sends.
func (h *Handlers) refusePipelineOwned(w http.ResponseWriter, r *http.Request, c collectors.Collector) bool {
	if c.ConfigOwner != collectors.OwnerPipeline || viaAPI(r) {
		return false
	}
	httpserver.WriteError(w, http.StatusConflict, errPipelineOwned)
	return true
}

// setCollectorOwner: PUT /api/v1/collectors/{id}/owner
//
// Who owns the collector's configuration: "sluicio" or "pipeline" (the
// team's own deployment pipeline). For the owner team's admins and org
// admins, like the team's tokens and settings. Open source.
func (h *Handlers) setCollectorOwner(w http.ResponseWriter, r *http.Request) {
	c, ok := h.collectorFromPath(w, r)
	if !ok {
		return
	}
	if !h.canManageCollectorTokens(r, c.GroupID, nil) {
		httpserver.WriteError(w, http.StatusForbidden, "only the owner team's admins and org admins can change who owns the configuration")
		return
	}
	var body struct {
		Owner string `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Owner != collectors.OwnerSluicio && body.Owner != collectors.OwnerPipeline) {
		httpserver.WriteError(w, http.StatusBadRequest, `owner is "sluicio" or "pipeline"`)
		return
	}
	if err := h.Collectors.SetConfigOwner(r.Context(), h.collectorScope(r), c.ID, body.Owner, h.actorLabel(r)); err != nil {
		h.Logger.Error("set collector owner failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "collector.config_owner_changed", "collector", c.ID.String(), map[string]any{"from": c.ConfigOwner, "to": body.Owner})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"config_owner": body.Owner})
}

const errTemporaryActive = "a temporary change is already active on this collector; end it first (a permanent change can be sent meanwhile: it takes effect when the temporary one ends)"

// endCollectorDeployment: POST /api/v1/collectors/{id}/deployments/{dep}/end
//
// Ends a temporary change before its time and sends the collector its
// base: a permanent change made meanwhile, else the last permanent one
// it applied, else its local configuration. Enterprise.
func (h *Handlers) endCollectorDeployment(w http.ResponseWriter, r *http.Request) {
	c, ok := h.pushableCollector(w, r)
	if !ok {
		return
	}
	depID, ok := deploymentFromPath(w, r)
	if !ok {
		return
	}
	base, err := h.Collectors.EndTemporary(r.Context(), c.ID, depID, collectors.EndedEarly)
	if errors.Is(err, collectors.ErrNotFound) {
		httpserver.WriteError(w, http.StatusConflict, "this is not an active temporary change")
		return
	}
	if err != nil {
		h.Logger.Error("end temporary deployment failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not end the temporary change")
		return
	}
	delivered := h.OpAMP != nil && h.OpAMP.Deliver(r.Context(), c.ID)
	h.recordAudit(r, "collector.config_temporary_ended", "collector", c.ID.String(), map[string]any{"deployment_id": depID.String(), "base_id": base.ID.String()})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"base": base, "delivered": delivered})
}

// proposeCollectorConfig files the proposal a proposed deployment waits on.
func (h *Handlers) proposeCollectorConfig(r *http.Request, c collectors.Collector, d collectors.Deployment, rationale string) (proposals.Proposal, int, error) {
	after, _ := json.Marshal(map[string]string{"deployment_id": d.ID.String()})
	if strings.TrimSpace(rationale) == "" {
		rationale = "Configuration change for " + c.Description["host.name"]
	}
	p, status, err := h.buildProposal(r, proposalCollectorConfig, c.ID.String(),
		[]proposals.Change{{Field: "deployment", After: after}}, rationale)
	if err != nil {
		return proposals.Proposal{}, status, err
	}
	out, err := h.Proposals.Create(r.Context(), p, h.proposalTTL(r.Context(), p.OrgID))
	if err != nil {
		return proposals.Proposal{}, http.StatusInternalServerError, errors.New("could not file the proposal")
	}
	if err := h.Collectors.LinkProposal(r.Context(), d.ID, out.ID); err != nil {
		// The proposal still names the deployment and approving it works;
		// only the Deployments tab loses which proposal the row waits on.
		h.Logger.Warn("link collector proposal failed", "err", err, "deployment", d.ID, "proposal", out.ID)
	}
	return out, 0, nil
}

func deploymentFromPath(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("dep"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid deployment id")
		return uuid.Nil, false
	}
	return id, true
}

// collectorDeploymentDiff: GET /api/v1/collectors/{id}/deployments/{dep}/diff
//
// Both sides masked: what the collector runs as written, and what the
// deployment sends. For a reviewer deciding a proposal.
func (h *Handlers) collectorDeploymentDiff(w http.ResponseWriter, r *http.Request) {
	c, ok := h.collectorFromPath(w, r)
	if !ok {
		return
	}
	depID, ok := deploymentFromPath(w, r)
	if !ok {
		return
	}
	d, body, err := h.Collectors.DeploymentBody(r.Context(), c.ID, depID)
	if errors.Is(err, collectors.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not read the deployment")
		return
	}
	current, _ := h.Collectors.LatestRaw(r.Context(), c.ID)
	before, _ := collectors.PrepareRemote(current)
	maskedBefore, _ := collectors.MaskConfig(before)
	maskedAfter, okAfter := collectors.MaskConfig(body)
	if !okAfter {
		httpserver.WriteError(w, http.StatusUnprocessableEntity, "the deployment cannot be read to mask it")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"deployment": d, "before": maskedBefore, "after": maskedAfter})
}

// rollbackCollectorDeployment: POST /api/v1/collectors/{id}/deployments/{dep}/rollback
//
// Sends an applied configuration again, as a new deployment that names the
// one it restores. Enterprise. The supervisor's own
// automatic_config_rollback, which goes back when a configuration fails
// to start, is the collector's and stays free.
func (h *Handlers) rollbackCollectorDeployment(w http.ResponseWriter, r *http.Request) {
	c, ok := h.pushableCollector(w, r)
	if !ok || h.refusePipelineOwned(w, r, c) {
		return
	}
	depID, ok := deploymentFromPath(w, r)
	if !ok {
		return
	}
	d, body, err := h.Collectors.DeploymentBody(r.Context(), c.ID, depID)
	if errors.Is(err, collectors.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not read the deployment")
		return
	}
	if d.Status != collectors.DeploymentApplied {
		httpserver.WriteError(w, http.StatusConflict, "only a configuration the collector applied can be restored")
		return
	}
	p := middleware.Principal(r)
	nd, err := h.Collectors.CreateDeployment(r.Context(), collectors.NewDeployment{
		CollectorID: c.ID, Body: body, Message: "Restore of " + d.CreatedAt.UTC().Format("2006-01-02 15:04"),
		CreatedBy: p.UserID, CreatedByName: h.actorLabel(r), Via: "rollback", RolledBackFrom: &d.ID,
	})
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not store the configuration")
		return
	}
	delivered := h.OpAMP != nil && h.OpAMP.Deliver(r.Context(), c.ID)
	h.recordAudit(r, "collector.config_rolled_back", "collector", c.ID.String(), map[string]any{"deployment_id": nd.ID.String(), "restores": d.ID.String()})
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{"deployment": nd, "delivered": delivered})
}

// getCollectorTeamSettings: GET /api/v1/settings/groups/{id}/collector-settings
func (h *Handlers) getCollectorTeamSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := h.teamForTokens(w, r)
	if !ok {
		return
	}
	v, err := h.Collectors.RequireApproval(r.Context(), id)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"require_approval": v && h.featureEntitled(license.FeatureAdvisor),
		"entitled":         h.featureEntitled(license.FeatureAdvisor),
	})
}

// putCollectorTeamSettings: PUT /api/v1/settings/groups/{id}/collector-settings
func (h *Handlers) putCollectorTeamSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := h.teamForTokens(w, r)
	if !ok {
		return
	}
	var body struct {
		RequireApproval bool `json:"require_approval"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.Collectors.SetRequireApproval(r.Context(), middleware.OrgID(r), id, body.RequireApproval); err != nil {
		if errors.Is(err, collectors.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "team not found")
			return
		}
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "collector.team_settings_updated", "group", id.String(), map[string]any{"require_approval": body.RequireApproval})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"require_approval": body.RequireApproval, "entitled": true})
}

func init() {
	// A configuration waiting for approval. The proposal carries only the
	// deployment's id; the configuration stays encrypted in
	// collector_deployments, never in the proposals table in the clear.
	proposalTargets[proposalCollectorConfig] = proposalTarget{
		// Approval means a second person, checked against who filed the
		// proposal so a service account counts as itself.
		secondPerson: true,
		fields:       map[string]bool{"deployment": true},
		needsTarget:  true,
		check: func(h *Handlers, r *http.Request, id *uuid.UUID, changes []proposals.Change) (string, string, error) {
			if h.Collectors == nil {
				return "", "", errors.New("collector management is off")
			}
			c, err := h.Collectors.Get(r.Context(), h.collectorScope(r), *id)
			if err != nil {
				return "", "", errors.New("collector not found")
			}
			var ref struct {
				DeploymentID uuid.UUID `json:"deployment_id"`
			}
			if err := json.Unmarshal(changes[0].After, &ref); err != nil {
				return "", "", errors.New("deployment: expected {\"deployment_id\": ...}")
			}
			d, _, err := h.Collectors.DeploymentBody(r.Context(), c.ID, ref.DeploymentID)
			if err != nil || d.Status != collectors.DeploymentProposed {
				return "", "", errors.New("the deployment is not waiting for approval")
			}
			name := c.Description["host.name"]
			if name == "" {
				name = c.InstanceUID
			}
			return "Collector configuration: " + name + " (" + c.GroupName + ")", "", nil
		},
		// The drift check's input: the configuration the collector reported
		// last. If it changed while the proposal waited, the reviewer is
		// told before approving a change made against an older one.
		snapshot: func(h *Handlers, ctx context.Context, orgID, id uuid.UUID) (map[string]json.RawMessage, string, error) {
			c, err := h.Collectors.Get(ctx, collectors.Scope{OrgID: orgID, All: true}, id)
			if err != nil {
				return nil, "", err
			}
			v, _ := json.Marshal(c.ConfigHash)
			return map[string]json.RawMessage{"deployment": v}, "Collector configuration: " + c.Description["host.name"], nil
		},
		// Seen only by those who can see the collector: the proposal names
		// the collector and its team, which another team has no business
		// reading in its inbox.
		visible: func(h *Handlers, r *http.Request, p proposals.Proposal) bool {
			if h.Collectors == nil || p.TargetID == nil {
				return false
			}
			_, err := h.Collectors.Get(r.Context(), h.collectorScope(r), *p.TargetID)
			return err == nil
		},
		// Deciding is the owner team's: its editors and admins, and org
		// admins, exactly who could send the change directly.
		mayDecide: func(h *Handlers, r *http.Request, p proposals.Proposal) bool {
			if h.Collectors == nil || p.TargetID == nil {
				return false
			}
			c, err := h.Collectors.Get(r.Context(), h.collectorScope(r), *p.TargetID)
			return err == nil && h.canPushCollector(r, c.GroupID)
		},
		apply: func(h *Handlers, r *http.Request, id *uuid.UUID, changes []proposals.Change) error {
			c, err := h.Collectors.Get(r.Context(), h.collectorScope(r), *id)
			if err != nil {
				return errors.New("collector not found")
			}
			if !h.canPushCollector(r, c.GroupID) {
				return errors.New("only the owner team's editors and admins, and org admins, can approve a configuration for this collector")
			}
			var ref struct {
				DeploymentID uuid.UUID `json:"deployment_id"`
			}
			if err := json.Unmarshal(changes[0].After, &ref); err != nil {
				return err
			}
			d, _, err := h.Collectors.DeploymentBody(r.Context(), c.ID, ref.DeploymentID)
			if err != nil {
				return errors.New("deployment not found")
			}
			if c.ConfigOwner == collectors.OwnerPipeline {
				return errors.New(errPipelineOwned)
			}
			if err := h.Collectors.Release(r.Context(), c.ID, d.ID); err != nil {
				if errors.Is(err, collectors.ErrTemporaryActive) {
					return errors.New(errTemporaryActive)
				}
				return errors.New("the deployment is no longer waiting for approval")
			}
			if h.OpAMP != nil {
				h.OpAMP.Deliver(r.Context(), c.ID)
			}
			// An approved Advisor change is the decision the suggestion
			// waited for: accepted, so the Advisor's own verification can
			// confirm it once the metric stops arriving.
			if d.AdvisorSuggestionID != nil && h.Advisor != nil {
				if s, err := h.Advisor.Get(r.Context(), middleware.OrgID(r), *d.AdvisorSuggestionID); err == nil && s.State == "open" {
					var by uuid.UUID
					if p := middleware.Principal(r); p.UserID != nil {
						by = *p.UserID
					}
					name := c.Description["host.name"]
					if name == "" {
						name = c.InstanceUID
					}
					if _, err := h.Advisor.Decide(r.Context(), middleware.OrgID(r), s.ID, by, "accepted", "Applied to collector "+name+" ("+c.GroupName+") by approval"); err != nil {
						h.Logger.Warn("advisor: accept after approval failed", "err", err)
					}
				}
			}
			h.recordAudit(r, "collector.config_approved", "collector", c.ID.String(), map[string]any{"deployment_id": d.ID.String()})
			return nil
		},
	}
}
