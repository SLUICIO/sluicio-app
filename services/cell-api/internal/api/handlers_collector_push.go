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
	if v == nil {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"checked": false})
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"checked": true, "validation": v})
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
	if !ok {
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
	restored, err := collectors.RestoreSecrets(body.Config, original)
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
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
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
	} else if h.featureEntitled(license.FeatureAdvisor) {
		approval, _ = h.Collectors.RequireApproval(r.Context(), c.GroupID)
	}
	d, err := h.Collectors.CreateDeployment(r.Context(), collectors.NewDeployment{
		CollectorID: c.ID, Body: payload, Message: message,
		CreatedBy: p.UserID, CreatedByName: h.actorLabel(r), Via: via, Proposed: approval,
	})
	if err != nil {
		h.Logger.Error("create deployment failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "could not store the configuration")
		return
	}

	if approval {
		prop, status, err := h.proposeCollectorConfig(r, c, d, message)
		if err != nil {
			httpserver.WriteError(w, status, err.Error())
			return
		}
		meta := map[string]any{"deployment_id": d.ID.String(), "proposal_id": prop.ID.String()}
		if suggestion != nil {
			meta["suggestion_id"] = suggestion.ID.String()
		}
		h.recordAudit(r, "collector.config_proposed", "collector", c.ID.String(), meta)
		httpserver.WriteJSON(w, http.StatusAccepted, map[string]any{"deployment": d, "proposal": prop, "validation": v})
		return
	}
	delivered := h.OpAMP != nil && h.OpAMP.Deliver(r.Context(), c.ID)
	h.recordAudit(r, "collector.config_sent", "collector", c.ID.String(), map[string]any{"deployment_id": d.ID.String(), "forced": body.Force && v != nil && v.Errors() > 0})
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{"deployment": d, "delivered": delivered, "validation": v})
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
	_ = h.Collectors.LinkProposal(r.Context(), d.ID, out.ID)
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
		fields:      map[string]bool{"deployment": true},
		needsTarget: true,
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
			// Approval means a second person: the author of a change cannot
			// approve it, or requiring approval would require nothing.
			if p := middleware.Principal(r); p.UserID != nil && d.CreatedBy != nil && *p.UserID == *d.CreatedBy {
				return errors.New("a change is approved by someone other than its author")
			}
			if err := h.Collectors.Release(r.Context(), c.ID, d.ID); err != nil {
				return errors.New("the deployment is no longer waiting for approval")
			}
			if h.OpAMP != nil {
				h.OpAMP.Deliver(r.Context(), c.ID)
			}
			h.recordAudit(r, "collector.config_approved", "collector", c.ID.String(), map[string]any{"deployment_id": d.ID.String()})
			return nil
		},
	}
}
