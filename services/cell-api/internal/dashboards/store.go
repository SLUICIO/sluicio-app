// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package dashboards

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

// ErrNotFound is returned when a dashboard lookup misses.
var ErrNotFound = errors.New("dashboard not found")

// Store is the Postgres-backed CRUD layer for dashboards.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// dashboardCols is the SELECT list used everywhere. One source of truth
// so new columns get picked up consistently across List/Get/Create/
// Update without scan/column drift.
const dashboardCols = `id, organization_id, owner_user_id, name,
	is_default, auto_include_all, default_widget_type, position,
	group_id, created_at, updated_at`

// itemCols mirrors dashboardCols for the items table.
const itemCols = `id, dashboard_id, entity_kind, integration_id, system_name, widget_type, position, created_at`

// List returns every dashboard visible to the given org, ordered by
// position then created_at. Items are populated in one extra round trip
// (a JOIN would force COALESCE gymnastics for the empty-items case).
// If callerID is non-nil, dashboards owned by that user get Mine=true.
func (s *Store) List(ctx context.Context, orgID uuid.UUID, callerID *uuid.UUID) ([]Dashboard, error) {
	q := `
		SELECT ` + dashboardCols + `
		FROM dashboards
		WHERE organization_id = $1
		ORDER BY position ASC, created_at ASC
	`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("list dashboards: %w", err)
	}
	defer rows.Close()

	out := make([]Dashboard, 0)
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		d, err := scanDashboard(rows, callerID)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
		ids = append(ids, d.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	itemsByDashboard, err := s.itemsForDashboards(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Items = itemsByDashboard[out[i].ID]
		if out[i].Items == nil {
			out[i].Items = []Item{}
		}
	}
	return out, nil
}

// Get returns one dashboard (with items) by id, scoped to the org.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID, callerID *uuid.UUID) (Dashboard, error) {
	q := `
		SELECT ` + dashboardCols + `
		FROM dashboards
		WHERE organization_id = $1 AND id = $2
	`
	row := s.pool.QueryRow(ctx, q, orgID, id)
	d, err := scanDashboard(row, callerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Dashboard{}, ErrNotFound
	}
	if err != nil {
		return Dashboard{}, fmt.Errorf("get dashboard: %w", err)
	}
	items, err := s.itemsForDashboards(ctx, []uuid.UUID{id})
	if err != nil {
		return Dashboard{}, err
	}
	d.Items = items[id]
	if d.Items == nil {
		d.Items = []Item{}
	}
	return d, nil
}

// Create inserts a new dashboard and its items in one transaction.
// IsDefault=true clears the same flag on every other dashboard for the
// same (org, owner) — there can be only one default per user.
func (s *Store) Create(ctx context.Context, orgID uuid.UUID, ownerUserID *uuid.UUID, req CreateRequest) (Dashboard, error) {
	if err := req.Validate(); err != nil {
		return Dashboard{}, err
	}
	defaultWidget := req.DefaultWidgetType
	if defaultWidget == "" {
		defaultWidget = WidgetTrafficSparkline
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Dashboard{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if req.IsDefault {
		if err := clearDefaults(ctx, tx, orgID, ownerUserID, uuid.Nil); err != nil {
			return Dashboard{}, err
		}
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO dashboards
			(organization_id, owner_user_id, name, is_default,
			 auto_include_all, default_widget_type, position, group_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+dashboardCols, orgID, ownerUserID, req.Name, req.IsDefault,
		req.AutoIncludeAll, string(defaultWidget), req.Position, req.GroupID,
	)
	d, err := scanDashboard(row, ownerUserID)
	if err != nil {
		return Dashboard{}, fmt.Errorf("insert dashboard: %w", err)
	}

	if err := insertItems(ctx, tx, d.ID, req.Items); err != nil {
		return Dashboard{}, err
	}

	items, err := itemsInTx(ctx, tx, d.ID)
	if err != nil {
		return Dashboard{}, err
	}
	d.Items = items

	if err := tx.Commit(ctx); err != nil {
		return Dashboard{}, fmt.Errorf("commit: %w", err)
	}
	return d, nil
}

// Update replaces every mutable field and re-syncs the items list. The
// items behave like a set: rows missing from req are deleted, rows
// present are upserted by (dashboard, integration).
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, ownerUserID *uuid.UUID, req UpdateRequest) (Dashboard, error) {
	if err := req.Validate(); err != nil {
		return Dashboard{}, err
	}
	defaultWidget := req.DefaultWidgetType
	if defaultWidget == "" {
		defaultWidget = WidgetTrafficSparkline
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Dashboard{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if req.IsDefault {
		if err := clearDefaults(ctx, tx, orgID, ownerUserID, id); err != nil {
			return Dashboard{}, err
		}
	}

	row := tx.QueryRow(ctx, `
		UPDATE dashboards
		SET name = $3, is_default = $4, auto_include_all = $5,
		    default_widget_type = $6, position = $7,
		    updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING `+dashboardCols, orgID, id, req.Name, req.IsDefault,
		req.AutoIncludeAll, string(defaultWidget), req.Position,
	)
	d, err := scanDashboard(row, ownerUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Dashboard{}, ErrNotFound
	}
	if err != nil {
		return Dashboard{}, fmt.Errorf("update dashboard: %w", err)
	}

	if err := replaceItems(ctx, tx, d.ID, req.Items); err != nil {
		return Dashboard{}, err
	}

	items, err := itemsInTx(ctx, tx, d.ID)
	if err != nil {
		return Dashboard{}, err
	}
	d.Items = items

	if err := tx.Commit(ctx); err != nil {
		return Dashboard{}, fmt.Errorf("commit: %w", err)
	}
	return d, nil
}

// Delete removes a dashboard (items cascade).
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM dashboards WHERE organization_id = $1 AND id = $2`,
		orgID, id,
	)
	if err != nil {
		return fmt.Errorf("delete dashboard: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// helpers ----------------------------------------------------------

// clearDefaults flips is_default to FALSE on every dashboard owned by
// the same (org, owner) except `keep`. Pass uuid.Nil as keep on Create.
func clearDefaults(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, ownerUserID *uuid.UUID, keep uuid.UUID) error {
	// owner_user_id NULL means org-shared. Use IS NOT DISTINCT FROM so
	// NULL matches NULL and a specific user matches their own rows.
	_, err := tx.Exec(ctx, `
		UPDATE dashboards
		SET is_default = FALSE, updated_at = now()
		WHERE organization_id = $1
		  AND owner_user_id IS NOT DISTINCT FROM $2
		  AND id <> $3
		  AND is_default = TRUE
	`, orgID, ownerUserID, keep)
	if err != nil {
		return fmt.Errorf("clear defaults: %w", err)
	}
	return nil
}

func insertItems(ctx context.Context, tx pgx.Tx, dashboardID uuid.UUID, items []ItemRequest) error {
	for _, it := range items {
		kind := it.EntityKind
		if kind == "" {
			kind = EntityIntegration
		}
		if kind == EntitySystem {
			name := strings.TrimSpace(it.SystemName)
			if name == "" {
				return errInvalid("system item requires systemName")
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO dashboard_items (dashboard_id, entity_kind, system_name, widget_type, position)
				VALUES ($1, 'system', $2, 'system_health', $3)
			`, dashboardID, name, it.Position); err != nil {
				return fmt.Errorf("insert system item: %w", err)
			}
			continue
		}
		intID, err := uuid.Parse(it.IntegrationID)
		if err != nil {
			return errInvalid("integrationId must be a UUID")
		}
		wt := it.WidgetType
		if wt == "" {
			wt = WidgetTrafficSparkline
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO dashboard_items (dashboard_id, entity_kind, integration_id, widget_type, position)
			VALUES ($1, 'integration', $2, $3, $4)
		`, dashboardID, intID, string(wt), it.Position); err != nil {
			return fmt.Errorf("insert dashboard item: %w", err)
		}
	}
	return nil
}

// replaceItems is a full replace: drop every item then re-insert the new set.
// Update semantics are already "swap the whole list", and a clean replace keeps
// the polymorphic (integration|system) insert simple. Runs in the caller's tx.
func replaceItems(ctx context.Context, tx pgx.Tx, dashboardID uuid.UUID, items []ItemRequest) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM dashboard_items WHERE dashboard_id = $1`, dashboardID,
	); err != nil {
		return fmt.Errorf("clear dashboard items: %w", err)
	}
	return insertItems(ctx, tx, dashboardID, items)
}

func itemsInTx(ctx context.Context, tx pgx.Tx, dashboardID uuid.UUID) ([]Item, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+itemCols+`
		FROM dashboard_items
		WHERE dashboard_id = $1
		ORDER BY position ASC, created_at ASC
	`, dashboardID)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()
	out := make([]Item, 0)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) itemsForDashboards(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]Item, error) {
	out := make(map[uuid.UUID][]Item, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+itemCols+`
		FROM dashboard_items
		WHERE dashboard_id = ANY($1::uuid[])
		ORDER BY dashboard_id, position ASC, created_at ASC
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("list items batch: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id            uuid.UUID
			dashID        uuid.UUID
			entityKind    string
			integrationID *uuid.UUID
			systemName    *string
			widgetType    string
			position      int
			createdAt     time.Time
		)
		if err := rows.Scan(&id, &dashID, &entityKind, &integrationID, &systemName, &widgetType, &position, &createdAt); err != nil {
			return nil, err
		}
		item := Item{
			ID:         id,
			EntityKind: EntityKind(entityKind),
			WidgetType: WidgetType(widgetType),
			Position:   position,
			CreatedAt:  createdAt,
		}
		if integrationID != nil {
			item.IntegrationID = *integrationID
		}
		if systemName != nil {
			item.SystemName = *systemName
		}
		out[dashID] = append(out[dashID], item)
	}
	return out, rows.Err()
}

// scanner mirrors messageviews.scanner — lets one scan path serve both
// pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanDashboard(s scanner, callerID *uuid.UUID) (Dashboard, error) {
	var (
		d                 Dashboard
		ownerNullable     *uuid.UUID
		defaultWidgetType string
		createdAt         time.Time
		updatedAt         time.Time
	)
	if err := s.Scan(
		&d.ID, &d.OrganizationID, &ownerNullable, &d.Name,
		&d.IsDefault, &d.AutoIncludeAll, &defaultWidgetType, &d.Position,
		&d.GroupID, &createdAt, &updatedAt,
	); err != nil {
		return Dashboard{}, err
	}
	d.OwnerUserID = ownerNullable
	d.DefaultWidgetType = WidgetType(defaultWidgetType)
	d.CreatedAt = createdAt
	d.UpdatedAt = updatedAt
	if callerID != nil && ownerNullable != nil && *ownerNullable == *callerID {
		d.Mine = true
	}
	return d, nil
}

func scanItem(s scanner) (Item, error) {
	var (
		it            Item
		dashboardID   uuid.UUID
		entityKind    string
		integrationID *uuid.UUID
		systemName    *string
		widgetType    string
		createdAt     time.Time
	)
	if err := s.Scan(&it.ID, &dashboardID, &entityKind, &integrationID, &systemName, &widgetType, &it.Position, &createdAt); err != nil {
		return Item{}, err
	}
	it.EntityKind = EntityKind(entityKind)
	if integrationID != nil {
		it.IntegrationID = *integrationID
	}
	if systemName != nil {
		it.SystemName = *systemName
	}
	it.WidgetType = WidgetType(widgetType)
	it.CreatedAt = createdAt
	return it, nil
}
