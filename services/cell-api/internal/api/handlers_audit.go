// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/audit"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/license"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// recordAudit appends one audit entry for a request, attributing it to the
// caller. It is a no-op unless the audit_log entitlement is active and the
// store is wired — so core builds running without a license simply don't
// audit. Best-effort: a write failure is logged, never surfaced to the
// caller, and never blocks the action being audited.
func (h *Handlers) recordAudit(r *http.Request, action, targetType, targetID string, metadata map[string]any) {
	if h.Audit == nil || !h.featureEntitled(license.FeatureAuditLog) {
		return
	}
	p := middleware.Principal(r)
	if err := h.Audit.Record(r.Context(), audit.Entry{
		OrgID:       p.OrgID,
		ActorUserID: p.UserID,
		ActorName:   p.Name,
		ActorEmail:  p.Email,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		Metadata:    metadata,
		IP:          clientIP(r),
	}); err != nil {
		h.Logger.Warn("audit record failed", "err", err, "action", action)
	}
}

// recordAuditInOrg is recordAudit with an explicit org: the entry lands in
// orgID's log instead of the caller's active org. Used for cross-org writes.
func (h *Handlers) recordAuditInOrg(r *http.Request, orgID uuid.UUID, action, targetType, targetID string, metadata map[string]any) {
	if h.Audit == nil || !h.featureEntitled(license.FeatureAuditLog) {
		return
	}
	p := middleware.Principal(r)
	if err := h.Audit.Record(r.Context(), audit.Entry{
		OrgID:       orgID,
		ActorUserID: p.UserID,
		ActorName:   p.Name,
		ActorEmail:  p.Email,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		Metadata:    metadata,
		IP:          clientIP(r),
	}); err != nil {
		h.Logger.Warn("audit record failed", "err", err, "action", action)
	}
}

// recordOperatorAudit writes an operator action to the operator's own org
// log and — when the action targets a different org — to that org's log
// too. Without the second write, a tenant's admins would never see that a
// cell operator changed their org or membership.
func (h *Handlers) recordOperatorAudit(r *http.Request, targetOrg uuid.UUID, action, targetType, targetID string, metadata map[string]any) {
	h.recordAudit(r, action, targetType, targetID, metadata)
	if targetOrg != uuid.Nil && targetOrg != middleware.OrgID(r) {
		h.recordAuditInOrg(r, targetOrg, action, targetType, targetID, metadata)
	}
}

// recordAuthAudit is recordAudit for the auth surface, where no principal
// is resolved yet (login, password reset) or the event isn't tied to the
// active org. Auth events are written once per org the user belongs to:
// each org's admins have a legitimate interest in their members' sign-in
// history, and an account-level event would otherwise be invisible to all
// of them. memberships may be nil — it's looked up best-effort.
func (h *Handlers) recordAuthAudit(ctx context.Context, user identity.User, memberships []identity.Membership, action, ip string, metadata map[string]any) {
	if h.Audit == nil || !h.featureEntitled(license.FeatureAuditLog) {
		return
	}
	if memberships == nil {
		var err error
		memberships, err = h.Identity.ListMemberships(ctx, user.ID)
		if err != nil {
			h.Logger.Warn("audit: list memberships failed", "err", err, "action", action)
			return
		}
	}
	uid := user.ID
	for _, m := range memberships {
		if err := h.Audit.Record(ctx, audit.Entry{
			OrgID:       m.Org.ID,
			ActorUserID: &uid,
			ActorName:   user.Name,
			ActorEmail:  user.Email,
			Action:      action,
			TargetType:  "user",
			TargetID:    user.ID.String(),
			Metadata:    metadata,
			IP:          ip,
		}); err != nil {
			h.Logger.Warn("audit record failed", "err", err, "action", action)
		}
	}
}

// clientIP extracts the best-guess client address: the first hop in
// X-Forwarded-For when present (we sit behind a reverse proxy in prod),
// else the connection's RemoteAddr without the port.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// listAuditLog: GET /api/v1/audit-log — Enterprise + admin gated (composed
// at the route). Returns an org's recent audit entries, newest first.
//
// Query params (all optional, combinable):
//
//	limit, before        — page size + keyset cursor (id < before)
//	actor                — substring of actor name OR email, case-insensitive
//	actor_id             — exact actor user UUID
//	action               — action prefix ("member." matches member.added …)
//	target_type, target  — exact resource match
//	from, to             — RFC3339 bounds on occurred_at (from ≤ t < to)
//
// Together these answer "what did user X do between 8 and 10" directly:
// ?actor=x@example.com&from=…T08:00:00Z&to=…T10:00:00Z.
func (h *Handlers) listAuditLog(w http.ResponseWriter, r *http.Request) {
	if h.Audit == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "audit store unavailable")
		return
	}
	qp := r.URL.Query()
	limit := 100
	if v := qp.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	var before int64
	if v := qp.Get("before"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			before = n
		}
	}
	f := audit.Filter{
		ActorQ:     strings.TrimSpace(qp.Get("actor")),
		Action:     strings.TrimSpace(qp.Get("action")),
		TargetType: strings.TrimSpace(qp.Get("target_type")),
		TargetID:   strings.TrimSpace(qp.Get("target")),
	}
	if v := qp.Get("actor_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "actor_id must be a UUID")
			return
		}
		f.ActorUserID = &id
	}
	for _, b := range []struct {
		key string
		dst *time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if v := qp.Get(b.key); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				httpserver.WriteError(w, http.StatusBadRequest, b.key+" must be RFC3339 (e.g. 2026-07-03T08:00:00Z)")
				return
			}
			*b.dst = t
		}
	}
	if qp.Get("format") == "csv" {
		// Exports always leave a trace: pulling the whole log is a
		// security-relevant act in itself.
		h.recordAudit(r, "audit_log.exported", "audit_log", "", exportAuditMeta(f))
		h.exportAuditCSV(w, r, f)
		return
	}
	h.recordAuditViewed(r)
	entries, err := h.Audit.List(r.Context(), middleware.OrgID(r), f, limit, before)
	if err != nil {
		h.Logger.Error("list audit log failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// exportAuditMeta captures which slice of the log was exported — without
// it, "exported the audit log" says nothing about how much left the cell.
func exportAuditMeta(f audit.Filter) map[string]any {
	m := map[string]any{}
	if f.ActorQ != "" {
		m["actor"] = f.ActorQ
	}
	if f.Action != "" {
		m["action"] = f.Action
	}
	if !f.From.IsZero() {
		m["from"] = f.From.Format(time.RFC3339)
	}
	if !f.To.IsZero() {
		m["to"] = f.To.Format(time.RFC3339)
	}
	if len(m) == 0 {
		m["scope"] = "unfiltered"
	}
	return m
}

// auditViewThrottle bounds audit_log.viewed noise: reading the log is
// worth knowing about (SOC 2 asks who looked at the evidence), but one
// entry per filter keystroke would drown the log in itself. One entry
// per admin per org per hour. In-memory is fine — cell-api is single-
// replica by deployment constraint, and losing the throttle state on
// restart just means one extra entry.
const auditViewWindow = time.Hour

func (h *Handlers) recordAuditViewed(r *http.Request) {
	p := middleware.Principal(r)
	if p.UserID == nil {
		return
	}
	key := p.UserID.String() + "/" + p.OrgID.String()
	now := time.Now()
	h.auditViewMu.Lock()
	if h.auditViewSeen == nil {
		h.auditViewSeen = make(map[string]time.Time)
	}
	last, seen := h.auditViewSeen[key]
	if seen && now.Sub(last) < auditViewWindow {
		h.auditViewMu.Unlock()
		return
	}
	h.auditViewSeen[key] = now
	// Opportunistic sweep so the map can't grow unbounded across months.
	for k, t := range h.auditViewSeen {
		if now.Sub(t) > 2*auditViewWindow {
			delete(h.auditViewSeen, k)
		}
	}
	h.auditViewMu.Unlock()
	h.recordAudit(r, "audit_log.viewed", "audit_log", "", nil)
}

// verifyAuditChain: GET /api/v1/audit-log/verify — Enterprise + admin gated
// (composed at the route). Walks the org's hash chain and reports whether
// every entry's content hash and prev-link still hold. Legacy entries from
// before chaining shipped are counted separately, not failed.
func (h *Handlers) verifyAuditChain(w http.ResponseWriter, r *http.Request) {
	if h.Audit == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "audit store unavailable")
		return
	}
	res, err := h.Audit.Verify(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("audit chain verify failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "verification failed")
		return
	}
	if !res.OK {
		h.Logger.Warn("audit chain verification FAILED",
			"org", middleware.OrgID(r), "first_broken_id", res.FirstBrokenID, "detail", res.Detail)
	}
	httpserver.WriteJSON(w, http.StatusOK, res)
}

// auditExportCap bounds a CSV export. Compliance pulls are time-boxed by
// the from/to filter; 50k rows of admin actions is months of history even
// on a busy cell, while still being a trivially small download.
const auditExportCap = 50_000

// exportAuditCSV streams the filtered audit entries as a CSV attachment,
// newest first, paging the store internally until done or capped.
func (h *Handlers) exportAuditCSV(w http.ResponseWriter, r *http.Request, f audit.Filter) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="sluicio-audit-log.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"id", "occurred_at", "actor_name", "actor_email", "actor_user_id",
		"action", "target_type", "target_id", "ip", "metadata",
	})
	var before int64
	written := 0
	for written < auditExportCap {
		page, err := h.Audit.List(r.Context(), middleware.OrgID(r), f, 500, before)
		if err != nil {
			// Headers are gone; the best we can do is truncate the stream.
			h.Logger.Error("audit csv export failed", "err", err, "written", written)
			break
		}
		for _, v := range page {
			actorID := ""
			if v.ActorUserID != nil {
				actorID = v.ActorUserID.String()
			}
			meta := ""
			if len(v.Metadata) > 0 {
				if b, err := json.Marshal(v.Metadata); err == nil {
					meta = string(b)
				}
			}
			_ = cw.Write([]string{
				strconv.FormatInt(v.ID, 10),
				v.CreatedAt.UTC().Format(time.RFC3339),
				v.ActorName, v.ActorEmail, actorID,
				v.Action, v.TargetType, v.TargetID, v.IP, meta,
			})
			written++
		}
		if len(page) < 500 {
			break
		}
		before = page[len(page)-1].ID
	}
	cw.Flush()
}
