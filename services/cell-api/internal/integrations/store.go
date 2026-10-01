// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when an integration or matcher is not found.
var ErrNotFound = errors.New("integration not found")

// Store is the Postgres-backed CRUD layer.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by the given Postgres pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// List returns every integration for the org, ordered by name.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Integration, error) {
	const q = `
		SELECT id, organization_id, slug, name, COALESCE(description, ''), rule_match, created_at, updated_at
		FROM integrations
		WHERE organization_id = $1
		ORDER BY name
	`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("list integrations: %w", err)
	}
	defer rows.Close()

	var out []Integration
	for rows.Next() {
		var i Integration
		if err := rows.Scan(&i.ID, &i.OrganizationID, &i.Slug, &i.Name, &i.Description, &i.RuleMatch, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Get returns a single integration with its matchers.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (IntegrationWithMatchers, error) {
	const q = `
		SELECT id, organization_id, slug, name, COALESCE(description, ''), badge_public, rule_match,
		       COALESCE(message_columns, '[]'::jsonb), COALESCE(message_filters, '[]'::jsonb),
		       created_at, updated_at
		FROM integrations
		WHERE organization_id = $1 AND id = $2
	`
	var i Integration
	var cols, filters []byte
	err := s.pool.QueryRow(ctx, q, orgID, id).Scan(&i.ID, &i.OrganizationID, &i.Slug, &i.Name, &i.Description, &i.BadgePublic, &i.RuleMatch, &cols, &filters, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return IntegrationWithMatchers{}, ErrNotFound
	}
	if err != nil {
		return IntegrationWithMatchers{}, fmt.Errorf("get integration: %w", err)
	}
	// A column list that fails to parse must not take the integration
	// down with it: the detail page still works, it just shows the
	// default columns. Nothing else on this screen depends on it.
	if err := json.Unmarshal(cols, &i.MessageColumns); err != nil {
		i.MessageColumns = nil
	}
	// Same reasoning for the filter list, with one difference worth
	// naming: a filter list that fails to parse falls back to nil, which
	// means UNRESTRICTED. That is the safe direction here — this is
	// tidiness, not access control, and RBAC still decides what the
	// caller may read. Failing closed would hide every filter on the
	// integration over a malformed row.
	if err := json.Unmarshal(filters, &i.MessageFilters); err != nil {
		i.MessageFilters = nil
	}

	matchers, err := s.MatchersForIntegration(ctx, id)
	if err != nil {
		return IntegrationWithMatchers{}, err
	}
	return IntegrationWithMatchers{Integration: i, Matchers: matchers}, nil
}

// SetBadgePublic flips whether this integration exposes a public status badge.
// Org-scoped; ErrNotFound if the integration isn't in the org.
func (s *Store) SetBadgePublic(ctx context.Context, orgID, id uuid.UUID, public bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE integrations SET badge_public = $3, updated_at = now()
		 WHERE organization_id = $1 AND id = $2`, orgID, id, public)
	if err != nil {
		return fmt.Errorf("set integration badge_public: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetMessageColumns replaces the integration's promoted message columns.
//
// A whole-list replace rather than add/remove/reorder endpoints: the
// order IS the column order, so every mutation is a rewrite of the list
// anyway, and three endpoints that each have to agree about ordering is
// three chances to disagree.
func (s *Store) SetMessageColumns(ctx context.Context, orgID, id uuid.UUID, cols []MessageColumn) error {
	normalized, err := NormalizeMessageColumns(cols)
	if err != nil {
		return err
	}
	// Marshal the empty list as [] rather than null — the column is NOT
	// NULL, and a reader that has to handle both is a reader that will
	// eventually handle one of them wrong.
	if normalized == nil {
		normalized = []MessageColumn{}
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("encode message columns: %w", err)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE integrations SET message_columns = $3, updated_at = now()
		 WHERE organization_id = $1 AND id = $2`, orgID, id, raw)
	if err != nil {
		return fmt.Errorf("set message columns: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetMessageFilters replaces the integration's filter-field list.
//
// An empty list is stored as [] and means unrestricted, so clearing the
// list is how an editor goes back to offering every attribute.
func (s *Store) SetMessageFilters(ctx context.Context, orgID, id uuid.UUID, filters []MessageFilter) error {
	normalized, err := NormalizeMessageFilters(filters)
	if err != nil {
		return err
	}
	if normalized == nil {
		normalized = []MessageFilter{}
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("encode message filters: %w", err)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE integrations SET message_filters = $3, updated_at = now()
		 WHERE organization_id = $1 AND id = $2`, orgID, id, raw)
	if err != nil {
		return fmt.Errorf("set message filters: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Create inserts a new integration and its matchers in one transaction.
func (s *Store) Create(ctx context.Context, in IntegrationWithMatchers) (IntegrationWithMatchers, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return IntegrationWithMatchers{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var i Integration
	err = tx.QueryRow(ctx, `
		INSERT INTO integrations (organization_id, slug, name, description, rule_match)
		VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, ''), 'any'))
		RETURNING id, organization_id, slug, name, COALESCE(description, ''), rule_match, created_at, updated_at
	`, in.OrganizationID, in.Slug, in.Name, in.Description, string(in.RuleMatch)).Scan(
		&i.ID, &i.OrganizationID, &i.Slug, &i.Name, &i.Description, &i.RuleMatch, &i.CreatedAt, &i.UpdatedAt,
	)
	if err != nil {
		return IntegrationWithMatchers{}, fmt.Errorf("insert integration: %w", err)
	}

	matchers := make([]Matcher, 0, len(in.Matchers))
	for _, m := range in.Matchers {
		if err := m.Validate(); err != nil {
			return IntegrationWithMatchers{}, err
		}
		var created Matcher
		err := tx.QueryRow(ctx, `
			INSERT INTO integration_matchers (integration_id, attribute, operator, value, match_group, include_descendants)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, integration_id, attribute, operator, value, match_group, include_descendants, created_at
		`, i.ID, m.Attribute, m.Operator, m.Value, m.MatchGroup, m.IncludeDescendants).Scan(
			&created.ID, &created.IntegrationID, &created.Attribute, &created.Operator, &created.Value, &created.MatchGroup, &created.IncludeDescendants, &created.CreatedAt,
		)
		if err != nil {
			return IntegrationWithMatchers{}, fmt.Errorf("insert matcher: %w", err)
		}
		matchers = append(matchers, created)
	}

	if err := tx.Commit(ctx); err != nil {
		return IntegrationWithMatchers{}, err
	}
	return IntegrationWithMatchers{Integration: i, Matchers: matchers}, nil
}

// Update changes the mutable fields (name, description, rule_match) of an
// integration. Slug is intentionally immutable to keep URLs stable.
//
// A nil ruleMatch leaves the mode alone: the matcher editor saves the mode
// and the details form saves the name, and neither should quietly revert
// what the other set.
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, name, description string, ruleMatch *RuleMatch) (Integration, error) {
	var mode *string
	if ruleMatch != nil {
		v := string(*ruleMatch)
		mode = &v
	}
	var i Integration
	err := s.pool.QueryRow(ctx, `
		UPDATE integrations
		SET name = $3, description = $4, rule_match = COALESCE($5, rule_match), updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING id, organization_id, slug, name, COALESCE(description, ''), rule_match, created_at, updated_at
	`, orgID, id, name, description, mode).Scan(
		&i.ID, &i.OrganizationID, &i.Slug, &i.Name, &i.Description, &i.RuleMatch, &i.CreatedAt, &i.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	}
	if err != nil {
		return Integration{}, fmt.Errorf("update integration: %w", err)
	}
	return i, nil
}

// BoundCheck is a health check (alert rule) that is deleted together with
// the integration it is bound to.
type BoundCheck struct {
	ID   uuid.UUID
	Name string
}

// boundChecksWhere selects the checks that belong to an integration and
// so go with it. A check that ALSO names a system is about the system
// (system wins over integration wherever a rule's scope is resolved), so
// it is not one of them: Delete unbinds it from the integration instead.
// Only rules from before the API rejected ambiguous scopes look like that.
const boundChecksWhere = `organization_id = $1 AND integration_id = $2 AND system_id IS NULL`

// CountBoundChecks reports how many health checks Delete would take with
// it, for the confirmation that has to say so beforehand.
func (s *Store) CountBoundChecks(ctx context.Context, orgID, id uuid.UUID) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM alert_rules WHERE `+boundChecksWhere, orgID, id).Scan(&n); err != nil {
		return 0, fmt.Errorf("count bound checks: %w", err)
	}
	return n, nil
}

// Delete removes the integration and, in the same transaction, the health
// checks bound to it; matchers cascade. It returns the checks it removed.
//
// The checks cannot outlive it. Unbound, a check has no service, no
// integration and no system, which every evaluator reads as "all
// services": a threshold written for one queue would start firing on
// every queue in the org. The foreign key cascades too (migration 0099);
// deleting them here first is what lets the caller say which went.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) ([]BoundCheck, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE alert_rules SET integration_id = NULL, updated_at = now()
		 WHERE organization_id = $1 AND integration_id = $2 AND system_id IS NOT NULL`, orgID, id); err != nil {
		return nil, fmt.Errorf("delete integration: unbind system checks: %w", err)
	}
	rows, err := tx.Query(ctx,
		`DELETE FROM alert_rules WHERE `+boundChecksWhere+` RETURNING id, name`, orgID, id)
	if err != nil {
		return nil, fmt.Errorf("delete integration: delete bound checks: %w", err)
	}
	checks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (BoundCheck, error) {
		var c BoundCheck
		err := row.Scan(&c.ID, &c.Name)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("delete integration: delete bound checks: %w", err)
	}

	tag, err := tx.Exec(ctx, `DELETE FROM integrations WHERE organization_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return nil, fmt.Errorf("delete integration: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("delete integration: commit: %w", err)
	}
	return checks, nil
}

// AddMatcher inserts a matcher under the given integration, which must
// belong to orgID: ErrNotFound otherwise.
//
// The org is in the query rather than left to the caller, like every
// other integration write here. The route guard in front of it checks the
// caller's role, and for an ordinary org editor that holds for any
// integration id at all - including another org's.
func (s *Store) AddMatcher(ctx context.Context, orgID, integrationID uuid.UUID, m Matcher) (Matcher, error) {
	if err := m.Validate(); err != nil {
		return Matcher{}, err
	}
	var created Matcher
	err := s.pool.QueryRow(ctx, `
		INSERT INTO integration_matchers (integration_id, attribute, operator, value, match_group, include_descendants)
		SELECT i.id, $3, $4, $5, $6, $7
		FROM integrations i
		WHERE i.id = $1 AND i.organization_id = $2
		RETURNING id, integration_id, attribute, operator, value, match_group, include_descendants, created_at
	`, integrationID, orgID, m.Attribute, m.Operator, m.Value, m.MatchGroup, m.IncludeDescendants).Scan(
		&created.ID, &created.IntegrationID, &created.Attribute, &created.Operator, &created.Value, &created.MatchGroup, &created.IncludeDescendants, &created.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Matcher{}, ErrNotFound
	}
	if err != nil {
		return Matcher{}, fmt.Errorf("insert matcher: %w", err)
	}
	return created, nil
}

// ReplaceMatchers swaps an integration's whole matcher set, and
// optionally how its rules combine, in one transaction. The integration
// must belong to orgID: ErrNotFound otherwise.
//
// It exists because the editor used to save by adding the new rows and
// then deleting the old ones, one request each. A failure part-way
// through left the integration matching both sets, or half of one, and
// nothing told the person who pressed Save. Here every matcher is
// validated before anything is written, and the swap either happens
// whole or not at all. Returns the stored set and the previous count.
func (s *Store) ReplaceMatchers(ctx context.Context, orgID, integrationID uuid.UUID, ms []Matcher, ruleMatch *RuleMatch) ([]Matcher, int, error) {
	for _, m := range ms {
		if err := m.Validate(); err != nil {
			return nil, 0, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Locked, so two saves of the same integration serialise rather than
	// interleave their deletes and inserts.
	var locked uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM integrations WHERE id = $1 AND organization_id = $2 FOR UPDATE`,
		integrationID, orgID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("lock integration: %w", err)
	}
	if ruleMatch != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE integrations SET rule_match = $2, updated_at = now() WHERE id = $1`,
			integrationID, string(*ruleMatch)); err != nil {
			return nil, 0, fmt.Errorf("update rule match: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM integration_matchers WHERE integration_id = $1`, integrationID)
	if err != nil {
		return nil, 0, fmt.Errorf("delete matchers: %w", err)
	}
	out := make([]Matcher, 0, len(ms))
	for _, m := range ms {
		var created Matcher
		if err := tx.QueryRow(ctx, `
			INSERT INTO integration_matchers (integration_id, attribute, operator, value, match_group, include_descendants)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, integration_id, attribute, operator, value, match_group, include_descendants, created_at
		`, integrationID, m.Attribute, m.Operator, m.Value, m.MatchGroup, m.IncludeDescendants).Scan(
			&created.ID, &created.IntegrationID, &created.Attribute, &created.Operator, &created.Value, &created.MatchGroup, &created.IncludeDescendants, &created.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("insert matcher: %w", err)
		}
		out = append(out, created)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return out, int(tag.RowsAffected()), nil
}

// RemoveMatcher deletes one matcher of one integration of orgID:
// ErrNotFound unless all three agree. A matcher id alone is not enough,
// since the integration named in the URL is the one the caller was
// checked against.
func (s *Store) RemoveMatcher(ctx context.Context, orgID, integrationID, matcherID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM integration_matchers m
		USING integrations i
		WHERE m.id = $1 AND m.integration_id = $2
		  AND i.id = m.integration_id AND i.organization_id = $3
	`, matcherID, integrationID, orgID)
	if err != nil {
		return fmt.Errorf("delete matcher: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveServiceMatchers deletes the exact-match matchers (operator=equals,
// value=serviceName) that tie a service to an integration — the inverse of
// the "add to integration" action. Broad rules (prefix / suffix / contains /
// regex) are left untouched, since deleting those would silently change
// matching for other services. Returns how many were removed (0 means the
// service is matched by a rule, not a direct link).
func (s *Store) RemoveServiceMatchers(ctx context.Context, integrationID uuid.UUID, serviceName string) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM integration_matchers WHERE integration_id = $1 AND operator = 'equals' AND value = $2`,
		integrationID, serviceName)
	if err != nil {
		return 0, fmt.Errorf("integrations: remove service matchers: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RuleMatchForIntegration returns how this integration's rules combine.
// A missing row reads as the union, which is what a caller that cannot
// find the integration should assume rather than an empty slice.
func (s *Store) RuleMatchForIntegration(ctx context.Context, integrationID uuid.UUID) (RuleMatch, error) {
	var mode RuleMatch
	err := s.pool.QueryRow(ctx,
		`SELECT rule_match FROM integrations WHERE id = $1`, integrationID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleMatchAny, nil
	}
	if err != nil {
		return RuleMatchAny, fmt.Errorf("rule match for integration: %w", err)
	}
	return mode, nil
}

// MatchersForIntegration returns all matchers for the given integration.
func (s *Store) MatchersForIntegration(ctx context.Context, integrationID uuid.UUID) ([]Matcher, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, integration_id, attribute, operator, value, match_group, include_descendants, created_at
		FROM integration_matchers
		WHERE integration_id = $1
		ORDER BY match_group, created_at
	`, integrationID)
	if err != nil {
		return nil, fmt.Errorf("matchers for integration: %w", err)
	}
	defer rows.Close()

	out := make([]Matcher, 0)
	for rows.Next() {
		var m Matcher
		if err := rows.Scan(&m.ID, &m.IntegrationID, &m.Attribute, &m.Operator, &m.Value, &m.MatchGroup, &m.IncludeDescendants, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AllMatchersWithIntegration returns every matcher in the org joined
// with its integration. Used by the resolver to classify services.
type MatcherWithIntegration struct {
	Matcher     Matcher
	Integration Integration
}

func (s *Store) AllMatchersWithIntegration(ctx context.Context, orgID uuid.UUID) ([]MatcherWithIntegration, error) {
	const q = `
		SELECT
			m.id, m.integration_id, m.attribute, m.operator, m.value, m.match_group, m.include_descendants, m.created_at,
			i.id, i.organization_id, i.slug, i.name, COALESCE(i.description, ''), i.rule_match,
			i.created_at, i.updated_at
		FROM integration_matchers m
		JOIN integrations i ON i.id = m.integration_id
		WHERE i.organization_id = $1
	`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("all matchers: %w", err)
	}
	defer rows.Close()

	var out []MatcherWithIntegration
	for rows.Next() {
		var mi MatcherWithIntegration
		if err := rows.Scan(
			&mi.Matcher.ID, &mi.Matcher.IntegrationID, &mi.Matcher.Attribute, &mi.Matcher.Operator, &mi.Matcher.Value, &mi.Matcher.MatchGroup, &mi.Matcher.IncludeDescendants, &mi.Matcher.CreatedAt,
			&mi.Integration.ID, &mi.Integration.OrganizationID, &mi.Integration.Slug, &mi.Integration.Name, &mi.Integration.Description, &mi.Integration.RuleMatch,
			&mi.Integration.CreatedAt, &mi.Integration.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, mi)
	}
	return out, rows.Err()
}
