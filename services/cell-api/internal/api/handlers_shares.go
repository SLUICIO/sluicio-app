// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Resource sharing (RBAC v2 phase 3, docs/rbac-v2-design.md §6):
//
//   GET    /api/v1/{integrations|systems}/{id}/shares
//   POST   /api/v1/{integrations|systems}/{id}/shares
//   DELETE /api/v1/{integrations|systems}/{id}/shares/{shareId}
//
// Viewer-only grants of a single integration or system to a user (by
// email) or a group. EE-gated (rbac_advanced). Who may share: org
// admins, or anyone whose managed scope covers the resource ("you can
// share what you can manage"). Creating a share notifies the grantee:
// a digest entry (handlers_digest.go) plus an email when SMTP is up.

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
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

type createShareBody struct {
	GranteeKind    string `json:"grantee_kind"`     // user | group
	GranteeEmail   string `json:"grantee_email"`    // for kind=user
	GranteeGroupID string `json:"grantee_group_id"` // for kind=group
}

// shareGate: EE + "you can share what you can manage".
func (h *Handlers) shareGate(w http.ResponseWriter, r *http.Request, kind identity.ShareResourceKind, resourceID uuid.UUID) bool {
	canManage := false
	switch kind {
	case identity.ShareIntegration:
		canManage = h.canManageIntegration(r, resourceID)
	case identity.ShareSystem:
		canManage = h.canManageSystem(r, resourceID)
	}
	if !canManage {
		httpserver.WriteError(w, http.StatusForbidden, "you can only share resources you manage")
		return false
	}
	return true
}

// resolveShareGrantee validates + resolves the request grantee to an id
// inside the caller's org. Users are addressed by email (no member-list
// exposure needed); groups by id.
func (h *Handlers) resolveShareGrantee(r *http.Request, body createShareBody) (kind string, id uuid.UUID, displayName, email string, err error) {
	orgID := middleware.OrgID(r)
	switch body.GranteeKind {
	case "user":
		addr := strings.TrimSpace(strings.ToLower(body.GranteeEmail))
		if addr == "" {
			return "", uuid.Nil, "", "", errors.New("grantee_email is required")
		}
		u, uerr := h.Identity.GetUserByEmail(r.Context(), addr)
		if uerr != nil {
			return "", uuid.Nil, "", "", errors.New("no such user in this organization")
		}
		memberships, merr := h.Identity.ListMemberships(r.Context(), u.ID)
		if merr != nil {
			return "", uuid.Nil, "", "", errors.New("could not verify membership")
		}
		member := false
		for _, m := range memberships {
			if m.Org.ID == orgID {
				member = true
				break
			}
		}
		if !member {
			return "", uuid.Nil, "", "", errors.New("no such user in this organization")
		}
		name := u.Name
		if name == "" {
			name = u.Email
		}
		return "user", u.ID, name, u.Email, nil
	case "group":
		gid, gerr := uuid.Parse(strings.TrimSpace(body.GranteeGroupID))
		if gerr != nil {
			return "", uuid.Nil, "", "", errors.New("grantee_group_id must be a UUID")
		}
		g, gerr2 := h.Identity.GetGroup(r.Context(), orgID, gid)
		if gerr2 != nil {
			return "", uuid.Nil, "", "", errors.New("no such group in this organization")
		}
		return "group", g.ID, g.Name, "", nil
	default:
		return "", uuid.Nil, "", "", errors.New("grantee_kind must be user or group")
	}
}

// notifyShare emails the grantee(s), best-effort on a detached context so
// SMTP latency never blocks the response. Group grantees fan out to
// member emails (capped — a huge team gets the digest entry regardless).
func (h *Handlers) notifyShare(sharer, resourceKind, resourceName string, granteeKind string, granteeID uuid.UUID, granteeEmail string) {
	if h.Mail == nil {
		return
	}
	const emailCap = 50
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if !h.Mail.Configured(ctx) {
			return
		}
		var to []string
		if granteeKind == "user" {
			to = []string{granteeEmail}
		} else {
			members, err := h.Identity.ListGroupMembers(ctx, granteeID)
			if err != nil {
				return
			}
			for i, m := range members {
				if i >= emailCap {
					break
				}
				// SA members have no mailbox — notify user members only.
				if m.User != nil && m.User.Email != "" {
					to = append(to, m.User.Email)
				}
			}
		}
		if len(to) == 0 {
			return
		}
		subject := fmt.Sprintf("%s shared a Sluicio %s with you: %s", sharer, resourceKind, resourceName)
		body := fmt.Sprintf(
			"%s shared the %s %q with you on Sluicio.\n\n"+
				"You can now view it — and its services' health, traces, logs and metrics — from your dashboard.",
			sharer, resourceKind, resourceName)
		if err := h.Mail.Send(ctx, to, subject, body); err != nil {
			h.Logger.Warn("share notification email failed", "err", err)
		}
	}()
}

// shareHandlers builds the GET/POST/DELETE trio for one resource kind so
// integrations and systems stay behaviorally identical.
func (h *Handlers) listShares(kind identity.ShareResourceKind, inOrg func(http.ResponseWriter, *http.Request, uuid.UUID) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parsePathUUID(w, r, "id")
		if !ok || !inOrg(w, r, id) || !h.shareGate(w, r, kind, id) {
			return
		}
		shares, err := h.Identity.ListSharesForResource(r.Context(), middleware.OrgID(r), kind, id)
		if err != nil {
			h.Logger.Error("list shares failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"shares": shares})
	}
}

func (h *Handlers) createShare(kind identity.ShareResourceKind, inOrg func(http.ResponseWriter, *http.Request, uuid.UUID) bool, resourceName func(*http.Request, uuid.UUID) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parsePathUUID(w, r, "id")
		if !ok || !inOrg(w, r, id) || !h.shareGate(w, r, kind, id) {
			return
		}
		var body createShareBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		gKind, gID, gName, gEmail, err := h.resolveShareGrantee(r, body)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		p := middleware.Principal(r)
		shareID, err := h.Identity.CreateShare(r.Context(), middleware.OrgID(r), kind, id, gKind, gID, p.UserID)
		if errors.Is(err, identity.ErrShareExists) {
			httpserver.WriteError(w, http.StatusConflict, "already shared with them")
			return
		}
		if err != nil {
			h.Logger.Error("create share failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "share failed")
			return
		}
		h.recordAudit(r, "share.created", string(kind), id.String(),
			map[string]any{"grantee_kind": gKind, "grantee": gName})
		sharer := p.Name
		if sharer == "" {
			sharer = p.Email
		}
		h.notifyShare(sharer, string(kind), resourceName(r, id), gKind, gID, gEmail)
		httpserver.WriteJSON(w, http.StatusCreated, map[string]any{"id": shareID})
	}
}

func (h *Handlers) deleteShare(kind identity.ShareResourceKind, inOrg func(http.ResponseWriter, *http.Request, uuid.UUID) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parsePathUUID(w, r, "id")
		if !ok || !inOrg(w, r, id) || !h.shareGate(w, r, kind, id) {
			return
		}
		shareID, err := uuid.Parse(r.PathValue("shareId"))
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid share id")
			return
		}
		if err := h.Identity.DeleteShare(r.Context(), middleware.OrgID(r), kind, id, shareID); err != nil {
			if errors.Is(err, identity.ErrNotFound) {
				httpserver.WriteError(w, http.StatusNotFound, "share not found")
				return
			}
			h.Logger.Error("delete share failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
			return
		}
		h.recordAudit(r, "share.revoked", string(kind), id.String(),
			map[string]any{"share_id": shareID.String()})
		w.WriteHeader(http.StatusNoContent)
	}
}

// integrationDisplayName / systemDisplayName back the notification text.
func (h *Handlers) integrationDisplayName(r *http.Request, id uuid.UUID) string {
	if ig, err := h.Integrations.Get(r.Context(), middleware.OrgID(r), id); err == nil {
		return ig.Integration.Name
	}
	return "integration"
}

func (h *Handlers) systemDisplayName(r *http.Request, id uuid.UUID) string {
	if sy, ok, err := h.Catalog.GetSystem(r.Context(), middleware.OrgID(r), id); err == nil && ok {
		return sy.Name
	}
	return "system"
}
