// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/pkg/audit"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/license"
	"github.com/sluicio/sluicio-app/pkg/mail"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/catalog"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/dashboards"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/erroracks"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetmappings"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetoverrides"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/ingestkeys"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/maintenance"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/maps"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/messageviews"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/metadata"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/monitoringtemplates"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/notifyprofiles"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/oauth"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/retention"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/schemas"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/servicefacets"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/servicemeta"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/servicetypes"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/settings"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/systemtypes"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tags"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tracecompletion"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handlers groups the cell-api HTTP handlers and their dependencies.
type Handlers struct {
	// mfaPolicy caches the org-wide mfa_required flag for the per-request
	// enforcement wrap (see mfa_enforce.go).
	mfaPolicy mfaPolicyCache

	Store          *store.Store
	ClickHouseConn driver.Conn
	Integrations   *integrations.Store
	Resolver       *integrations.Resolver
	ServiceFacets  *servicetypes.Registry
	// ServiceFacetsCustom holds the org's user-defined facets, merged with the
	// built-in ServiceFacets registry on read (see mergedFacets).
	ServiceFacetsCustom *servicefacets.Store
	FacetMappings       *facetmappings.Store
	FacetOverrides      *facetoverrides.Store
	MessageViews        *messageviews.Store
	Dashboards          *dashboards.Store
	Tags                *tags.Store
	Alerts              *alerting.Store
	// Maintenance backs announcements + maintenance windows (see
	// handlers_maintenance.go).
	Maintenance *maintenance.Store
	// PGPool is the raw Postgres pool for the few handlers that need
	// transaction ownership across many domains (config export/import —
	// the whole-bundle atomicity contract lives on one tx).
	PGPool      *pgxpool.Pool
	ServiceMeta *servicemeta.Store
	Metadata    *metadata.Store
	Catalog     *catalog.Store
	Schemas     *schemas.Store
	Maps        *maps.Store
	Identity    *identity.Store
	// Settings is the cell-wide key/value store (retention policy and
	// future global knobs). Nil-safe at the handler level — the cell-
	// settings endpoints return 503 if it's not wired.
	Settings *settings.Store
	// TraceCompletion is the store for integration trace-completion
	// rules. Rules live in the existing alert_rules table with
	// signal='trace'; the store is a typed view over them with the
	// right JSON shape for the spec. Nil-safe at the handler level —
	// the trace-completion endpoints return 503 when nil.
	TraceCompletion *tracecompletion.Store
	// TraceCompletionEvaluator runs the periodic CH classification
	// query + fires sticky-delayed alert instances through the
	// existing alerting machinery. Nil-safe: the read endpoints
	// degrade to "zero counts" when the evaluator is absent.
	TraceCompletionEvaluator *tracecompletion.Evaluator
	// IngestKeys manages per-org OTLP ingest API keys (cell-ingest
	// authenticates batches against these). Nil-safe — the endpoints
	// return 503 when not wired.
	IngestKeys *ingestkeys.Store
	// ErrorAcks holds per-service "clear errors" watermarks. When set,
	// service health/error-count ignores error traces at or before a
	// service's watermark. Nil-safe — handlers treat nil as "no acks".
	ErrorAcks *erroracks.Store
	// Profiles is the notification-profile store: per-team / org-wide
	// bundles of channels + behaviour, resolved per alert. Nil-safe.
	Profiles *notifyprofiles.Store
	// Templates is the user-defined monitoring-template store (custom +
	// forked templates of health checks).
	Templates *monitoringtemplates.Store
	// SystemTypes is the org-customisable system-types catalog (detection
	// prefixes + starter checks per type). Built-ins stay code-defined.
	SystemTypes *systemtypes.Store
	// SelfBaseURL is cell-api's own loopback base (e.g. http://127.0.0.1:8081).
	// The HTTP MCP endpoint re-dispatches tool calls here, forwarding the
	// caller's token, so they reuse the exact REST + auth + RBAC.
	SelfBaseURL string
	// OAuth backs the OAuth 2.1 authorization server cell-api runs in front of
	// the MCP endpoint (DCR + authorization-code/PKCE), for OAuth-only clients.
	OAuth *oauth.Store
	// RetentionEnforcer pushes telemetry.retention.* into ClickHouse.
	// The PATCH handler calls ApplyOnce synchronously so the user
	// sees their change reflected immediately; the periodic Run loop
	// (started in main) repairs drift. Nil-safe — without it the
	// PATCH still persists, only the synchronous CH apply is skipped.
	RetentionEnforcer *retention.Enforcer
	// AuthMW is the auth resolver used by Mount() to gate protected
	// routes. Nil-safe — routes can also be registered un-gated for
	// the unauthenticated phase (P2 keeps the existing surface open;
	// P3 turns it on everywhere).
	AuthMW *middleware.Resolver
	// CatalogReconciler is invoked synchronously after integration /
	// matcher edits so the materialised integration_services rows
	// reflect the change immediately, without waiting for the next
	// reconcile tick. Nil-safe: handlers fall back to the tick if it's
	// not wired (e.g. in unit tests).
	CatalogReconciler *catalog.Reconciler
	// License is the Enterprise license manager (ee/license). It answers
	// feature-gate questions via requireFeature/Entitled and powers
	// GET /api/v1/license. Nil-safe: a nil manager — or no/expired key —
	// means every Enterprise feature is gated off while the core API keeps
	// working. The product never blocks core flows on licensing.
	License *license.Manager
	// Mail is the global transactional-email sender (password resets, …).
	// Nil-safe: handlers that need email check Configured() and degrade
	// gracefully when SMTP isn't set up.
	Mail *mail.Sender
	// Audit records security-relevant admin actions. The core holds only
	// the audit.Recorder interface (pkg/audit); the Enterprise edition wires
	// the persistent ee/audit store, a community build the no-op. Nil-safe:
	// the audit handlers no-op when it's unwired or the audit_log entitlement
	// is inactive, so the core never depends on Enterprise code.
	Audit  audit.Recorder
	Logger *slog.Logger

	// audit_log.viewed throttle state (see recordAuditViewed). Guarded by
	// auditViewMu; keyed user/org.
	auditViewMu   sync.Mutex
	auditViewSeen map[string]time.Time
}

// ioResolverFor loads the user-defined facet attribute mappings for a
// service and compiles them into an IO attribute resolver. The
// resolver is used by every code path that reads io.kind / io.role
// from spans (widget queries, service-profile classification) so a
// service without those attributes can still be classified via UI
// rules. A fetch failure logs a warning and falls back to the
// identity resolver — the worst case is that classification reverts
// to the un-overridden behaviour for one request, which is fine.
func (h *Handlers) ioResolverFor(ctx context.Context, serviceName string) facetmappings.Resolver {
	if h.FacetMappings == nil {
		return facetmappings.IdentityResolver()
	}
	rules, err := h.FacetMappings.ListForService(ctx, middleware.OrgIDFromContext(ctx), serviceName)
	if err != nil {
		h.Logger.Warn("load facet mappings failed", "err", err, "service", serviceName)
		return facetmappings.IdentityResolver()
	}
	return facetmappings.BuildResolver(rules)
}

// facetOverridesFor loads a service's manual facet overrides. A fetch
// failure logs a warning and falls back to an empty set — the worst
// case is the service shows its un-overridden, auto-detected facets for
// one request, which is safe.
func (h *Handlers) facetOverridesFor(ctx context.Context, serviceName string) facetoverrides.Set {
	if h.FacetOverrides == nil {
		return facetoverrides.NewSet(nil)
	}
	rows, err := h.FacetOverrides.ListForService(ctx, middleware.OrgIDFromContext(ctx), serviceName)
	if err != nil {
		h.Logger.Warn("load facet overrides failed", "err", err, "service", serviceName)
		return facetoverrides.NewSet(nil)
	}
	return facetoverrides.NewSet(rows)
}

// resolvedFacet pairs a facet with the reason it's on a service, so the
// widget path can both compute the facet's widgets and tag the section
// auto / manual in the response.
type resolvedFacet struct {
	facet  servicetypes.ServiceFacet
	source string
}

// gateServiceRoute wraps a per-service handler with the policy
// visibility check. Every route that takes a {name} path parameter
// for a service should go through this — it 404's the request
// before the handler runs if the user can't see the service.
//
// 404 (not 403) is intentional: it doesn't leak whether a service
// exists or not. Same rationale as `canSeeService`.
//
// Org admins and wildcard-policy holders bypass automatically via
// `canSeeService`. Skips the check on requests with no path-value
// (defensive — shouldn't happen on real routes).
func (h *Handlers) gateServiceRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name != "" && !h.canSeeService(r, name) {
			httpserver.WriteError(w, http.StatusNotFound, "service not found")
			return
		}
		next.ServeHTTP(w, r)
	}
}

// writeService gates a per-service WRITE handler: the caller must be an
// editor+ (viewers are read-only) AND be able to see the service. The
// visibility check (gateServiceRoute) alone is for reads — it does not stop
// a viewer from mutating a service they can see, which is why config writes
// must layer the write-role gate on top. No-auth mode (AuthMW nil, dev/test)
// keeps the visibility-only behaviour.
func (h *Handlers) writeService(next http.HandlerFunc) http.HandlerFunc {
	// Visibility 404s first (out-of-scope reads as nonexistent), then the
	// scoped-manage 403 (RBAC v2 §5.2): org editors/admins manage all;
	// group-editors manage exactly their groups' scopes; viewers nothing.
	gated := h.gateServiceRoute(h.requireManageService(next))
	if h.AuthMW == nil {
		return gated
	}
	return h.AuthMW.Require(gated)
}

// writeOrg gates class-A org-global config (tags, schemas, channels,
// templates…): org editor/admin ONLY — group-editor roles deliberately
// do not count here (spec §5.2, the phase-2 tightening).
func (h *Handlers) writeOrg(next http.HandlerFunc) http.HandlerFunc {
	if h.AuthMW == nil {
		return next
	}
	return h.AuthMW.RequireRole(identity.Role.CanWrite, next)
}

// writeAnywhere gates a scoped-resource write handler: org editor+, or —
// Enterprise only (spec §3) — editor in any group. In CE a group role
// above viewer has no effect, so the gate collapses to the org role.
// No-auth mode is a pass-through.
func (h *Handlers) writeAnywhere(next http.HandlerFunc) http.HandlerFunc {
	if h.AuthMW == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if h.featureEntitled(license.FeatureRBACAdvanced) {
			h.AuthMW.RequireWriteAnywhere(next).ServeHTTP(w, r)
			return
		}
		h.AuthMW.RequireRole(identity.Role.CanWrite, next).ServeHTTP(w, r)
	}
}

// policyResolution is the merged result of policy + request-param
// filtering for a cross-service handler. The handler short-circuits
// on Blocked / EmptyAccess and uses ServiceIn as its store-call
// filter.
type policyResolution struct {
	// ServiceIn is the final ServiceName IN (...) allowlist. nil
	// means "no restriction" (admin / wildcard). Empty AND EmptyAccess
	// means "match nothing."
	ServiceIn []string
	// Service is the sanitised ?service= parameter, empty if not
	// specified or already merged into ServiceIn.
	Service string
	// Blocked is true when the request explicitly asked for a service
	// the caller can't see. Handler should return 404.
	Blocked bool
	// EmptyAccess is true when the caller has zero policy reach.
	// Handler should return an empty response without hitting ClickHouse.
	EmptyAccess bool
}

// resolveServiceFilter merges the policy-derived service allowlist
// with the request's ?service / ?integration / ?service_in
// parameters. Pass nil for serviceIn when the handler doesn't
// already have one (e.g. the integration param hasn't been parsed
// yet). The return shape lets the handler do:
//
//	res := h.resolveServiceFilter(r, service, currentServiceIn)
//	if res.Blocked     { 404 }
//	if res.EmptyAccess { return empty JSON }
//	// otherwise pass res.ServiceIn to the store query
func (h *Handlers) resolveServiceFilter(r *http.Request, service string, serviceIn []string) policyResolution {
	allowed, hasFilter := h.visibleServiceFilter(r)
	return applyAllowlist(service, serviceIn, allowed, hasFilter)
}

// resolveServiceFilterSignal is resolveServiceFilter narrowed to one
// telemetry signal (RBAC v2 §7): logs endpoints pass SignalLogs, etc.
// Union-visible services missing this signal read as empty data.
func (h *Handlers) resolveServiceFilterSignal(r *http.Request, service string, serviceIn []string, sig identity.Signal) policyResolution {
	allowed, hasFilter := h.signalServiceFilter(r, sig)
	return applyAllowlist(service, serviceIn, allowed, hasFilter)
}

// visibilityMember decides how READ visibility resolves for this
// request's principal (docs/service-account-scoping-design.md):
//
//   - admins (uncapped read role) and internal callers carrying no
//     principal at all: unrestricted (restricted=false);
//   - org-wide service accounts: unrestricted — unless the cell
//     forbids org-wide SAs, in which case they resolve as scoped;
//   - scoped service accounts: resolve via SA group memberships;
//   - users: resolve via user group memberships.
//
// This replaces the old "UserID == nil → allow" shortcut, which
// conflated internal callers with service accounts and gave every SA
// token org-wide reads (issue #2).
func (h *Handlers) visibilityMember(r *http.Request) (identity.MemberRef, bool) {
	p := middleware.Principal(r)
	if p.ReadRole().CanAdmin() {
		return identity.MemberRef{}, false
	}
	switch {
	case p.Kind == identity.PrincipalServiceAccount && p.ServiceAccountID != nil:
		if p.SAScope == identity.SAScopeOrgWide && !h.forbidOrgWideServiceAccounts(r) {
			return identity.MemberRef{}, false
		}
		return identity.ServiceAccountRef(*p.ServiceAccountID), true
	case p.UserID != nil:
		return identity.UserRef(*p.UserID), true
	default:
		// No principal at all — internal caller. Authenticated external
		// requests always carry a user or service-account principal.
		return identity.MemberRef{}, false
	}
}

// forbidOrgWideServiceAccounts reads the cell-wide prohibition knob.
// Only consulted on the (rare) org-wide-SA request path. Fails open to
// the SA's stored scope — the knob is a posture tightener, not the
// primary gate.
func (h *Handlers) forbidOrgWideServiceAccounts(r *http.Request) bool {
	if h.Settings == nil {
		return false
	}
	forbid, err := h.Settings.GetForbidOrgWideServiceAccounts(r.Context())
	if err != nil {
		h.Logger.Warn("forbid_org_wide_service_accounts read failed; honoring stored scope", "err", err)
		return false
	}
	return forbid
}

// signalServiceFilter mirrors visibleServiceFilter for one signal.
func (h *Handlers) signalServiceFilter(r *http.Request, sig identity.Signal) ([]string, bool) {
	ref, restricted := h.visibilityMember(r)
	if !restricted {
		return nil, false
	}
	sets, err := h.Identity.ResolveAccessSetsMember(r.Context(), ref, middleware.Principal(r).OrgID, h.integrationExpander, h.systemExpander, h.serviceUniverse)
	if err != nil {
		h.Logger.Warn("signal filter resolve failed; allowing", "err", err, "signal", sig)
		return nil, false
	}
	set, wildcard := sets.VisibleFor(sig)
	if wildcard {
		return nil, false
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	return out, true
}

// serviceSignalVisible reports whether one (union-visible) service also
// carries this signal for the caller — per-service telemetry endpoints
// return empty data when it doesn't.
func (h *Handlers) serviceSignalVisible(r *http.Request, name string, sig identity.Signal) bool {
	allowed, hasFilter := h.signalServiceFilter(r, sig)
	if !hasFilter {
		return true
	}
	for _, n := range allowed {
		if n == name {
			return true
		}
	}
	return false
}

func applyAllowlist(service string, serviceIn []string, allowed []string, hasFilter bool) policyResolution {
	res := policyResolution{Service: service, ServiceIn: serviceIn}
	if !hasFilter {
		return res // admin / wildcard — no policy restriction
	}
	if len(allowed) == 0 {
		res.EmptyAccess = true
		return res
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, n := range allowed {
		allowedSet[n] = struct{}{}
	}
	// Block ?service=X if X isn't visible.
	if service != "" {
		if _, ok := allowedSet[service]; !ok {
			res.Blocked = true
			return res
		}
	}
	// Intersect with caller's serviceIn if any; otherwise impose the
	// policy allowlist as the filter.
	switch {
	case len(serviceIn) > 0:
		out := make([]string, 0, len(serviceIn))
		for _, n := range serviceIn {
			if _, ok := allowedSet[n]; ok {
				out = append(out, n)
			}
		}
		res.ServiceIn = out
		if len(out) == 0 {
			// The integration / explicit filter has no overlap with
			// what the user can see. Same as EmptyAccess from the
			// caller's perspective.
			res.EmptyAccess = true
		}
	default:
		res.ServiceIn = allowed
	}
	return res
}

// visibleServiceFilter returns the allow-list of service names the
// caller can see, or nil to mean "no restriction" (org admin or
// wildcard policy). Used by handlers that aggregate across services
// to constrain their underlying ClickHouse query to the visible
// subset.
//
// An empty (non-nil) slice means the caller can see NOTHING — the
// caller should return an empty response without hitting ClickHouse.
func (h *Handlers) visibleServiceFilter(r *http.Request) ([]string, bool) {
	ref, restricted := h.visibilityMember(r)
	if !restricted {
		return nil, false
	}
	allowed, wildcard, err := h.Identity.ResolveVisibleServiceSetMember(r.Context(), ref, middleware.Principal(r).OrgID, h.integrationExpander, h.systemExpander, h.serviceUniverse)
	if err != nil {
		h.Logger.Warn("visible service filter resolve failed; allowing", "err", err)
		return nil, false
	}
	if wildcard {
		return nil, false
	}
	out := make([]string, 0, len(allowed))
	for name := range allowed {
		out = append(out, name)
	}
	return out, true
}

// integrationExpander is the closure the identity policy resolver
// uses to turn a kind=integration policy into a concrete service
// list. We use the existing catalog snapshot + the integration
// resolver — both are dependencies the cell-api already holds.
func (h *Handlers) integrationExpander(ctx context.Context, orgID, integrationID uuid.UUID) ([]string, error) {
	catalogRows, err := h.Catalog.AllServices(ctx, orgID)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, len(catalogRows))
	for _, c := range catalogRows {
		candidates = append(candidates, c.ServiceName)
	}
	return h.Resolver.ServicesForIntegration(ctx, integrationID, candidates)
}

// systemExpander is the closure the policy resolver uses to turn a
// kind=system policy into concrete member services. Narrowing:
// systemID (one system entity — the CE attach-group grant) wins when
// non-nil; else systemKind ("" = all flagged systems).
func (h *Handlers) systemExpander(ctx context.Context, orgID uuid.UUID, systemKind string, systemID *uuid.UUID) ([]string, error) {
	if systemID != nil {
		return h.Catalog.SystemMemberNames(ctx, orgID, *systemID)
	}
	catalogRows, err := h.Catalog.AllServices(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, c := range catalogRows {
		if c.IsSystem && (systemKind == "" || c.SystemKind == systemKind) {
			out = append(out, c.ServiceName)
		}
	}
	return out, nil
}

// serviceUniverse is the closure the policy resolver uses to evaluate
// kind=expression policies: every service name in the org, for NOT
// complement and service-name leaves.
func (h *Handlers) serviceUniverse(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	catalogRows, err := h.Catalog.AllServices(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(catalogRows))
	for _, c := range catalogRows {
		out = append(out, c.ServiceName)
	}
	return out, nil
}

// canSeeService is the per-service visibility check used by handlers
// that take a {name} path parameter. Org admins always pass; for
// everyone else we resolve the user's effective access via the
// policy engine and check the service name against the resolved set
// (or the wildcard).
func (h *Handlers) canSeeService(r *http.Request, serviceName string) bool {
	ref, restricted := h.visibilityMember(r)
	if !restricted {
		return true
	}
	allowed, wildcard, err := h.Identity.ResolveVisibleServiceSetMember(r.Context(), ref, middleware.Principal(r).OrgID, h.integrationExpander, h.systemExpander, h.serviceUniverse)
	if err != nil {
		h.Logger.Warn("service visibility check failed; allowing", "err", err, "service", serviceName)
		return true
	}
	if wildcard {
		return true
	}
	_, ok := allowed[serviceName]
	return ok
}

// applyServiceVisibility filters a catalog slice to the services the
// caller can see according to their group policies. Returns
// (filtered, true) when filtering applied, (nil, false) when the
// caller should see everything (org admins, wildcard policy holders,
// or no auth context).
func (h *Handlers) applyServiceVisibility(r *http.Request, catalogRows []catalog.Service) ([]catalog.Service, bool) {
	ref, restricted := h.visibilityMember(r)
	if !restricted {
		return nil, false
	}
	allowed, wildcard, err := h.Identity.ResolveVisibleServiceSetMember(r.Context(), ref, middleware.Principal(r).OrgID, h.integrationExpander, h.systemExpander, h.serviceUniverse)
	if err != nil {
		h.Logger.Warn("service visibility lookup failed; falling back to no filter", "err", err)
		return nil, false
	}
	if wildcard {
		return nil, false
	}
	out := make([]catalog.Service, 0, len(catalogRows))
	for _, s := range catalogRows {
		if _, ok := allowed[s.ServiceName]; ok {
			out = append(out, s)
		}
	}
	return out, true
}

// filterVisibleMembers narrows an integration's member-service names to the
// ones the caller is allowed to see, and reports whether ANY are visible.
// Admins / wildcard holders / no-auth see every member. The "anyVisible"
// flag is the access gate: a caller who can see none of an integration's
// services has no business viewing the integration.
func (h *Handlers) filterVisibleMembers(r *http.Request, members []string) (visible []string, anyVisible bool) {
	allowed, filtered := h.visibleServiceFilter(r)
	if !filtered {
		return members, true
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, n := range allowed {
		allow[n] = struct{}{}
	}
	visible = make([]string, 0, len(members))
	for _, m := range members {
		if _, ok := allow[m]; ok {
			visible = append(visible, m)
		}
	}
	return visible, len(visible) > 0
}

// gateIntegrationMembers is the access check for an integration {id} read
// endpoint: it resolves the integration's members the caller may see and
// writes 404 (so the integration's existence isn't revealed) when they can
// see none. Returns the visible member set so the handler restricts every
// downstream count/summary to it. Fails open on a lookup error (logged),
// matching canSeeService's posture so a transient DB blip can't lock admins
// out.
func (h *Handlers) gateIntegrationMembers(w http.ResponseWriter, r *http.Request, integrationID uuid.UUID) ([]string, bool) {
	all, err := h.Catalog.IntegrationServices(r.Context(), integrationID)
	if err != nil {
		h.Logger.Warn("integration members lookup for access gate failed; allowing", "err", err, "integration", integrationID)
		return all, true
	}
	visible, anyVisible := h.filterVisibleMembers(r, all)
	if !anyVisible {
		httpserver.WriteError(w, http.StatusNotFound, "integration not found")
		return nil, false
	}
	return visible, true
}

// canSeeIntegration is the non-writing predicate form of
// gateIntegrationMembers: true iff the caller can see at least one of
// the integration's member services. Fails open on lookup error (same
// as the gate), so a DB blip never hides data the user is allowed to
// see.
func (h *Handlers) canSeeIntegration(r *http.Request, integrationID uuid.UUID) bool {
	all, err := h.Catalog.IntegrationServices(r.Context(), integrationID)
	if err != nil {
		h.Logger.Warn("integration visibility check failed; allowing", "err", err, "integration", integrationID)
		return true
	}
	_, anyVisible := h.filterVisibleMembers(r, all)
	return anyVisible
}

// canSeeAlertTarget reports whether the caller may see an alert /
// failing health check bound to the given target. Service- and
// integration-bound checks are gated by the *telemetry* visibility
// boundary (canSeeService / canSeeIntegration) — a service-scoped
// health check with no team must not leak to someone who can't see the
// service. Global (unbound) checks carry no service data, so they fall
// back to the team-ownership filter applied by the caller.
func (h *Handlers) canSeeAlertTarget(r *http.Request, serviceName string, integrationID *uuid.UUID) bool {
	if serviceName != "" {
		return h.canSeeService(r, serviceName)
	}
	if integrationID != nil {
		return h.canSeeIntegration(r, *integrationID)
	}
	return true
}

// attrFilterFromMatcher converts an integration matcher (e.g. producer = ttf,
// or service.name = order-gateway) into a ClickHouse attribute predicate. A
// service.name matcher carries Key "service.name", which attrClauseIn compiles
// against the indexed ServiceName column — so it scopes a rule to its service.
func attrFilterFromMatcher(m integrations.Matcher) store.LogAttrFilter {
	op := store.AttrOpEq
	switch m.Operator {
	case integrations.OperatorPrefix:
		op = store.AttrOpStartsWith
	case integrations.OperatorSuffix:
		op = store.AttrOpEndsWith
	case integrations.OperatorContains:
		op = store.AttrOpContains
	case integrations.OperatorRegex:
		op = store.AttrOpMatches
	}
	return store.LogAttrFilter{Key: m.Attribute, Op: op, Value: m.Value}
}

// integrationGroups returns an integration's matchers as a DNF predicate: a
// list of groups (by match_group), each a list of filters. Within a group the
// filters are AND-ed; the groups are OR-ed. ALL matchers participate —
// including service.name, which compiles against the ServiceName column (see
// attrClauseIn). That's what makes per-service rules work: a group like
// [service.name=A, producer=B] scopes the attribute condition to service A,
// and groups for other services are OR-ed alongside it. Threaded onto every
// query scoped to the integration (Messages / Logs / Metrics / Errors / Flow).
// Returns nil when the integration has no matchers.
//
// Note: a service.name=X matcher still independently drives membership via
// IsServiceMatcher (ServicesForIntegration), which gates visibility and the
// member/flow lists; the DNF refines per-service within that membership.
func (h *Handlers) integrationGroups(ctx context.Context, integrationID uuid.UUID) [][]store.LogAttrFilter {
	matchers, err := h.Integrations.MatchersForIntegration(ctx, integrationID)
	if err != nil {
		h.Logger.Warn("integration groups: load matchers failed", "err", err, "integration", integrationID)
		return nil
	}
	byGroup := map[int][]store.LogAttrFilter{}
	order := make([]int, 0)
	for _, m := range matchers {
		if _, ok := byGroup[m.MatchGroup]; !ok {
			order = append(order, m.MatchGroup)
		}
		byGroup[m.MatchGroup] = append(byGroup[m.MatchGroup], attrFilterFromMatcher(m))
	}
	if len(order) == 0 {
		return nil
	}
	sort.Ints(order)
	out := make([][]store.LogAttrFilter, 0, len(order))
	for _, g := range order {
		out = append(out, byGroup[g])
	}
	return out
}

// resolveFacets applies a service's manual overrides to its
// auto-detected facet set and returns the effective facets in registry
// declaration order:
//
//	effective = (auto ∪ includes) − excludes
//
// Each result is tagged "manual" when it's present only because of an
// include override, otherwise "auto". The always-on core facet is never
// removed, even if an exclude override names it.
func (h *Handlers) resolveFacets(allFacets, autoMatched []servicetypes.ServiceFacet, ov facetoverrides.Set) []resolvedFacet {
	auto := make(map[string]bool, len(autoMatched))
	for _, f := range autoMatched {
		auto[f.Slug] = true
	}
	out := make([]resolvedFacet, 0, len(autoMatched))
	for _, f := range allFacets {
		isAuto := auto[f.Slug]
		included := ov.Include[f.Slug]
		excluded := ov.Exclude[f.Slug] && f.Slug != servicetypes.CoreSlug
		if !((isAuto || included) && !excluded) {
			continue
		}
		source := FacetSourceAuto
		if !isAuto && included {
			source = FacetSourceManual
		}
		out = append(out, resolvedFacet{facet: f, source: source})
	}
	return out
}

// Mount registers the API routes on the given mux.
func (h *Handlers) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/license", h.licenseStatus)
	mux.HandleFunc("GET /api/v1/services", h.listServices)
	mux.HandleFunc("GET /api/v1/systems", h.listSystems)
	mux.HandleFunc("GET /api/v1/systems/{id}", h.getSystem)
	mux.HandleFunc("POST /api/v1/systems", h.writeAnywhere(h.createSystem))
	mux.HandleFunc("PUT /api/v1/systems/{id}", h.writeAnywhere(h.requireManageSystem(h.updateSystem)))
	mux.HandleFunc("DELETE /api/v1/systems/{id}", h.writeAnywhere(h.requireManageSystem(h.deleteSystem)))
	mux.HandleFunc("POST /api/v1/systems/{id}/services", h.writeAnywhere(h.requireManageSystem(h.attachSystemService)))
	mux.HandleFunc("DELETE /api/v1/systems/{id}/services/{name}", h.writeAnywhere(h.requireManageSystem(h.detachSystemService)))
	// Resource sharing (RBAC v2 phase 3, EE): viewer-only grants of one
	// integration/system to a user or group. Sharer must manage the
	// resource (gated in-handler); entitlement-gated at the route.
	if h.AuthMW != nil {
		shareEE := func(next http.HandlerFunc) http.HandlerFunc {
			return h.AuthMW.Require(h.requireFeature(license.FeatureRBACAdvanced, next))
		}
		mux.HandleFunc("GET /api/v1/integrations/{id}/shares",
			shareEE(h.listShares(identity.ShareIntegration, h.integrationInOrg)))
		mux.HandleFunc("POST /api/v1/integrations/{id}/shares",
			shareEE(h.createShare(identity.ShareIntegration, h.integrationInOrg, h.integrationDisplayName)))
		mux.HandleFunc("DELETE /api/v1/integrations/{id}/shares/{shareId}",
			shareEE(h.deleteShare(identity.ShareIntegration, h.integrationInOrg)))
		mux.HandleFunc("GET /api/v1/systems/{id}/shares",
			shareEE(h.listShares(identity.ShareSystem, h.systemInOrg)))
		mux.HandleFunc("POST /api/v1/systems/{id}/shares",
			shareEE(h.createShare(identity.ShareSystem, h.systemInOrg, h.systemDisplayName)))
		mux.HandleFunc("DELETE /api/v1/systems/{id}/shares/{shareId}",
			shareEE(h.deleteShare(identity.ShareSystem, h.systemInOrg)))
	}

	// Resource ⇄ group attachment (RBAC v2 phase 1): the CE-facing "which
	// groups can view this" surface. Reads open to members; writes admin.
	// Not entitlement-gated — see handlers_resource_groups.go.
	if h.AuthMW != nil {
		mux.HandleFunc("GET /api/v1/systems/{id}/groups", h.listSystemGroups)
		mux.HandleFunc("PUT /api/v1/systems/{id}/groups",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.putSystemGroups))
		mux.HandleFunc("GET /api/v1/integrations/{id}/groups", h.listIntegrationGroups)
		mux.HandleFunc("PUT /api/v1/integrations/{id}/groups",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.putIntegrationGroups))
	}

	mux.HandleFunc("GET /api/v1/systems/{id}/metadata", h.getSystemMetadata)
	mux.HandleFunc("PUT /api/v1/systems/{id}/metadata", h.writeAnywhere(h.requireManageSystem(h.putSystemMetadata)))

	// Remote MCP transport — authed (Bearer); tools re-dispatch over loopback
	// so they reuse the REST + RBAC. Served on the app URL behind the proxy.
	mux.HandleFunc("POST /api/v1/mcp", h.mcpEndpoint)

	// OAuth 2.1 authorization server for the MCP endpoint (public; skip-listed
	// in main). Lets OAuth-only MCP connectors (Claude remote / Cowork) connect.
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", h.oauthProtectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/api/v1/mcp", h.oauthProtectedResource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.oauthASMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/api/v1/mcp", h.oauthASMetadata)
	mux.HandleFunc("GET /.well-known/openid-configuration", h.oauthASMetadata)
	mux.HandleFunc("POST /api/v1/oauth/register", h.oauthRegister)
	mux.HandleFunc("GET /api/v1/oauth/authorize", h.oauthAuthorize)
	mux.HandleFunc("POST /api/v1/oauth/authorize", h.oauthAuthorizeDecision)
	mux.HandleFunc("POST /api/v1/oauth/token", h.oauthToken)

	mux.HandleFunc("POST /api/v1/systems/{id}/apply-template", h.writeAnywhere(h.requireManageSystem(h.applySystemTemplateAll)))
	// Public status badges: the SVG is PUBLIC (skip-listed in main.go) and
	// renders only for opted-in entities. Publishing one exposes an entity's
	// health with NO auth, so the toggles are gated like the entity's other
	// edits — writeService = org editor+ (RequireRole CanWrite), and for the
	// service route it also enforces per-service visibility (canSeeService),
	// so an editor can't publish a service outside their group scope. This is
	// stricter than the old RequireWriteAnywhere, which let a group-editor
	// (org-viewer) publish any service in the org.
	mux.HandleFunc("GET /api/v1/badges/{kind}/{id}", h.badgeSVG)
	mux.HandleFunc("PUT /api/v1/integrations/{id}/badge", h.writeService(h.putIntegrationBadge))
	mux.HandleFunc("PUT /api/v1/systems/{id}/badge", h.writeService(h.putSystemBadge))
	mux.HandleFunc("PUT /api/v1/services/{name}/badge", h.writeService(h.putServiceBadge))
	mux.HandleFunc("GET /api/v1/errors", h.errorsFeed)
	mux.HandleFunc("GET /api/v1/unhealthy", h.unhealthyFeed)
	mux.HandleFunc("GET /api/v1/digest", h.getDigest)
	mux.HandleFunc("POST /api/v1/digest/seen", h.markDigestSeen)
	mux.HandleFunc("GET /api/v1/services/{name}", h.gateServiceRoute(h.serviceDetail))
	mux.HandleFunc("GET /api/v1/services/{name}/widgets", h.gateServiceRoute(h.serviceWidgets))
	mux.HandleFunc("GET /api/v1/services/{name}/traces", h.gateServiceRoute(h.serviceTraces))
	mux.HandleFunc("GET /api/v1/services/{name}/neighbors", h.gateServiceRoute(h.serviceNeighbors))

	// Ingested OTLP logs + metrics for a service. These are the raw
	// telemetry browse endpoints; distinct from the custom-metrics
	// threshold endpoints under /metrics. See handlers_logs.go and
	// handlers_otlp_metrics.go.
	mux.HandleFunc("GET /api/v1/services/{name}/logs", h.gateServiceRoute(h.listServiceLogs))
	mux.HandleFunc("GET /api/v1/services/{name}/metric-names", h.gateServiceRoute(h.listServiceMetricNames))
	mux.HandleFunc("GET /api/v1/services/{name}/metric-series", h.gateServiceRoute(h.serviceMetricSeries))

	// Facet attribute mappings — user-defined rules that classify a
	// service into the built-in I/O facets when io.kind / io.role
	// aren't emitted on spans. See handlers_facet_mappings.go.
	mux.HandleFunc("GET /api/v1/services/{name}/facet-mappings", h.gateServiceRoute(h.listFacetMappings))
	mux.HandleFunc("POST /api/v1/services/{name}/facet-mappings", h.writeService(h.createFacetMapping))
	mux.HandleFunc("DELETE /api/v1/services/{name}/facet-mappings/{id}", h.writeService(h.deleteFacetMapping))

	// Manual facet overrides — direct human include/exclude decisions
	// layered on top of auto-detection, for facets the OTLP data can't
	// express. See handlers_facet_overrides.go.
	mux.HandleFunc("GET /api/v1/services/{name}/facet-overrides", h.gateServiceRoute(h.getFacetOverrides))
	mux.HandleFunc("PUT /api/v1/services/{name}/facet-overrides", h.writeService(h.putFacetOverrides))
	mux.HandleFunc("GET /api/v1/search", h.search)
	mux.HandleFunc("GET /api/v1/global-search", h.globalSearch)
	mux.HandleFunc("GET /api/v1/traces/{traceId}", h.traceDetail)

	mux.HandleFunc("GET /api/v1/service-facets", h.listServiceFacets)
	mux.HandleFunc("GET /api/v1/service-facets/{slug}", h.getServiceFacet)
	// Custom facet management (create / rename / delete) — editor+.
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/service-facets", h.AuthMW.RequireRole(identity.Role.CanWrite, h.createServiceFacet))
		mux.HandleFunc("PUT /api/v1/service-facets/{slug}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.updateServiceFacet))
		mux.HandleFunc("DELETE /api/v1/service-facets/{slug}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.deleteServiceFacet))
	}

	// "Show on service page" health-check value tiles, and the external
	// push endpoint for pushed-source health checks (custom metrics are
	// unified into health checks — see package alerting).
	mux.HandleFunc("GET /api/v1/services/{name}/readings", h.gateServiceRoute(h.serviceReadings))
	mux.HandleFunc("POST /api/v1/services/{name}/health-checks/{id}/value", h.gateServiceRoute(h.pushHealthCheckValue))
	// "Clear errors" acknowledgement: mark a service's current failures
	// reviewed (POST) or undo it (DELETE). Visibility-gated like the rest
	// of the per-service surface.
	mux.HandleFunc("POST /api/v1/services/{name}/clear-errors", h.writeService(h.clearServiceErrors))
	mux.HandleFunc("DELETE /api/v1/services/{name}/clear-errors", h.writeService(h.unclearServiceErrors))

	mux.HandleFunc("GET /api/v1/integrations", h.listIntegrations)
	mux.HandleFunc("GET /api/v1/integrations/{id}", h.getIntegration)
	mux.HandleFunc("GET /api/v1/integrations/{id}/span-names", h.integrationSpanNames)
	mux.HandleFunc("GET /api/v1/integrations/{id}/attribute-keys", h.integrationAttributeKeys)
	mux.HandleFunc("GET /api/v1/integrations/{id}/attribute-values", h.integrationAttributeValues)
	mux.HandleFunc("GET /api/v1/integrations/{id}/flow", h.integrationFlow)
	// Org-wide service topology graph (visibility-filtered inside the handler).
	mux.HandleFunc("GET /api/v1/topology", h.topologyGraph)
	// Metadata relationship graph: integrations ↔ metadata values + tags.
	mux.HandleFunc("GET /api/v1/metadata-graph", h.metadataGraph)
	// Mutations (create / rename / delete / matcher edits) require editor+
	// — org-wide or in any group (RequireWriteAnywhere, same gate as
	// dashboards + alerts). Viewers are read-only.
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/integrations", h.writeAnywhere(h.createIntegration))
		mux.HandleFunc("PUT /api/v1/integrations/{id}", h.writeAnywhere(h.requireManageIntegration(h.updateIntegration)))
		mux.HandleFunc("DELETE /api/v1/integrations/{id}", h.writeAnywhere(h.requireManageIntegration(h.deleteIntegration)))
		mux.HandleFunc("POST /api/v1/integrations/{id}/matchers", h.writeAnywhere(h.requireManageIntegration(h.addMatcher)))
		mux.HandleFunc("DELETE /api/v1/integrations/{id}/matchers/{matcherId}", h.writeAnywhere(h.requireManageIntegration(h.removeMatcher)))
		mux.HandleFunc("DELETE /api/v1/integrations/{id}/services/{name}", h.writeAnywhere(h.requireManageIntegration(h.removeServiceFromIntegration)))
	} else {
		mux.HandleFunc("POST /api/v1/integrations", h.createIntegration)
		mux.HandleFunc("PUT /api/v1/integrations/{id}", h.updateIntegration)
		mux.HandleFunc("DELETE /api/v1/integrations/{id}", h.deleteIntegration)
		mux.HandleFunc("POST /api/v1/integrations/{id}/matchers", h.addMatcher)
		mux.HandleFunc("DELETE /api/v1/integrations/{id}/matchers/{matcherId}", h.removeMatcher)
		mux.HandleFunc("DELETE /api/v1/integrations/{id}/services/{name}", h.removeServiceFromIntegration)
	}

	// Global OTLP logs + metrics browse pages (across all services).
	// See handlers_logs.go and handlers_otlp_metrics.go.
	mux.HandleFunc("GET /api/v1/logs", h.listLogs)
	mux.HandleFunc("GET /api/v1/log-services", h.logServices)
	mux.HandleFunc("GET /api/v1/log-fields", h.logFields)
	mux.HandleFunc("GET /api/v1/log-attributes/{key}/values", h.logAttrValues)
	mux.HandleFunc("GET /api/v1/logs/volume", h.logVolume)
	mux.HandleFunc("GET /api/v1/logs/groups", h.logGroups)
	mux.HandleFunc("GET /api/v1/logs/{id}", h.getLog)
	mux.HandleFunc("GET /api/v1/metric-names", h.listMetricCatalog)
	mux.HandleFunc("GET /api/v1/metric-series", h.listMetricSeries)
	// Metric explorer: rich catalog (sparkline table) + attribute picker.
	// See handlers_metrics_explorer.go.
	mux.HandleFunc("GET /api/v1/metric-catalog", h.metricCatalog)
	// Usage report (Settings → Reports): what share of each signal is
	// unwatched by any rule + what it costs — admin-only for now.
	mux.HandleFunc("GET /api/v1/reports/usage",
		h.AuthMW.RequireRole(identity.Role.CanAdmin, h.usageReport))
	mux.HandleFunc("GET /api/v1/metric-fields", h.metricFields)
	mux.HandleFunc("GET /api/v1/metric-attributes/{key}/values", h.metricAttributeValues)
	mux.HandleFunc("GET /api/v1/metric-groups", h.metricGroups)

	// Usage report: ingested telemetry volume per service (org-admin only).
	// See handlers_usage.go.
	mux.HandleFunc("GET /api/v1/usage/volume", h.AuthMW.RequireRole(identity.Role.CanAdmin, h.usageVolume))

	// Alert rules + notification channels. See handlers_alerts.go.
	// Reads stay open; ALL mutations (create/update/delete of rules and
	// channels) require editor+ ("contributor") via RequireWriteAnywhere
	// (G6): editor in the org OR in any group they belong to. This is the
	// floor for "who can set up notification channels and alert rules".
	// (Trace-completion rule mutations stay admin-gated; see below.)
	mux.HandleFunc("GET /api/v1/alert-rules", h.listAlertRules)
	mux.HandleFunc("POST /api/v1/alert-rules/preview", h.previewAlertRule)
	mux.HandleFunc("GET /api/v1/alert-rules/{id}", h.getAlertRule)
	mux.HandleFunc("GET /api/v1/alert-instances", h.listAlertInstances)
	mux.HandleFunc("GET /api/v1/alert-deliveries", h.listAlertDeliveries)
	mux.HandleFunc("GET /api/v1/notification-channels", h.listChannels)
	// Alert template preview (renders a sample notification) + org-default
	// email template — preview is read-only; the default-template GET too.
	mux.HandleFunc("POST /api/v1/alert-templates/preview", h.previewAlertTemplate)
	mux.HandleFunc("GET /api/v1/alert-email-template", h.getAlertEmailTemplate)
	// Notification profiles (per-team / org-wide behaviour + channels).
	mux.HandleFunc("GET /api/v1/notification-profiles", h.listNotificationProfiles)
	mux.HandleFunc("GET /api/v1/integrations/{id}/notification-profile", h.getIntegrationProfile)
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/notification-profiles", h.AuthMW.RequireRole(identity.Role.CanWrite, h.createNotificationProfile))
		mux.HandleFunc("PUT /api/v1/notification-profiles/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.updateNotificationProfile))
		mux.HandleFunc("DELETE /api/v1/notification-profiles/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.deleteNotificationProfile))
		mux.HandleFunc("PUT /api/v1/notification-profiles/{id}/channels", h.AuthMW.RequireRole(identity.Role.CanWrite, h.setNotificationProfileChannels))
		mux.HandleFunc("PUT /api/v1/integrations/{id}/notification-profile", h.writeAnywhere(h.requireManageIntegration(h.assignIntegrationProfile)))
		mux.HandleFunc("POST /api/v1/alert-instances/{id}/acknowledge", h.writeAnywhere(h.acknowledgeAlertInstance))
		mux.HandleFunc("POST /api/v1/alert-instances/{id}/resolve", h.writeAnywhere(h.resolveAlertInstance))
		mux.HandleFunc("POST /api/v1/alert-rules", h.writeAnywhere(h.createAlertRule))
		mux.HandleFunc("PUT /api/v1/alert-rules/{id}", h.writeAnywhere(h.updateAlertRule))
		mux.HandleFunc("DELETE /api/v1/alert-rules/{id}", h.writeAnywhere(h.deleteAlertRule))
		mux.HandleFunc("POST /api/v1/notification-channels", h.AuthMW.RequireRole(identity.Role.CanWrite, h.createChannel))
		mux.HandleFunc("PUT /api/v1/notification-channels/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.updateChannel))
		mux.HandleFunc("DELETE /api/v1/notification-channels/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.deleteChannel))
		mux.HandleFunc("POST /api/v1/notification-channels/{id}/test", h.AuthMW.RequireRole(identity.Role.CanWrite, h.testChannel))
		mux.HandleFunc("PUT /api/v1/alert-email-template", h.AuthMW.RequireRole(identity.Role.CanWrite, h.putAlertEmailTemplate))
	} else {
		mux.HandleFunc("POST /api/v1/notification-profiles", h.createNotificationProfile)
		mux.HandleFunc("PUT /api/v1/notification-profiles/{id}", h.updateNotificationProfile)
		mux.HandleFunc("DELETE /api/v1/notification-profiles/{id}", h.deleteNotificationProfile)
		mux.HandleFunc("PUT /api/v1/notification-profiles/{id}/channels", h.setNotificationProfileChannels)
		mux.HandleFunc("PUT /api/v1/integrations/{id}/notification-profile", h.assignIntegrationProfile)
		mux.HandleFunc("POST /api/v1/alert-rules", h.createAlertRule)
		mux.HandleFunc("PUT /api/v1/alert-rules/{id}", h.updateAlertRule)
		mux.HandleFunc("DELETE /api/v1/alert-rules/{id}", h.deleteAlertRule)
		mux.HandleFunc("POST /api/v1/notification-channels", h.createChannel)
		mux.HandleFunc("PUT /api/v1/notification-channels/{id}", h.updateChannel)
		mux.HandleFunc("DELETE /api/v1/notification-channels/{id}", h.deleteChannel)
		mux.HandleFunc("POST /api/v1/notification-channels/{id}/test", h.testChannel)
		mux.HandleFunc("PUT /api/v1/alert-email-template", h.putAlertEmailTemplate)
	}

	// Per-org OTLP ingest keys. Listing is open to any authed org member;
	// minting/revoking is admin-only (a leaked key lets anyone write
	// telemetry as the org). See handlers_ingest_keys.go.
	mux.HandleFunc("GET /api/v1/ingest-keys", h.listIngestKeys)
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/ingest-keys", h.AuthMW.RequireRole(identity.Role.CanAdmin, h.blockDemo(h.createIngestKey)))
		mux.HandleFunc("DELETE /api/v1/ingest-keys/{id}", h.AuthMW.RequireRole(identity.Role.CanAdmin, h.blockDemo(h.revokeIngestKey)))
	} else {
		mux.HandleFunc("POST /api/v1/ingest-keys", h.createIngestKey)
		mux.HandleFunc("DELETE /api/v1/ingest-keys/{id}", h.revokeIngestKey)
	}

	// Messages: structured search + saved views. See handlers_messages.go.
	mux.HandleFunc("GET /api/v1/messages/fields", h.fieldsCatalog)
	mux.HandleFunc("POST /api/v1/messages/search", h.searchMessages)
	mux.HandleFunc("GET /api/v1/message-views", h.listMessageViews)
	// Saved views are org-visible, so mutations take the same editor+
	// gate as dashboards — viewers read views, they don't shape them.
	mux.HandleFunc("POST /api/v1/message-views", h.writeOrg(h.createMessageView))
	mux.HandleFunc("GET /api/v1/message-views/{id}", h.getMessageView)
	mux.HandleFunc("PUT /api/v1/message-views/{id}", h.writeOrg(h.updateMessageView))
	mux.HandleFunc("DELETE /api/v1/message-views/{id}", h.writeOrg(h.deleteMessageView))

	// Dashboards: per-user, named, customizable Home-page layouts.
	// See handlers_dashboards.go.
	mux.HandleFunc("GET /api/v1/dashboards", h.listDashboards)
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/dashboards", h.writeAnywhere(h.createDashboard))
	} else {
		mux.HandleFunc("POST /api/v1/dashboards", h.createDashboard)
	}
	mux.HandleFunc("GET /api/v1/dashboards/{id}", h.getDashboard)
	mux.HandleFunc("PUT /api/v1/dashboards/{id}", h.writeAnywhere(h.updateDashboard))
	mux.HandleFunc("DELETE /api/v1/dashboards/{id}", h.writeAnywhere(h.deleteDashboard))

	// Tags: the org-wide tag vocabulary plus per-integration and
	// per-service attachments. See handlers_tags.go. Reads stay open to
	// any authed member; managing the vocabulary (create/rename/recolor/
	// delete) requires editor+ — viewers are read-only (RequireRole.CanWrite).
	mux.HandleFunc("GET /api/v1/tags", h.listTags)
	mux.HandleFunc("GET /api/v1/tags/{id}", h.getTag)
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/tags", h.AuthMW.RequireRole(identity.Role.CanWrite, h.createTag))
		mux.HandleFunc("PATCH /api/v1/tags/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.updateTag))
		mux.HandleFunc("DELETE /api/v1/tags/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.deleteTag))
	} else {
		mux.HandleFunc("POST /api/v1/tags", h.createTag)
		mux.HandleFunc("PATCH /api/v1/tags/{id}", h.updateTag)
		mux.HandleFunc("DELETE /api/v1/tags/{id}", h.deleteTag)
	}

	mux.HandleFunc("GET /api/v1/integrations/{id}/tags", h.listIntegrationTags)
	mux.HandleFunc("POST /api/v1/integrations/{id}/tags/{tagId}", h.writeAnywhere(h.requireManageIntegration(h.attachIntegrationTag)))
	mux.HandleFunc("DELETE /api/v1/integrations/{id}/tags/{tagId}", h.writeAnywhere(h.requireManageIntegration(h.detachIntegrationTag)))

	mux.HandleFunc("GET /api/v1/services/{name}/metadata", h.gateServiceRoute(h.getServiceMetadata))
	mux.HandleFunc("PUT /api/v1/services/{name}/metadata", h.writeService(h.putServiceMetadata))
	mux.HandleFunc("PUT /api/v1/services/{name}/metadata-extras", h.writeService(h.putServiceMetadataExtras))
	mux.HandleFunc("PUT /api/v1/services/{name}/system", h.writeService(h.putServiceSystem))
	mux.HandleFunc("POST /api/v1/services/{name}/system/apply-template", h.writeService(h.applySystemTemplate))
	mux.HandleFunc("POST /api/v1/services/{name}/apply-template", h.writeService(h.applyTemplate))
	mux.HandleFunc("POST /api/v1/services/{name}/remove-template", h.writeService(h.removeTemplate))
	mux.HandleFunc("GET /api/v1/services/{name}/template-suggestions", h.gateServiceRoute(h.templateSuggestions))
	// User-defined monitoring templates (custom + forks).
	mux.HandleFunc("GET /api/v1/monitoring-templates", h.listMonitoringTemplates)
	mux.HandleFunc("POST /api/v1/monitoring-templates", h.AuthMW.RequireRole(identity.Role.CanWrite, h.createMonitoringTemplate))
	mux.HandleFunc("PUT /api/v1/monitoring-templates/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.updateMonitoringTemplate))
	mux.HandleFunc("DELETE /api/v1/monitoring-templates/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.deleteMonitoringTemplate))

	// System-types catalog (managed list of system kinds: detection + checks).
	mux.HandleFunc("GET /api/v1/system-types", h.listSystemTypes)
	// Shareable system types (docs/system-types-sharing.md): export any
	// catalog entry (built-in or org) as a portable YAML/JSON doc;
	// import creates/replaces an org type.
	mux.HandleFunc("GET /api/v1/system-types/{key}/export", h.exportSystemType)
	mux.HandleFunc("POST /api/v1/system-types/import", h.AuthMW.RequireRole(identity.Role.CanWrite, h.importSystemType))
	mux.HandleFunc("POST /api/v1/system-types", h.AuthMW.RequireRole(identity.Role.CanWrite, h.createSystemType))
	mux.HandleFunc("PUT /api/v1/system-types/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.updateSystemType))
	mux.HandleFunc("DELETE /api/v1/system-types/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.deleteSystemType))
	mux.HandleFunc("PUT /api/v1/services/{name}/schemas", h.writeService(h.putServiceSchemas))

	// Data schemas (In-Schema / Out-Schema per service). Reads open to any
	// member; mutations require editor+ (viewers are read-only).
	mux.HandleFunc("GET /api/v1/schemas", h.listSchemas)
	mux.HandleFunc("GET /api/v1/schemas/{id}", h.getSchema)
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/schemas", h.AuthMW.RequireRole(identity.Role.CanWrite, h.createSchema))
		mux.HandleFunc("PATCH /api/v1/schemas/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.updateSchema))
		mux.HandleFunc("DELETE /api/v1/schemas/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.deleteSchema))
	} else {
		mux.HandleFunc("POST /api/v1/schemas", h.createSchema)
		mux.HandleFunc("PATCH /api/v1/schemas/{id}", h.updateSchema)
		mux.HandleFunc("DELETE /api/v1/schemas/{id}", h.deleteSchema)
	}

	// Maps — data transformations (XSLT, jq, JSONata, Liquid, …)
	// optionally pinning input ("from") and output ("to") schemas. Reads +
	// the dry-run /execute stay open; create/edit/delete require editor+.
	mux.HandleFunc("GET /api/v1/maps", h.listMaps)
	mux.HandleFunc("GET /api/v1/maps/{id}", h.getMap)
	mux.HandleFunc("POST /api/v1/maps/{id}/execute", h.executeMap)
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/maps", h.AuthMW.RequireRole(identity.Role.CanWrite, h.createMap))
		mux.HandleFunc("PATCH /api/v1/maps/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.updateMap))
		mux.HandleFunc("DELETE /api/v1/maps/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.deleteMap))
	} else {
		mux.HandleFunc("POST /api/v1/maps", h.createMap)
		mux.HandleFunc("PATCH /api/v1/maps/{id}", h.updateMap)
		mux.HandleFunc("DELETE /api/v1/maps/{id}", h.deleteMap)
	}

	// Auth surface. login + logout are listed in main.go's Wrap()
	// skip list so they bypass the auth gate; everything else
	// (including /me) inherits the gate from Wrap.
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("GET /api/v1/auth/install-state", h.installState)
	mux.HandleFunc("POST /api/v1/auth/bootstrap-admin", h.bootstrapAdmin)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", h.forgotPassword)
	mux.HandleFunc("POST /api/v1/auth/reset-password", h.resetPassword)
	mux.HandleFunc("POST /api/v1/auth/mfa-verify", h.mfaVerify)

	// SSO/OIDC login flow — public (pre-session), gated inside the handlers by
	// FeatureSSO. Skip-listed in main (prefix /api/v1/auth/sso/).
	mux.HandleFunc("GET /api/v1/auth/sso/providers", h.ssoProviders)
	mux.HandleFunc("GET /api/v1/auth/sso/{id}/start", h.ssoStart)
	mux.HandleFunc("GET /api/v1/auth/sso/callback", h.ssoCallback)
	// Per-user MFA enrollment (the caller manages their own — session-gated
	// by the middleware Wrap, which requires auth for everything not listed
	// public).
	mux.HandleFunc("GET /api/v1/account/mfa", h.mfaStatus)
	mux.HandleFunc("POST /api/v1/account/mfa/setup", h.mfaSetup)
	mux.HandleFunc("POST /api/v1/account/mfa/enable", h.mfaEnable)
	mux.HandleFunc("POST /api/v1/account/mfa/disable", h.mfaDisable)
	mux.HandleFunc("GET /api/v1/me", h.me)
	// Self-service account edits — name/email + password. Both are
	// gated to the caller's own row by reading p.UserID, so no
	// per-route role check is needed beyond "you must be signed in".
	mux.HandleFunc("PATCH /api/v1/me", h.updateMe)
	mux.HandleFunc("POST /api/v1/me/password", h.changeMyPassword)

	// Organization profile: read for any member, mutate / delete are
	// admin-only. The handler also enforces "the path id == your
	// active org" — there's no cross-org admin path yet.
	mux.HandleFunc("GET /api/v1/orgs/{id}", h.getOrg)
	if h.AuthMW != nil {
		mux.HandleFunc("PATCH /api/v1/orgs/{id}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.blockDemo(h.updateOrg)))
	}

	// Cell-operator surface (super-admin above the org roles): the org
	// lifecycle across the whole cell, cross-org member assignment, and
	// operator management. Every route is RequireOperator-gated — see
	// handlers_operator.go. In single-org self-hosted the bootstrap admin
	// is the operator, so nothing here is out of reach for that deploy.
	if h.AuthMW != nil {
		mux.HandleFunc("GET /api/v1/operator/orgs", h.AuthMW.RequireOperator(h.listOperatorOrgs))
		mux.HandleFunc("POST /api/v1/operator/orgs", h.AuthMW.RequireOperator(h.createOperatorOrg))
		mux.HandleFunc("PATCH /api/v1/operator/orgs/{id}", h.AuthMW.RequireOperator(h.updateOperatorOrg))
		mux.HandleFunc("DELETE /api/v1/operator/orgs/{id}", h.AuthMW.RequireOperator(h.deleteOperatorOrg))
		mux.HandleFunc("GET /api/v1/operator/orgs/{id}/members", h.AuthMW.RequireOperator(h.listOperatorOrgMembers))
		mux.HandleFunc("POST /api/v1/operator/orgs/{id}/members", h.AuthMW.RequireOperator(h.addOperatorOrgMember))
		mux.HandleFunc("PATCH /api/v1/operator/orgs/{id}/members/{user_id}", h.AuthMW.RequireOperator(h.updateOperatorOrgMemberRole))
		mux.HandleFunc("DELETE /api/v1/operator/orgs/{id}/members/{user_id}", h.AuthMW.RequireOperator(h.removeOperatorOrgMember))
		mux.HandleFunc("GET /api/v1/operator/users", h.AuthMW.RequireOperator(h.listOperatorUsers))
		mux.HandleFunc("PUT /api/v1/operator/users/{user_id}/operator", h.AuthMW.RequireOperator(h.setOperatorFlag))
		mux.HandleFunc("PUT /api/v1/operator/users/{user_id}/demo", h.AuthMW.RequireOperator(h.setDemoFlag))
	}

	// Cell-wide settings — telemetry retention, SMTP, and cell security
	// policy. These are SHARED across every org on the cell, so mutating
	// them is the cell operator's job, not a per-org admin's (in single-
	// org self-hosted the bootstrap admin IS the operator, so this stays
	// reachable there). Reads of the non-secret knobs (retention, system)
	// stay open to any signed-in user for UI value; SMTP/security reads
	// are operator-gated because they surface configuration detail.
	mux.HandleFunc("GET /api/v1/cell-settings/retention", h.getRetention)
	mux.HandleFunc("GET /api/v1/cell-settings/system", h.getSystemSettings)
	if h.AuthMW != nil {
		mux.HandleFunc("PATCH /api/v1/cell-settings/retention",
			h.AuthMW.RequireOperator(h.patchRetention))
		mux.HandleFunc("PATCH /api/v1/cell-settings/system",
			h.AuthMW.RequireOperator(h.patchSystemSettings))
		// Global SMTP — operator-only. Read masks the password.
		mux.HandleFunc("GET /api/v1/cell-settings/smtp",
			h.AuthMW.RequireOperator(h.getSMTP))
		mux.HandleFunc("PATCH /api/v1/cell-settings/smtp",
			h.AuthMW.RequireOperator(h.patchSMTP))
		mux.HandleFunc("POST /api/v1/cell-settings/smtp/test",
			h.AuthMW.RequireOperator(h.testSMTP))
		// Cell security policy: operator-only; toggling org-wide MFA
		// enforcement also needs the Enterprise mfa_policy entitlement.
		mux.HandleFunc("GET /api/v1/cell-settings/security",
			h.AuthMW.RequireOperator(h.getSecuritySettings))
		mux.HandleFunc("PATCH /api/v1/cell-settings/security",
			h.AuthMW.RequireOperator(h.requireFeature(license.FeatureMFAPolicy, h.patchSecuritySettings)))
	}

	// Trace-completion rules per integration. Reads open to any
	// signed-in user (the counts feed the integration-detail chip);
	// mutations require admin since rules drive alerts.
	mux.HandleFunc("GET /api/v1/integrations/{id}/completion-rules", h.listTraceCompletionRules)
	mux.HandleFunc("GET /api/v1/integrations/{id}/completion-counts", h.completionCounts)
	mux.HandleFunc("GET /api/v1/integrations/{id}/completion-firings", h.listCompletionFirings)
	// Per-trace firings — drives the trace-level StatusPip on
	// TraceDetail (sticky-delayed traces flip warn or err depending
	// on the rule's severity).
	mux.HandleFunc("GET /api/v1/traces/{id}/completion-firings", h.listCompletionFiringsForTrace)
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/integrations/{id}/completion-rules",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.createTraceCompletionRule))
		mux.HandleFunc("PATCH /api/v1/integrations/{id}/completion-rules/{rid}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.updateTraceCompletionRule))
		mux.HandleFunc("DELETE /api/v1/integrations/{id}/completion-rules/{rid}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.deleteTraceCompletionRule))
		// Marking a delayed trace handled is an operational ack (not a
		// config change), so it only needs write, not admin.
		mux.HandleFunc("POST /api/v1/integrations/{id}/completion-firings/{iid}/handle",
			h.writeAnywhere(h.requireManageIntegration(h.handleCompletionFiring)))
	}

	// Settings: members + tokens. Member mutations require admin
	// (RequireRole with Role.CanAdmin); token mutations are
	// user-scoped (any authed user manages their own PATs, the
	// handler enforces ownership of the target row).
	// The member list is admin-only even for reads: it carries emails,
	// must_reset_password (i.e. which accounts still run initial
	// passwords), login stats, and SSO linkage — a reconnaissance
	// payload, not a directory. Its only UI consumer (Settings) is
	// admin-gated anyway.
	if h.AuthMW != nil {
		mux.HandleFunc("GET /api/v1/settings/members",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.listMembers))
	} else {
		mux.HandleFunc("GET /api/v1/settings/members", h.listMembers)
	}
	// Scoped-capability mirror for the frontend (RBAC v2 §5): what may
	// this session manage? Server gates stay authoritative.
	mux.HandleFunc("GET /api/v1/me/access", h.meAccess)
	// Per-user UI preferences (column layouts etc.) — any authed user,
	// own rows only (user id comes from the session, never the path).
	mux.HandleFunc("GET /api/v1/me/preferences/{key}", h.getPreference)
	mux.HandleFunc("PUT /api/v1/me/preferences/{key}", h.putPreference)
	mux.HandleFunc("GET /api/v1/settings/tokens", h.listTokens)
	mux.HandleFunc("POST /api/v1/settings/tokens", h.createToken)
	mux.HandleFunc("DELETE /api/v1/settings/tokens/{id}", h.revokeToken)
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/settings/members",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.blockDemo(h.addMember)))
		mux.HandleFunc("PATCH /api/v1/settings/members/{user_id}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.blockDemo(h.updateMemberRole)))
		mux.HandleFunc("DELETE /api/v1/settings/members/{user_id}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.blockDemo(h.removeMember)))
		mux.HandleFunc("POST /api/v1/settings/members/{user_id}/password",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.blockDemo(h.adminResetMemberPassword)))

		// Settings → Service accounts (machine identities + their tokens).
		// Org-admin only; tokens are minted/listed/revoked per service account.
		admin := func(fn http.HandlerFunc) http.HandlerFunc { return h.AuthMW.RequireRole(identity.Role.CanAdmin, fn) }
		mux.HandleFunc("GET /api/v1/settings/service-accounts", admin(h.listServiceAccounts))
		mux.HandleFunc("POST /api/v1/settings/service-accounts", admin(h.createServiceAccount))
		mux.HandleFunc("PUT /api/v1/settings/service-accounts/{id}", admin(h.updateServiceAccount))
		mux.HandleFunc("DELETE /api/v1/settings/service-accounts/{id}", admin(h.deleteServiceAccount))
		mux.HandleFunc("GET /api/v1/settings/service-accounts/{id}/groups", admin(h.listServiceAccountGroups))
		mux.HandleFunc("GET /api/v1/settings/service-accounts/{id}/tokens", admin(h.listServiceAccountTokens))
		mux.HandleFunc("POST /api/v1/settings/service-accounts/{id}/tokens", admin(h.createServiceAccountToken))
		mux.HandleFunc("DELETE /api/v1/settings/service-accounts/{id}/tokens/{tid}", admin(h.revokeServiceAccountToken))

		// Announcements (persistent banners) + maintenance windows
		// (alert-delivery suppression). Reading + dismissing is any authed
		// user — announcements are broadcast by design. Org management is
		// admin (+ demo-blocked: it's org communication); cell-wide rows
		// are operator-only. Windows: editors for scoped, admin for
		// all_org (enforced in the handler).
		mux.HandleFunc("GET /api/v1/announcements", h.listMyAnnouncements)
		mux.HandleFunc("POST /api/v1/announcements/{id}/dismiss", h.dismissAnnouncement)
		mux.HandleFunc("GET /api/v1/settings/announcements", admin(h.listOrgAnnouncements))
		mux.HandleFunc("POST /api/v1/settings/announcements", admin(h.blockDemo(h.createOrgAnnouncement)))
		mux.HandleFunc("DELETE /api/v1/settings/announcements/{id}", admin(h.blockDemo(h.deleteOrgAnnouncement)))
		mux.HandleFunc("GET /api/v1/operator/announcements", h.AuthMW.RequireOperator(h.listCellAnnouncements))
		mux.HandleFunc("POST /api/v1/operator/announcements", h.AuthMW.RequireOperator(h.createCellAnnouncement))
		mux.HandleFunc("DELETE /api/v1/operator/announcements/{id}", h.AuthMW.RequireOperator(h.deleteCellAnnouncement))
		// Config export & import — org-admin, demo-blocked, CE both ways
		// (docs/config-transfer-design.md).
		mux.HandleFunc("GET /api/v1/settings/config-export", admin(h.blockDemo(h.exportConfig)))
		mux.HandleFunc("POST /api/v1/settings/config-import", admin(h.blockDemo(h.importConfig)))

		editor := func(fn http.HandlerFunc) http.HandlerFunc { return h.AuthMW.RequireRole(identity.Role.CanWrite, fn) }
		mux.HandleFunc("GET /api/v1/maintenance-windows", h.listMaintenanceWindows)
		mux.HandleFunc("POST /api/v1/maintenance-windows", editor(h.createMaintenanceWindow))
		mux.HandleFunc("PATCH /api/v1/maintenance-windows/{id}", editor(h.updateMaintenanceWindow))
		mux.HandleFunc("DELETE /api/v1/maintenance-windows/{id}", editor(h.endMaintenanceWindow))
	}

	// Settings → Groups. Listing + getting + per-group membership is
	// authed-but-not-admin (any org member can see the group structure
	// so they know where to ask for access). All mutations require
	// org admin.
	mux.HandleFunc("GET /api/v1/settings/groups", h.listGroupsAdmin)
	mux.HandleFunc("GET /api/v1/settings/groups/{id}", h.getGroupAdmin)
	mux.HandleFunc("GET /api/v1/settings/groups/{id}/members", h.listGroupMembersAdmin)
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/settings/groups",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.createGroupAdmin))
		mux.HandleFunc("PATCH /api/v1/settings/groups/{id}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.updateGroupAdmin))
		mux.HandleFunc("DELETE /api/v1/settings/groups/{id}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.deleteGroupAdmin))
		mux.HandleFunc("POST /api/v1/settings/groups/{id}/members",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.addGroupMemberAdmin))
		mux.HandleFunc("PATCH /api/v1/settings/groups/{id}/members/{user_id}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.updateGroupMemberRoleAdmin))
		mux.HandleFunc("DELETE /api/v1/settings/groups/{id}/members/{user_id}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.removeGroupMemberAdmin))
		// Service-account memberships — how scoped SAs gain visibility
		// (docs/service-account-scoping-design.md). Adding goes through
		// POST …/members with service_account_id in the body.
		mux.HandleFunc("PATCH /api/v1/settings/groups/{id}/service-accounts/{sa_id}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.updateGroupServiceAccountRoleAdmin))
		mux.HandleFunc("DELETE /api/v1/settings/groups/{id}/service-accounts/{sa_id}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.removeGroupServiceAccountAdmin))
	}

	// Access policies on a group — the second-axis ABAC layer that
	// generalises kind=service (the static service→group association)
	// into service / integration / attributes / compound / all_org.
	// Reading policies stays open (so the UI can show what exists + an
	// upgrade prompt). Creating/deleting fine-grained access policies is
	// the *advanced* RBAC surface — Enterprise-gated on top of the admin
	// role. Basic admin/editor/viewer + static service→group membership
	// stay in core.
	mux.HandleFunc("GET /api/v1/settings/groups/{id}/policies", h.listGroupPolicies)
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/settings/groups/{id}/policies",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.requireFeature(license.FeatureRBACAdvanced, h.createGroupPolicy)))
		mux.HandleFunc("DELETE /api/v1/settings/groups/{id}/policies/{policy_id}",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.requireFeature(license.FeatureRBACAdvanced, h.deleteGroupPolicy)))
	}

	// Enterprise audit log — admin-only and audit_log-entitlement gated.
	if h.AuthMW != nil {
		mux.HandleFunc("GET /api/v1/audit-log",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.requireFeature(license.FeatureAuditLog, h.listAuditLog)))
		mux.HandleFunc("GET /api/v1/audit-log/verify",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.requireFeature(license.FeatureAuditLog, h.verifyAuditChain)))
	}

	// SSO/OIDC provider + claim-mapping config — admin-only and sso-entitlement
	// gated. The login flow itself is the public /api/v1/auth/sso/* block above.
	if h.AuthMW != nil {
		adminSSO := func(next http.HandlerFunc) http.HandlerFunc {
			return h.AuthMW.RequireRole(identity.Role.CanAdmin, h.requireFeature(license.FeatureSSO, next))
		}
		mux.HandleFunc("GET /api/v1/settings/auth-providers", adminSSO(h.listAuthProviders))
		mux.HandleFunc("POST /api/v1/settings/auth-providers", adminSSO(h.blockDemo(h.createAuthProvider)))
		mux.HandleFunc("PUT /api/v1/settings/auth-providers/{id}", adminSSO(h.blockDemo(h.updateAuthProvider)))
		mux.HandleFunc("DELETE /api/v1/settings/auth-providers/{id}", adminSSO(h.blockDemo(h.deleteAuthProvider)))
		mux.HandleFunc("GET /api/v1/settings/auth-providers/{id}/mappings", adminSSO(h.listClaimMappings))
		mux.HandleFunc("POST /api/v1/settings/auth-providers/{id}/mappings", adminSSO(h.blockDemo(h.createClaimMapping)))
		mux.HandleFunc("DELETE /api/v1/settings/auth-providers/{id}/mappings/{mid}", adminSSO(h.blockDemo(h.deleteClaimMapping)))
	}

	// Per-service group assignment. Listing the groups a service is
	// in is open to anyone with visibility on the service; mutating
	// the set is admin-only.
	mux.HandleFunc("GET /api/v1/services/{name}/groups", h.gateServiceRoute(h.listServiceGroupsHandler))
	if h.AuthMW != nil {
		mux.HandleFunc("PUT /api/v1/services/{name}/groups",
			h.AuthMW.RequireRole(identity.Role.CanAdmin, h.putServiceGroupsHandler))
	}

	// User-defined metadata fields (org-scoped schema + per-target values).
	// Defining the field schema (create/edit/delete) requires editor+;
	// listing + setting per-integration values stay open to any member.
	mux.HandleFunc("GET /api/v1/metadata-fields", h.listMetadataFields)
	mux.HandleFunc("PUT /api/v1/integrations/{id}/metadata", h.writeAnywhere(h.requireManageIntegration(h.putIntegrationMetadata)))
	if h.AuthMW != nil {
		mux.HandleFunc("POST /api/v1/metadata-fields", h.AuthMW.RequireRole(identity.Role.CanWrite, h.createMetadataField))
		mux.HandleFunc("PATCH /api/v1/metadata-fields/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.updateMetadataField))
		mux.HandleFunc("DELETE /api/v1/metadata-fields/{id}", h.AuthMW.RequireRole(identity.Role.CanWrite, h.deleteMetadataField))
	} else {
		mux.HandleFunc("POST /api/v1/metadata-fields", h.createMetadataField)
		mux.HandleFunc("PATCH /api/v1/metadata-fields/{id}", h.updateMetadataField)
		mux.HandleFunc("DELETE /api/v1/metadata-fields/{id}", h.deleteMetadataField)
	}

	mux.HandleFunc("GET /api/v1/services/{name}/tags", h.gateServiceRoute(h.listServiceTags))
	mux.HandleFunc("POST /api/v1/services/{name}/tags/{tagId}", h.writeService(h.attachServiceTag))
	mux.HandleFunc("DELETE /api/v1/services/{name}/tags/{tagId}", h.writeService(h.detachServiceTag))

	mux.HandleFunc("GET /healthz", h.healthz)
	// API docs (public; see the auth skip-list in main.go).
	mux.HandleFunc("GET /api/v1/openapi.json", h.openapiSpec)
	mux.HandleFunc("GET /api/v1/llms.txt", h.llmsSpec)
	mux.HandleFunc("GET /api/docs", h.apiDocs)
	mux.HandleFunc("GET /api/docs/scalar.js", h.scalarAsset)
}

func (h *Handlers) healthz(w http.ResponseWriter, _ *http.Request) {
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// listServices: GET /api/v1/services?range=1h or ?range=ISO/ISO
//
// The persisted services catalog (Postgres) is the source of truth for
// "which services exist". Per-window stats (trace / error counts, last
// seen *in this window*) are layered on from ClickHouse — so services
// that produced no traffic in the picked window still appear, with zero
// counts. That matches the integration detail behaviour the user asked
// for: discovery is window-independent, activity is window-bounded.
func (h *Handlers) listServices(w http.ResponseWriter, r *http.Request) {
	tr := ParseRange(r, time.Hour)

	out, err := h.serviceSummaries(r, tr)
	if err != nil {
		h.Logger.Error("read services catalog failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// List-only enrichment: metadata values + dependency degrees (for the
	// metadata + dependency filters). Kept off the shared serviceSummaries
	// path so the Errors feed doesn't pay for it.
	h.enrichServiceListExtras(r, tr, out)

	// Sort most-recently-active first (within-window if it had any, else
	// catalog last-seen) so the list reads usefully on first glance.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastSeen.After(out[j].LastSeen)
	})

	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"window":   tr.Window(),
		"services": out,
	})
}

// enrichServiceListExtras decorates the services list with the data only the
// list view needs: custom metadata values (for the metadata filter) and the
// service dependency degrees — how many distinct services called each service
// (upstream callers) and how many it called (downstream callees), from the
// window's flow graph. Failures are non-fatal; the list still renders.
func (h *Handlers) enrichServiceListExtras(r *http.Request, tr TimeRange, out []ServiceSummary) {
	if len(out) == 0 {
		return
	}
	orgID := middleware.OrgID(r)
	names := make([]string, len(out))
	for i := range out {
		names[i] = out[i].ServiceName
	}

	// Metadata values, translated from field-id to field-key.
	if fields, err := h.Metadata.ListFields(r.Context(), orgID); err != nil {
		h.Logger.Warn("service list: metadata fields failed", "err", err)
	} else if valuesByService, err := h.Metadata.ServiceValuesBulk(r.Context(), orgID, names); err != nil {
		h.Logger.Warn("service list: metadata values failed", "err", err)
	} else {
		keyByID := make(map[uuid.UUID]string, len(fields))
		for _, f := range fields {
			keyByID[f.ID] = f.Key
		}
		for i := range out {
			raw := valuesByService[out[i].ServiceName]
			if len(raw) == 0 {
				continue
			}
			mv := make(map[string]string, len(raw))
			for fid, v := range raw {
				if k, ok := keyByID[fid]; ok {
					mv[k] = v
				}
			}
			if len(mv) > 0 {
				out[i].MetadataValues = mv
			}
		}
	}

	// Dependency degrees from the window's service flow graph (distinct
	// callers in, distinct callees out).
	edges, err := h.Store.ServiceEdges(r.Context(), names, tr.From, tr.To, nil)
	if err != nil {
		h.Logger.Warn("service list: service edges failed", "err", err)
		return
	}
	upstream := make(map[string]map[string]struct{})   // target → distinct sources
	downstream := make(map[string]map[string]struct{}) // source → distinct targets
	add := func(m map[string]map[string]struct{}, k, v string) {
		set, ok := m[k]
		if !ok {
			set = make(map[string]struct{})
			m[k] = set
		}
		set[v] = struct{}{}
	}
	for _, e := range edges {
		add(upstream, e.Target, e.Source)
		add(downstream, e.Source, e.Target)
	}
	for i := range out {
		out[i].UpstreamCount = len(upstream[out[i].ServiceName])
		out[i].DownstreamCount = len(downstream[out[i].ServiceName])
	}
}

// serviceSummaries builds a ServiceSummary for every catalog service the
// caller is allowed to see, with status computed against the window's
// error count after applying each service's "clear errors" watermark.
// Shared by listServices and the Errors feed so the two never drift on
// visibility or ack semantics. Unsorted — callers order as they like.
func (h *Handlers) serviceSummaries(r *http.Request, tr TimeRange) ([]ServiceSummary, error) {
	catalogServices, err := h.Catalog.AllServices(r.Context(), middleware.OrgID(r))
	if err != nil {
		return nil, err
	}

	// Apply group-based visibility. Org admins see everything; for
	// everyone else, filter the catalog to services that live in at
	// least one of the user's groups. Users with no group memberships
	// see no services (strict-isolation default — group adds OR'd in).
	if filtered, ok := h.applyServiceVisibility(r, catalogServices); ok {
		catalogServices = filtered
	}

	// Per-window stats. Quiet services simply aren't in this map; we
	// look up with a zero default below.
	windowRows, err := h.Store.ListServices(r.Context(), tr.From, tr.To)
	if err != nil {
		h.Logger.Warn("list window services failed", "err", err)
		windowRows = nil
	}
	statsByName := make(map[string]store.ServiceRow, len(windowRows))
	for _, w := range windowRows {
		statsByName[w.ServiceName] = w
	}

	firingServices, err := h.Alerts.FiringHealthServices(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("firing health services failed", "err", err)
		firingServices = map[string]bool{}
	}
	// "Clear errors" watermarks — applied to the error count below.
	errAcks := h.errorAcks(r.Context(), middleware.OrgID(r))
	// Persisted, unacknowledged trace errors — drive "unhealthy" until acked.
	openErrors := h.openErrorServices(r.Context(), middleware.OrgID(r))

	// Bulk tag load for the full catalog list.
	serviceNames := make([]string, 0, len(catalogServices))
	for _, cs := range catalogServices {
		serviceNames = append(serviceNames, cs.ServiceName)
	}
	tagsByService, err := h.Tags.ListForServices(r.Context(), middleware.OrgID(r), serviceNames)
	if err != nil {
		h.Logger.Warn("list tags for services failed", "err", err)
		tagsByService = map[string][]tags.Tag{}
	}

	out := make([]ServiceSummary, 0, len(catalogServices))
	for _, cs := range catalogServices {
		win := statsByName[cs.ServiceName] // zero-valued if no window traffic

		ints, err := h.Resolver.IntegrationsFor(r.Context(), middleware.OrgID(r), cs.ServiceName)
		if err != nil {
			h.Logger.Warn("resolve integrations failed", "err", err, "service", cs.ServiceName)
		}

		// Status is computed against the window's error count, after
		// applying any "clear errors" watermark (errors the team has
		// already reviewed don't count). A firing health check still
		// drives unhealthy since that reflects current state, not the
		// window — and pushed/telemetry health checks both fire through
		// the same instance path, so firingServices covers them.
		effErr := h.effectiveErrorCount(r.Context(), cs.ServiceName, win.ErrorTraceCount, tr.From, tr.To, errAcks)
		status := statusWithOpenErrors(
			computeServiceStatus(effErr, firingServices[cs.ServiceName]),
			openErrors[cs.ServiceName],
		)

		// Discovery timestamps come from the catalog (window-independent).
		firstSeen := cs.FirstSeenAt
		lastSeen := cs.LastSeenAt
		if !win.LastSeen.IsZero() {
			// Within-window last-seen is more useful when the window has
			// data: it answers "was this service active *during this
			// window*?" rather than "ever?".
			lastSeen = win.LastSeen
		}

		// Namespace falls back to the catalog when the window's row is
		// missing (no traces -> blank string from the zero ServiceRow).
		namespace := win.ServiceNamespace
		if namespace == "" {
			namespace = cs.ServiceNamespace
		}

		svcTags := tagsByService[cs.ServiceName]
		if svcTags == nil {
			svcTags = []tags.Tag{}
		}
		out = append(out, ServiceSummary{
			ServiceName:      cs.ServiceName,
			ServiceNamespace: namespace,
			FirstSeen:        &firstSeen,
			LastSeen:         lastSeen,
			TraceCount:       win.TraceCount,
			ErrorTraceCount:  effErr,
			Integrations:     toIntegrationRefs(ints),
			ServiceFacets:    h.classifyServiceFacets(r.Context(), cs.ServiceName, tr),
			Tags:             svcTags,
			Status:           status,
			IsSystem:         cs.IsSystem,
			SystemKind:       cs.SystemKind,
		})
	}

	return out, nil
}

// errorAcks fetches every service's "clear errors" watermark for the
// org in one round trip. Nil-safe + fails open to an empty map so a
// missing store or a DB blip never hides errors.
func (h *Handlers) errorAcks(ctx context.Context, orgID uuid.UUID) map[string]erroracks.Ack {
	if h.ErrorAcks == nil {
		return map[string]erroracks.Ack{}
	}
	acks, err := h.ErrorAcks.GetAll(ctx, orgID)
	if err != nil {
		h.Logger.Warn("load error acks failed; ignoring", "err", err)
		return map[string]erroracks.Ack{}
	}
	return acks
}

// openErrorServices returns the set of services that currently have
// persisted, UNACKNOWLEDGED trace errors — the same signal the Errors feed
// surfaces as "open errors" (openServiceErrors). A service is open iff its
// most recent error trace over the retention lookback post-dates its
// clear-errors watermark. This is window-INDEPENDENT on purpose: an
// unacknowledged error keeps the service unhealthy even after it scrolls
// out of the active time window, until someone acknowledges it. Fails open
// to an empty set (status simply won't reflect persisted errors) so a
// ClickHouse blip never wedges the lists.
func (h *Handlers) openErrorServices(ctx context.Context, orgID uuid.UUID) map[string]bool {
	to := time.Now().UTC()
	from := to.Add(-openErrorLookback)
	stats, err := h.Store.ErrorTraceStatsByService(ctx, from, to)
	if err != nil {
		h.Logger.Warn("open-error services scan failed; status omits persisted errors", "err", err)
		return map[string]bool{}
	}
	acks := h.errorAcks(ctx, orgID)
	out := make(map[string]bool, len(stats))
	for _, st := range stats {
		if st.ErrorTraces == 0 {
			continue
		}
		watermark := acks[st.ServiceName].AcknowledgedUntil
		// Unacknowledged iff never cleared, or the latest error is newer
		// than the watermark — mirrors openServiceErrors so the Errors page
		// and the health status always agree.
		if !watermark.IsZero() && !st.LastErrorAt.After(watermark) {
			continue
		}
		out[st.ServiceName] = true
	}
	return out
}

// openErrorCount returns the number of persisted, unacknowledged error
// traces for a single service over the open-error lookback — the count
// behind the built-in "error span → unhealthy" check. 0 when the service
// has no open errors (acknowledged, or none). Mirrors openErrorServices'
// acknowledgement logic for exactly one service so the health pill, the
// Errors page, and the service's health-check view all agree.
func (h *Handlers) openErrorCount(ctx context.Context, orgID uuid.UUID, service string) uint64 {
	to := time.Now().UTC()
	from := to.Add(-openErrorLookback)
	stats, err := h.Store.ErrorTraceStatsByService(ctx, from, to)
	if err != nil {
		h.Logger.Warn("open-error count scan failed; status omits persisted errors", "err", err, "service", service)
		return 0
	}
	acks := h.errorAcks(ctx, orgID)
	for _, st := range stats {
		if st.ServiceName != service || st.ErrorTraces == 0 {
			continue
		}
		watermark := acks[st.ServiceName].AcknowledgedUntil
		// Unacknowledged iff never cleared, or the latest error post-dates
		// the watermark.
		if !watermark.IsZero() && !st.LastErrorAt.After(watermark) {
			return 0
		}
		return st.ErrorTraces
	}
	return 0
}

// effectiveErrorCount applies a service's "clear errors" watermark to a
// window error count: if the team cleared errors at a point inside the
// window, the count becomes the number of NEW error traces since that
// point (so the service reads healthy again until fresh failures). No
// ack, or an ack older than the window start, leaves the count
// unchanged — and a zero count short-circuits without a ClickHouse hit,
// so the common (unacked) path costs nothing extra.
func (h *Handlers) effectiveErrorCount(ctx context.Context, service string, windowErr uint64, from, to time.Time, acks map[string]erroracks.Ack) uint64 {
	if windowErr == 0 {
		return 0
	}
	ack, ok := acks[service]
	if !ok || !ack.AcknowledgedUntil.After(from) {
		return windowErr
	}
	n, err := h.Store.ErrorTraceCountSince(ctx, service, ack.AcknowledgedUntil, to, nil)
	if err != nil {
		h.Logger.Warn("effective error count failed; using raw", "err", err, "service", service)
		return windowErr
	}
	return n
}

// computeServiceStatus returns a service's status. Health is driven SOLELY
// by configured health checks: a service is "unhealthy" iff a check bound to
// it is firing, otherwise "ok". Raw trace errors no longer flip a service to
// an error state on their own — only an unhealthy health check does. The
// errorCount arg is retained (callers still pass the windowed error count for
// the response body) but deliberately does not affect status.
func computeServiceStatus(errorCount uint64, healthRuleFiring bool) string {
	_ = errorCount // intentionally ignored: status is health-check-driven only
	if healthRuleFiring {
		return "unhealthy"
	}
	return "ok"
}

// aggregateStatus folds many service statuses into one. Used for an
// integration whose health is the worst of its constituents. Empty
// input means the integration has no matching services yet — we
// surface that as "quiet" rather than "ok" so it reads honestly.
func aggregateStatus(statuses []string) string {
	if len(statuses) == 0 {
		return "quiet"
	}
	hasErrors := false
	for _, s := range statuses {
		switch s {
		case "unhealthy":
			return "unhealthy"
		case "errors":
			hasErrors = true
		}
	}
	if hasErrors {
		return "errors"
	}
	return "ok"
}

// statusWithDelays folds trace-completion delays into an integration's
// aggregate status. A missed SLA is a failure, so any open delay pulls
// an otherwise-healthy integration to "errors". It never downgrades
// "unhealthy" (the worse state wins).
func statusWithDelays(base string, delayed uint64) string {
	if delayed == 0 || base == "unhealthy" || base == "errors" {
		return base
	}
	return "errors"
}

// statusWithIntegrationCheck pulls an integration to "unhealthy" when it
// has a firing health check bound directly to it (FiringHealthIntegrations).
// A failing check the operator defined for the integration is the strongest
// "this is broken" signal, so it wins over service-derived status — the
// same way a service-scoped firing check makes its service unhealthy.
func statusWithIntegrationCheck(base string, failing bool) string {
	if failing {
		return "unhealthy"
	}
	return base
}

// statusWithOpenErrors used to escalate a service to "unhealthy" on
// persisted, unacknowledged trace errors (the built-in "error span detected"
// signal). That auto-flag is gone: a service's health now reflects ONLY its
// configured health checks, so unacknowledged error traces no longer change
// its status. The function is kept (callers still pass the open-error flag)
// but is now a pass-through. The errors themselves remain visible on the
// Errors page; they just don't colour the service red by themselves.
func statusWithOpenErrors(base string, hasOpenErrors bool) string {
	_ = hasOpenErrors // intentionally ignored: open errors no longer drive status
	return base
}

// mergedFacets is the built-in code-defined facets plus the org's custom
// facets (servicefacets store), the latter marked Custom + never auto-matched
// (applied only via overrides). The single source of truth for the facet
// universe an org sees. The custom table is tiny + indexed.
func (h *Handlers) mergedFacets(ctx context.Context, orgID uuid.UUID) []servicetypes.ServiceFacet {
	all := h.ServiceFacets.All()
	if h.ServiceFacetsCustom == nil {
		return all
	}
	custom, err := h.ServiceFacetsCustom.List(ctx, orgID)
	if err != nil {
		h.Logger.Warn("merged facets: list custom failed", "err", err)
		return all
	}
	out := make([]servicetypes.ServiceFacet, 0, len(all)+len(custom))
	out = append(out, all...)
	for _, c := range custom {
		out = append(out, servicetypes.ServiceFacet{Slug: c.Slug, Name: c.Name, Description: c.Description, Custom: true})
	}
	return out
}

// classifyServiceFacets runs the service profile + registry match,
// applies the service's manual overrides, and returns the compact list
// of every effective facet the service carries (each tagged auto /
// manual). Profiling failures are logged; the always-on `core` facet
// still matches the empty profile, so the response always has at least
// one entry.
func (h *Handlers) classifyServiceFacets(ctx context.Context, serviceName string, tr TimeRange) []ServiceFacetRef {
	resolver := h.ioResolverFor(ctx, serviceName)
	prof, err := h.Store.ServiceProfile(ctx, resolver, serviceName, tr.From, tr.To)
	if err != nil {
		h.Logger.Warn("service profile failed during classify", "err", err, "service", serviceName)
	}
	profile := toProfile(serviceName, prof)
	auto := h.ServiceFacets.MatchAll(profile)
	resolved := h.resolveFacets(h.mergedFacets(ctx, middleware.OrgIDFromContext(ctx)), auto, h.facetOverridesFor(ctx, serviceName))
	out := make([]ServiceFacetRef, 0, len(resolved))
	for _, rf := range resolved {
		out = append(out, ServiceFacetRef{Slug: rf.facet.Slug, Name: rf.facet.Name, Source: rf.source})
	}
	return out
}

// serviceDetail: GET /api/v1/services/{name}?range=1h
func (h *Handlers) serviceDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	// Visibility is enforced by gateServiceRoute at the mux layer.
	tr := ParseRange(r, time.Hour)

	stats, err := h.Store.ServiceStats(r.Context(), name, tr.From, tr.To)
	if err != nil {
		h.Logger.Error("service stats failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	recent, err := h.Store.RecentSpans(r.Context(), name, tr.From, tr.To, 50)
	if err != nil {
		h.Logger.Error("recent spans failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	ints, err := h.Resolver.IntegrationsFor(r.Context(), middleware.OrgID(r), name)
	if err != nil {
		h.Logger.Warn("resolve integrations failed", "err", err, "service", name)
	}

	svcTags, err := h.Tags.ListForService(r.Context(), middleware.OrgID(r), name)
	if err != nil {
		h.Logger.Warn("list service tags failed", "err", err, "service", name)
		svcTags = []tags.Tag{}
	}

	errRate := 0.0
	if stats.TraceCount > 0 {
		errRate = float64(stats.ErrorTraceCount) / float64(stats.TraceCount)
	}

	// Bucketed golden-signal series for the sparklines. Non-fatal: a
	// failure just omits the series and the sparklines render flat.
	var statsSeries *ServiceStatsSeries
	if ser, sErr := h.Store.ServiceStatsSeries(r.Context(), name, tr.From, tr.To, 24); sErr != nil {
		h.Logger.Warn("service stats series failed", "err", sErr, "service", name)
	} else {
		statsSeries = &ServiceStatsSeries{
			Traces:    ser.Traces,
			ErrorRate: ser.ErrorRate,
			P50Ms:     ser.P50Ms,
			P95Ms:     ser.P95Ms,
		}
	}

	// Health: a firing service-bound health check makes the service
	// unhealthy (same logic as the services list). Pushed + telemetry
	// checks both fire through the same instance path.
	firingServices, err := h.Alerts.FiringHealthServices(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Warn("firing health services failed", "err", err, "service", name)
		firingServices = map[string]bool{}
	}

	// Apply the "clear errors" watermark: status + the headline error
	// count/rate reflect only errors since the team last cleared.
	errAcks := h.errorAcks(r.Context(), middleware.OrgID(r))
	effErr := h.effectiveErrorCount(r.Context(), name, stats.ErrorTraceCount, tr.From, tr.To, errAcks)
	if stats.TraceCount > 0 {
		errRate = float64(effErr) / float64(stats.TraceCount)
	}
	// Persisted, unacknowledged errors keep the service unhealthy regardless
	// of the active window — until they're acknowledged on the Errors page.
	// The count is also surfaced so the health-check view can show the
	// built-in "error span detected" check as the firing reason.
	openErrCount := h.openErrorCount(r.Context(), middleware.OrgID(r), name)
	status := statusWithOpenErrors(
		computeServiceStatus(effErr, firingServices[name]),
		openErrCount > 0,
	)

	resp := ServiceDetail{
		ServiceName:      name,
		ServiceNamespace: stats.ServiceNamespace,
		Status:           status,
		Window:           tr.Window(),
		Stats: ServiceStats{
			TraceCount:      stats.TraceCount,
			ErrorTraceCount: effErr,
			ErrorRate:       errRate,
			P50DurationMs:   stats.P50DurationNs / 1_000_000,
			P95DurationMs:   stats.P95DurationNs / 1_000_000,
		},
		StatsSeries:    statsSeries,
		Integrations:   toIntegrationRefs(ints),
		Tags:           svcTags,
		RecentSpans:    toSpanSummaries(recent),
		ErrorAck:       h.errorAckView(r.Context(), name, errAcks),
		OpenErrorCount: openErrCount,
	}
	// System flag (from the catalog) — drives the "Mark as system" control.
	if cat, ok, err := h.Catalog.GetService(r.Context(), middleware.OrgID(r), name); err == nil && ok {
		resp.IsSystem = cat.IsSystem
		resp.SystemKind = cat.SystemKind
		resp.BadgePublic = cat.BadgePublic
	}
	// Per-signal visibility for the caller (RBAC v2 §7) — drives tab display.
	for _, sig := range identity.AllSignals {
		if h.serviceSignalVisible(r, name, sig) {
			resp.VisibleSignals = append(resp.VisibleSignals, string(sig))
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

// errorAckView returns the display model for a service's current
// "clear errors" acknowledgement (with the clearer's name resolved), or
// nil if the service hasn't been cleared.
func (h *Handlers) errorAckView(ctx context.Context, service string, acks map[string]erroracks.Ack) *erroracks.Ack {
	ack, ok := acks[service]
	if !ok {
		return nil
	}
	if ack.AcknowledgedBy != nil && h.Identity != nil {
		if u, err := h.Identity.GetUserByID(ctx, *ack.AcknowledgedBy); err == nil {
			ack.AcknowledgedByName = u.Name
		}
	}
	return &ack
}

// search: GET /api/v1/search?q=foo&window=1h&integration=<uuid>&service=<name>
//
// The optional filters narrow the spans considered:
//   - `service` restricts to spans from exactly that service name.
//   - `integration` resolves the integration's matchers to a list of
//     service names and restricts to those.
//
// If both are set, `service` takes precedence (more specific). The
// matcher resolution path uses the same logic as the integration
// detail page so the two views stay consistent.
func (h *Handlers) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "query parameter q is required")
		return
	}
	if len(q) > 256 {
		httpserver.WriteError(w, http.StatusBadRequest, "q is too long")
		return
	}
	tr := ParseRange(r, time.Hour)
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	onlyFailed := strings.EqualFold(r.URL.Query().Get("only_failed"), "true") ||
		r.URL.Query().Get("only_failed") == "1"

	var serviceFilter []string
	// `service` (single-name) takes precedence over `integration` —
	// it's more specific and avoids a round trip to resolve matchers.
	if v := strings.TrimSpace(r.URL.Query().Get("service")); v != "" {
		serviceFilter = []string{v}
	} else if v := strings.TrimSpace(r.URL.Query().Get("integration")); v != "" {
		intID, err := uuid.Parse(v)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
			return
		}
		candidates, err := h.Store.DistinctServiceNames(r.Context(), tr.From, tr.To)
		if err != nil {
			h.Logger.Error("distinct services failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		matched, err := h.Resolver.ServicesForIntegration(r.Context(), intID, candidates)
		if err != nil {
			h.Logger.Error("services for integration failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		if len(matched) == 0 {
			httpserver.WriteJSON(w, http.StatusOK, SearchResponse{
				Query:   q,
				Window:  tr.Window(),
				Total:   0,
				Results: []TraceSearchResult{},
			})
			return
		}
		serviceFilter = matched
	}

	// G5: enforce policy-based service visibility — intersect with
	// the policy allowlist after the explicit ?service= / ?integration
	// filters have been applied.
	pf := h.resolveServiceFilterSignal(r, "", serviceFilter, identity.SignalMessages)
	if pf.Blocked || pf.EmptyAccess {
		httpserver.WriteJSON(w, http.StatusOK, SearchResponse{
			Query:   q,
			Window:  tr.Window(),
			Total:   0,
			Results: []TraceSearchResult{},
		})
		return
	}
	serviceFilter = pf.ServiceIn

	rows, err := h.Store.SearchTraces(r.Context(), q, tr.From, tr.To, limit, serviceFilter, onlyFailed)
	if err != nil {
		h.Logger.Error("search failed", "err", err, "q", q)
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
		Query:   q,
		Window:  tr.Window(),
		Total:   len(results),
		Results: results,
	})
}

// serviceTraces: GET /api/v1/services/{name}/traces?window=1h&limit=50
//
// Returns the recent traces that involved this service, summarized
// (duration, span/service counts, error flag) plus a representative
// attribute snapshot from the service's first span in each trace.
func (h *Handlers) serviceTraces(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	if !h.serviceSignalVisible(r, name, identity.SignalTraces) {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"service_name": name, "window": ParseRange(r, time.Hour).Window(), "traces": []any{}})
		return
	}
	tr := ParseRange(r, time.Hour)
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	onlyFailed := strings.EqualFold(r.URL.Query().Get("only_failed"), "true") ||
		r.URL.Query().Get("only_failed") == "1"
	rows, err := h.Store.RecentTracesForService(r.Context(), name, tr.From, tr.To, limit, onlyFailed)
	if err != nil {
		h.Logger.Error("recent traces for service failed", "err", err, "service", name)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}

	out := make([]TraceSummary, 0, len(rows))
	for _, t := range rows {
		out = append(out, TraceSummary{
			TraceID:       t.TraceID,
			TraceStart:    t.TraceStart,
			DurationMs:    t.DurationMs,
			HasError:      t.HasError,
			TotalSpans:    t.TotalSpans,
			ServiceCount:  t.ServiceCount,
			FirstSpanName: t.ServiceFirstSpanName,
			Attributes:    mergeAttributes(t.ServiceResourceAttrs, t.ServiceSpanAttrs),
		})
	}
	httpserver.WriteJSON(w, http.StatusOK, ServiceTracesResponse{
		ServiceName: name,
		Window:      tr.Window(),
		Traces:      out,
	})
}

// traceDetail: GET /api/v1/traces/{traceId}
func (h *Handlers) traceDetail(w http.ResponseWriter, r *http.Request) {
	traceID := strings.ToLower(strings.TrimSpace(r.PathValue("traceId")))
	if traceID == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "trace id is required")
		return
	}
	// Fetch one over the default cap so we can detect truncation
	// cheaply (no separate count query). If we get back > cap rows we
	// drop the surplus and flag the response. The +1 row is the
	// signal; nothing else.
	const cap = 5000
	rows, err := h.Store.SpansForTrace(r.Context(), traceID, cap+1)
	if err != nil {
		h.Logger.Error("trace detail failed", "err", err, "trace_id", traceID)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if len(rows) == 0 {
		httpserver.WriteError(w, http.StatusNotFound, "no spans found for this trace")
		return
	}
	truncated := false
	if len(rows) > cap {
		rows = rows[:cap]
		truncated = true
	}
	// Group-scoped visibility: the deep link must honour the caller's
	// policy allowlist like the trace *list* endpoints already do —
	// otherwise a trace id leaked via logs reads any payload in the org.
	// Spans from invisible services are dropped; a trace with no visible
	// spans is indistinguishable from a nonexistent one.
	if allowed, hasFilter := h.signalServiceFilter(r, identity.SignalTraces); hasFilter {
		allowedSet := make(map[string]struct{}, len(allowed))
		for _, n := range allowed {
			allowedSet[n] = struct{}{}
		}
		kept := rows[:0]
		for _, row := range rows {
			if _, ok := allowedSet[row.ServiceName]; ok {
				kept = append(kept, row)
			}
		}
		rows = kept
		if len(rows) == 0 {
			httpserver.WriteError(w, http.StatusNotFound, "no spans found for this trace")
			return
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, TraceDetail{
		TraceID:   traceID,
		Spans:     toSpanSummaries(rows),
		Truncated: truncated,
	})
}

// helpers

func toSpanSummaries(rows []store.SpanRow) []SpanSummary {
	out := make([]SpanSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, SpanSummary{
			Timestamp:          r.Timestamp,
			TraceID:            r.TraceID,
			SpanID:             r.SpanID,
			ParentSpanID:       r.ParentSpanID,
			ServiceName:        r.ServiceName,
			SpanName:           r.SpanName,
			SpanKind:           r.SpanKind,
			StatusCode:         r.StatusCode,
			StatusMessage:      r.StatusMessage,
			DurationMs:         float64(r.DurationNs) / 1_000_000,
			Attributes:         mergeAttributes(r.ResourceAttributes, r.SpanAttributes),
			ResourceAttributes: r.ResourceAttributes,
			SpanAttributes:     r.SpanAttributes,
		})
	}
	return out
}

// mergeAttributes flattens resource and span attributes into a single
// map for UI display. Span attributes win on conflict because they're
// more specific to the operation.
func mergeAttributes(resource, span map[string]string) map[string]string {
	if len(resource) == 0 && len(span) == 0 {
		return nil
	}
	out := make(map[string]string, len(resource)+len(span))
	for k, v := range resource {
		out[k] = v
	}
	for k, v := range span {
		out[k] = v
	}
	return out
}

func toIntegrationRefs(ints []integrations.Integration) []IntegrationRef {
	out := make([]IntegrationRef, 0, len(ints))
	for _, i := range ints {
		out = append(out, IntegrationRef{
			ID:   i.ID.String(),
			Slug: i.Slug,
			Name: i.Name,
		})
	}
	return out
}
