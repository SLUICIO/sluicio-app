// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Proposals — the agent write path (issue #8, WS2).
//
// Agents never mutate monitoring config. They file a proposal; a human
// with the rights to make that change approves it, and approval runs the
// SAME store update a manual edit runs. That last part is deliberate: an
// approved proposal must leave the entity exactly as editable as before,
// with no "managed by agent" mode to escape from later.
//
// Permissions follow the issue's rule. PROPOSING needs only the
// visibility the caller already has — a proposal is inert until someone
// acts on it, so letting a scoped SA file one grants nothing. DECIDING
// needs the same rights as making the change by hand, so approve/reject
// sit behind the identical middleware as PUT /alert-rules.
//
// Target kinds live in a registry rather than a switch so adding one is
// a self-contained change: snapshot() reads the current values for the
// drift check, apply() writes the approved ones.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
)

// proposalTarget is one supported entity family.
type proposalTarget struct {
	// Fields the kind accepts. A proposal naming anything else is
	// rejected at create time — an agent must not be able to smuggle an
	// arbitrary field past review by burying it in a long diff.
	fields map[string]bool
	// creates marks a kind that makes something new: every change's
	// before is null, and a dedup key stands in for superseding.
	creates bool
	// needsTarget: target_id is required (the entity changed, or the
	// integration a new rule goes on). A new health check has none.
	needsTarget bool
	// check, when set, validates the proposed values as they are filed,
	// so the inbox only holds changes a reviewer could approve. It
	// returns the inbox label and, for a create, the dedup key.
	check func(h *Handlers, r *http.Request, id *uuid.UUID, changes []proposals.Change) (label, dedup string, err error)
	// snapshot returns the current value of each tunable field plus a
	// human label for the inbox.
	snapshot func(h *Handlers, ctx context.Context, orgID, id uuid.UUID) (map[string]json.RawMessage, string, error)
	// apply writes the approved values, as the reviewer whose request r
	// is.
	apply func(h *Handlers, r *http.Request, id *uuid.UUID, changes []proposals.Change) error
	// mayDecide, when set, replaces the org-wide write gate for deciding
	// this kind: deciding is gated like making the change by hand, and
	// for a kind owned by a team (a collector) that is the team's right,
	// which an org viewer who edits in the team has and an org editor
	// outside it does not.
	mayDecide func(h *Handlers, r *http.Request, p proposals.Proposal) bool
}

// proposalDecisionGate gates approving or rejecting one proposal: the
// kind's own mayDecide when it has one, the org-wide write gate
// otherwise. A proposal that cannot be read falls through to the default
// gate, and the handler answers for it.
func (h *Handlers) proposalDecisionGate(next http.HandlerFunc) http.HandlerFunc {
	gated := h.writeAnywhere(next)
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Proposals != nil {
			if id, err := uuid.Parse(r.PathValue("id")); err == nil {
				if p, err := h.Proposals.Get(r.Context(), middleware.OrgID(r), id); err == nil {
					if t, ok := proposalTargets[p.TargetKind]; ok && t.mayDecide != nil {
						if !t.mayDecide(h, r, p) {
							httpserver.WriteError(w, http.StatusForbidden, "insufficient role for this action")
							return
						}
						next(w, r)
						return
					}
				}
			}
		}
		gated(w, r)
	}
}

// alertRuleTunables are the knobs an agent may propose changing on an
// existing check. Deliberately narrow: these are the "this alert is too
// noisy / too quiet" dials. Retargeting a rule (metric, service, signal)
// is authoring, not tuning, and stays a human action.
//
// A field belongs here ONLY if Alerts.UpdateRule actually persists it.
// evaluation_seconds looks tunable — it is on AlertRule and is read back
// from the database — but UpdateRule's SET clause omits it, so proposing
// it produced an approved proposal that changed nothing. An approval
// that silently no-ops is worse than a rejected one: the audit trail
// then records a change that never happened. If a field becomes
// updatable, add it here and prove it round-trips end to end.
//
// channel_ids and the environment scope are here because setting up a
// cell's alerting is mostly these: where each check sends (an empty list
// follows the notification profile), and production-only checks.
var alertRuleTunables = map[string]bool{
	"threshold":        true,
	"severity":         true,
	"for_window":       true,
	"enabled":          true,
	"channel_ids":      true,
	"environment_mode": true,
	"environments":     true,
}

var proposalTargets = map[string]proposalTarget{
	proposalAlertRule: {
		fields:      alertRuleTunables,
		needsTarget: true,
		snapshot: func(h *Handlers, ctx context.Context, orgID, id uuid.UUID) (map[string]json.RawMessage, string, error) {
			r, err := h.Alerts.GetRule(ctx, orgID, id)
			if err != nil {
				return nil, "", err
			}
			out := map[string]json.RawMessage{}
			put := func(k string, v any) {
				b, mErr := json.Marshal(v)
				if mErr == nil {
					out[k] = b
				}
			}
			put("threshold", r.Spec.Threshold)
			put("severity", string(r.Severity))
			put("for_window", r.Spec.ForWindow)
			put("enabled", r.Enabled)
			channels := make([]string, 0, len(r.ChannelIDs))
			for _, c := range r.ChannelIDs {
				channels = append(channels, c.String())
			}
			// Sorted: the routes come back in no set order, and the drift
			// check compares lists as written.
			sort.Strings(channels)
			put("channel_ids", channels)
			put("environment_mode", environmentModeName(r.EnvironmentMode))
			envs := r.Environments
			if envs == nil {
				envs = []string{}
			}
			put("environments", envs)
			return out, r.Name, nil
		},
		check: checkAlertRuleTuning,
		apply: func(h *Handlers, req *http.Request, id *uuid.UUID, changes []proposals.Change) error {
			ctx, orgID := req.Context(), middleware.OrgID(req)
			r, err := h.Alerts.GetRule(ctx, orgID, *id)
			if err != nil {
				return err
			}
			for _, c := range changes {
				switch c.Field {
				case "channel_ids":
					ids, err := proposedChannelIDs(c.After)
					if err != nil {
						return err
					}
					r.ChannelIDs = ids
				case "environment_mode":
					var v string
					if err := json.Unmarshal(c.After, &v); err != nil {
						return fmt.Errorf("environment_mode: %w", err)
					}
					r.EnvironmentMode = environmentModeName(v)
				case "environments":
					var v []string
					if err := json.Unmarshal(c.After, &v); err != nil {
						return fmt.Errorf("environments: %w", err)
					}
					r.Environments = normalizeEnvironments(v)
				case "threshold":
					var v float64
					if err := json.Unmarshal(c.After, &v); err != nil {
						return fmt.Errorf("threshold: %w", err)
					}
					r.Spec.Threshold = v
				case "severity":
					var v string
					if err := json.Unmarshal(c.After, &v); err != nil {
						return fmt.Errorf("severity: %w", err)
					}
					sev := alerting.Severity(v)
					if !alerting.ValidSeverity(sev) {
						return fmt.Errorf("severity: %q is not a valid severity", v)
					}
					r.Severity = sev
				case "for_window":
					var v string
					if err := json.Unmarshal(c.After, &v); err != nil {
						return fmt.Errorf("for_window: %w", err)
					}
					if _, err := time.ParseDuration(v); err != nil {
						return fmt.Errorf("for_window: %q is not a duration", v)
					}
					r.Spec.ForWindow = v
				case "enabled":
					var v bool
					if err := json.Unmarshal(c.After, &v); err != nil {
						return fmt.Errorf("enabled: %w", err)
					}
					r.Enabled = v
				default:
					return fmt.Errorf("field %q is not tunable on an alert rule", c.Field)
				}
			}
			if r.EnvironmentMode == alerting.EnvironmentEach {
				r.Environments = nil
			} else if err := checkEnvironmentScope(r); err != nil {
				return err
			}
			_, err = h.Alerts.UpdateRule(ctx, orgID, r)
			return err
		},
	},
}

// ── create ───────────────────────────────────────────────────────────

type proposalBody struct {
	TargetKind string             `json:"target_kind"`
	TargetID   string             `json:"target_id"`
	Changes    []proposals.Change `json:"changes"`
	Rationale  string             `json:"rationale"`
}

// createProposal: POST /api/v1/proposals
//
// Open to any authenticated caller who can see the target: filing is
// inert. The value of the rationale is high enough to require it — a
// diff with no reason isn't reviewable, it's a puzzle.
func (h *Handlers) createProposal(w http.ResponseWriter, r *http.Request) {
	if h.Proposals == nil {
		httpserver.WriteError(w, http.StatusNotImplemented, "proposals are not enabled on this cell")
		return
	}
	var body proposalBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Rationale) == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "rationale is required — a proposal without a reason cannot be reviewed")
		return
	}
	p, status, err := h.buildProposal(r, body.TargetKind, body.TargetID, body.Changes, body.Rationale)
	if err != nil {
		httpserver.WriteError(w, status, err.Error())
		return
	}
	out, err := h.Proposals.Create(r.Context(), p, h.proposalTTL(r.Context(), p.OrgID))
	if errors.Is(err, proposals.ErrDuplicatePending) {
		httpserver.WriteError(w, http.StatusConflict, "the same proposal is already waiting for review")
		return
	}
	if err != nil {
		h.Logger.Error("create proposal failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "proposal.created", "proposal", out.ID.String(), map[string]any{
		"target_kind": out.TargetKind, "target_label": out.TargetLabel, "via": out.Via,
	})
	httpserver.WriteJSON(w, http.StatusCreated, out)
}

// buildProposal checks one proposed change and snapshots its target, the
// way it is stored: shared by a single proposal and every item of a
// batch, so a batch can hold nothing a single proposal could not. It
// answers the HTTP status to refuse with.
func (h *Handlers) buildProposal(r *http.Request, kind, rawTargetID string, in []proposals.Change, rationale string) (proposals.Proposal, int, error) {
	target, ok := proposalTargets[kind]
	if !ok {
		return proposals.Proposal{}, http.StatusBadRequest, errors.New("unsupported target_kind: " + kind)
	}
	if len(in) == 0 {
		return proposals.Proposal{}, http.StatusBadRequest, errors.New("at least one change is required")
	}
	if target.creates && len(in) != 1 {
		return proposals.Proposal{}, http.StatusBadRequest, errors.New("a " + kind + " proposal carries one change, the rule")
	}
	seen := map[string]bool{}
	for _, c := range in {
		if !target.fields[c.Field] {
			return proposals.Proposal{}, http.StatusBadRequest, errors.New("field is not proposable on " + kind + ": " + c.Field)
		}
		if seen[c.Field] {
			return proposals.Proposal{}, http.StatusBadRequest, errors.New("field named twice: " + c.Field)
		}
		seen[c.Field] = true
	}
	var targetID *uuid.UUID
	if target.needsTarget {
		id, err := uuid.Parse(strings.TrimSpace(rawTargetID))
		if err != nil {
			return proposals.Proposal{}, http.StatusBadRequest, errors.New("target_id is required")
		}
		targetID = &id
	}
	orgID := middleware.OrgID(r)

	var label, dedup string
	if target.check != nil {
		l, d, err := target.check(h, r, targetID, in)
		if err != nil {
			return proposals.Proposal{}, http.StatusBadRequest, err
		}
		label, dedup = l, d
	}

	// Snapshot now, and overwrite whatever `before` the caller sent. An
	// agent's view can be stale or simply wrong, and `before` is the
	// drift check's input — accepting it unverified would let a caller
	// disable the very guard that protects the human's edits.
	snapID := uuid.Nil
	if targetID != nil {
		snapID = *targetID
	}
	current, snapLabel, err := target.snapshot(h, r.Context(), orgID, snapID)
	if err != nil {
		return proposals.Proposal{}, http.StatusNotFound, errors.New("target not found")
	}
	if label == "" {
		label = snapLabel
	}
	changes := make([]proposals.Change, 0, len(in))
	for _, c := range in {
		before, ok := current[c.Field]
		if !ok {
			return proposals.Proposal{}, http.StatusBadRequest, errors.New("field unavailable on this target: " + c.Field)
		}
		changes = append(changes, proposals.Change{Field: c.Field, Before: before, After: c.After})
	}

	p := proposals.Proposal{
		OrgID:       orgID,
		TargetKind:  kind,
		TargetID:    targetID,
		TargetLabel: label,
		Changes:     changes,
		Rationale:   strings.TrimSpace(rationale),
		DedupKey:    dedup,
	}
	pr := middleware.Principal(r)
	if pr.ServiceAccountID != nil {
		p.ProposedByKind, p.ProposedByID, p.Via = "service_account", pr.ServiceAccountID, "mcp"
	} else {
		p.ProposedByKind, p.ProposedByID, p.Via = "user", pr.UserID, "api"
	}
	p.ProposedByLabel = h.actorLabel(r)
	return p, 0, nil
}

// proposalTTL is the org's review window. Config lands with the settings
// surface; until then every cell uses the documented default.
func (h *Handlers) proposalTTL(_ context.Context, _ uuid.UUID) time.Duration {
	return proposals.DefaultTTL
}

// actorLabel is a best-effort human name for the proposer, snapshotted so
// the inbox stays readable after a token is revoked.
func (h *Handlers) actorLabel(r *http.Request) string {
	p := middleware.Principal(r)
	if p.ServiceAccountID != nil {
		if h.Identity != nil {
			if sa, err := h.Identity.GetServiceAccount(r.Context(), *p.ServiceAccountID); err == nil {
				return sa.Name
			}
		}
		return "service account"
	}
	if p.UserID != nil && h.Identity != nil {
		if u, err := h.Identity.GetUserByID(r.Context(), *p.UserID); err == nil {
			return u.Email
		}
	}
	return "unknown"
}

// ── read ─────────────────────────────────────────────────────────────

// listProposals: GET /api/v1/proposals?state=pending&limit=50
func (h *Handlers) listProposals(w http.ResponseWriter, r *http.Request) {
	if h.Proposals == nil {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"proposals": []any{}, "pending_count": 0})
		return
	}
	orgID := middleware.OrgID(r)
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	list, err := h.Proposals.List(r.Context(), orgID, state, limit)
	if err != nil {
		h.Logger.Error("list proposals failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	pending, err := h.Proposals.PendingCount(r.Context(), orgID)
	if err != nil {
		h.Logger.Warn("proposal pending count failed", "err", err)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"proposals": list, "pending_count": pending})
}

// getProposal: GET /api/v1/proposals/{id}
//
// Returns the stored diff alongside the target's values NOW, so the
// reviewer sees drift before deciding rather than discovering it in the
// approve error.
func (h *Handlers) getProposal(w http.ResponseWriter, r *http.Request) {
	if h.Proposals == nil {
		httpserver.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	orgID := middleware.OrgID(r)
	p, err := h.Proposals.Get(r.Context(), orgID, id)
	if errors.Is(err, proposals.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	resp := map[string]any{"proposal": p}
	if t, ok := proposalTargets[p.TargetKind]; ok && (p.TargetID != nil || !t.needsTarget) && p.Pending() {
		snapID := uuid.Nil
		if p.TargetID != nil {
			snapID = *p.TargetID
		}
		if current, _, sErr := t.snapshot(h, r.Context(), orgID, snapID); sErr == nil {
			resp["drifted_fields"] = proposals.CheckDrift(p.Changes, current)
		} else {
			resp["target_missing"] = true
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

// ── decide ───────────────────────────────────────────────────────────

type decisionBody struct {
	Note string `json:"note"`
	// Force approves despite drift. Deliberately explicit: the reviewer
	// is overriding somebody else's edit and should have to say so.
	Force bool `json:"force"`
}

// approveProposal: POST /api/v1/proposals/{id}/approve
func (h *Handlers) approveProposal(w http.ResponseWriter, r *http.Request) {
	h.decideProposal(w, r, "approved")
}

// rejectProposal: POST /api/v1/proposals/{id}/reject
func (h *Handlers) rejectProposal(w http.ResponseWriter, r *http.Request) {
	h.decideProposal(w, r, "rejected")
}

func (h *Handlers) decideProposal(w http.ResponseWriter, r *http.Request, decision string) {
	if h.Proposals == nil {
		httpserver.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	var body decisionBody
	_ = json.NewDecoder(r.Body).Decode(&body) // body is optional

	orgID := middleware.OrgID(r)
	p, err := h.Proposals.Get(r.Context(), orgID, id)
	if errors.Is(err, proposals.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !p.Pending() {
		httpserver.WriteError(w, http.StatusConflict, "this proposal is already "+p.State)
		return
	}

	// Approving MUTATES, so the drift check runs first and applying comes
	// before the state flip: a proposal recorded as approved whose apply
	// failed would be a lie in the audit trail.
	if decision == "approved" {
		if drifted, err := h.checkProposal(r, p); err != nil {
			if len(drifted) > 0 && !body.Force {
				httpserver.WriteJSON(w, http.StatusConflict, map[string]any{
					"error":          err.Error(),
					"drifted_fields": drifted,
					"hint":           "re-read the current values and approve with force=true to override, or reject and let the agent re-propose",
				})
				return
			}
			if len(drifted) == 0 {
				httpserver.WriteError(w, http.StatusConflict, err.Error())
				return
			}
		}
		if aErr := proposalTargets[p.TargetKind].apply(h, r, p.TargetID, p.Changes); aErr != nil {
			h.Logger.Error("apply proposal failed", "err", aErr, "proposal", id)
			httpserver.WriteError(w, http.StatusBadRequest, "could not apply: "+aErr.Error())
			return
		}
	}

	decidedBy := uuid.Nil
	if pr := middleware.Principal(r); pr.UserID != nil {
		decidedBy = *pr.UserID
	}
	out, err := h.Proposals.Decide(r.Context(), orgID, id, decidedBy, decision, strings.TrimSpace(body.Note))
	if errors.Is(err, proposals.ErrNotPending) {
		// Lost a race with another reviewer. The apply above may already
		// have run, which is why this is reported rather than swallowed.
		httpserver.WriteError(w, http.StatusConflict, "another reviewer decided this proposal first")
		return
	}
	if err != nil {
		h.Logger.Error("decide proposal failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "proposal."+decision, "proposal", out.ID.String(), map[string]any{
		"target_kind": out.TargetKind, "target_label": out.TargetLabel,
		"forced": body.Force, "via": out.Via,
	})
	httpserver.WriteJSON(w, http.StatusOK, out)
}

// errProposalDrift is checkProposal's answer when the target changed.
var errProposalDrift = errors.New("the target changed since this was proposed")

// checkProposal is what approving needs to know before it writes: that
// the kind is known, the target still exists, and nothing proposed was
// edited since. It returns the drifted fields with errProposalDrift, or
// another error the approval cannot get past.
func (h *Handlers) checkProposal(r *http.Request, p proposals.Proposal) ([]string, error) {
	target, ok := proposalTargets[p.TargetKind]
	if !ok {
		return nil, errors.New("unsupported target_kind: " + p.TargetKind)
	}
	if p.TargetID == nil && target.needsTarget {
		return nil, errors.New("proposal has no target")
	}
	snapID := uuid.Nil
	if p.TargetID != nil {
		snapID = *p.TargetID
	}
	current, _, err := target.snapshot(h, r.Context(), middleware.OrgID(r), snapID)
	if err != nil {
		return nil, errors.New("the target no longer exists")
	}
	if drifted := proposals.CheckDrift(p.Changes, current); len(drifted) > 0 {
		return drifted, errProposalDrift
	}
	return nil, nil
}

// checkAlertRuleTuning refuses a tuning the apply would refuse, when it is
// filed: a channel that does not exist, an environment scope the check
// cannot have. The values are checked against the rule as it is now,
// merged with the proposed ones.
func checkAlertRuleTuning(h *Handlers, r *http.Request, id *uuid.UUID, changes []proposals.Change) (string, string, error) {
	rule, err := h.Alerts.GetRule(r.Context(), middleware.OrgID(r), *id)
	if err != nil {
		return "", "", errors.New("target not found")
	}
	for _, c := range changes {
		switch c.Field {
		case "channel_ids":
			ids, err := proposedChannelIDs(c.After)
			if err != nil {
				return "", "", err
			}
			for _, cid := range ids {
				if _, err := h.Alerts.GetChannel(r.Context(), middleware.OrgID(r), cid); err != nil {
					return "", "", fmt.Errorf("channel_ids: no channel %s (sluicio_list_notification_channels lists them)", cid)
				}
			}
		case "environment_mode":
			var v string
			if err := json.Unmarshal(c.After, &v); err != nil || (v != alerting.EnvironmentEach && v != alerting.EnvironmentOnly) {
				return "", "", errors.New(`environment_mode: "each" or "only"`)
			}
			rule.EnvironmentMode = v
		case "environments":
			var v []string
			if err := json.Unmarshal(c.After, &v); err != nil {
				return "", "", errors.New("environments: a list of environment names")
			}
			rule.Environments = normalizeEnvironments(v)
		case "severity":
			var v string
			if err := json.Unmarshal(c.After, &v); err != nil || !alerting.ValidSeverity(alerting.Severity(v)) {
				return "", "", errors.New("severity: info, warning or critical")
			}
		case "for_window":
			var v string
			if err := json.Unmarshal(c.After, &v); err != nil {
				return "", "", errors.New("for_window: a duration such as \"5m\"")
			}
			if _, err := time.ParseDuration(v); err != nil {
				return "", "", fmt.Errorf("for_window: %q is not a duration", v)
			}
		}
	}
	if environmentModeName(rule.EnvironmentMode) == alerting.EnvironmentOnly {
		if err := checkEnvironmentScope(rule); err != nil {
			return "", "", err
		}
	}
	return "", "", nil
}

// proposedChannelIDs reads a proposed channel list. An empty list is a
// value of its own: the check then follows its notification profile.
func proposedChannelIDs(raw json.RawMessage) ([]uuid.UUID, error) {
	var v []string
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errors.New("channel_ids: a list of channel ids, or [] to follow the notification profile")
	}
	ids, err := parseChannelIDs(v)
	if err != nil {
		return nil, fmt.Errorf("channel_ids: %w", err)
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return ids, nil
}

// environmentModeName is the mode as stored and shown: "" reads as each.
func environmentModeName(m string) string {
	if m == "" {
		return alerting.EnvironmentEach
	}
	return m
}

// normalizeEnvironments trims, dedups and sorts a list of environments,
// as buildRule does for a check written by hand.
func normalizeEnvironments(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// checkEnvironmentScope is buildRule's rule for "only": a check bound to
// an integration, with at least one environment.
func checkEnvironmentScope(r alerting.AlertRule) error {
	if r.IntegrationID == nil {
		return errors.New("environment_mode only applies to a check bound to an integration")
	}
	if len(r.Environments) == 0 {
		return errors.New(`environment_mode "only" needs at least one environment`)
	}
	for _, e := range r.Environments {
		if len(e) > 128 {
			return errors.New("environment name too long (max 128)")
		}
	}
	return nil
}
