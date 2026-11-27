// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Payload links: span attributes shown as links to a message's payload
// (the file picked up, the invoice posted). The organization lists the
// attributes; every message list shows one column per attribute when a
// message on the page carries it, and trace detail shows the link on every
// span. Sluicio never fetches an address: the reader's browser opens it
// with the reader's own access, so the archive decides who may see it.

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetmappings"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/payloadlinks"
)

// payloadLinkAttributes is the organization's list, or none when it
// cannot be read: a list without link columns is better than no list.
func (h *Handlers) payloadLinkAttributes(r *http.Request) []payloadlinks.Attribute {
	if h.PayloadLinks == nil {
		return nil
	}
	attrs, err := h.PayloadLinks.Get(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("payload links: read settings", "err", err)
		return nil
	}
	return attrs
}

// getPayloadLinks: GET /api/v1/settings/payload-links
func (h *Handlers) getPayloadLinks(w http.ResponseWriter, r *http.Request) {
	if h.PayloadLinks == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "payload links are not available")
		return
	}
	attrs, err := h.PayloadLinks.Get(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("get payload links failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"attributes": attrs})
}

// putPayloadLinks: PUT /api/v1/settings/payload-links  (admin, not demo)
func (h *Handlers) putPayloadLinks(w http.ResponseWriter, r *http.Request) {
	if h.PayloadLinks == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "payload links are not available")
		return
	}
	var body struct {
		Attributes []payloadlinks.Attribute `json:"attributes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	attrs, err := payloadlinks.Normalize(body.Attributes)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.PayloadLinks.Put(r.Context(), middleware.OrgID(r), attrs); err != nil {
		h.Logger.Error("put payload links failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "payload_links.update", "payload_links", middleware.OrgID(r).String(),
		map[string]any{"attributes": payloadlinks.Keys(attrs)})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"attributes": attrs})
}

// resolvePayloadLinks: POST /api/v1/payload-links {trace_ids, from, integration?}
//
// The payload-link columns of a message list, fetched after the list has
// rendered (like the completion column), so showing them never makes the
// list slower. With an integration, only the spans of its members count:
// the integration's list shows its own slice.
func (h *Handlers) resolvePayloadLinks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TraceIDs    []string  `json:"trace_ids"`
		From        time.Time `json:"from"`
		Integration string    `json:"integration,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.TraceIDs) > 1000 {
		httpserver.WriteError(w, http.StatusBadRequest, "at most 1000 trace ids")
		return
	}
	var integID uuid.UUID
	if ref := strings.TrimSpace(body.Integration); ref != "" {
		id, err := uuid.Parse(ref)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
			return
		}
		if _, ok := h.gateIntegrationMembers(w, r, id); !ok {
			return
		}
		integID = id
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"links": h.payloadLinksFor(r, body.TraceIDs, body.From, integID),
	})
}

// payloadLinksFor reads the links of the given traces, as far as the
// reader may see them. A failure leaves the messages without links rather
// than failing the caller: the messages are the answer.
func (h *Handlers) payloadLinksFor(r *http.Request, traceIDs []string, from time.Time, integID uuid.UUID) map[string]map[string]payloadlinks.Links {
	out := map[string]map[string]payloadlinks.Links{}
	attrs := h.payloadLinkAttributes(r)
	ids := make([]string, 0, len(traceIDs))
	for _, id := range traceIDs {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
			ids = append(ids, id)
		}
	}
	if len(attrs) == 0 || len(ids) == 0 || h.Store == nil {
		return out
	}
	// Never further back than the longest message window, whatever the
	// client sends.
	if floor := time.Now().Add(-45 * 24 * time.Hour); from.IsZero() || from.Before(floor) {
		from = floor
	}
	services, ok := h.payloadLinkServices(r, integID)
	if !ok {
		return out
	}
	// Received and sent come from io.role, with the facet mappings applied
	// as everywhere else, so a span classified by a mapping says so here too.
	roleExpr, roleArgs := h.payloadLinkRoleExpr(r)
	rows, err := h.Store.PayloadLinkSpans(r.Context(), ids, payloadlinks.Keys(attrs), services, from, roleExpr, roleArgs)
	if err != nil {
		h.Logger.Warn("payload links: read spans", "err", err)
		return out
	}
	spans := make([]payloadlinks.Span, len(rows))
	for i, row := range rows {
		spans[i] = payloadlinks.Span{
			TraceID: row.TraceID, SpanID: row.SpanID, ServiceName: row.ServiceName,
			SpanName: row.SpanName, Role: row.Role, Time: row.Time, Values: row.Values,
		}
	}
	return payloadlinks.Collect(attrs, spans)
}

// payloadLinkServices is the set of services whose spans may lend a
// message its links: the integration's members inside an integration,
// the reader's visible services for a reader who sees part of the estate,
// and every service (nil) otherwise. ok is false when the reader may see
// none, so the read is skipped rather than run unfiltered.
func (h *Handlers) payloadLinkServices(r *http.Request, integID uuid.UUID) ([]string, bool) {
	if integID != uuid.Nil {
		all, err := h.Catalog.IntegrationServices(r.Context(), integID)
		if err != nil {
			h.Logger.Warn("payload links: integration members", "err", err, "integration", integID)
			return nil, false
		}
		members, ok := h.visibleIntegrationMembers(r, integID, all)
		return members, ok && len(members) > 0
	}
	allowed, restricted := h.signalServiceFilter(r, identity.SignalTraces)
	if !restricted {
		return nil, true
	}
	return allowed, len(allowed) > 0
}

// payloadLinkRoleExpr is the effective io.role across the services of a
// page: each service's facet mappings, the raw attribute otherwise.
func (h *Handlers) payloadLinkRoleExpr(r *http.Request) (string, []any) {
	if h.FacetMappings == nil {
		return "", nil
	}
	mappings, err := h.FacetMappings.ListAll(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("payload links: facet mappings", "err", err)
		return "", nil
	}
	return facetmappings.RoleExprByService(mappings)
}
