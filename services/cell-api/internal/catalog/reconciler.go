// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Reconciler keeps the Postgres `services` catalog and the
// `integration_services` membership table in sync with what's currently
// in ClickHouse. Runs on a fixed cadence in cell-api.
//
// One tick does three things:
//   1. Discover services: SELECT DISTINCT ServiceName ... FROM traces
//      over a wide window (90d), with their first / last span times.
//   2. Upsert into services (updating last_seen_at).
//   3. For every integration in the org, run its matchers over the
//      now-canonical services list and rewrite its row set in
//      integration_services.

package catalog

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// IntegrationSource is the minimum interface the reconciler needs from
// the integrations.Store. Returns every matcher in the org joined with
// its integration; the reconciler groups them.
type IntegrationSource interface {
	AllMatchersWithIntegration(ctx context.Context, orgID uuid.UUID) ([]integrations.MatcherWithIntegration, error)
}

// DiscoverySource is the minimum interface the reconciler needs from
// the ClickHouse store.
type DiscoverySource interface {
	DiscoverServices(ctx context.Context, from, to time.Time) ([]store.ServiceDiscovery, error)
	DiscoverServiceResourceAttributes(ctx context.Context, from, to time.Time) ([]store.ServiceAttribute, error)
	// DistinctAttributeKeys returns distinct span + resource attribute
	// keys seen in the window — the source for the attribute-key catalog.
	DistinctAttributeKeys(ctx context.Context, from, to time.Time, sampleLimit int) ([]store.AttributeKeysRow, error)
}

// AttributeSink is what the reconciler needs from the identity store
// to persist per-service resource-attribute samples (consumed later
// by attribute-based access policies) and the org-wide attribute-key
// catalog (the matcher / filter pickers). Kept as an interface so the
// catalog package doesn't directly import identity.
type AttributeSink interface {
	UpsertServiceResourceAttributes(ctx context.Context, orgID uuid.UUID, serviceName string, attrs map[string]string) error
	PruneStaleServiceResourceAttributes(ctx context.Context, olderThan time.Time) (int64, error)
	UpsertAttributeKeys(ctx context.Context, orgID uuid.UUID, keys map[string]string) error
	PruneStaleAttributeKeys(ctx context.Context, olderThan time.Time) (int64, error)
}

// Reconciler is the background loop that drives the sync.
type Reconciler struct {
	catalog      *Store
	integrations IntegrationSource
	clickhouse   DiscoverySource
	attrs        AttributeSink
	orgID        uuid.UUID
	logger       *slog.Logger
	interval     time.Duration
	window       time.Duration
}

// NewReconciler builds a reconciler. interval is the tick cadence;
// window is how far back to scan ClickHouse for service-name discovery
// each tick. attrs may be nil — in that case attribute discovery is
// skipped (useful in tests + during the bring-up of policy features).
func NewReconciler(
	catalog *Store,
	integrationsSource IntegrationSource,
	clickhouseSource DiscoverySource,
	attrs AttributeSink,
	orgID uuid.UUID,
	logger *slog.Logger,
	interval, window time.Duration,
) *Reconciler {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if window <= 0 {
		window = 90 * 24 * time.Hour
	}
	return &Reconciler{
		catalog:      catalog,
		integrations: integrationsSource,
		clickhouse:   clickhouseSource,
		attrs:        attrs,
		orgID:        orgID,
		logger:       logger,
		interval:     interval,
		window:       window,
	}
}

// Run blocks until ctx is cancelled, ticking every `interval`. A warm
// tick fires immediately so first-request reads see fresh data.
func (r *Reconciler) Run(ctx context.Context) {
	if err := r.RunOnce(ctx); err != nil {
		r.logger.Warn("catalog reconcile (warm) failed", "err", err)
	}
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.RunOnce(ctx); err != nil {
				r.logger.Warn("catalog reconcile failed", "err", err)
			}
		}
	}
}

// RunOnce performs one full reconcile pass: discover, upsert, rewrite
// memberships. Safe to call out-of-band (e.g. right after a matcher
// edit so the UI sees the new membership immediately).
func (r *Reconciler) RunOnce(ctx context.Context) error {
	now := time.Now().UTC()
	from := now.Add(-r.window)

	// 1) Discover services seen in the window.
	rows, err := r.clickhouse.DiscoverServices(ctx, from, now)
	if err != nil {
		return err
	}

	discoveries := make([]Discovery, 0, len(rows))
	for _, row := range rows {
		discoveries = append(discoveries, Discovery{
			ServiceName:      row.ServiceName,
			ServiceNamespace: row.ServiceNamespace,
			FirstSeen:        row.FirstSeen,
			LastSeen:         row.LastSeen,
		})
	}
	if err := r.catalog.UpsertServices(ctx, r.orgID, discoveries); err != nil {
		return err
	}

	// 2) Project matchers across the canonical services list and rewrite
	// the integration_services rows for each integration.
	services, err := r.catalog.AllServices(ctx, r.orgID)
	if err != nil {
		return err
	}
	serviceNames := make([]string, len(services))
	for i, s := range services {
		serviceNames[i] = s.ServiceName
	}

	flat, err := r.integrations.AllMatchersWithIntegration(ctx, r.orgID)
	if err != nil {
		return err
	}

	// Group flat (integration, matcher) rows back into per-integration
	// matcher lists. We also remember integrations that exist but have
	// no matchers (no rows in `flat`) — those should have empty
	// integration_services rather than be skipped.
	byInteg := make(map[uuid.UUID][]integrations.Matcher)
	for _, row := range flat {
		byInteg[row.Integration.ID] = append(byInteg[row.Integration.ID], row.Matcher)
	}

	for integID, matchers := range byInteg {
		matched := projectMatchers(matchers, serviceNames)
		if err := r.catalog.ReplaceIntegrationServices(ctx, integID, r.orgID, matched); err != nil {
			r.logger.Warn("rewrite integration_services failed",
				"integration_id", integID, "err", err)
			continue
		}
	}

	// 3) Per-service resource-attribute snapshot — feeds attribute-
	//    based access policies. Best-effort: a failure logs a warning
	//    but doesn't block the main reconcile path (catalog + matcher
	//    rewrite are the critical-path; attribute snapshot is only a
	//    convenience for the policy resolver, which degrades to "no
	//    match" on stale data).
	if r.attrs != nil {
		attrRows, err := r.clickhouse.DiscoverServiceResourceAttributes(ctx, from, now)
		if err != nil {
			r.logger.Warn("discover service resource attributes failed", "err", err)
		} else {
			// Group by service to minimise per-service round-trips.
			perService := make(map[string]map[string]string, len(attrRows))
			for _, sa := range attrRows {
				m, ok := perService[sa.ServiceName]
				if !ok {
					m = map[string]string{}
					perService[sa.ServiceName] = m
				}
				m[sa.Key] = sa.Value
			}
			for svc, kv := range perService {
				if err := r.attrs.UpsertServiceResourceAttributes(ctx, r.orgID, svc, kv); err != nil {
					r.logger.Warn("upsert service resource attributes failed", "err", err, "service", svc)
				}
			}
			// Drop tuples that haven't been seen in twice the discovery
			// window — attributes a service no longer emits should
			// stop granting access. Twice the window so a brief
			// outage doesn't drop policy fits we'd otherwise want.
			cutoff := now.Add(-2 * r.window)
			if removed, err := r.attrs.PruneStaleServiceResourceAttributes(ctx, cutoff); err != nil {
				r.logger.Warn("prune stale resource attributes failed", "err", err)
			} else if removed > 0 {
				r.logger.Debug("pruned stale resource attributes", "removed", removed)
			}
		}

		// 4) Attribute-KEY catalog — distinct span + resource attribute
		//    keys for the matcher / filter pickers. Same eventual-consistency
		//    + prune model as the resource-attribute snapshot above.
		if keyRows, err := r.clickhouse.DistinctAttributeKeys(ctx, from, now, 5000); err != nil {
			r.logger.Warn("discover attribute keys failed", "err", err)
		} else {
			keys := make(map[string]string, len(keyRows))
			for _, k := range keyRows {
				// Prefer "span" when a key shows up in both maps — a span
				// attribute is the more specific, per-operation source.
				if existing, ok := keys[k.Key]; !ok || (existing != "span" && k.Source == "span") {
					keys[k.Key] = k.Source
				}
			}
			if err := r.attrs.UpsertAttributeKeys(ctx, r.orgID, keys); err != nil {
				r.logger.Warn("upsert attribute keys failed", "err", err)
			}
			cutoff := now.Add(-2 * r.window)
			if removed, err := r.attrs.PruneStaleAttributeKeys(ctx, cutoff); err != nil {
				r.logger.Warn("prune stale attribute keys failed", "err", err)
			} else if removed > 0 {
				r.logger.Debug("pruned stale attribute keys", "removed", removed)
			}
		}
	}

	r.logger.Debug("catalog reconciled",
		"services", len(services),
		"integrations", len(byInteg))
	return nil
}

// projectMatchers returns the subset of services that any of the
// integration's SERVICE matchers selects, preserving service-name order.
// Attribute matchers (producer, consumer, …) don't affect membership —
// they're row-level predicates applied when querying the integration's
// telemetry — so they're skipped here.
func projectMatchers(matchers []integrations.Matcher, services []string) []string {
	var serviceMatchers []integrations.Matcher
	for _, m := range matchers {
		if m.IsServiceMatcher() {
			serviceMatchers = append(serviceMatchers, m)
		}
	}
	if len(serviceMatchers) == 0 {
		return nil
	}
	out := make([]string, 0)
	for _, name := range services {
		for _, m := range serviceMatchers {
			if m.Match(name) {
				out = append(out, name)
				break
			}
		}
	}
	return out
}
