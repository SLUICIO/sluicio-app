// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Global search — the top-navbar "search everything" finder (#28).
//
// Phase 1 covers the five prioritised sources: integrations, services,
// messages (trace spans), logs (body free-text), and metrics (by name).
// The remaining catalog sources (service facets, tags, metadata fields,
// maps, schemas) are a documented follow-up.
//
// Matching is name-oriented and forgiving: the query and each candidate
// name are normalised (lower-cased, non-alphanumerics stripped) before a
// substring test, so "kalk" matches "PreKalk", "pre-kalk" and
// "kalkylator". ClickHouse-backed sources (traces/logs/metrics) use the
// store's built-in positionCaseInsensitive substring filter and are then
// re-ranked here for prefix-first ordering.
//
// Scoping: org isolation is automatic on every ClickHouse read (the
// request context carries the org filter) and explicit for the Postgres
// integrations list; the G5 service-visibility policy is applied to the
// service-scoped sources via resolveServiceFilter, exactly like the
// existing /search endpoint.

package api

import (
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/store"
)

const (
	// globalSearchPerGroup is how many hits each source returns before
	// the UI shows a "see all" link.
	globalSearchPerGroup = 10
	// globalSearchFetch is how many candidates we pull from each
	// ClickHouse source so the prefix-first re-rank has room to work
	// before trimming to globalSearchPerGroup.
	globalSearchFetch = 25
)

// GlobalSearchHit is a single result row.
type GlobalSearchHit struct {
	Type     string `json:"type"`               // integration|service|message|log|metric
	Label    string `json:"label"`              // primary name / matched text
	Sublabel string `json:"sublabel,omitempty"` // secondary context (service, etc.)
	Href     string `json:"href"`               // frontend route to open
}

// GlobalSearchGroup is the set of hits for one source.
type GlobalSearchGroup struct {
	Type       string            `json:"type"`
	Label      string            `json:"label"` // human group title
	Hits       []GlobalSearchHit `json:"hits"`
	HasMore    bool              `json:"has_more"`
	SeeAllHref string            `json:"see_all_href,omitempty"`
}

// GlobalSearchResponse is the /api/v1/global-search payload.
type GlobalSearchResponse struct {
	Query  string              `json:"query"`
	Groups []GlobalSearchGroup `json:"groups"`
}

// normalizeSearch lower-cases and strips non-alphanumerics so that
// separators (spaces, hyphens, dots, underscores) don't block a match.
func normalizeSearch(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchRank scores a candidate name against the normalised query.
// rank 0 = normalised prefix, 1 = normalised substring; ok=false means
// the name doesn't match at all (used to drop non-matches from the
// Postgres sources, which we filter in Go).
func matchRank(name, nq string) (rank int, ok bool) {
	if nq == "" {
		return 1, true
	}
	nn := normalizeSearch(name)
	if strings.HasPrefix(nn, nq) {
		return 0, true
	}
	if strings.Contains(nn, nq) {
		return 1, true
	}
	return 0, false
}

// buildGlobalSearchGroup ranks candidates (prefix-first, then alpha),
// trims to the per-group cap, and reports whether more were available.
// dropNonMatch=true removes candidates whose name doesn't match (for
// the Postgres sources we filter ourselves); false keeps them ranked
// last (for ClickHouse sources, which may have matched a non-name field
// like an attribute and are already substring-filtered upstream).
func buildGlobalSearchGroup(typ, label, seeAll, nq string, cands []GlobalSearchHit, dropNonMatch bool) (GlobalSearchGroup, bool) {
	type scored struct {
		hit  GlobalSearchHit
		rank int
	}
	ranked := make([]scored, 0, len(cands))
	for _, c := range cands {
		rank, ok := matchRank(c.Label, nq)
		if !ok {
			if dropNonMatch {
				continue
			}
			rank = 2 // matched a non-name field upstream; sort last
		}
		ranked = append(ranked, scored{c, rank})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].rank != ranked[j].rank {
			return ranked[i].rank < ranked[j].rank
		}
		return strings.ToLower(ranked[i].hit.Label) < strings.ToLower(ranked[j].hit.Label)
	})
	hasMore := len(ranked) > globalSearchPerGroup
	if hasMore {
		ranked = ranked[:globalSearchPerGroup]
	}
	hits := make([]GlobalSearchHit, len(ranked))
	for i, s := range ranked {
		hits[i] = s.hit
	}
	g := GlobalSearchGroup{Type: typ, Label: label, Hits: hits, HasMore: hasMore, SeeAllHref: seeAll}
	return g, len(hits) > 0
}

// globalSearch: GET /api/v1/global-search?q=foo
//
// Returns up to globalSearchPerGroup hits per source, grouped and
// ordered: integrations, services, messages, logs, metrics.
func (h *Handlers) globalSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 128 {
		q = q[:128]
	}
	resp := GlobalSearchResponse{Query: q, Groups: []GlobalSearchGroup{}}
	if q == "" {
		httpserver.WriteJSON(w, http.StatusOK, resp)
		return
	}
	nq := normalizeSearch(q)
	tr := ParseRange(r, time.Hour)

	// Service-visibility policy (org isolation is automatic on CH reads).
	pf := h.resolveServiceFilterSignal(r, "", nil, identity.SignalMessages)
	serviceAllowed := !(pf.Blocked || pf.EmptyAccess)
	serviceIn := pf.ServiceIn

	add := func(g GlobalSearchGroup, ok bool) {
		if ok {
			resp.Groups = append(resp.Groups, g)
		}
	}

	// 1. Integrations (Postgres, org-scoped) — match on name.
	if ints, err := h.Integrations.List(r.Context(), middleware.OrgID(r)); err == nil {
		cands := make([]GlobalSearchHit, 0, len(ints))
		for _, in := range ints {
			cands = append(cands, GlobalSearchHit{
				Type:     "integration",
				Label:    in.Name,
				Sublabel: in.Slug,
				Href:     "/integrations/" + url.PathEscape(in.ID.String()),
			})
		}
		add(buildGlobalSearchGroup("integration", "Integrations", "/integrations", nq, cands, true))
	} else {
		h.Logger.Warn("global search: integrations failed", "err", err)
	}

	// 2. Services (ClickHouse, org-scoped + policy) — match on name.
	if serviceAllowed {
		if rows, err := h.Store.ListServices(r.Context(), tr.From, tr.To); err == nil {
			allow := serviceInSet(serviceIn)
			cands := make([]GlobalSearchHit, 0, len(rows))
			for _, s := range rows {
				if allow != nil {
					if _, ok := allow[s.ServiceName]; !ok {
						continue
					}
				}
				cands = append(cands, GlobalSearchHit{
					Type:  "service",
					Label: s.ServiceName,
					Href:  "/services/" + url.PathEscape(s.ServiceName),
				})
			}
			add(buildGlobalSearchGroup("service", "Services", "/services", nq, cands, true))
		} else {
			h.Logger.Warn("global search: services failed", "err", err)
		}
	}

	// 3. Messages — trace spans containing the query.
	if serviceAllowed {
		if rows, err := h.Store.SearchTraces(r.Context(), q, tr.From, tr.To, globalSearchFetch, serviceIn, false); err == nil {
			cands := make([]GlobalSearchHit, 0, len(rows))
			for _, t := range rows {
				cands = append(cands, GlobalSearchHit{
					Type:     "message",
					Label:    t.MatchedSpanName,
					Sublabel: t.MatchedService,
					Href:     "/traces/" + url.PathEscape(t.TraceID),
				})
			}
			add(buildGlobalSearchGroup("message", "Messages", "/search?q="+url.QueryEscape(q), nq, cands, false))
		} else {
			h.Logger.Warn("global search: messages failed", "err", err)
		}
	}

	// 4. Logs — body free-text contains.
	if serviceAllowed {
		if rows, err := h.Store.SearchLogs(r.Context(), store.LogQueryParams{
			ServiceIn:    serviceIn,
			From:         tr.From,
			To:           tr.To,
			Limit:        globalSearchFetch,
			BodyContains: q,
		}); err == nil {
			cands := make([]GlobalSearchHit, 0, len(rows))
			for _, l := range rows {
				cands = append(cands, GlobalSearchHit{
					Type:     "log",
					Label:    logSnippet(l.Body),
					Sublabel: l.ServiceName,
					Href:     "/logs?q=" + url.QueryEscape(q),
				})
			}
			// Logs match on body text, not a name — keep all, rank by recency
			// order from the store (don't drop on name mismatch).
			add(buildGlobalSearchGroup("log", "Logs", "/logs?q="+url.QueryEscape(q), nq, cands, false))
		} else {
			h.Logger.Warn("global search: logs failed", "err", err)
		}
	}

	// 5. Metrics — by metric name.
	if serviceAllowed {
		if rows, err := h.Store.MetricCatalog(r.Context(), store.MetricCatalogParams{
			ServiceIn: serviceIn,
			NameQuery: q,
			From:      tr.From,
			To:        tr.To,
		}); err == nil {
			cands := make([]GlobalSearchHit, 0, len(rows))
			for _, m := range rows {
				cands = append(cands, GlobalSearchHit{
					Type:     "metric",
					Label:    m.MetricName,
					Sublabel: m.MetricType,
					Href:     "/metrics?q=" + url.QueryEscape(m.MetricName),
				})
			}
			add(buildGlobalSearchGroup("metric", "Metrics", "/metrics?q="+url.QueryEscape(q), nq, cands, true))
		} else {
			h.Logger.Warn("global search: metrics failed", "err", err)
		}
	}

	// ── Catalog sources (phase 2) ────────────────────────────────────
	// Org-level config, not telemetry — scoped by org only (no service
	// policy). Each matches on its display name. The four without a
	// detail route link to their list page.
	orgID := middleware.OrgID(r)

	// 6. Service facets (built-in registry; not org-scoped).
	{
		facets := h.mergedFacets(r.Context(), orgID)
		cands := make([]GlobalSearchHit, 0, len(facets))
		for _, f := range facets {
			cands = append(cands, GlobalSearchHit{
				Type:     "facet",
				Label:    f.Name,
				Sublabel: f.Slug,
				Href:     "/service-facets/" + url.PathEscape(f.Slug),
			})
		}
		add(buildGlobalSearchGroup("facet", "Service facets", "/service-facets", nq, cands, true))
	}

	// 7. Tags.
	if rows, err := h.Tags.List(r.Context(), orgID); err == nil {
		cands := make([]GlobalSearchHit, 0, len(rows))
		for _, t := range rows {
			cands = append(cands, GlobalSearchHit{Type: "tag", Label: t.Name, Href: "/tags"})
		}
		add(buildGlobalSearchGroup("tag", "Tags", "/tags", nq, cands, true))
	} else {
		h.Logger.Warn("global search: tags failed", "err", err)
	}

	// 8. Metadata fields — match on the human label.
	if rows, err := h.Metadata.ListFields(r.Context(), orgID); err == nil {
		cands := make([]GlobalSearchHit, 0, len(rows))
		for _, f := range rows {
			cands = append(cands, GlobalSearchHit{
				Type:     "metadata",
				Label:    f.Label,
				Sublabel: f.Key,
				Href:     "/metadata-fields",
			})
		}
		add(buildGlobalSearchGroup("metadata", "Metadata fields", "/metadata-fields", nq, cands, true))
	} else {
		h.Logger.Warn("global search: metadata fields failed", "err", err)
	}

	// 9. Maps.
	if rows, err := h.Maps.List(r.Context(), orgID); err == nil {
		cands := make([]GlobalSearchHit, 0, len(rows))
		for _, m := range rows {
			cands = append(cands, GlobalSearchHit{Type: "map", Label: m.Name, Sublabel: m.Format, Href: "/maps"})
		}
		add(buildGlobalSearchGroup("map", "Maps", "/maps", nq, cands, true))
	} else {
		h.Logger.Warn("global search: maps failed", "err", err)
	}

	// 10. Schemas.
	if rows, err := h.Schemas.List(r.Context(), orgID); err == nil {
		cands := make([]GlobalSearchHit, 0, len(rows))
		for _, s := range rows {
			cands = append(cands, GlobalSearchHit{Type: "schema", Label: s.Name, Sublabel: s.Format, Href: "/schemas"})
		}
		add(buildGlobalSearchGroup("schema", "Schemas", "/schemas", nq, cands, true))
	} else {
		h.Logger.Warn("global search: schemas failed", "err", err)
	}

	httpserver.WriteJSON(w, http.StatusOK, resp)
}

// serviceInSet turns the policy allowlist into a set for O(1) lookup, or
// nil when there's no restriction (admin / wildcard).
func serviceInSet(serviceIn []string) map[string]struct{} {
	if serviceIn == nil {
		return nil
	}
	set := make(map[string]struct{}, len(serviceIn))
	for _, s := range serviceIn {
		set[s] = struct{}{}
	}
	return set
}

// logSnippet trims a log body to a single short line for the result row.
func logSnippet(body string) string {
	body = strings.TrimSpace(body)
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[:i]
	}
	const max = 90
	if len(body) > max {
		return body[:max] + "…"
	}
	return body
}
