// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package notifytemplates stores notification MESSAGE templates — the
// formatting side of alerting's org→team ladder (issue #5). One set per
// scope: group_id NULL is the org default, a group row is that team's
// override. Every field is Liquid and optional; empty = inherit, so
// Resolve merges per FIELD (team over org), never per set. Routing
// (notification profiles) stays a separate entity on purpose.
package notifytemplates

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TemplateSet is one stored scope's fields. Empty string = inherit.
type TemplateSet struct {
	ID           uuid.UUID  `json:"id"`
	GroupID      *uuid.UUID `json:"group_id,omitempty"`
	EmailSubject string     `json:"email_subject"`
	EmailBody    string     `json:"email_body"`
	SlackTitle   string     `json:"slack_title"`
	SlackBody    string     `json:"slack_body"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Resolved is the per-field merge of (team over org) for delivery. Any
// field may still be empty — the caller falls through to the cell-wide
// email setting / built-in constants.
type Resolved struct {
	EmailSubject string
	EmailBody    string
	SlackTitle   string
	SlackBody    string
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, group_id, email_subject, email_body, slack_title, slack_body, updated_at`

func scan(row pgx.Row) (TemplateSet, error) {
	var t TemplateSet
	err := row.Scan(&t.ID, &t.GroupID, &t.EmailSubject, &t.EmailBody, &t.SlackTitle, &t.SlackBody, &t.UpdatedAt)
	return t, err
}

// Get returns the stored set for one scope (groupID nil = org default).
// A scope with no row yet returns an all-empty set, not an error — the
// GET endpoints render "everything inherits".
func (s *Store) Get(ctx context.Context, orgID uuid.UUID, groupID *uuid.UUID) (TemplateSet, error) {
	q := `SELECT ` + cols + ` FROM notification_message_templates WHERE organization_id = $1 AND group_id IS NULL`
	args := []any{orgID}
	if groupID != nil {
		q = `SELECT ` + cols + ` FROM notification_message_templates WHERE organization_id = $1 AND group_id = $2`
		args = append(args, *groupID)
	}
	t, err := scan(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return TemplateSet{GroupID: groupID}, nil
	}
	if err != nil {
		return TemplateSet{}, fmt.Errorf("notifytemplates: get: %w", err)
	}
	return t, nil
}

// Upsert stores one scope's fields wholesale (the PUT endpoints send the
// full set; empty strings are meaningful — they mean inherit).
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, groupID *uuid.UUID, set TemplateSet) (TemplateSet, error) {
	// Two statements because the uniqueness for the org-default row lives
	// in a partial index, which ON CONFLICT can target only by predicate.
	var row pgx.Row
	if groupID == nil {
		row = s.pool.QueryRow(ctx, `
			INSERT INTO notification_message_templates (organization_id, group_id, email_subject, email_body, slack_title, slack_body)
			VALUES ($1, NULL, $2, $3, $4, $5)
			ON CONFLICT (organization_id) WHERE group_id IS NULL
			DO UPDATE SET email_subject = $2, email_body = $3, slack_title = $4, slack_body = $5, updated_at = now()
			RETURNING `+cols,
			orgID, set.EmailSubject, set.EmailBody, set.SlackTitle, set.SlackBody)
	} else {
		row = s.pool.QueryRow(ctx, `
			INSERT INTO notification_message_templates (organization_id, group_id, email_subject, email_body, slack_title, slack_body)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (organization_id, group_id) WHERE group_id IS NOT NULL
			DO UPDATE SET email_subject = $3, email_body = $4, slack_title = $5, slack_body = $6, updated_at = now()
			RETURNING `+cols,
			orgID, *groupID, set.EmailSubject, set.EmailBody, set.SlackTitle, set.SlackBody)
	}
	t, err := scan(row)
	if err != nil {
		return TemplateSet{}, fmt.Errorf("notifytemplates: upsert: %w", err)
	}
	return t, nil
}

// Resolve merges the ladder's stored rungs per field: the rule's owning
// group first (when groupID non-nil), then the org default. One query
// fetches both rows.
func (s *Store) Resolve(ctx context.Context, orgID uuid.UUID, groupID *uuid.UUID) (Resolved, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+cols+` FROM notification_message_templates
		WHERE organization_id = $1 AND (group_id IS NULL OR group_id = $2)`,
		orgID, groupID)
	if err != nil {
		return Resolved{}, fmt.Errorf("notifytemplates: resolve: %w", err)
	}
	defer rows.Close()
	var org, grp TemplateSet
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return Resolved{}, err
		}
		if t.GroupID == nil {
			org = t
		} else {
			grp = t
		}
	}
	if err := rows.Err(); err != nil {
		return Resolved{}, err
	}
	pick := func(g, o string) string {
		if g != "" {
			return g
		}
		return o
	}
	return Resolved{
		EmailSubject: pick(grp.EmailSubject, org.EmailSubject),
		EmailBody:    pick(grp.EmailBody, org.EmailBody),
		SlackTitle:   pick(grp.SlackTitle, org.SlackTitle),
		SlackBody:    pick(grp.SlackBody, org.SlackBody),
	}, nil
}
