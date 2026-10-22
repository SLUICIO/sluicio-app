// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Environments (issue #40): an integration is defined once and read for
// all environments or for one. The environment a request reads travels in
// its context, set from ?env=, and integrationGroups applies it, so
// everything that reads an integration's slice follows it.

package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/erroracks"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/envctx"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// NoEnvironmentParam is how ?env= asks for telemetry with no environment.
// Parenthesised so it cannot be mistaken for an environment someone
// actually named "none".
const NoEnvironmentParam = "(none)"

// AllEnvironmentsParam is how ?env= says "all environments" explicitly,
// where its absence would mean the default environment to a page.
const AllEnvironmentsParam = "*"

// withRequestEnvironment narrows an integration's groups to the
// environments the context reads, if any. For the places that build
// groups from matchers they already hold rather than through
// integrationGroups.
func withRequestEnvironment(ctx context.Context, groups [][]store.LogAttrFilter) [][]store.LogAttrFilter {
	if envs, ok := envctx.Set(ctx); ok {
		return store.WithEnvironments(groups, envs)
	}
	return groups
}

// firingInRequest reports whether a check bound to integration id fires
// in any environment the request reads.
func firingInRequest(ctx context.Context, byEnv map[uuid.UUID]map[string]bool, id uuid.UUID) bool {
	envs, _ := envctx.Set(ctx)
	for _, env := range envs {
		if alerting.FiringIn(byEnv, id, env) {
			return true
		}
	}
	return false
}

// parseEnvironmentParam reads ?env=, repeated for several environments
// (a dashboard reading production and test). Absent, empty or * means
// all environments.
func parseEnvironmentParam(r *http.Request) ([]string, bool) {
	var envs []string
	for _, raw := range r.URL.Query()["env"] {
		switch raw {
		case "", AllEnvironmentsParam:
			return nil, false
		case NoEnvironmentParam:
			raw = store.NoEnvironment
		}
		envs = append(envs, raw)
	}
	if len(envs) > 20 {
		envs = envs[:20]
	}
	return envs, len(envs) > 0
}

// WithEnvironmentParam puts ?env= into the request context. Wraps the
// whole API: the parameter only has an effect where an integration's
// slice is read, and everywhere else it is ignored.
func WithEnvironmentParam(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if envs, ok := parseEnvironmentParam(r); ok {
			r = r.WithContext(envctx.WithSet(r.Context(), envs))
		}
		next.ServeHTTP(w, r)
	})
}

// envStatusOrder orders statuses worst first, for the per-environment
// chips.
var envStatusOrder = map[string]int{"unhealthy": 0, "errors": 1, "ok": 2, "quiet": 3}

// fillEnvironmentStatuses sets each summary's per-environment status from
// one batched read of every row's slice split by environment. The fold is
// integrationRollupStatus over that environment's traffic and the checks
// firing for it, with the integration's own rules for the rest: open
// error traces colour a slice only, and member checks speak for it as
// they do across all environments (they know no environment).
func (h *Handlers) fillEnvironmentStatuses(
	ctx context.Context, summaries []IntegrationSummary, slices []store.IntegrationSlice,
	firingByEnv map[uuid.UUID]map[string]bool, memberFiringBy map[uuid.UUID]alerting.ServiceFiring,
	sliceBy map[uuid.UUID]bool, tr TimeRange, acks map[string]erroracks.Ack,
) {
	acked := make(map[string]time.Time, len(acks))
	for name, a := range acks {
		acked[name] = a.AcknowledgedUntil
	}
	byEnv := map[string]map[string]store.SliceStat{}
	if len(slices) > 0 {
		var err error
		byEnv, err = h.Store.SliceTraceStatsByEnvironment(ctx, slices, store.SliceQuery{From: tr.From, To: tr.To, AckedUntil: acked})
		if err != nil {
			h.Logger.Warn("integration environment stats failed", "err", err)
			return
		}
	}
	for i := range summaries {
		s := &summaries[i]
		stats := byEnv[s.ID.String()]
		names := map[string]bool{}
		for env := range stats {
			names[env] = true
		}
		declared := map[string]bool{}
		for _, env := range s.DeclaredEnvironments {
			names[env] = true
			declared[env] = true
		}
		out := make([]EnvironmentStatus, 0, len(names))
		for env := range names {
			st := stats[env]
			// The integration's own rule for what colours it, read in
			// this environment: open error traces count for a slice only,
			// exactly as on the integration's overall status.
			health := integrationHealth{
				Slice:             sliceBy[s.ID],
				Active:            st.Traces > 0,
				MemberFiring:      memberFiringBy[s.ID],
				IntegrationFiring: alerting.FiringIn(firingByEnv, s.ID, env),
			}
			if health.Slice {
				health.OpenSliceErrors = st.OpenErrorTraces
			}
			out = append(out, EnvironmentStatus{
				Environment:     env,
				Status:          integrationRollupStatus(health),
				TraceCount:      st.Traces,
				ErrorTraceCount: st.ErrorTraces,
				Declared:        declared[env],
			})
		}
		sort.Slice(out, func(a, b int) bool {
			if ra, rb := envStatusOrder[out[a].Status], envStatusOrder[out[b].Status]; ra != rb {
				return ra < rb
			}
			return out[a].Environment < out[b].Environment
		})
		s.Environments = out
	}
}
