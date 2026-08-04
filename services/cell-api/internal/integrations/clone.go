// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Cloning an integration.
//
// The point of a clone is a second integration that watches a parallel
// flow: same matcher shape, same tags and metadata, same health checks,
// different name. What it must NEVER be is a way to acquire something
// the caller could not have created directly — which is the whole reason
// the decision of WHAT gets copied lives in its own function with its
// own tests, rather than being implied by whichever SQL happened to get
// written.
//
// Two things are deliberately not copied by default:
//
//   - Group access policies. Granting a group access to an integration
//     requires ADMIN (see the PUT /integrations/{id}/groups route), while
//     creating one only requires editor. If a clone carried those rows,
//     an editor would be performing an admin-only action sideways. So
//     they are reproduced only for a caller who could set them by hand.
//     Dropping them is the safe direction: an integration's visibility
//     flows through its member services, so a clone with the same
//     matchers is visible to exactly the people the source was — the
//     grants only ever ADD access, never remove it.
//
//   - The public status badge. It serves an unauthenticated endpoint.
//     Cloning an integration should not quietly publish a second public
//     URL; that has to be an explicit act on the new integration.
//
// What IS copied unconditionally is everything that describes the
// integration rather than who may reach it: matchers, description, tags,
// metadata values, and the health checks that define its healthy state.

package integrations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CloneScope is what one clone will carry. Built by PlanClone so the
// rules are stated once and can be asserted without a database.
type CloneScope struct {
	// GroupAccess reproduces the source's group_access_policies rows.
	GroupAccess bool
	// HealthChecks copies the alert rules bound to the source, including
	// which channels they notify.
	HealthChecks bool
	// PublicBadge is always false; present so the decision is visible in
	// the type rather than implied by an omission in the SQL.
	PublicBadge bool
}

// PlanClone decides what a caller's clone may carry.
//
// callerIsAdmin is the same authority the group-access route requires —
// not "can manage this integration", which an editor also has.
func PlanClone(callerIsAdmin bool) CloneScope {
	return CloneScope{
		GroupAccess: callerIsAdmin,
		// Health checks are settings of the integration, and creating
		// them needs no more authority than creating the integration.
		HealthChecks: true,
		PublicBadge:  false,
	}
}

// CloneOptions is a clone request: the new identity plus the scope.
type CloneOptions struct {
	Name  string
	Slug  string
	Scope CloneScope
}

// ErrSourceNotFound is returned when the integration being cloned does
// not exist in the caller's org.
var ErrSourceNotFound = errors.New("source integration not found")

// Clone copies an integration within one org, in a single transaction so
// a partial copy can never be left behind for someone to find later.
//
// integration_services is deliberately not copied: it is the reconciled
// catalog, derived from the matchers, and the reconciler will rebuild it.
// Copying it would assert membership the clone has not been shown to
// have. dashboard_items are not copied either — they belong to other
// people's dashboards, not to this integration.
func (s *Store) Clone(ctx context.Context, orgID, srcID uuid.UUID, opt CloneOptions) (Integration, error) {
	name := strings.TrimSpace(opt.Name)
	slug := strings.TrimSpace(opt.Slug)
	if name == "" || slug == "" {
		return Integration{}, errInvalid("name and slug are required")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Integration{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var srcDescription string
	err = tx.QueryRow(ctx,
		`SELECT description FROM integrations WHERE id = $1 AND organization_id = $2`,
		srcID, orgID,
	).Scan(&srcDescription)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrSourceNotFound
	}
	if err != nil {
		return Integration{}, fmt.Errorf("load source: %w", err)
	}

	var out Integration
	// badge_public is left at its column default: a clone never inherits
	// a public endpoint (see the file comment).
	err = tx.QueryRow(ctx, `
		INSERT INTO integrations (organization_id, slug, name, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, organization_id, slug, name, description, created_at, updated_at`,
		orgID, slug, name, srcDescription,
	).Scan(&out.ID, &out.OrganizationID, &out.Slug, &out.Name, &out.Description, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Integration{}, errInvalid("an integration with that slug already exists")
		}
		return Integration{}, fmt.Errorf("insert clone: %w", err)
	}

	copies := []struct {
		what string
		sql  string
		run  bool
	}{
		{"matchers", `
			INSERT INTO integration_matchers (integration_id, attribute, operator, value, match_group)
			SELECT $1, attribute, operator, value, match_group
			FROM integration_matchers WHERE integration_id = $2`, true},
		{"tags", `
			INSERT INTO integration_tags (integration_id, tag_id)
			SELECT $1, tag_id FROM integration_tags WHERE integration_id = $2
			ON CONFLICT DO NOTHING`, true},
		{"metadata", `
			INSERT INTO integration_metadata (integration_id, field_id, value)
			SELECT $1, field_id, value FROM integration_metadata WHERE integration_id = $2`, true},
		{"group access", `
			INSERT INTO group_access_policies (
				group_id, kind, target_service_name, target_integration_id,
				attribute_match, target_system_kind, conditions, target_system_id, signals)
			SELECT group_id, kind, target_service_name, $1,
			       attribute_match, target_system_kind, conditions, target_system_id, signals
			FROM group_access_policies WHERE target_integration_id = $2`, opt.Scope.GroupAccess},
	}
	for _, c := range copies {
		if !c.run {
			continue
		}
		if _, err := tx.Exec(ctx, c.sql, out.ID, srcID); err != nil {
			return Integration{}, fmt.Errorf("copy %s: %w", c.what, err)
		}
	}

	if opt.Scope.HealthChecks {
		if err := cloneAlertRules(ctx, tx, orgID, srcID, out.ID); err != nil {
			return Integration{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Integration{}, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

// cloneAlertRules copies the health checks bound to an integration,
// along with which channels each one notifies.
//
// Rules are copied one at a time rather than with a single INSERT
// ... SELECT because each needs its new id to carry its channel routes
// across; a set-based copy cannot pair old and new rows.
func cloneAlertRules(ctx context.Context, tx pgx.Tx, orgID, srcID, dstID uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT id FROM alert_rules
		WHERE organization_id = $1 AND integration_id = $2`, orgID, srcID)
	if err != nil {
		return fmt.Errorf("list source rules: %w", err)
	}
	var ruleIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan source rule: %w", err)
		}
		ruleIDs = append(ruleIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list source rules: %w", err)
	}

	for _, src := range ruleIDs {
		var newID uuid.UUID
		// Every column except id, created_at and updated_at (fresh on the
		// clone) and integration_id (rebound). Spelled out rather than
		// SELECT *, which cannot work anyway with a rebound column — the
		// cost is that a new alert_rules column has to be added here too,
		// or it silently will not clone.
		err := tx.QueryRow(ctx, `
			INSERT INTO alert_rules (
				organization_id, integration_id, name, description, signal, rule_spec,
				severity, evaluation_interval, enabled, service_name, group_id,
				title_template, body_template, source, display_on_service, unit,
				resolve_mode, notification_config, runbook, system_id
			)
			SELECT organization_id, $1, name, description, signal, rule_spec,
			       severity, evaluation_interval, enabled, service_name, group_id,
			       title_template, body_template, source, display_on_service, unit,
			       resolve_mode, notification_config, runbook, system_id
			FROM alert_rules WHERE id = $2
			RETURNING id`, dstID, src).Scan(&newID)
		if err != nil {
			return fmt.Errorf("copy alert rule: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO alert_rule_routes (alert_rule_id, channel_id)
			SELECT $1, channel_id FROM alert_rule_routes WHERE alert_rule_id = $2
			ON CONFLICT DO NOTHING`, newID, src); err != nil {
			return fmt.Errorf("copy alert rule routes: %w", err)
		}
	}
	return nil
}
