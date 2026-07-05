// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Cell-operator (super-admin) store surface: the org lifecycle and the
// operator flag itself. Operators sit above the org-scoped roles — they
// create/rename/delete orgs, assign users to them, and own the cell-wide
// settings (SMTP / retention / license) shared across every org.
//
// There is deliberately no self-service org creation: CreateOrg is only
// reached from the operator-gated API. Single-org self-hosted never needs
// it (the bootstrap admin is promoted to operator on first boot).

package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// OrgWithCounts is an org plus its member count, for the operator's
// org list (avoids an N+1 from the UI).
type OrgWithCounts struct {
	Org
	MemberCount int `json:"member_count"`
}

// CreateOrg creates a new org. Returns ErrSlugTaken if the slug collides
// with an existing org. Operator-only.
func (s *Store) CreateOrg(ctx context.Context, name, slug string) (Org, error) {
	name = strings.TrimSpace(name)
	slug = strings.TrimSpace(slug)
	if name == "" || slug == "" {
		return Org{}, fmt.Errorf("identity: org name and slug are required")
	}
	const q = `
		INSERT INTO orgs (slug, name) VALUES ($1, $2)
		RETURNING id, slug, name, created_at, updated_at`
	row := s.pool.QueryRow(ctx, q, slug, name)
	var o Org
	if err := row.Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if strings.Contains(err.Error(), "orgs_slug_key") || strings.Contains(err.Error(), "idx_orgs_slug") {
			return Org{}, ErrSlugTaken
		}
		return Org{}, fmt.Errorf("identity: create org: %w", err)
	}
	return o, nil
}

// ListOrgs returns every org on the cell with its member count, oldest
// first (the default org sorts to the top). Operator-only.
func (s *Store) ListOrgs(ctx context.Context) ([]OrgWithCounts, error) {
	const q = `
		SELECT o.id, o.slug, o.name, o.created_at, o.updated_at,
		       (SELECT COUNT(*) FROM org_members m WHERE m.org_id = o.id)
		FROM orgs o
		ORDER BY o.created_at`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("identity: list orgs: %w", err)
	}
	defer rows.Close()
	out := make([]OrgWithCounts, 0)
	for rows.Next() {
		var o OrgWithCounts
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedAt, &o.UpdatedAt, &o.MemberCount); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ── operator flag ────────────────────────────────────────────────────

// SetUserOperator promotes (or demotes) a user to/from cell operator.
// Returns ErrNotFound if the user id doesn't exist.
func (s *Store) SetUserOperator(ctx context.Context, userID uuid.UUID, isOperator bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET is_operator = $2, updated_at = now() WHERE id = $1`,
		userID, isOperator)
	if err != nil {
		return fmt.Errorf("identity: set operator: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountOperators returns how many cell operators currently exist. Used by
// the bootstrap (promote the admin only if there are none) and by the
// demote guard (never leave the cell with zero operators).
func (s *Store) CountOperators(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE is_operator`).Scan(&n); err != nil {
		return 0, fmt.Errorf("identity: count operators: %w", err)
	}
	return n, nil
}

// EnsureBootstrapOperator promotes the user with the given email to
// operator IFF the cell currently has no operator at all. Idempotent:
// once any operator exists it never re-promotes, so a later demotion
// sticks. Returns whether it promoted someone.
func (s *Store) EnsureBootstrapOperator(ctx context.Context, email string) (bool, error) {
	n, err := s.CountOperators(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET is_operator = true, updated_at = now() WHERE lower(email) = lower($1)`,
		strings.TrimSpace(email))
	if err != nil {
		return false, fmt.Errorf("identity: bootstrap operator: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListUsersPage returns a filtered, paged slice of the cell's users plus
// the total match count. Operator-only — backs the Operator page's user
// list, which must stay usable at thousands of users. search matches
// email or name, case-insensitively. Does not populate password/activity
// fields.
func (s *Store) ListUsersPage(ctx context.Context, search string, limit, offset int) ([]User, int, error) {
	// Escape LIKE metacharacters so a search for "100%" behaves literally.
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(search))
	pattern := "%" + esc + "%"
	const where = `($1 = '%%' OR email ILIKE $1 OR name ILIKE $1)`

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE `+where, pattern).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("identity: count users: %w", err)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, email, name, is_operator, is_demo FROM users WHERE `+where+`
		 ORDER BY lower(email) LIMIT $2 OFFSET $3`, pattern, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("identity: list users page: %w", err)
	}
	defer rows.Close()
	out := make([]User, 0, limit)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.IsOperator, &u.IsDemo); err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}
