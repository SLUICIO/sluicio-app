// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Resource ⇄ group attachment (RBAC v2 phase 1, docs/rbac-v2-design.md §4).
// The CE-facing "which groups can view this integration / system" surface:
// a restricted façade over group_access_policies that only ever touches
// the single-resource grant kinds —
//
//   integrations: kind='integration', target_integration_id = X
//   systems:      kind='system',      target_system_id      = X
//
// Replace-set semantics per resource. Policies of any other kind (or
// system policies narrowed by kind instead of id) are invisible to and
// untouchable by this surface, so an EE org's richer policies can't be
// clobbered from the simple UI.

package identity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ResourceGroup is one group attached to a resource via the façade.
type ResourceGroup struct {
	GroupID uuid.UUID `json:"group_id"`
	Slug    string    `json:"slug"`
	Name    string    `json:"name"`
}

// ListGroupsForIntegration returns the groups holding a single-integration
// grant on the given integration, name-ordered.
func (s *Store) ListGroupsForIntegration(ctx context.Context, orgID, integrationID uuid.UUID) ([]ResourceGroup, error) {
	return s.listResourceGroups(ctx, orgID, `p.kind = 'integration' AND p.target_integration_id = $2`, integrationID)
}

// ListGroupsForSystem returns the groups holding a single-system grant on
// the given system, name-ordered.
func (s *Store) ListGroupsForSystem(ctx context.Context, orgID, systemID uuid.UUID) ([]ResourceGroup, error) {
	return s.listResourceGroups(ctx, orgID, `p.kind = 'system' AND p.target_system_id = $2`, systemID)
}

func (s *Store) listResourceGroups(ctx context.Context, orgID uuid.UUID, cond string, target uuid.UUID) ([]ResourceGroup, error) {
	q := `
		SELECT g.id, g.slug, g.name
		FROM group_access_policies p
		JOIN groups g ON g.id = p.group_id
		WHERE g.org_id = $1 AND ` + cond + `
		ORDER BY lower(g.name)`
	rows, err := s.pool.Query(ctx, q, orgID, target)
	if err != nil {
		return nil, fmt.Errorf("identity: list resource groups: %w", err)
	}
	defer rows.Close()
	out := make([]ResourceGroup, 0)
	for rows.Next() {
		var rg ResourceGroup
		if err := rows.Scan(&rg.GroupID, &rg.Slug, &rg.Name); err != nil {
			return nil, err
		}
		out = append(out, rg)
	}
	return out, rows.Err()
}

// SetIntegrationGroups replaces the set of groups granted this integration.
// grantServices decides whether the attachment also makes the member
// services viewable in their own right.
//
// TRUE is the default and the documented CE contract: attaching a group
// to an integration makes the resource AND its member services
// viewable, and Community has no other route to grant service
// visibility because the richer policy kinds are EE. Silently
// redefining it to integration-only would take a capability away from
// exactly the users with no alternative.
//
// FALSE is the least-privilege option added by issue #28, for the case
// that motivated it: several flows on one runtime, where granting the
// service hands over every sibling integration it carries. Chosen per
// attachment, because whether the services should come along depends on
// whether they are shared, and only the person attaching knows that.
func (s *Store) SetIntegrationGroups(ctx context.Context, orgID, integrationID uuid.UUID, groupIDs []uuid.UUID, grantServices bool) error {
	insert := `INSERT INTO group_access_policies (group_id, kind, target_integration_id, grant_services)
		 VALUES ($1, 'integration', $2, TRUE)`
	if !grantServices {
		insert = `INSERT INTO group_access_policies (group_id, kind, target_integration_id, grant_services)
		 VALUES ($1, 'integration', $2, FALSE)`
	}
	return s.setResourceGroups(ctx, orgID, groupIDs,
		`DELETE FROM group_access_policies p
		 USING groups g
		 WHERE g.id = p.group_id AND g.org_id = $1
		   AND p.kind = 'integration' AND p.target_integration_id = $2`,
		insert,
		integrationID)
}

// SetSystemGroups replaces the set of groups granted this system.
func (s *Store) SetSystemGroups(ctx context.Context, orgID, systemID uuid.UUID, groupIDs []uuid.UUID) error {
	return s.setResourceGroups(ctx, orgID, groupIDs,
		`DELETE FROM group_access_policies p
		 USING groups g
		 WHERE g.id = p.group_id AND g.org_id = $1
		   AND p.kind = 'system' AND p.target_system_id = $2`,
		`INSERT INTO group_access_policies (group_id, kind, target_system_id)
		 VALUES ($1, 'system', $2)`,
		systemID)
}

// setResourceGroups is the shared replace-set transaction: every group
// must belong to the org (a cross-org id fails the whole call), the
// resource's single-resource grants are cleared, then re-created for the
// requested groups.
func (s *Store) setResourceGroups(ctx context.Context, orgID uuid.UUID, groupIDs []uuid.UUID, deleteQ, insertQ string, target uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("identity: set resource groups: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Validate group ownership up front — fail closed on any foreign id.
	for _, gid := range groupIDs {
		var ok bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM groups WHERE id = $1 AND org_id = $2)`,
			gid, orgID).Scan(&ok); err != nil {
			return fmt.Errorf("identity: set resource groups: %w", err)
		}
		if !ok {
			return ErrNotFound
		}
	}
	if _, err := tx.Exec(ctx, deleteQ, orgID, target); err != nil {
		return fmt.Errorf("identity: set resource groups: clear: %w", err)
	}
	for _, gid := range groupIDs {
		if _, err := tx.Exec(ctx, insertQ, gid, target); err != nil {
			return fmt.Errorf("identity: set resource groups: insert: %w", err)
		}
	}
	return tx.Commit(ctx)
}
