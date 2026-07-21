// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/messageviews"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// MessageViewWire is the JSON shape returned to the frontend. It
// mirrors the SavedView interface in the FilterEditor: id, name,
// filters[], plus presentation hints (mine, pinned, lastEditedAt,
// resultCount, scope).
type MessageViewWire struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Mine        bool                  `json:"mine"`
	Pinned      bool                  `json:"pinned"`
	Shared      bool                  `json:"shared"`
	Filters     []messageviews.Filter `json:"filters"`
	// Scope is always emitted (even when empty) so the frontend can
	// safely read view.scope.integrationId without checking presence.
	Scope        messageviews.Scope `json:"scope"`
	ResultCount  *int64             `json:"resultCount,omitempty"`
	LastEditedAt time.Time          `json:"lastEditedAt"`
	CreatedAt    time.Time          `json:"createdAt"`
	UpdatedAt    time.Time          `json:"updatedAt"`
}

// validateScope enforces the shape of a scope object on inbound
// requests: integration IDs must parse as UUIDs (the migration column
// is UUID-typed), service IDs are bounded in length. The store also
// silently drops malformed UUIDs, but rejecting at the API boundary
// gives the caller a clear 400 instead of a quiet drop to NULL.
func validateScope(s messageviews.Scope) error {
	if s.IntegrationID != "" {
		if _, err := uuid.Parse(s.IntegrationID); err != nil {
			return errors.New("scope.integrationId must be a UUID")
		}
	}
	if len(s.ServiceID) > 256 {
		return errors.New("scope.serviceId is too long")
	}
	return nil
}

func toWire(v messageviews.View) MessageViewWire {
	return MessageViewWire{
		ID:           v.ID.String(),
		Name:         v.Name,
		Description:  v.Description,
		Mine:         v.Mine,
		Pinned:       v.Pinned,
		Shared:       v.Shared,
		Filters:      v.Filters,
		Scope:        v.Scope,
		ResultCount:  v.ResultCount,
		LastEditedAt: v.LastEditedAt,
		CreatedAt:    v.CreatedAt,
		UpdatedAt:    v.UpdatedAt,
	}
}

// listMessageViews: GET /api/v1/message-views
func (h *Handlers) listMessageViews(w http.ResponseWriter, r *http.Request) {
	views, err := h.MessageViews.List(r.Context(), middleware.OrgID(r), nil)
	if err != nil {
		h.Logger.Error("list message views failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]MessageViewWire, 0, len(views))
	for _, v := range views {
		out = append(out, toWire(v))
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"views": out})
}

// getMessageView: GET /api/v1/message-views/{id}
func (h *Handlers) getMessageView(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid view id")
		return
	}
	v, err := h.MessageViews.Get(r.Context(), middleware.OrgID(r), id, nil)
	if errors.Is(err, messageviews.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "view not found")
		return
	}
	if err != nil {
		h.Logger.Error("get message view failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, toWire(v))
}

// createMessageView: POST /api/v1/message-views
func (h *Handlers) createMessageView(w http.ResponseWriter, r *http.Request) {
	var req messageviews.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 200 {
		httpserver.WriteError(w, http.StatusBadRequest, "name is too long")
		return
	}
	if err := validateScope(req.Scope); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	v, err := h.MessageViews.Create(r.Context(), middleware.OrgID(r), nil, req)
	if err != nil {
		// Surface validation errors as 400 — they come from
		// messageviews.ValidateAll.
		if strings.HasPrefix(err.Error(), "filter[") {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("create message view failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, toWire(v))
}

// updateMessageView: PUT /api/v1/message-views/{id}
func (h *Handlers) updateMessageView(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid view id")
		return
	}
	var req messageviews.UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 200 {
		httpserver.WriteError(w, http.StatusBadRequest, "name is too long")
		return
	}
	if err := validateScope(req.Scope); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	v, err := h.MessageViews.Update(r.Context(), middleware.OrgID(r), id, nil, req)
	if errors.Is(err, messageviews.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "view not found")
		return
	}
	if err != nil {
		if strings.HasPrefix(err.Error(), "filter[") {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("update message view failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, toWire(v))
}

// deleteMessageView: DELETE /api/v1/message-views/{id}
func (h *Handlers) deleteMessageView(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid view id")
		return
	}
	err = h.MessageViews.Delete(r.Context(), middleware.OrgID(r), id)
	if errors.Is(err, messageviews.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "view not found")
		return
	}
	if err != nil {
		h.Logger.Error("delete message view failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MessageFieldDescriptor is one entry returned by /messages/fields. It
// tells the frontend FieldPicker what fields the search engine
// understands, which operators each accepts, and (for closed-set
// fields) the enumerable values.
type MessageFieldDescriptor struct {
	Field       string `json:"field"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Operators is the closed list of operators the API will accept
	// for this field. Mirrors the picker's per-field hint.
	Operators []string `json:"operators"`
	// EnumValues lists the picker presets for closed-set fields
	// (status, time, …). Empty for open-text fields.
	EnumValues []string `json:"enumValues,omitempty"`
	// AttributeKeys lists the discovered keys for the payload field.
	// Each entry includes source ("span" / "resource") and a usage
	// count so the picker can sort by relevance.
	AttributeKeys []AttributeKeyInfo `json:"attributeKeys,omitempty"`
}

// AttributeKeyInfo is one entry in the dynamically-discovered list
// of payload attribute keys, surfaced to populate the FieldPicker's
// "payload field path" autocompletion.
type AttributeKeyInfo struct {
	Key      string `json:"key"`
	Source   string `json:"source"`
	UseCount uint64 `json:"useCount"`
}

// fieldsCatalog: GET /api/v1/messages/fields?range=24h
//
// Returns the field catalog the FilterEditor renders. Closed-set
// fields (status, time, integration) return their preset enums;
// payload returns the recently-seen attribute keys so users can pick
// one without typing.
func (h *Handlers) fieldsCatalog(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, 24*time.Hour)

	// Prefer the persisted attribute-key catalog (the reconciler keeps it
	// complete + current, so a rarely-emitted key isn't missed) and fall
	// back to a live ClickHouse sample only while it's still empty — a
	// fresh install before the first reconcile tick.
	var flat []AttributeKeyInfo
	if persisted, perr := h.Identity.ListAttributeKeys(r.Context(), middleware.OrgID(r)); perr == nil && len(persisted) > 0 {
		flat = make([]AttributeKeyInfo, 0, len(persisted))
		for _, k := range persisted {
			flat = append(flat, AttributeKeyInfo{Key: k.Key, Source: k.Source})
		}
	} else {
		keys, err := h.Store.DistinctAttributeKeys(r.Context(), tr.From, tr.To, 2000)
		if err != nil {
			h.Logger.Warn("distinct attribute keys failed", "err", err)
			keys = nil
		}
		// De-dup keys that show up in both maps; prefer the higher count.
		bySource := map[string]AttributeKeyInfo{}
		for _, k := range keys {
			existing, ok := bySource[k.Key]
			if !ok || k.UseCount > existing.UseCount {
				bySource[k.Key] = AttributeKeyInfo{Key: k.Key, Source: k.Source, UseCount: k.UseCount}
			}
		}
		flat = make([]AttributeKeyInfo, 0, len(bySource))
		for _, v := range bySource {
			flat = append(flat, v)
		}
		sort.Slice(flat, func(i, j int) bool {
			if flat[i].UseCount != flat[j].UseCount {
				return flat[i].UseCount > flat[j].UseCount
			}
			return flat[i].Key < flat[j].Key
		})
	}

	// List the integrations in the org for the integration field's
	// closed-set value picker.
	var integrationNames []string
	if ints, err := h.Integrations.List(r.Context(), middleware.OrgID(r)); err == nil {
		integrationNames = make([]string, 0, len(ints))
		for _, it := range ints {
			integrationNames = append(integrationNames, it.Name)
		}
	}

	// List service NAMES for the service field's value picker — the UI
	// filters services by name, never by id. Visibility-filtered so a
	// restricted caller only sees services they're allowed to.
	var serviceNames []string
	if svcs, err := h.Catalog.AllServices(r.Context(), middleware.OrgID(r)); err == nil {
		if filtered, ok := h.applyServiceVisibility(r, svcs); ok {
			svcs = filtered
		}
		serviceNames = make([]string, 0, len(svcs))
		for _, s := range svcs {
			serviceNames = append(serviceNames, s.ServiceName)
		}
		sort.Strings(serviceNames)
	}

	errorTypes, etErr := h.Store.DistinctErrorTypes(r.Context(), tr.From, tr.To, 50)
	if etErr != nil {
		h.Logger.Warn("distinct error types failed", "err", etErr)
		errorTypes = nil
	}

	descriptors := []MessageFieldDescriptor{
		{
			// Wire value stays "payload" (saved views + share URLs depend
			// on it); the LABEL says what it actually matches. Sluicio
			// stores no message payloads — deliberately.
			Field:         "payload",
			Label:         "attribute",
			Description:   "Match a value inside a specific span or resource attribute (e.g. orderId, http.route). Sluicio does not store message payloads.",
			Operators:     []string{"equals", "contains", "matches", "in"},
			AttributeKeys: flat,
		},
		{
			Field:       "time",
			Label:       "time",
			Description: "Restrict the search to a relative time window. Overrides the global window selector.",
			Operators:   []string{"is"},
			EnumValues:  []string{"last 15 minutes", "last 1 hour", "last 24 hours", "last 7 days"},
		},
		{
			Field:       "integration",
			Label:       "integration",
			Description: "Restrict to messages flowing through a specific integration.",
			Operators:   []string{"is", "in"},
			EnumValues:  integrationNames,
		},
		{
			Field:       "status",
			Label:       "status",
			Description: "Filter by the overall trace status.",
			Operators:   []string{"is"},
			EnumValues:  []string{"any (ok, warn, err)", "ok only", "err only", "warn or err"},
		},
		{
			Field:       "service",
			Label:       "service",
			Description: "Restrict to a specific service. Supports a comma-separated list with the in operator.",
			Operators:   []string{"is", "in"},
			EnumValues:  serviceNames,
		},
		{
			Field:       "errorType",
			Label:       "error type",
			Description: "Match the span's StatusMessage or exception.type attribute.",
			Operators:   []string{"equals", "contains", "matches", "in"},
			// Observed error identifiers in the window (not a static
			// vocabulary — whatever the telemetry emits), so the picker
			// offers real values instead of a blind text box. Free text
			// stays available for values outside the window.
			EnumValues: errorTypes,
		},
	}

	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"window": tr.Window(),
		"fields": descriptors,
	})
}

// searchMessages: POST /api/v1/messages/search
//
// Body shape (see messageviews.SearchRequest):
//
//	{
//	  "range": "1h",                     // optional; query string range= also accepted
//	  "limit": 200,                      // optional; default 200, cap 1000
//	  "filters": [
//	    {"field":"payload","fieldPath":"orderId","op":"equals","value":"1323"},
//	    {"field":"status","op":"is","value":"err only"},
//	    {"field":"time","op":"is","value":"last 24 hours"}
//	  ]
//	}
//
// Returns the same SearchResponse shape as /api/v1/search so the
// frontend's TraceSearchResult rendering stays a single code path.
func (h *Handlers) searchMessages(w http.ResponseWriter, r *http.Request) {
	var req messageviews.SearchRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	if err := messageviews.ValidateAll(req.Filters); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}

	// Range precedence: filter time > body range > URL range > default.
	tr := h.resolveRange(r, req)

	plan, err := messageviews.Build(req.Filters)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Resolve integration filter → service-name allowlist.
	serviceFilter := plan.ServiceNameLiterals
	if plan.IntegrationName != "" {
		names, err := h.resolveIntegrationServices(r.Context(), plan.IntegrationName, tr)
		if err != nil {
			h.Logger.Error("resolve integration filter failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		// An attribute-defined integration (producer=abc OR consumer=dce, …)
		// also AND-s its DNF attribute predicate onto the span search, so the
		// Messages view shows only the integration's slice of its services'
		// traffic.
		if id, ok := h.integrationIDByName(r.Context(), plan.IntegrationName); ok {
			if clause, cargs := store.SpanAttrGroupsClause(h.integrationGroups(r.Context(), id)); clause != "" {
				plan.Clauses = append(plan.Clauses, clause)
				plan.Args = append(plan.Args, cargs...)
			}
		}
		if len(serviceFilter) == 0 {
			serviceFilter = names
		} else {
			serviceFilter = intersectStrings(serviceFilter, names)
		}
		if len(serviceFilter) == 0 {
			// Integration matched nothing → empty result.
			httpserver.WriteJSON(w, http.StatusOK, SearchResponse{
				Window:  tr.Window(),
				Total:   0,
				Results: []TraceSearchResult{},
			})
			return
		}
	}

	// G5: enforce policy-based service visibility. Intersect the
	// existing serviceFilter (from filter literals + integration
	// expansion) with what the caller is allowed to see.
	pf := h.resolveServiceFilterSignal(r, "", serviceFilter, identity.SignalMessages)
	if pf.Blocked || pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, SearchResponse{
			Window:  tr.Window(),
			Total:   0,
			Results: []TraceSearchResult{},
		})
		return
	}
	serviceFilter = pf.ServiceIn

	rows, err := h.Store.SearchMessages(r.Context(), store.MessagesSearchParams{
		From:          tr.From,
		To:            tr.To,
		Limit:         limit,
		ServiceFilter: serviceFilter,
		OnlyFailed:    plan.OnlyFailed,
		StatusOK:      plan.StatusOK,
		Clauses:       plan.Clauses,
		Args:          plan.Args,
		Before:        parseMessageCursor(req.Cursor),
	})
	if err != nil {
		h.Logger.Error("messages search failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	results := make([]TraceSearchResult, 0, len(rows))
	for _, t := range rows {
		results = append(results, TraceSearchResult{
			TraceID:         t.TraceID,
			TraceStart:      t.TraceStart,
			DurationMs:      t.DurationMs,
			HasError:        t.HasError,
			TotalSpans:      t.TotalSpans,
			ServiceCount:    t.ServiceCount,
			MatchedService:  t.MatchedService,
			MatchedSpanName: t.MatchedSpanName,
			Attributes:      mergeAttributes(t.MatchedResourceAttrs, t.MatchedSpanAttrs),
		})
	}

	httpserver.WriteJSON(w, http.StatusOK, SearchResponse{
		Window:     tr.Window(),
		Total:      len(results),
		Results:    results,
		NextCursor: nextMessageCursor(rows, limit),
	})
}

// parseMessageCursor turns the request's keyset cursor into the store
// form, or nil when absent/unparseable (treated as "first page").
func parseMessageCursor(c *messageviews.SearchCursor) *store.MessageCursor {
	if c == nil {
		return nil
	}
	ts := strings.TrimSpace(c.TS)
	id := strings.TrimSpace(c.ID)
	if ts == "" || id == "" {
		return nil
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil
	}
	return &store.MessageCursor{LatestMatchNano: n, TraceID: id}
}

// nextMessageCursor builds the cursor for the following page from the
// last row, or nil when this page wasn't full (no more rows).
func nextMessageCursor(rows []store.SearchTraceRow, limit int) *MessageCursorJSON {
	if limit <= 0 || len(rows) < limit {
		return nil
	}
	last := rows[len(rows)-1]
	return &MessageCursorJSON{
		TS: strconv.FormatInt(last.LatestMatch.UnixNano(), 10),
		ID: last.TraceID,
	}
}

// resolveRange picks the most specific time window: a `time` filter
// preset in the body, an explicit range in the body, or the URL
// query-param range; falls back to 1h.
func (h *Handlers) resolveRange(r *http.Request, req messageviews.SearchRequest) TimeRange {
	// Body filter preset wins.
	for _, f := range req.Filters {
		if f.Field == messageviews.FieldTime {
			if preset := durationFromPreset(f.Value); preset != "" {
				return rangeFromRelative(preset)
			}
		}
	}
	if strings.TrimSpace(req.Range) != "" {
		// Reuse the URL parser by stamping the preset onto a cloned
		// request — keeps a single code path for the duration grammar.
		stub := r.Clone(r.Context())
		q := stub.URL.Query()
		q.Set("range", req.Range)
		stub.URL.RawQuery = q.Encode()
		return ParseRange(stub, time.Hour)
	}
	return ParseRange(r, time.Hour)
}

func durationFromPreset(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, "last ")
	switch v {
	case "15 minutes", "15m":
		return "15m"
	case "1 hour", "1h":
		return "1h"
	case "24 hours", "24h", "1 day":
		return "24h"
	case "7 days", "7d", "1 week":
		return "168h"
	}
	return ""
}

func rangeFromRelative(s string) TimeRange {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		now := time.Now().UTC()
		return TimeRange{From: now.Add(-time.Hour), To: now}
	}
	now := time.Now().UTC()
	return TimeRange{From: now.Add(-d), To: now}
}

// resolveIntegrationServices looks up the integration by name (the UI
// stores names, not IDs, in the value pill) and resolves it to the
// list of service names that match its matcher rules. Reuses the
// same code path as the /search endpoint when given an integration ID.
func (h *Handlers) resolveIntegrationServices(ctx context.Context, name string, tr TimeRange) ([]string, error) {
	all, err := h.Integrations.List(ctx, middleware.OrgIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	var target *integrations.Integration
	for i := range all {
		if strings.EqualFold(all[i].Name, name) {
			target = &all[i]
			break
		}
	}
	if target == nil {
		return nil, nil
	}
	candidates, err := h.Store.DistinctServiceNames(ctx, tr.From, tr.To)
	if err != nil {
		return nil, err
	}
	return h.Resolver.ServicesForIntegration(ctx, target.ID, candidates)
}

// intersectStrings returns the elements present in both slices,
// preserving the order of the first slice. Used to combine the
// service literal allowlist with the integration-resolved one.
func intersectStrings(a, b []string) []string {
	bset := make(map[string]struct{}, len(b))
	for _, x := range b {
		bset[x] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, x := range a {
		if _, ok := bset[x]; ok {
			out = append(out, x)
		}
	}
	return out
}
