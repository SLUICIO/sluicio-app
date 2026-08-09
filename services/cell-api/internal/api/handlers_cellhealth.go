// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Reporting this cell's own health (issue #14).
//
// The trap this is designed around: a monitoring system reporting on its
// own health is only credible while it is running. The single most
// important fact a platform engineer needs — Sluicio is down — is
// exactly the fact a self-hosted health page can never deliver, because
// it will be as down as everything else.
//
// So the API endpoint is the load-bearing part, not the page. It is
// meant to be polled by something outside the cell. Three questions,
// deliberately separated because they have different consumers:
//
//  1. /healthz — am I alive? For orchestrators. Cheap, unauthenticated,
//     touches no database. Already existed and is unchanged.
//  2. /readyz — are my dependencies reachable? For a load balancer, so a
//     cell that cannot reach Postgres stops receiving traffic rather
//     than failing every request it is handed.
//  3. /api/v1/cell-health — am I doing my job? The full report,
//     including whether the background loops are still running, which is
//     the failure nothing else notices.
//
// Two rules, both settled on the issue:
//
//   - Nothing here is keyed by ORGANISATION. A cell operator and a
//     tenant's admins can be different parties, and per-org figures
//     would let the operator read each tenant's activity level. That is
//     customer information wearing operational clothing.
//   - Capacity is REPORTED, not judged. A number with a documented
//     meaning beats an unexplained red state that operators learn to
//     ignore. A fact becomes a fault only when it has a consequence: a
//     dependency that has stopped answering is a fault; a disk at 85% is
//     an observation.

package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/cellhealth"
)

// dependencyProbe is one backing service's reachability.
type dependencyProbe struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// LatencyMS is how long the probe took. Reported rather than judged:
	// slow is a fact whose threshold differs per deployment.
	LatencyMS int64 `json:"latency_ms"`
	// Error is the failure as the cell saw it, so an operator does not
	// have to reproduce the probe to find out what went wrong.
	Error string `json:"error,omitempty"`
}

// CellHealthResponse is the machine-readable report.
type CellHealthResponse struct {
	// Status is "ok" or "degraded". It is driven ONLY by things with a
	// consequence: a dependency that will not answer, or a background
	// loop that has stopped. Capacity never moves it.
	Status       string                  `json:"status"`
	CheckedAt    time.Time               `json:"checked_at"`
	Dependencies []dependencyProbe       `json:"dependencies"`
	Loops        []cellhealth.LoopHealth `json:"loops"`
	// Problems restates what is wrong in one flat list, so a caller can
	// alert on it without walking the structure.
	Problems []string `json:"problems"`
}

// probeDependencies checks that the databases answer.
//
// A probe is a real query rather than a connection-pool inspection: a
// pool can hold handles to a server that stopped answering, which is
// precisely the outage worth catching.
func (h *Handlers) probeDependencies(ctx context.Context) []dependencyProbe {
	out := []dependencyProbe{}

	pg := dependencyProbe{Name: "postgres", Status: string(cellhealth.StatusOK)}
	start := time.Now()
	if h.PGPool == nil {
		pg.Status = string(cellhealth.StatusUnknown)
		pg.Error = "no pool configured"
	} else if err := h.PGPool.Ping(ctx); err != nil {
		pg.Status = "down"
		pg.Error = err.Error()
	}
	pg.LatencyMS = time.Since(start).Milliseconds()
	out = append(out, pg)

	ch := dependencyProbe{Name: "clickhouse", Status: string(cellhealth.StatusOK)}
	start = time.Now()
	if h.ClickHouseConn == nil {
		ch.Status = string(cellhealth.StatusUnknown)
		ch.Error = "no connection configured"
	} else if err := h.ClickHouseConn.Ping(ctx); err != nil {
		ch.Status = "down"
		ch.Error = err.Error()
	}
	ch.LatencyMS = time.Since(start).Milliseconds()
	out = append(out, ch)

	return out
}

// readyz: GET /readyz — dependencies reachable?
//
// Unauthenticated like /healthz, and deliberately says nothing beyond
// which dependency is unhappy: it is reachable by anyone who can reach
// the port, so it must not carry anything worth learning.
func (h *Handlers) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	deps := h.probeDependencies(ctx)
	ready := true
	names := []string{}
	for _, d := range deps {
		if d.Status == "down" {
			ready = false
			names = append(names, d.Name)
		}
	}
	code := http.StatusOK
	status := "ready"
	if !ready {
		// 503 so a load balancer takes the instance out of rotation
		// rather than handing it requests it cannot serve.
		code = http.StatusServiceUnavailable
		status = "not ready"
	}
	httpserver.WriteJSON(w, code, map[string]any{"status": status, "unavailable": names})
}

// healthTokenOK reports whether the request carries the cell's health
// token.
//
// This exists because the operator gate structurally cannot serve the
// use this endpoint is FOR. RequireOperator demands a user flagged
// is_operator and deliberately refuses scope-capped API tokens even
// when their owner is an operator — a hard ceiling worth keeping. But a
// health endpoint that only a logged-in human can call is a health
// endpoint that cannot be polled, and polling from outside is the only
// way to learn that the cell is down.
//
// So the machine path is a shared secret set on the cell
// (SLUICIO_HEALTH_TOKEN), the way this class of endpoint is normally
// secured in infrastructure. It is safe precisely because the response
// was designed to carry nothing worth stealing: no organisation names,
// no per-tenant figures, no customer data. Safety comes from what the
// response contains, not from who managed to call it.
//
// Unset means no machine access, not open access.
func healthTokenOK(r *http.Request) bool {
	want := strings.TrimSpace(os.Getenv("SLUICIO_HEALTH_TOKEN"))
	if want == "" {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if got == "" {
		return false
	}
	// Constant time so the endpoint cannot be used as an oracle.
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// cellHealth: GET /api/v1/cell-health — is this cell doing its job?
func (h *Handlers) cellHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	deps := h.probeDependencies(ctx)
	loops := cellhealth.Default().Loops()

	problems := []string{}
	for _, d := range deps {
		if d.Status == "down" {
			problems = append(problems, d.Name+" is not answering: "+d.Error)
		}
	}
	for _, l := range loops {
		if l.Status == cellhealth.StatusStale {
			// Named with its overdue time, because "stopped an hour ago"
			// and "stopped last Tuesday" call for different urgency and
			// an operator should not have to subtract timestamps.
			problems = append(problems, l.Name+" has not completed a cycle, overdue by "+l.Overdue)
		}
	}

	status := "ok"
	if len(problems) > 0 {
		status = "degraded"
	}

	httpserver.WriteJSON(w, http.StatusOK, CellHealthResponse{
		Status:       status,
		CheckedAt:    time.Now().UTC(),
		Dependencies: deps,
		Loops:        loops,
		Problems:     problems,
	})
}
