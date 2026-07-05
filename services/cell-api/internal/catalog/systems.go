// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Systems as first-class entities (docs/systems.md phase 2). A system is an
// instance of a system type (type_key) that spans member services
// (services.system_id). Attaching/detaching keeps the member's is_system /
// system_kind in sync with membership, so existing health, badges, templates,
// and RBAC (which read those fields) are unaffected.

package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrSystemNotFound is returned when a system lookup misses.
var ErrSystemNotFound = errors.New("system not found")

// System is one system entity with its member service names.
type System struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	TypeKey     string    `json:"type_key"`
	Description string    `json:"description"`
	// BadgePublic opts this system into a public status badge at
	// /api/v1/badges/system/<id>.
	BadgePublic bool     `json:"badge_public"`
	Members     []string `json:"members"`
}

const systemSelect = `
	SELECT sys.id, sys.name, sys.type_key, sys.description, sys.badge_public,
	       COALESCE(array_agg(s.service_name ORDER BY s.service_name) FILTER (WHERE s.service_name IS NOT NULL), '{}') AS members
	FROM systems sys
	LEFT JOIN services s ON s.system_id = sys.id AND s.organization_id = sys.org_id`

func scanSystem(row pgx.Row) (System, error) {
	var sy System
	if err := row.Scan(&sy.ID, &sy.Name, &sy.TypeKey, &sy.Description, &sy.BadgePublic, &sy.Members); err != nil {
		return System{}, err
	}
	if sy.Members == nil {
		sy.Members = []string{}
	}
	return sy, nil
}

// ListSystems returns every system in the org with its member service names.
func (s *Store) ListSystems(ctx context.Context, orgID uuid.UUID) ([]System, error) {
	rows, err := s.pool.Query(ctx, systemSelect+`
		WHERE sys.org_id = $1
		GROUP BY sys.id
		ORDER BY sys.name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list systems: %w", err)
	}
	defer rows.Close()
	out := make([]System, 0)
	for rows.Next() {
		sy, err := scanSystem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sy)
	}
	return out, rows.Err()
}

// GetSystem returns one system (with members) by id within the org.
func (s *Store) GetSystem(ctx context.Context, orgID, id uuid.UUID) (System, bool, error) {
	row := s.pool.QueryRow(ctx, systemSelect+`
		WHERE sys.org_id = $1 AND sys.id = $2
		GROUP BY sys.id`, orgID, id)
	sy, err := scanSystem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return System{}, false, nil
	}
	if err != nil {
		return System{}, false, fmt.Errorf("get system: %w", err)
	}
	return sy, true, nil
}

// SystemMemberNames returns the service names attached to one system.
// Used by the policy resolver to expand a system-instance grant.
func (s *Store) SystemMemberNames(ctx context.Context, orgID, systemID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT service_name FROM services
		 WHERE organization_id = $1 AND system_id = $2`, orgID, systemID)
	if err != nil {
		return nil, fmt.Errorf("system member names: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// SetSystemBadgePublic flips whether this system exposes a public status badge.
// Org-scoped; ErrSystemNotFound if it isn't in the org.
func (s *Store) SetSystemBadgePublic(ctx context.Context, orgID, id uuid.UUID, public bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE systems SET badge_public = $3, updated_at = now()
		 WHERE org_id = $1 AND id = $2`, orgID, id, public)
	if err != nil {
		return fmt.Errorf("set system badge_public: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSystemNotFound
	}
	return nil
}

// findOrCreateSystemByType returns the (oldest) system of the given type for
// the org, creating one named after the type key if none exists. Used by the
// service "mark as system" flag flow so it populates the entity layer.
func (s *Store) findOrCreateSystemByType(ctx context.Context, orgID uuid.UUID, typeKey string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM systems WHERE org_id = $1 AND type_key = $2 ORDER BY created_at LIMIT 1`,
		orgID, typeKey).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	name := typeKey
	if name == "" {
		name = "system"
	}
	// If a system with that name already exists (e.g. a differently-typed one),
	// reuse it rather than failing the flag flow.
	err = s.pool.QueryRow(ctx,
		`INSERT INTO systems (org_id, name, type_key) VALUES ($1, $2, $3)
		 ON CONFLICT (org_id, name) DO UPDATE SET name = systems.name
		 RETURNING id`, orgID, name, typeKey).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("find-or-create system: %w", err)
	}
	return id, nil
}

// CreateSystem creates a new (empty) system entity.
func (s *Store) CreateSystem(ctx context.Context, orgID uuid.UUID, name, typeKey, description string) (System, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`INSERT INTO systems (org_id, name, type_key, description) VALUES ($1, $2, $3, $4) RETURNING id`,
		orgID, name, typeKey, description).Scan(&id)
	if err != nil {
		return System{}, fmt.Errorf("create system: %w", err)
	}
	sy, _, err := s.GetSystem(ctx, orgID, id)
	return sy, err
}

// UpdateSystem renames / retypes a system and re-syncs its members' system_kind.
func (s *Store) UpdateSystem(ctx context.Context, orgID, id uuid.UUID, name, typeKey, description string) (System, bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE systems SET name = $3, type_key = $4, description = $5, updated_at = now()
		 WHERE org_id = $1 AND id = $2`, orgID, id, name, typeKey, description)
	if err != nil {
		return System{}, false, fmt.Errorf("update system: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return System{}, false, nil
	}
	// Keep member services' kind in sync with the (possibly changed) type.
	if _, err := s.pool.Exec(ctx,
		`UPDATE services SET system_kind = $3 WHERE organization_id = $1 AND system_id = $2`,
		orgID, id, typeKey); err != nil {
		return System{}, false, fmt.Errorf("update system: resync members: %w", err)
	}
	sy, ok, err := s.GetSystem(ctx, orgID, id)
	return sy, ok, err
}

// DeleteSystem removes a system; its members are detached (is_system cleared).
func (s *Store) DeleteSystem(ctx context.Context, orgID, id uuid.UUID) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE services SET is_system = false, system_kind = '', system_id = NULL
		 WHERE organization_id = $1 AND system_id = $2`, orgID, id); err != nil {
		return fmt.Errorf("delete system: detach members: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM systems WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("delete system: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSystemNotFound
	}
	return nil
}

// AttachService adds a service to a system, syncing is_system/system_kind.
func (s *Store) AttachService(ctx context.Context, orgID, systemID uuid.UUID, serviceName string) (bool, error) {
	sy, ok, err := s.GetSystem(ctx, orgID, systemID)
	if err != nil || !ok {
		return false, err
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE services SET is_system = true, system_kind = $3, system_id = $4
		 WHERE organization_id = $1 AND service_name = $2`,
		orgID, serviceName, sy.TypeKey, systemID)
	if err != nil {
		return false, fmt.Errorf("attach service: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DetachService removes a service from its system, clearing the system flag.
func (s *Store) DetachService(ctx context.Context, orgID, systemID uuid.UUID, serviceName string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE services SET is_system = false, system_kind = '', system_id = NULL
		 WHERE organization_id = $1 AND service_name = $2 AND system_id = $3`,
		orgID, serviceName, systemID)
	if err != nil {
		return false, fmt.Errorf("detach service: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
