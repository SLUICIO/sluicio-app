// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Groups — the second access-control axis under org. See the comment
// block at the top of internal/migrations/sql/0019_groups.up.sql for
// the model overview.
//
// Group membership and resource-to-group assignment live here so the
// rest of the cell-api has one place to ask "who's in this group?"
// and "what services can this user see?". Visibility filtering itself
// is enforced at the handler layer; this file just exposes the reads
// + writes the handlers stitch together.

package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Group is one row in the groups table.
type Group struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	// Counts populated by ListGroups for the admin table —
	// inexpensive subqueries beat per-row round-trips.
	MemberCount  int `json:"member_count"`
	ServiceCount int `json:"service_count"`
}

// GroupMember is one row in the group_members table joined with the
// member's identity row, ready for rendering on the per-group screen.
// Exactly one of User / ServiceAccount is set — memberships are
// polymorphic since service-account scoping
// (docs/service-account-scoping-design.md).
type GroupMember struct {
	User           *User           `json:"user,omitempty"`
	ServiceAccount *ServiceAccount `json:"service_account,omitempty"`
	Role           Role            `json:"role"`
	JoinedAt       time.Time       `json:"joined_at"`
}

// UserGroupRole is what the visibility filter cares about: which
// groups a user belongs to + what role they hold in each. Cached
// per request in the middleware so a single request doesn't
// re-query.
type UserGroupRole struct {
	GroupID uuid.UUID `json:"group_id"`
	Role    Role      `json:"role"`
}

// GroupInput is the write payload for create / update.
type GroupInput struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

// CreateGroup inserts a new group in the org. Slug is required and
// must be unique within the org.
func (s *Store) CreateGroup(ctx context.Context, orgID uuid.UUID, in GroupInput) (Group, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.TrimSpace(in.Slug)
	if in.Name == "" || in.Slug == "" {
		return Group{}, fmt.Errorf("identity: group name + slug required")
	}
	const q = `
		INSERT INTO groups (org_id, slug, name, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, org_id, slug, name, description, created_at, updated_at`
	row := s.pool.QueryRow(ctx, q, orgID, in.Slug, in.Name, in.Description)
	var g Group
	if err := row.Scan(&g.ID, &g.OrgID, &g.Slug, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt); err != nil {
		if strings.Contains(err.Error(), "groups_org_id_slug_key") {
			return Group{}, ErrGroupSlugExists
		}
		return Group{}, fmt.Errorf("identity: create group: %w", err)
	}
	return g, nil
}

// GetGroup returns the group + counts. Returns ErrNotFound if the id
// is unknown or belongs to a different org.
func (s *Store) GetGroup(ctx context.Context, orgID, id uuid.UUID) (Group, error) {
	const q = `
		SELECT g.id, g.org_id, g.slug, g.name, g.description, g.created_at, g.updated_at,
		       COALESCE((SELECT COUNT(*) FROM group_members  m WHERE m.group_id = g.id), 0) AS member_count,
		       COALESCE((SELECT COUNT(*) FROM group_access_policies s WHERE s.group_id = g.id), 0) AS service_count
		FROM groups g
		WHERE g.org_id = $1 AND g.id = $2`
	row := s.pool.QueryRow(ctx, q, orgID, id)
	var g Group
	if err := row.Scan(&g.ID, &g.OrgID, &g.Slug, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt,
		&g.MemberCount, &g.ServiceCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Group{}, ErrNotFound
		}
		return Group{}, err
	}
	return g, nil
}

// ListGroups returns every group in the org, alphabetical by name,
// each decorated with member + service counts.
func (s *Store) ListGroups(ctx context.Context, orgID uuid.UUID) ([]Group, error) {
	const q = `
		SELECT g.id, g.org_id, g.slug, g.name, g.description, g.created_at, g.updated_at,
		       COALESCE((SELECT COUNT(*) FROM group_members  m WHERE m.group_id = g.id), 0) AS member_count,
		       COALESCE((SELECT COUNT(*) FROM group_access_policies s WHERE s.group_id = g.id), 0) AS service_count
		FROM groups g
		WHERE g.org_id = $1
		ORDER BY lower(g.name)`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: list groups: %w", err)
	}
	defer rows.Close()
	out := make([]Group, 0)
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.OrgID, &g.Slug, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt,
			&g.MemberCount, &g.ServiceCount); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpdateGroup mutates name + description (slug is immutable).
func (s *Store) UpdateGroup(ctx context.Context, orgID, id uuid.UUID, in GroupInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return fmt.Errorf("identity: group name required")
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE groups SET name = $3, description = $4, updated_at = now()
		   WHERE org_id = $1 AND id = $2`,
		orgID, id, in.Name, in.Description)
	if err != nil {
		return fmt.Errorf("identity: update group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteGroup removes a group + cascades all its memberships +
// service assignments. The handler is expected to confirm with the
// user first — there's no undo here.
func (s *Store) DeleteGroup(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM groups WHERE org_id = $1 AND id = $2`,
		orgID, id)
	if err != nil {
		return fmt.Errorf("identity: delete group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── group members ─────────────────────────────────────────────────────

// AddGroupMember inserts (user, group, role). Idempotent on collision
// — caller treats ErrAlreadyMember as success if they were aiming for
// "ensure this user is in this group."
func (s *Store) AddGroupMember(ctx context.Context, userID, groupID uuid.UUID, role Role) error {
	if !role.IsValid() {
		return fmt.Errorf("identity: invalid role: %s", role)
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO group_members (user_id, group_id, role) VALUES ($1, $2, $3)`,
		userID, groupID, role)
	if err != nil {
		if strings.Contains(err.Error(), "group_members_user_group_key") {
			return ErrAlreadyMember
		}
		return fmt.Errorf("identity: add group member: %w", err)
	}
	return nil
}

// ── service-account group membership ──────────────────────────────────
//
// Service accounts join groups exactly like users; the group's policies
// become the SA's visibility scope (design doc §"service accounts are
// group members"). Only SAs with scope='scoped' consult these rows, but
// membership may be prepared on an org-wide SA before narrowing it.

// AddGroupServiceAccount inserts (service_account, group, role).
func (s *Store) AddGroupServiceAccount(ctx context.Context, saID, groupID uuid.UUID, role Role) error {
	if !role.IsValid() {
		return fmt.Errorf("identity: invalid role: %s", role)
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO group_members (service_account_id, group_id, role) VALUES ($1, $2, $3)`,
		saID, groupID, role)
	if err != nil {
		if strings.Contains(err.Error(), "group_members_sa_group_key") {
			return ErrAlreadyMember
		}
		return fmt.Errorf("identity: add group service account: %w", err)
	}
	return nil
}

// UpdateGroupServiceAccountRole changes an SA membership's role.
func (s *Store) UpdateGroupServiceAccountRole(ctx context.Context, saID, groupID uuid.UUID, role Role) error {
	if !role.IsValid() {
		return fmt.Errorf("identity: invalid role: %s", role)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE group_members SET role = $3 WHERE service_account_id = $1 AND group_id = $2`,
		saID, groupID, role)
	if err != nil {
		return fmt.Errorf("identity: update group service-account role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveGroupServiceAccount deletes (service_account, group).
func (s *Store) RemoveGroupServiceAccount(ctx context.Context, saID, groupID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM group_members WHERE service_account_id = $1 AND group_id = $2`,
		saID, groupID)
	if err != nil {
		return fmt.Errorf("identity: remove group service account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ServiceAccountGroup is one group a service account belongs to, with
// the membership role — the SA-blade's membership editor shape.
type ServiceAccountGroup struct {
	Group    Group     `json:"group"`
	Role     Role      `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// ListGroupsForServiceAccount returns the SA's group memberships
// within an org, alphabetical by group name.
func (s *Store) ListGroupsForServiceAccount(ctx context.Context, saID, orgID uuid.UUID) ([]ServiceAccountGroup, error) {
	const q = `
		SELECT g.id, g.org_id, g.slug, g.name, g.description, g.created_at, g.updated_at,
		       gm.role, gm.joined_at
		FROM group_members gm
		JOIN groups g ON g.id = gm.group_id
		WHERE gm.service_account_id = $1 AND g.org_id = $2
		ORDER BY lower(g.name)`
	rows, err := s.pool.Query(ctx, q, saID, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: list service-account groups: %w", err)
	}
	defer rows.Close()
	out := make([]ServiceAccountGroup, 0)
	for rows.Next() {
		var sg ServiceAccountGroup
		if err := rows.Scan(&sg.Group.ID, &sg.Group.OrgID, &sg.Group.Slug, &sg.Group.Name, &sg.Group.Description,
			&sg.Group.CreatedAt, &sg.Group.UpdatedAt, &sg.Role, &sg.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

// UpdateGroupMemberRole changes the role of an existing membership.
func (s *Store) UpdateGroupMemberRole(ctx context.Context, userID, groupID uuid.UUID, role Role) error {
	if !role.IsValid() {
		return fmt.Errorf("identity: invalid role: %s", role)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE group_members SET role = $3 WHERE user_id = $1 AND group_id = $2`,
		userID, groupID, role)
	if err != nil {
		return fmt.Errorf("identity: update group role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveGroupMember deletes (user, group). Returns ErrNotFound if no
// such membership exists.
func (s *Store) RemoveGroupMember(ctx context.Context, userID, groupID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM group_members WHERE user_id = $1 AND group_id = $2`,
		userID, groupID)
	if err != nil {
		return fmt.Errorf("identity: remove group member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListGroupMembers returns every membership row for the group — user
// members joined with their user row, service-account members joined
// with the SA row. Users sort first (by email), then SAs (by name).
func (s *Store) ListGroupMembers(ctx context.Context, groupID uuid.UUID) ([]GroupMember, error) {
	const q = `
		SELECT u.id, u.email, u.name,
		       COALESCE(u.password_hash, ''), u.must_reset_password,
		       u.last_login_at, u.created_at, u.updated_at,
		       gm.role, gm.joined_at
		FROM group_members gm
		JOIN users u ON u.id = gm.user_id
		WHERE gm.group_id = $1
		ORDER BY lower(u.email)`
	rows, err := s.pool.Query(ctx, q, groupID)
	if err != nil {
		return nil, fmt.Errorf("identity: list group members: %w", err)
	}
	defer rows.Close()
	out := make([]GroupMember, 0)
	for rows.Next() {
		var u User
		var gm GroupMember
		if err := rows.Scan(
			&u.ID, &u.Email, &u.Name,
			&u.PasswordHash, &u.MustResetPassword,
			&u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
			&gm.Role, &gm.JoinedAt,
		); err != nil {
			return nil, err
		}
		gm.User = &u
		out = append(out, gm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	const qsa = `
		SELECT sa.id, sa.org_id, sa.name, sa.description, sa.role, sa.scope, sa.created_by, sa.created_at,
		       gm.role, gm.joined_at
		FROM group_members gm
		JOIN service_accounts sa ON sa.id = gm.service_account_id
		WHERE gm.group_id = $1
		ORDER BY lower(sa.name)`
	saRows, err := s.pool.Query(ctx, qsa, groupID)
	if err != nil {
		return nil, fmt.Errorf("identity: list group service-account members: %w", err)
	}
	defer saRows.Close()
	for saRows.Next() {
		var sa ServiceAccount
		var gm GroupMember
		if err := saRows.Scan(
			&sa.ID, &sa.OrgID, &sa.Name, &sa.Description, &sa.Role, &sa.Scope, &sa.CreatedBy, &sa.CreatedAt,
			&gm.Role, &gm.JoinedAt,
		); err != nil {
			return nil, err
		}
		gm.ServiceAccount = &sa
		out = append(out, gm)
	}
	return out, saRows.Err()
}

// CanUserWriteAnywhere reports whether the user has a non-viewer
// role somewhere in this org — either org-level editor/admin, or
// editor/admin in any group they belong to.
//
// This is the gate for "can this user create dashboards / alerts?"
// in the G6 model: a viewer org-wide who is editor in Group A
// should be able to create dashboards (which they'd scope to A's
// data on the handler side); a viewer everywhere should not.
func (s *Store) CanUserWriteAnywhere(ctx context.Context, userID, orgID uuid.UUID) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM org_members
			WHERE user_id = $1 AND org_id = $2 AND role IN ('admin','editor')
		) OR EXISTS (
			SELECT 1
			FROM group_members gm
			JOIN groups g ON g.id = gm.group_id
			WHERE gm.user_id = $1 AND g.org_id = $2 AND gm.role IN ('admin','editor')
		)`
	var ok bool
	if err := s.pool.QueryRow(ctx, q, userID, orgID).Scan(&ok); err != nil {
		return false, fmt.Errorf("identity: can write anywhere: %w", err)
	}
	return ok, nil
}

// ListUserGroups returns the (group_id, role) pairs for one user
// within an org. Used by the visibility filter (the auth middleware
// also caches this per request so concurrent handlers can share).
func (s *Store) ListUserGroups(ctx context.Context, userID, orgID uuid.UUID) ([]UserGroupRole, error) {
	return s.ListMemberGroups(ctx, UserRef(userID), orgID)
}

// ListMemberGroups is ListUserGroups generalised over the two
// membership kinds (user / service account).
func (s *Store) ListMemberGroups(ctx context.Context, ref MemberRef, orgID uuid.UUID) ([]UserGroupRole, error) {
	memberCol, memberID := "gm.user_id", uuid.Nil
	switch {
	case ref.UserID != nil:
		memberID = *ref.UserID
	case ref.ServiceAccountID != nil:
		memberCol, memberID = "gm.service_account_id", *ref.ServiceAccountID
	default:
		return nil, fmt.Errorf("identity: empty member ref")
	}
	q := `
		SELECT gm.group_id, gm.role
		FROM group_members gm
		JOIN groups g ON g.id = gm.group_id
		WHERE ` + memberCol + ` = $1 AND g.org_id = $2`
	rows, err := s.pool.Query(ctx, q, memberID, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: list user groups: %w", err)
	}
	defer rows.Close()
	out := make([]UserGroupRole, 0)
	for rows.Next() {
		var u UserGroupRole
		if err := rows.Scan(&u.GroupID, &u.Role); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ── service ↔ group (policy convenience wrappers) ────────────────────
//
// The per-service surface (GET / PUT /api/v1/services/{name}/groups)
// pre-dates the general policy model from migration 0020. We preserve
// the simple "which groups is this service in?" / "set the groups
// for this service" API by translating to/from kind=service policies
// — they're the explicit-allowlist case of the new model.

// ListServiceGroups returns the groups a single service belongs to
// via kind=service policies (NOT compound — compound policies have
// AND'd attribute filters and aren't pure "this service is in this
// group" assertions).
func (s *Store) ListServiceGroups(ctx context.Context, orgID uuid.UUID, serviceName string) ([]Group, error) {
	const q = `
		SELECT g.id, g.org_id, g.slug, g.name, g.description, g.created_at, g.updated_at
		FROM group_access_policies p
		JOIN groups g ON g.id = p.group_id
		WHERE g.org_id = $1
		  AND p.kind = 'service'
		  AND p.target_service_name = $2
		ORDER BY lower(g.name)`
	rows, err := s.pool.Query(ctx, q, orgID, serviceName)
	if err != nil {
		return nil, fmt.Errorf("identity: list service groups: %w", err)
	}
	defer rows.Close()
	out := make([]Group, 0)
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.OrgID, &g.Slug, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetServiceGroups replaces the kind=service policies for this
// service across all groups. Compound + integration + attributes
// policies that happen to grant the same service via other means
// are untouched — this only affects explicit allow-list entries.
func (s *Store) SetServiceGroups(ctx context.Context, orgID uuid.UUID, serviceName string, groupIDs []uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("identity: tx begin: %w", err)
	}
	defer tx.Rollback(ctx)
	// Clear existing kind=service policies for this service across
	// the org's groups (the join through groups + org_id is the
	// safety belt against cross-org accidents).
	if _, err := tx.Exec(ctx, `
		DELETE FROM group_access_policies p
		USING groups g
		WHERE p.group_id = g.id
		  AND g.org_id = $1
		  AND p.kind = 'service'
		  AND p.target_service_name = $2`,
		orgID, serviceName); err != nil {
		return fmt.Errorf("identity: clear service policies: %w", err)
	}
	for _, gid := range groupIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO group_access_policies (group_id, kind, target_service_name)
			VALUES ($1, 'service', $2)`,
			gid, serviceName); err != nil {
			return fmt.Errorf("identity: insert service policy: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// ── extra error sentinels ────────────────────────────────────────────

var (
	ErrGroupSlugExists = errors.New("identity: a group with that slug already exists")
)
