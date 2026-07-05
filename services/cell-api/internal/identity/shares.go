// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Resource shares (RBAC v2 phase 3, docs/rbac-v2-design.md §6): grant one
// user or group VIEW of a single integration or system. Structurally
// viewer-only — no role column exists, so a share can never carry manage.
// Shares feed the Visible resolution tier only (see policies.go).

package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ShareResourceKind discriminates what a share points at.
type ShareResourceKind string

const (
	ShareIntegration ShareResourceKind = "integration"
	ShareSystem      ShareResourceKind = "system"
)

// ErrShareExists is returned when the identical grant already exists.
var ErrShareExists = errors.New("identity: share already exists")

// Share is one resource_shares row, hydrated with display names for the
// share-management UI.
type Share struct {
	ID           uuid.UUID         `json:"id"`
	ResourceKind ShareResourceKind `json:"resource_kind"`
	ResourceID   uuid.UUID         `json:"resource_id"`
	GranteeKind  string            `json:"grantee_kind"` // user | group
	GranteeID    uuid.UUID         `json:"grantee_id"`
	// GranteeName is the user's name/email or the group's name.
	GranteeName string    `json:"grantee_name"`
	CreatedBy   string    `json:"created_by,omitempty"` // sharer display name
	CreatedAt   time.Time `json:"created_at"`
}

// SharedResource is the digest/resolution view: which resource, granted when.
type SharedResource struct {
	ResourceKind ShareResourceKind `json:"resource_kind"`
	ResourceID   uuid.UUID         `json:"resource_id"`
	SharedBy     string            `json:"shared_by,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

// CreateShare inserts one grant. The grantee must already be validated as
// org-local by the caller (user: org member; group: org group). Returns
// ErrShareExists on the identical grant.
func (s *Store) CreateShare(ctx context.Context, orgID uuid.UUID, kind ShareResourceKind, resourceID uuid.UUID, granteeKind string, granteeID uuid.UUID, createdBy *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO resource_shares (org_id, resource_kind, resource_id, grantee_kind, grantee_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (org_id, resource_kind, resource_id, grantee_kind, grantee_id) DO NOTHING
		RETURNING id`, orgID, kind, resourceID, granteeKind, granteeID, createdBy).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrShareExists
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity: create share: %w", err)
	}
	return id, nil
}

// DeleteShare removes one grant (org-scoped + resource-scoped so a caller
// who may manage THIS resource can't delete another resource's share by id).
func (s *Store) DeleteShare(ctx context.Context, orgID uuid.UUID, kind ShareResourceKind, resourceID, shareID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM resource_shares
		WHERE id = $1 AND org_id = $2 AND resource_kind = $3 AND resource_id = $4`,
		shareID, orgID, kind, resourceID)
	if err != nil {
		return fmt.Errorf("identity: delete share: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSharesForResource clears every grant on a resource — called by the
// integration/system delete paths (resource_id has no FK).
func (s *Store) DeleteSharesForResource(ctx context.Context, orgID uuid.UUID, kind ShareResourceKind, resourceID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM resource_shares WHERE org_id = $1 AND resource_kind = $2 AND resource_id = $3`,
		orgID, kind, resourceID)
	if err != nil {
		return fmt.Errorf("identity: delete shares for resource: %w", err)
	}
	return nil
}

// ListSharesForResource returns a resource's grants with display names.
func (s *Store) ListSharesForResource(ctx context.Context, orgID uuid.UUID, kind ShareResourceKind, resourceID uuid.UUID) ([]Share, error) {
	const q = `
		SELECT sh.id, sh.resource_kind, sh.resource_id, sh.grantee_kind, sh.grantee_id, sh.created_at,
		       COALESCE(u.name, u.email, g.name, ''),
		       COALESCE(cb.name, cb.email, '')
		FROM resource_shares sh
		LEFT JOIN users  u  ON sh.grantee_kind = 'user'  AND u.id = sh.grantee_id
		LEFT JOIN groups g  ON sh.grantee_kind = 'group' AND g.id = sh.grantee_id
		LEFT JOIN users  cb ON cb.id = sh.created_by
		WHERE sh.org_id = $1 AND sh.resource_kind = $2 AND sh.resource_id = $3
		ORDER BY sh.created_at ASC`
	rows, err := s.pool.Query(ctx, q, orgID, kind, resourceID)
	if err != nil {
		return nil, fmt.Errorf("identity: list shares: %w", err)
	}
	defer rows.Close()
	out := make([]Share, 0)
	for rows.Next() {
		var sh Share
		if err := rows.Scan(&sh.ID, &sh.ResourceKind, &sh.ResourceID, &sh.GranteeKind, &sh.GranteeID,
			&sh.CreatedAt, &sh.GranteeName, &sh.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// sharesGranteeCond matches shares reaching a user directly or through
// any of their groups.
const sharesGranteeCond = `
	sh.org_id = $2 AND (
		(sh.grantee_kind = 'user' AND sh.grantee_id = $1)
		OR (sh.grantee_kind = 'group' AND sh.grantee_id IN (
			SELECT gm.group_id FROM group_members gm
			JOIN groups g ON g.id = gm.group_id
			WHERE gm.user_id = $1 AND g.org_id = $2))
	)`

// SharedResourcesForUser returns every resource shared with the user
// (directly or via their groups) in the org — the resolution input.
func (s *Store) SharedResourcesForUser(ctx context.Context, userID, orgID uuid.UUID) ([]SharedResource, error) {
	q := `
		SELECT DISTINCT sh.resource_kind, sh.resource_id
		FROM resource_shares sh
		WHERE ` + sharesGranteeCond
	rows, err := s.pool.Query(ctx, q, userID, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: shared resources: %w", err)
	}
	defer rows.Close()
	out := make([]SharedResource, 0)
	for rows.Next() {
		var sr SharedResource
		if err := rows.Scan(&sr.ResourceKind, &sr.ResourceID); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// SharedResourcesSince returns resources shared with the user after the
// watermark — the digest's "shared with you" section.
func (s *Store) SharedResourcesSince(ctx context.Context, userID, orgID uuid.UUID, since time.Time) ([]SharedResource, error) {
	q := `
		SELECT sh.resource_kind, sh.resource_id, COALESCE(cb.name, cb.email, ''), sh.created_at
		FROM resource_shares sh
		LEFT JOIN users cb ON cb.id = sh.created_by
		WHERE sh.created_at > $3 AND ` + sharesGranteeCond + `
		ORDER BY sh.created_at DESC
		LIMIT 20`
	rows, err := s.pool.Query(ctx, q, userID, orgID, since)
	if err != nil {
		return nil, fmt.Errorf("identity: shared since: %w", err)
	}
	defer rows.Close()
	out := make([]SharedResource, 0)
	for rows.Next() {
		var sr SharedResource
		if err := rows.Scan(&sr.ResourceKind, &sr.ResourceID, &sr.SharedBy, &sr.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}
