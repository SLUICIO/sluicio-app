// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package catalog persists the services-known-to-this-cell list and the
// materialised integration→service membership in Postgres. ClickHouse is
// still the OTel-ingest target of truth; this package keeps Postgres in
// sync so the membership relationship is queryable / stable across
// empty windows / etc.
//
// The reconciler in this same package is what actually does the sync —
// see Reconciler.RunOnce / Run.

package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service is one row in the services catalog.
type Service struct {
	OrganizationID   uuid.UUID `json:"organization_id"`
	ServiceName      string    `json:"service_name"`
	ServiceNamespace string    `json:"service_namespace"`
	FirstSeenAt      time.Time `json:"first_seen_at"`
	LastSeenAt       time.Time `json:"last_seen_at"`
	CreatedAt        time.Time `json:"created_at"`
	// IsSystem flags this service as a monitored "system" (RabbitMQ, SQL
	// Server, …); SystemKind names which one (drives icon + template). Both
	// user-set, untouched by the discovery upsert.
	IsSystem   bool   `json:"is_system"`
	SystemKind string `json:"system_kind"`
	// SystemID is the owning system entity (phase 2). Nil when the service is
	// not a member of any system. is_system/system_kind are kept in sync with
	// membership so existing health/RBAC/template paths are unaffected.
	SystemID *uuid.UUID `json:"system_id,omitempty"`
	// BadgePublic opts this service into a public status badge at
	// /api/v1/badges/service/<name>. Only populated by GetService.
	BadgePublic bool `json:"badge_public"`
}

// Discovery is what the reconciler passes to UpsertServices: one entry
// per service name observed in telemetry (typically over the last N
// days).
type Discovery struct {
	ServiceName      string
	ServiceNamespace string
	FirstSeen        time.Time
	LastSeen         time.Time
}

// Store is the Postgres-backed read / write layer for the catalog.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ── services ────────────────────────────────────────────────────────────

// UpsertServices writes a batch of discoveries to the services table.
// Existing rows have last_seen_at refreshed (and first_seen_at extended
// backwards if the discovery's first_seen is earlier — useful when the
// reconciler's window grows). New rows are inserted with the discovery's
// timestamps. Runs in a single transaction.
func (s *Store) UpsertServices(ctx context.Context, orgID uuid.UUID, items []Discovery) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	const q = `
		INSERT INTO services (organization_id, service_name, service_namespace, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_id, service_name) DO UPDATE
		SET service_namespace = COALESCE(NULLIF(EXCLUDED.service_namespace, ''), services.service_namespace),
		    first_seen_at = LEAST(services.first_seen_at, EXCLUDED.first_seen_at),
		    last_seen_at  = GREATEST(services.last_seen_at, EXCLUDED.last_seen_at)`
	for _, it := range items {
		if _, err := tx.Exec(ctx, q,
			orgID, it.ServiceName, it.ServiceNamespace, it.FirstSeen, it.LastSeen,
		); err != nil {
			return fmt.Errorf("upsert %q: %w", it.ServiceName, err)
		}
	}
	return tx.Commit(ctx)
}

// AllServices returns every service in the catalog for the org, sorted
// by service name. Used by the reconciler to evaluate matchers, and by
// any read path that wants the canonical list independent of the
// telemetry window.
func (s *Store) AllServices(ctx context.Context, orgID uuid.UUID) ([]Service, error) {
	const q = `
		SELECT organization_id, service_name, service_namespace,
		       first_seen_at, last_seen_at, created_at, is_system, system_kind, system_id
		FROM services WHERE organization_id = $1
		ORDER BY service_name`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("all services: %w", err)
	}
	defer rows.Close()
	out := make([]Service, 0)
	for rows.Next() {
		var svc Service
		if err := rows.Scan(
			&svc.OrganizationID, &svc.ServiceName, &svc.ServiceNamespace,
			&svc.FirstSeenAt, &svc.LastSeenAt, &svc.CreatedAt,
			&svc.IsSystem, &svc.SystemKind, &svc.SystemID,
		); err != nil {
			return nil, err
		}
		out = append(out, svc)
	}
	return out, rows.Err()
}

// GetService returns one catalog service (incl. its is_system / system_kind),
// or ErrNotFound-style empty + ok=false if the org has no such service.
func (s *Store) GetService(ctx context.Context, orgID uuid.UUID, serviceName string) (Service, bool, error) {
	const q = `
		SELECT organization_id, service_name, service_namespace,
		       first_seen_at, last_seen_at, created_at, is_system, system_kind, system_id, badge_public
		FROM services WHERE organization_id = $1 AND service_name = $2`
	var svc Service
	err := s.pool.QueryRow(ctx, q, orgID, serviceName).Scan(
		&svc.OrganizationID, &svc.ServiceName, &svc.ServiceNamespace,
		&svc.FirstSeenAt, &svc.LastSeenAt, &svc.CreatedAt,
		&svc.IsSystem, &svc.SystemKind, &svc.SystemID, &svc.BadgePublic,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, false, nil
	}
	if err != nil {
		return Service{}, false, fmt.Errorf("get service: %w", err)
	}
	return svc, true, nil
}

// SetServiceBadgePublic flips whether this service exposes a public status
// badge. Org-scoped; ok=false if the org has no such service (nothing updated).
func (s *Store) SetServiceBadgePublic(ctx context.Context, orgID uuid.UUID, serviceName string, public bool) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE services SET badge_public = $3 WHERE organization_id = $1 AND service_name = $2`,
		orgID, serviceName, public)
	if err != nil {
		return false, fmt.Errorf("set service badge_public: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetServiceSystem marks (or unmarks) a service as a monitored system and sets
// its kind. Marking attaches the service to a system entity for its kind
// (find-or-create), so the flag flow keeps the entity layer populated;
// unmarking detaches it. is_system/system_kind stay in sync with membership so
// existing health/RBAC/template paths are unaffected. The service must already
// exist in the catalog (discovered from telemetry); a no-op if it doesn't.
func (s *Store) SetServiceSystem(ctx context.Context, orgID uuid.UUID, serviceName string, isSystem bool, kind string) error {
	if !isSystem {
		_, err := s.pool.Exec(ctx,
			`UPDATE services SET is_system = false, system_kind = '', system_id = NULL
			 WHERE organization_id = $1 AND service_name = $2`,
			orgID, serviceName)
		if err != nil {
			return fmt.Errorf("clear service system: %w", err)
		}
		return nil
	}
	sysID, err := s.findOrCreateSystemByType(ctx, orgID, kind)
	if err != nil {
		return fmt.Errorf("set service system: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE services SET is_system = true, system_kind = $3, system_id = $4
		 WHERE organization_id = $1 AND service_name = $2`,
		orgID, serviceName, kind, sysID)
	if err != nil {
		return fmt.Errorf("set service system: %w", err)
	}
	return nil
}

// ── integration membership ──────────────────────────────────────────────

// IntegrationServices returns the persisted service-name list for one
// integration, in service-name order.
func (s *Store) IntegrationServices(ctx context.Context, integrationID uuid.UUID) ([]string, error) {
	const q = `SELECT service_name FROM integration_services WHERE integration_id = $1 ORDER BY service_name`
	rows, err := s.pool.Query(ctx, q, integrationID)
	if err != nil {
		return nil, fmt.Errorf("integration services: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// IntegrationServicesBulk mirrors IntegrationServices but for every
// integration belonging to the org. Used by listIntegrations to avoid
// a per-row round-trip.
func (s *Store) IntegrationServicesBulk(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID][]string, error) {
	const q = `
		SELECT i.id, i.organization_id, isv.service_name
		FROM integration_services isv
		JOIN integrations i ON i.id = isv.integration_id
		WHERE i.organization_id = $1
		ORDER BY isv.service_name`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("bulk integration services: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID][]string)
	for rows.Next() {
		var iid, oid uuid.UUID
		var name string
		if err := rows.Scan(&iid, &oid, &name); err != nil {
			return nil, err
		}
		_ = oid // unused but kept for the join shape
		out[iid] = append(out[iid], name)
	}
	return out, rows.Err()
}

// ReplaceIntegrationServices rewrites the membership for one integration
// in one transaction: delete every existing row, insert the new set.
// Empty next is allowed (clears all rows).
func (s *Store) ReplaceIntegrationServices(ctx context.Context, integrationID, orgID uuid.UUID, next []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM integration_services WHERE integration_id = $1`, integrationID); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if len(next) > 0 {
		// Multi-row insert in chunks to keep parameter count sane.
		const chunkRows = 100
		for start := 0; start < len(next); start += chunkRows {
			end := start + chunkRows
			if end > len(next) {
				end = len(next)
			}
			values := make([]string, 0, end-start)
			args := []any{integrationID, orgID}
			for i := start; i < end; i++ {
				args = append(args, next[i])
				values = append(values, fmt.Sprintf("($1, $2, $%d)", len(args)))
			}
			sql := "INSERT INTO integration_services (integration_id, organization_id, service_name) VALUES " +
				strings.Join(values, ", ") +
				" ON CONFLICT DO NOTHING"
			if _, err := tx.Exec(ctx, sql, args...); err != nil {
				return fmt.Errorf("insert: %w", err)
			}
		}
	}
	return tx.Commit(ctx)
}

// IntegrationsForService returns the integration ids that currently
// list this service as a member. Useful for reverse lookups (e.g.
// "which integrations does service X belong to").
func (s *Store) IntegrationsForService(ctx context.Context, orgID uuid.UUID, service string) ([]uuid.UUID, error) {
	const q = `SELECT integration_id FROM integration_services WHERE organization_id = $1 AND service_name = $2`
	rows, err := s.pool.Query(ctx, q, orgID, service)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
