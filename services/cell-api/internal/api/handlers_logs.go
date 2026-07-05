// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/store"
)

// listServiceLogs: GET /api/v1/services/{name}/logs
//
// Returns the most recent ingested OTLP logs for a service in the
// window, newest first. Optional filters:
//   - `q`            case-insensitive substring of the log body.
//   - `min_severity` OTLP SeverityNumber floor (e.g. 9 = WARN, 17 = ERROR).
//   - `trace_id`     restrict to logs correlated to one trace.
//   - `limit`        cap on rows (default 100, max 1000).
func (h *Handlers) listServiceLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	if !h.serviceSignalVisible(r, name, identity.SignalLogs) {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"service_name": name, "logs": []any{}})
		return
	}
	tr := ParseRange(r, time.Hour)

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	var minSeverity int32
	if v := r.URL.Query().Get("min_severity"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24 {
			minSeverity = int32(n)
		}
	}

	body := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(body) > 256 {
		httpserver.WriteError(w, http.StatusBadRequest, "q is too long")
		return
	}
	traceID := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("trace_id")))

	attrs, attrErr := parseAttrFilters(r)
	if attrErr != nil {
		httpserver.WriteError(w, http.StatusBadRequest, attrErr.Error())
		return
	}

	rows, err := h.Store.SearchLogs(r.Context(), store.LogQueryParams{
		Service:      name,
		From:         tr.From,
		To:           tr.To,
		Limit:        limit,
		MinSeverity:  minSeverity,
		BodyContains: body,
		TraceID:      traceID,
		Attrs:        attrs,
		Before:       parseLogCursor(r),
	})
	if err != nil {
		h.Logger.Error("recent logs for service failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, ServiceLogsResponse{
		ServiceName: name,
		Window:      tr.Window(),
		Logs:        toLogEntries(rows),
		NextCursor:  nextLogCursor(rows, limit),
	})
}

// listLogs: GET /api/v1/logs
//
// The global Logs page: same filters as the per-service variant, plus
// an optional `service` to narrow to one service. With no `service` it
// searches across every service that has logs — including infra like a
// broker or streaming platform that isn't an integration service.
func (h *Handlers) listLogs(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)

	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	var minSeverity int32
	if v := r.URL.Query().Get("min_severity"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24 {
			minSeverity = int32(n)
		}
	}
	body := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(body) > 256 {
		httpserver.WriteError(w, http.StatusBadRequest, "q is too long")
		return
	}
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	traceID := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("trace_id")))

	attrs, attrErr := parseAttrFilters(r)
	if attrErr != nil {
		httpserver.WriteError(w, http.StatusBadRequest, attrErr.Error())
		return
	}

	// `integration=<name>` scopes to that integration's services (a
	// ServiceName IN filter); used by group expansion + ad-hoc filtering.
	// Candidates come from the Postgres catalog snapshot — see P0-5 in
	// docs/performance-audit.md. The prior CH-based DistinctLogServices
	// scan was running on every request that carried ?integration=.
	serviceIn, integGroups, _ := h.integrationFilter(r.Context(), r, func() ([]string, error) {
		return h.catalogServiceNames(r.Context())
	})

	// G5: merge in the policy allowlist so non-admin callers only see
	// logs from services they can access.
	pf := h.resolveServiceFilterSignal(r, service, serviceIn, identity.SignalLogs)
	if pf.Blocked {
		httpserver.WriteError(w, http.StatusNotFound, "service not found")
		return
	}
	if pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, LogsResponse{Window: tr.Window(), Logs: []LogEntry{}})
		return
	}
	service = pf.Service
	serviceIn = pf.ServiceIn

	rows, err := h.Store.SearchLogs(r.Context(), store.LogQueryParams{
		Service:      service,
		ServiceIn:    serviceIn,
		From:         tr.From,
		To:           tr.To,
		Limit:        limit,
		MinSeverity:  minSeverity,
		BodyContains: body,
		TraceID:      traceID,
		Attrs:        attrs,
		AttrGroups:   integGroups,
		Before:       parseLogCursor(r),
	})
	if err != nil {
		h.Logger.Error("search logs failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, LogsResponse{
		Window:     tr.Window(),
		Logs:       toLogEntries(rows),
		NextCursor: nextLogCursor(rows, limit),
	})
}

// getLog: GET /api/v1/logs/{id} — fetch a single log by its LogId.
// Backs the drawer's "Copy link" deep link, which encodes the LogId so
// the target log re-opens regardless of the current time window, filters
// or keyset page. The org filter (via request ctx) and the G5 policy
// allowlist both apply: a log from another tenant, or from a service the
// caller can't see, returns 404 rather than leaking its existence.
func (h *Handlers) getLog(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "missing log id")
		return
	}
	row, found, err := h.Store.GetLogByID(r.Context(), id)
	if err != nil {
		h.Logger.Error("get log by id failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !found {
		httpserver.WriteError(w, http.StatusNotFound, "log not found")
		return
	}
	// G5: hide the log if the caller's policy doesn't grant its service.
	if pf := h.resolveServiceFilterSignal(r, row.ServiceName, nil, identity.SignalLogs); pf.Blocked || pf.EmptyAccess {
		httpserver.WriteError(w, http.StatusNotFound, "log not found")
		return
	}
	entries := toLogEntries([]store.LogRow{row})
	httpserver.WriteJSON(w, http.StatusOK, entries[0])
}

// logServices: GET /api/v1/log-services — the distinct services that
// have logs in the window, for the searchable service filter. Sourced
// from the Postgres catalog (which the reconciler keeps current) so
// the autocomplete doesn't burn a ClickHouse scan every keystroke —
// see P0-5 in docs/performance-audit.md.
//
// `range` is accepted but no longer consulted: the catalog is the
// stable set of services we've ever seen, not the per-window slice.
// In practice this is what users want anyway — the picker should not
// hide a service just because the current window happens to have no
// rows for it.
func (h *Handlers) logServices(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)
	names, err := h.catalogServiceNames(r.Context())
	if err != nil {
		h.Logger.Error("catalog service names failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if names == nil {
		names = []string{}
	}
	httpserver.WriteJSON(w, http.StatusOK, LogServicesResponse{
		Window:   tr.Window(),
		Services: names,
	})
}

// logServicesSampleLimit bounds the per-side row sample used to derive
// attribute keys/types so the catalog query stays O(constant).
const logFieldsSampleLimit = 4000

// attrKeyRe restricts attribute keys to a safe charset because the key
// is interpolated into the ClickHouse map subscript (values are bound).
var attrKeyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

var validAttrOps = map[string]bool{
	store.AttrOpEq: true, store.AttrOpNeq: true,
	store.AttrOpContains: true, store.AttrOpNotContains: true,
	store.AttrOpStartsWith: true, store.AttrOpExists: true,
	store.AttrOpGt: true, store.AttrOpGte: true,
	store.AttrOpLt: true, store.AttrOpLte: true,
}

// parseAttrFilters reads the repeated `attr` query params, each a JSON
// object {"key","op","value"}, validating the key charset and operator.
// Returns a 400-worthy error on any malformed/invalid entry so the
// client gets a clear rejection instead of a silently-ignored filter.
func parseAttrFilters(r *http.Request) ([]store.LogAttrFilter, error) {
	raw := r.URL.Query()["attr"]
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > 25 {
		return nil, errors.New("too many attribute filters (max 25)")
	}
	out := make([]store.LogAttrFilter, 0, len(raw))
	for _, s := range raw {
		var f store.LogAttrFilter
		if err := json.Unmarshal([]byte(s), &f); err != nil {
			return nil, errors.New("invalid attr filter JSON")
		}
		f.Key = strings.TrimSpace(f.Key)
		if !attrKeyRe.MatchString(f.Key) {
			return nil, errors.New("invalid attribute key: " + f.Key)
		}
		if !validAttrOps[f.Op] {
			return nil, errors.New("invalid attribute operator: " + f.Op)
		}
		out = append(out, f)
	}
	return out, nil
}

// logFields: GET /api/v1/log-fields — the attribute keys seen on recent
// logs, each tagged numeric, for the Logs page's filter key picker.
func (h *Handlers) logFields(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)
	keys, err := h.Store.DistinctLogAttributeKeys(r.Context(), tr.From, tr.To, logFieldsSampleLimit)
	if err != nil {
		h.Logger.Error("distinct log attribute keys failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	fields := make([]LogFieldEntry, 0, len(keys))
	for _, k := range keys {
		t := "string"
		if k.Numeric {
			t = "number"
		}
		fields = append(fields, LogFieldEntry{Key: k.Key, Type: t, UseCount: k.UseCount, Cardinality: k.Cardinality})
	}
	httpserver.WriteJSON(w, http.StatusOK, LogFieldsResponse{
		Window: tr.Window(),
		Fields: fields,
	})
}

// logAttrValues: GET /api/v1/log-attributes/{key}/values — the top-N
// values for one attribute key, ranked by event count, for the value
// picker's second step.
func (h *Handlers) logAttrValues(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.PathValue("key"))
	if !attrKeyRe.MatchString(key) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid attribute key")
		return
	}
	tr := ParseRange(r, time.Hour)
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	rows, err := h.Store.LogAttrValues(r.Context(), key, tr.From, tr.To, limit)
	if err != nil {
		h.Logger.Error("log attr values failed", "err", err, "key", key)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	values := make([]LogAttrValue, 0, len(rows))
	for _, v := range rows {
		values = append(values, LogAttrValue{Value: v.Value, Events: v.Events})
	}
	httpserver.WriteJSON(w, http.StatusOK, LogAttrValuesResponse{
		Key:    key,
		Window: tr.Window(),
		Values: values,
	})
}

// logVolume: GET /api/v1/logs/volume — per-bucket log counts split by
// severity band for the volume histogram. Same filters as /logs (minus
// cursor/limit). `buckets` (default 60) sets the bar count; the bucket
// width is derived from the window.
func (h *Handlers) logVolume(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)

	var minSeverity int32
	if v := r.URL.Query().Get("min_severity"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24 {
			minSeverity = int32(n)
		}
	}
	body := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(body) > 256 {
		httpserver.WriteError(w, http.StatusBadRequest, "q is too long")
		return
	}
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	attrs, attrErr := parseAttrFilters(r)
	if attrErr != nil {
		httpserver.WriteError(w, http.StatusBadRequest, attrErr.Error())
		return
	}

	buckets := 60
	if v := r.URL.Query().Get("buckets"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 10 && n <= 240 {
			buckets = n
		}
	}
	stepSeconds := int(tr.To.Sub(tr.From).Seconds()) / buckets
	if stepSeconds < 1 {
		stepSeconds = 1
	}

	// `integration=<name>` narrows to that integration's services.
	// Same catalog-sourced candidates as the list endpoint (P0-5).
	serviceIn, integGroups, _ := h.integrationFilter(r.Context(), r, func() ([]string, error) {
		return h.catalogServiceNames(r.Context())
	})

	// G5: enforce policy-based service visibility for the global
	// volume histogram. Empty access → empty buckets; ?service=X
	// the caller can't see → 404.
	pf := h.resolveServiceFilterSignal(r, service, serviceIn, identity.SignalLogs)
	if pf.Blocked {
		httpserver.WriteError(w, http.StatusNotFound, "service not found")
		return
	}
	if pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, []LogVolumeBucketJSON{})
		return
	}

	rows, err := h.Store.LogVolume(r.Context(), store.LogQueryParams{
		Service:      pf.Service,
		ServiceIn:    pf.ServiceIn,
		From:         tr.From,
		To:           tr.To,
		MinSeverity:  minSeverity,
		BodyContains: body,
		Attrs:        attrs,
		AttrGroups:   integGroups,
	}, stepSeconds)
	if err != nil {
		h.Logger.Error("log volume failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	out := make([]LogVolumeBucketJSON, 0, len(rows))
	for _, b := range rows {
		out = append(out, LogVolumeBucketJSON{
			Start: b.Bucket, Info: b.Info, Warn: b.Warn, Err: b.Err, Fatal: b.Fatal,
		})
	}
	httpserver.WriteJSON(w, http.StatusOK, LogVolumeResponse{
		Window:      tr.Window(),
		StepSeconds: stepSeconds,
		Buckets:     out,
	})
}

// parseLogCursor reads the keyset cursor (before_ts + before_ord) from
// the query. before_ord carries the last row's LogId (a UUID string).
// Returns nil when the cursor is absent or unparseable (treated as
// "first page").
func parseLogCursor(r *http.Request) *store.LogCursor {
	ts := strings.TrimSpace(r.URL.Query().Get("before_ts"))
	ord := strings.TrimSpace(r.URL.Query().Get("before_ord"))
	if ts == "" || ord == "" {
		return nil
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil
	}
	return &store.LogCursor{TSNano: n, Ord: ord}
}

// nextLogCursor builds the cursor for the following page from the last
// row, or nil when this page wasn't full (no more rows).
func nextLogCursor(rows []store.LogRow, limit int) *LogCursorJSON {
	if limit <= 0 || len(rows) < limit {
		return nil
	}
	last := rows[len(rows)-1]
	return &LogCursorJSON{
		TS:  strconv.FormatInt(last.Timestamp.UnixNano(), 10),
		Ord: last.LogID,
	}
}

func toLogEntries(rows []store.LogRow) []LogEntry {
	out := make([]LogEntry, 0, len(rows))
	for _, l := range rows {
		out = append(out, LogEntry{
			LogID:              l.LogID,
			Timestamp:          l.Timestamp,
			ObservedTimestamp:  l.ObservedTimestamp,
			TraceID:            l.TraceID,
			SpanID:             l.SpanID,
			SeverityNumber:     l.SeverityNumber,
			SeverityText:       l.SeverityText,
			ServiceName:        l.ServiceName,
			ScopeName:          l.ScopeName,
			Body:               l.Body,
			Attributes:         mergeAttributes(l.ResourceAttributes, l.LogAttributes),
			ResourceAttributes: l.ResourceAttributes,
			LogAttributes:      l.LogAttributes,
		})
	}
	return out
}
