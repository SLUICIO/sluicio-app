// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Proposal batches: several changes an agent files together under one
// rationale, reviewed and approved as one. Setting up a cell's alerting
// is two dozen near-identical changes (channels on every check, the
// start span of every completion rule), and one approval each made the
// reviewer the bottleneck and invited hand-edits instead.
//
// Every item is an ordinary proposal of an existing kind, checked,
// snapshotted, drift-checked and applied by that kind's own rules, so a
// batch can do nothing a single proposal could not. What the batch adds:
//
//   - Filing is all or nothing. Every item is checked before any is
//     stored, and they are stored in one transaction.
//   - Approving checks every item's target for drift before writing any.
//   - The items are then applied in the order the agent gave, each
//     through its own route. That is not one database transaction (an
//     item may be a check, a completion rule or an integration's
//     matchers, each with its own route and audit entry), so a failure
//     stops the batch there: the items before it stay applied and
//     approved, the failed one and those after it stay pending, and the
//     answer says exactly which is which.
//   - Items can still be decided one by one from the inbox.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
)

// MaxProposalBatch bounds one batch: enough for a cell's checks, small
// enough to review.
const MaxProposalBatch = 100

type proposalBatchItem struct {
	TargetKind string             `json:"target_kind"`
	TargetID   string             `json:"target_id"`
	Changes    []proposals.Change `json:"changes"`
}

type proposalBatchBody struct {
	Rationale string              `json:"rationale"`
	Items     []proposalBatchItem `json:"items"`
}

// createProposalBatch: POST /api/v1/proposals/batch
func (h *Handlers) createProposalBatch(w http.ResponseWriter, r *http.Request) {
	if h.Proposals == nil {
		httpserver.WriteError(w, http.StatusNotImplemented, "proposals are not enabled on this cell")
		return
	}
	var body proposalBatchBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Rationale) == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "rationale is required — a proposal without a reason cannot be reviewed")
		return
	}
	if len(body.Items) == 0 {
		httpserver.WriteError(w, http.StatusBadRequest, "at least one item is required")
		return
	}
	if len(body.Items) > MaxProposalBatch {
		httpserver.WriteError(w, http.StatusBadRequest, fmt.Sprintf("a batch holds at most %d items", MaxProposalBatch))
		return
	}
	items := make([]proposals.Proposal, 0, len(body.Items))
	targets := map[string]int{}
	dedups := map[string]int{}
	for i, it := range body.Items {
		p, status, err := h.buildProposal(r, it.TargetKind, it.TargetID, it.Changes, body.Rationale)
		if err != nil {
			httpserver.WriteError(w, status, fmt.Sprintf("item %d: %s", i+1, err.Error()))
			return
		}
		// Two updates of one target would supersede each other inside the
		// batch; two identical creates would collide. Either way the agent
		// meant one change.
		if p.DedupKey != "" {
			if j, dup := dedups[p.DedupKey]; dup {
				httpserver.WriteError(w, http.StatusBadRequest, fmt.Sprintf("items %d and %d propose the same thing", j+1, i+1))
				return
			}
			dedups[p.DedupKey] = i
		} else if p.TargetID != nil {
			k := p.TargetKind + ":" + p.TargetID.String()
			if j, dup := targets[k]; dup {
				httpserver.WriteError(w, http.StatusBadRequest, fmt.Sprintf("items %d and %d change the same target: put their changes in one item", j+1, i+1))
				return
			}
			targets[k] = i
		}
		items = append(items, p)
	}
	out, err := h.Proposals.CreateBatch(r.Context(), items, h.proposalTTL(r.Context(), middleware.OrgID(r)))
	if errors.Is(err, proposals.ErrDuplicatePending) {
		httpserver.WriteError(w, http.StatusConflict, "an item is already waiting for review as its own proposal")
		return
	}
	if err != nil {
		h.Logger.Error("create proposal batch failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	for _, p := range out {
		h.recordAudit(r, "proposal.created", "proposal", p.ID.String(), map[string]any{
			"target_kind": p.TargetKind, "target_label": p.TargetLabel, "via": p.Via, "batch_id": p.BatchID,
		})
	}
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{"batch_id": out[0].BatchID, "proposals": out})
}

// batchItemDrift is one item a batch approval stopped on before writing.
type batchItemDrift struct {
	ID            uuid.UUID `json:"id"`
	TargetLabel   string    `json:"target_label"`
	DriftedFields []string  `json:"drifted_fields,omitempty"`
	Error         string    `json:"error"`
}

// approveProposalBatch: POST /api/v1/proposals/batches/{id}/approve
func (h *Handlers) approveProposalBatch(w http.ResponseWriter, r *http.Request) {
	h.decideProposalBatch(w, r, "approved")
}

// rejectProposalBatch: POST /api/v1/proposals/batches/{id}/reject
func (h *Handlers) rejectProposalBatch(w http.ResponseWriter, r *http.Request) {
	h.decideProposalBatch(w, r, "rejected")
}

func (h *Handlers) decideProposalBatch(w http.ResponseWriter, r *http.Request, decision string) {
	if h.Proposals == nil {
		httpserver.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	batchID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid batch id")
		return
	}
	var body decisionBody
	_ = json.NewDecoder(r.Body).Decode(&body) // body is optional
	orgID := middleware.OrgID(r)
	all, err := h.Proposals.ListBatch(r.Context(), orgID, batchID)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if len(all) == 0 {
		httpserver.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	pending := make([]proposals.Proposal, 0, len(all))
	for _, p := range all {
		if p.Pending() {
			pending = append(pending, p)
		}
	}
	if len(pending) == 0 {
		httpserver.WriteError(w, http.StatusConflict, "nothing in this batch is pending")
		return
	}
	decidedBy := uuid.Nil
	if pr := middleware.Principal(r); pr.UserID != nil {
		decidedBy = *pr.UserID
	}
	note := strings.TrimSpace(body.Note)

	if decision == "rejected" {
		rejected := []uuid.UUID{}
		for _, p := range pending {
			out, err := h.Proposals.Decide(r.Context(), orgID, p.ID, decidedBy, "rejected", note)
			if err != nil {
				continue // decided by someone else meanwhile
			}
			rejected = append(rejected, out.ID)
			h.recordAudit(r, "proposal.rejected", "proposal", out.ID.String(), map[string]any{
				"target_kind": out.TargetKind, "target_label": out.TargetLabel, "via": out.Via, "batch_id": batchID,
			})
		}
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"batch_id": batchID, "rejected": rejected})
		return
	}

	// Every target is checked before anything is written, so a drifted
	// item stops the batch while it is still whole.
	var blocked []batchItemDrift
	for _, p := range pending {
		drifted, err := h.checkProposal(r, p)
		if err == nil || (len(drifted) > 0 && body.Force) {
			continue
		}
		blocked = append(blocked, batchItemDrift{ID: p.ID, TargetLabel: p.TargetLabel, DriftedFields: drifted, Error: err.Error()})
	}
	if len(blocked) > 0 {
		resp := map[string]any{"error": "some items cannot be applied as proposed; nothing was applied", "items": blocked}
		if body.Force {
			resp["hint"] = "the items listed have no target any more: reject them, then approve the rest"
		} else {
			resp["hint"] = "re-read the items listed, then approve with force=true to override the newer edits, or reject the batch"
		}
		httpserver.WriteJSON(w, http.StatusConflict, resp)
		return
	}

	applied := []uuid.UUID{}
	for i, p := range pending {
		if aErr := proposalTargets[p.TargetKind].apply(h, r, p.TargetID, p.Changes); aErr != nil {
			h.Logger.Error("apply batch item failed", "err", aErr, "proposal", p.ID, "batch", batchID)
			remaining := make([]uuid.UUID, 0, len(pending)-i)
			for _, rest := range pending[i:] {
				remaining = append(remaining, rest.ID)
			}
			httpserver.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"error":     fmt.Sprintf("could not apply %s: %s", p.TargetLabel, aErr.Error()),
				"applied":   applied,
				"failed":    p.ID,
				"remaining": remaining,
				"hint":      "the items applied stay applied; the failed one and those after it are still pending",
			})
			return
		}
		out, err := h.Proposals.Decide(r.Context(), orgID, p.ID, decidedBy, "approved", note)
		if err != nil && !errors.Is(err, proposals.ErrNotPending) {
			h.Logger.Error("decide batch item failed", "err", err, "proposal", p.ID)
		}
		applied = append(applied, p.ID)
		if err == nil {
			h.recordAudit(r, "proposal.approved", "proposal", out.ID.String(), map[string]any{
				"target_kind": out.TargetKind, "target_label": out.TargetLabel,
				"forced": body.Force, "via": out.Via, "batch_id": batchID,
			})
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"batch_id": batchID, "applied": applied})
}
