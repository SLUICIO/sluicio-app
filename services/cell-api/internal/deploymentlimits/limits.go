// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package deploymentlimits holds the limits a deployment sets on an
// instance, and the counts they are measured against.
//
// The monitored-entity limit caps integrations and systems combined. It
// is enforced in Postgres (migration 0100), on every path that creates
// either; this package writes the deployment's value where the trigger
// reads it, and recognizes the trigger's refusal.
package deploymentlimits

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MonitoredEntityLimitEnv names the deployment's cap on integrations and
// systems combined.
const MonitoredEntityLimitEnv = "SLUICIO_MONITORED_ENTITY_LIMIT"

// MonitoredEntityLimitFromEnv reads the cap. Unset or empty is no cap
// (0). A value that is not a non-negative whole number is an error: a
// cap that was set and mistyped must not quietly become no cap.
func MonitoredEntityLimitFromEnv() (int, error) {
	raw := strings.TrimSpace(os.Getenv(MonitoredEntityLimitEnv))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s=%q: want a whole number of integrations and systems, e.g. 10", MonitoredEntityLimitEnv, raw)
	}
	return n, nil
}

// Store reads and writes the limits table.
type Store struct{ pool *pgxpool.Pool }

// New returns a Store on pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// SetMonitoredEntityLimit records the cap the trigger enforces. Zero
// clears it. Called at every start, so removing the setting from the
// deployment removes the cap.
//
// Lowering the cap below the current count deletes nothing: what exists
// stays, and creating another is refused until the count is under it.
func (s *Store) SetMonitoredEntityLimit(ctx context.Context, limit int) error {
	var v any
	if limit > 0 {
		v = limit
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO deployment_limits (id, monitored_entity_limit, updated_at) VALUES (TRUE, $1, now())
		 ON CONFLICT (id) DO UPDATE SET monitored_entity_limit = EXCLUDED.monitored_entity_limit, updated_at = now()`, v)
	if err != nil {
		return fmt.Errorf("set monitored entity limit: %w", err)
	}
	return nil
}

// OrgCount is one organization's integrations and systems.
type OrgCount struct {
	OrgID        uuid.UUID `json:"org_id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Integrations int       `json:"integrations"`
	Systems      int       `json:"systems"`
}

// Counts is the instance's integrations and systems, in total and per
// organization.
type Counts struct {
	Integrations int        `json:"integrations"`
	Systems      int        `json:"systems"`
	ByOrg        []OrgCount `json:"by_org"`
}

// Count reads the counts. The limit is on the total, across every
// organization in the instance.
func (s *Store) Count(ctx context.Context) (Counts, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.slug, o.name,
		       (SELECT count(*) FROM integrations i WHERE i.organization_id = o.id),
		       (SELECT count(*) FROM systems sy WHERE sy.org_id = o.id)
		FROM orgs o
		ORDER BY o.name`)
	if err != nil {
		return Counts{}, fmt.Errorf("count monitored entities: %w", err)
	}
	defer rows.Close()
	out := Counts{ByOrg: []OrgCount{}}
	for rows.Next() {
		var c OrgCount
		if err := rows.Scan(&c.OrgID, &c.Slug, &c.Name, &c.Integrations, &c.Systems); err != nil {
			return Counts{}, fmt.Errorf("count monitored entities: %w", err)
		}
		out.Integrations += c.Integrations
		out.Systems += c.Systems
		out.ByOrg = append(out.ByOrg, c)
	}
	return out, rows.Err()
}

// IsEntityLimit reports whether err is the trigger refusing a create
// because the instance is at its monitored-entity limit.
func IsEntityLimit(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23514" && pg.ConstraintName == "monitored_entity_limit"
}
