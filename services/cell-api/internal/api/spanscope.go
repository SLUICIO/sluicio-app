// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What span data a restricted caller may read across the whole cell
// (issue #28, phase 2).
//
// Phase 1 made object visibility correct: an integration granted
// directly is visible, its siblings on the same services are not. But
// the cross-cell surfaces — message search, trace search — filter on a
// list of service NAMES, and an integration-only grant contributes no
// service names at all. So after phase 1 those surfaces were safe and
// empty: the user could open their integration and read it, and found
// nothing when they searched.
//
// Empty is the right failure to have had. It is not a usable product.
//
// # Why a predicate rather than a wider allowlist
//
// The obvious fix is to add the integration's member services to the
// allowlist. That is exactly the bug from phase 1: on a runtime hosting
// several flows those services carry every sibling integration too, so
// the allowlist would hand back what the fix just took away.
//
// An integration is a SLICE of its members' traffic — the same
// definition #27 settled on — so the access predicate has to be the
// slice, not the members:
//
//	ServiceName IN (plainly visible services)
//	  OR (ServiceName IN (integration members) AND <integration matchers>)
//	  OR …one disjunct per granted integration
//
// The matcher DNF is the same one the message search and per-integration
// facet classification already use, so this reuses machinery rather than
// inventing a second definition of what an integration contains.
//
// # Signals this does NOT cover
//
// Logs and metrics deliberately stay closed for an integration-only
// grant. A log record and a metric point are emitted by the SERVICE and
// carry nothing that attributes them to one flow; there is no predicate
// that could separate the granted integration's logs from its
// siblings'. Opening those surfaces on a service-level grant-by-proxy
// would leak precisely what this issue is about, so an integration-only
// grant reads no logs and no metrics. Granting the services alongside
// the integration is the deliberate way to ask for that, and it is what
// the policy's grant_services flag is for.

package api

import (
	"net/http"
	"strings"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// spanScope is a restricted caller's reach over span data.
type spanScope struct {
	// Unrestricted is an admin or a wildcard policy: ignore every other
	// field and apply no filter at all.
	Unrestricted bool
	// Empty is a caller with no reach for this signal. The handler
	// should answer with an empty result and not query ClickHouse.
	Empty bool
	// ServiceIn is a PREFILTER, not the access decision: the union of
	// every service named anywhere in the scope. Safe to use as
	// `ServiceName IN (…)` because Clause is ANDed alongside and is the
	// thing that actually decides. Kept because it lets ClickHouse skip
	// parts before evaluating the predicate.
	ServiceIn []string
	// Clause is the exact predicate, in ClickHouse syntax, with `?`
	// placeholders filled by Args.
	Clause string
	Args   []any
}

// visibleSpanScope resolves a caller's reach over span data for one
// signal, honouring both plain service grants and integration grants.
//
// Fails open to Unrestricted on a resolver error, matching the posture
// of every other visibility helper here: a database blip must not hide
// data somebody is entitled to. It fails CLOSED (Empty) only when the
// resolution succeeded and genuinely returned nothing.
func (h *Handlers) visibleSpanScope(r *http.Request, sig identity.Signal) spanScope {
	allowed, restricted := h.signalServiceFilter(r, sig)
	if !restricted {
		return spanScope{Unrestricted: true}
	}
	granted, integRestricted := h.grantedIntegrations(r)
	if !integRestricted {
		return spanScope{Unrestricted: true}
	}

	var (
		parts    []string
		args     []any
		services []string
		seen     = map[string]struct{}{}
	)
	addService := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		services = append(services, name)
	}

	if len(allowed) > 0 {
		parts = append(parts, "ServiceName IN ("+placeholders(len(allowed))+")")
		for _, n := range allowed {
			args = append(args, n)
			addService(n)
		}
	}

	for id := range granted {
		members, err := h.Catalog.IntegrationServices(r.Context(), id)
		if err != nil {
			h.Logger.Warn("span scope: integration members failed; allowing", "err", err, "integration", id)
			return spanScope{Unrestricted: true}
		}
		if len(members) == 0 {
			continue
		}
		clause := "ServiceName IN (" + placeholders(len(members)) + ")"
		memberArgs := make([]any, 0, len(members))
		for _, n := range members {
			memberArgs = append(memberArgs, n)
			addService(n)
		}
		// The integration's own matchers. Without them this disjunct is
		// "every span of the member services", which is the phase 1 bug
		// wearing a different hat.
		if attrSQL, attrArgs := store.SpanAttrGroupsClause(h.integrationGroups(r.Context(), id)); attrSQL != "" {
			clause = "(" + clause + " AND " + attrSQL + ")"
			memberArgs = append(memberArgs, attrArgs...)
		}
		parts = append(parts, clause)
		args = append(args, memberArgs...)
	}

	if len(parts) == 0 {
		return spanScope{Empty: true}
	}
	return spanScope{
		ServiceIn: services,
		Clause:    "(" + strings.Join(parts, " OR ") + ")",
		Args:      args,
	}
}

// placeholders renders n comma-separated `?` binds.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// intersectServiceIn narrows a handler's own service filter to the
// scope's prefilter, preserving the handler's ordering.
//
// Returns ok=false when the intersection is empty, which the handler
// treats exactly as Empty: the caller asked for services none of which
// they can reach.
func intersectServiceIn(requested []string, scope spanScope) ([]string, bool) {
	if scope.Unrestricted || len(requested) == 0 {
		if scope.Unrestricted {
			return requested, true
		}
		return scope.ServiceIn, true
	}
	allow := make(map[string]struct{}, len(scope.ServiceIn))
	for _, n := range scope.ServiceIn {
		allow[n] = struct{}{}
	}
	out := make([]string, 0, len(requested))
	for _, n := range requested {
		if _, ok := allow[n]; ok {
			out = append(out, n)
		}
	}
	return out, len(out) > 0
}
