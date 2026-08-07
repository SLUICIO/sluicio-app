// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Human-demand touchpoints for the demand ledger (design §2). The read
// handlers call these to record that someone actually consumed a piece
// of telemetry — the counterweight to ingest volume that lets the
// Telemetry Advisor distinguish "nobody looks at this" from "expensive
// but load-bearing".
//
// Three rules these helpers exist to enforce in one place:
//
//  1. NEVER on the error path. Demand means consumption; a query that
//     400s or 404s consumed nothing, and counting it would keep dead
//     telemetry looking alive. Call these after the handler has decided
//     it will serve a result.
//  2. NEVER blocking. Writer.Record buffers in memory under a short
//     mutex — no I/O, no error to handle, safe on a nil writer.
//  3. NEVER identifying. Only (org, signal, service, key) reaches the
//     ledger; the request's user is deliberately not a parameter here,
//     so no call site can pass one even by accident.

package api

import (
	"net/http"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/demand"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/messageviews"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// recordDemand notes that a request consumed a signal for a service,
// optionally naming the keys it filtered or charted. An empty key is
// whole-signal demand ("someone read this service's logs").
func (h *Handlers) recordDemand(r *http.Request, signal demand.Signal, service string, keys ...string) {
	orgID := middleware.OrgID(r)
	h.Demand.Record(orgID, signal, service, "", demand.KindHuman)
	for _, k := range keys {
		if k != "" {
			h.Demand.Record(orgID, signal, service, k, demand.KindHuman)
		}
	}
}

// recordDemandServices is recordDemand across a resolved service set —
// the explorer views that scope by integration rather than by one
// service. Capped: a query spanning a hundred services is real demand
// on each, but recording it that way would let one broad query outweigh
// a hundred deliberate narrow ones.
func (h *Handlers) recordDemandServices(r *http.Request, signal demand.Signal, services []string, keys ...string) {
	const maxServices = 25
	if len(services) == 0 {
		h.recordDemand(r, signal, "", keys...)
		return
	}
	if len(services) > maxServices {
		services = services[:maxServices]
	}
	for _, svc := range services {
		h.recordDemand(r, signal, svc, keys...)
	}
}

// logAttrKeys pulls the filtered attribute keys out of a parsed log
// filter set — the keys someone actually used, which is exactly the
// demand signal T3 (dead-weight attribute) needs.
func logAttrKeys(attrs []store.LogAttrFilter) []string {
	out := make([]string, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, a.Key)
	}
	return out
}

// messageAttrKeys is logAttrKeys for the Messages view.
//
// Only payload filters yield keys. The other fields are structural —
// service, status, time, trace id — and are properties of every span
// rather than attributes anyone chose to emit, so counting them as
// attribute demand would make each of them look load-bearing while
// telling T3 nothing about which payload fields are worth their cost.
func messageAttrKeys(filters []messageviews.Filter) []string {
	out := make([]string, 0, len(filters))
	for _, f := range filters {
		if f.Field == messageviews.FieldPayload && f.FieldPath != "" {
			out = append(out, f.FieldPath)
		}
	}
	return out
}

// spanServices is the distinct service set of one fetched trace, in
// first-seen order, for recording demand on a trace deep link.
//
// Opening a trace is demand on every service it crosses, not only on
// whichever one the link came from: the reason the trace is worth
// keeping is the hop-to-hop story, and a ledger that credited just the
// entry point would recommend dropping the middle of every flow anyone
// actually reads.
func spanServices(rows []store.SpanRow) []string {
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for _, r := range rows {
		if r.ServiceName == "" {
			continue
		}
		if _, ok := seen[r.ServiceName]; ok {
			continue
		}
		seen[r.ServiceName] = struct{}{}
		out = append(out, r.ServiceName)
	}
	return out
}
