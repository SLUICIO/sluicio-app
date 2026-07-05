// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integrations

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Resolver classifies a service name into the integrations it belongs
// to, using a short-lived cache of matchers loaded from the store.
//
// The cache is refreshed lazily — when a request comes in and the
// cache is older than the TTL, the resolver reloads. CRUD handlers
// can call Invalidate() to force a refresh on the next call.
type Resolver struct {
	store *Store
	ttl   time.Duration

	mu       sync.RWMutex
	loadedAt time.Time
	matchers []MatcherWithIntegration
}

// NewResolver returns a Resolver that caches matchers for ttl.
func NewResolver(store *Store, ttl time.Duration) *Resolver {
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	return &Resolver{store: store, ttl: ttl}
}

// Invalidate forces the next call to reload matchers from the store.
func (r *Resolver) Invalidate() {
	r.mu.Lock()
	r.loadedAt = time.Time{}
	r.mu.Unlock()
}

// IntegrationsFor returns the integrations whose matchers match the
// supplied service name. The result is deduplicated by integration ID.
func (r *Resolver) IntegrationsFor(ctx context.Context, orgID uuid.UUID, serviceName string) ([]Integration, error) {
	matchers, err := r.snapshot(ctx, orgID)
	if err != nil {
		return nil, err
	}

	seen := map[uuid.UUID]struct{}{}
	var out []Integration
	for _, mi := range matchers {
		if _, dup := seen[mi.Integration.ID]; dup {
			continue
		}
		// Membership is decided by service matchers only; attribute
		// matchers refine the integration's telemetry at query time.
		if mi.Matcher.IsServiceMatcher() && mi.Matcher.Match(serviceName) {
			seen[mi.Integration.ID] = struct{}{}
			out = append(out, mi.Integration)
		}
	}
	return out, nil
}

// ServicesForIntegration returns, from the given set of service names,
// those that match any matcher belonging to the integration.
func (r *Resolver) ServicesForIntegration(ctx context.Context, integrationID uuid.UUID, candidates []string) ([]string, error) {
	matchers, err := r.store.MatchersForIntegration(ctx, integrationID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range candidates {
		for _, m := range matchers {
			// Only service matchers decide membership; attribute matchers
			// are applied as row predicates at query time.
			if m.IsServiceMatcher() && m.Match(name) {
				out = append(out, name)
				break
			}
		}
	}
	return out, nil
}

func (r *Resolver) snapshot(ctx context.Context, orgID uuid.UUID) ([]MatcherWithIntegration, error) {
	r.mu.RLock()
	fresh := !r.loadedAt.IsZero() && time.Since(r.loadedAt) < r.ttl
	matchers := r.matchers
	r.mu.RUnlock()
	if fresh {
		return matchers, nil
	}

	loaded, err := r.store.AllMatchersWithIntegration(ctx, orgID)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.matchers = loaded
	r.loadedAt = time.Now()
	r.mu.Unlock()
	return loaded, nil
}
