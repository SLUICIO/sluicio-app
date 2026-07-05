// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package messageviews

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a view lookup misses.
var ErrNotFound = errors.New("message view not found")

// Store is the Postgres-backed CRUD layer for saved Messages views.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// listColumns is the SELECT list used by List/Get/Create/Update. Kept
// in one place so a new column gets picked up consistently.
const listColumns = `id, organization_id, owner_user_id, name,
	COALESCE(description, ''), pinned, shared,
	filters, scope_integration_id, scope_service_id,
	last_result_count,
	last_edited_at, created_at, updated_at`

// List returns every view visible to the given org. With ownerUserID
// set, the returned views' Mine flag is populated for that user; an
// empty ownerUserID leaves Mine as false (good enough for the
// pre-auth phase).
//
// Order: pinned first, then most recently edited.
func (s *Store) List(ctx context.Context, orgID uuid.UUID, ownerUserID *uuid.UUID) ([]View, error) {
	q := `
		SELECT ` + listColumns + `
		FROM message_views
		WHERE organization_id = $1
		ORDER BY pinned DESC, last_edited_at DESC, created_at DESC
	`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("list message views: %w", err)
	}
	defer rows.Close()

	out := make([]View, 0)
	for rows.Next() {
		v, err := scanView(rows, ownerUserID)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Get returns one view by ID, scoped to the org.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID, ownerUserID *uuid.UUID) (View, error) {
	q := `
		SELECT ` + listColumns + `
		FROM message_views
		WHERE organization_id = $1 AND id = $2
	`
	row := s.pool.QueryRow(ctx, q, orgID, id)
	v, err := scanView(row, ownerUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("get message view: %w", err)
	}
	return v, nil
}

// Create inserts a new view and returns the persisted row.
func (s *Store) Create(ctx context.Context, orgID uuid.UUID, ownerUserID *uuid.UUID, req CreateRequest) (View, error) {
	filters := req.Filters
	if filters == nil {
		filters = []Filter{}
	}
	if err := ValidateAll(filters); err != nil {
		return View{}, err
	}
	bytes, err := json.Marshal(filters)
	if err != nil {
		return View{}, fmt.Errorf("marshal filters: %w", err)
	}
	q := `
		INSERT INTO message_views
			(organization_id, owner_user_id, name, description,
			 pinned, shared, filters,
			 scope_integration_id, scope_service_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING ` + listColumns
	row := s.pool.QueryRow(ctx, q,
		orgID, ownerUserID, req.Name, nilIfEmpty(req.Description),
		req.Pinned, req.Shared, bytes,
		uuidOrNil(req.Scope.IntegrationID), nilIfEmpty(req.Scope.ServiceID),
	)
	v, err := scanView(row, ownerUserID)
	if err != nil {
		return View{}, fmt.Errorf("create message view: %w", err)
	}
	return v, nil
}

// Update replaces every mutable field of the view in one statement and
// bumps last_edited_at. Partial updates are not supported in v1.
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, ownerUserID *uuid.UUID, req UpdateRequest) (View, error) {
	if err := ValidateAll(req.Filters); err != nil {
		return View{}, err
	}
	filters := req.Filters
	if filters == nil {
		filters = []Filter{}
	}
	bytes, err := json.Marshal(filters)
	if err != nil {
		return View{}, fmt.Errorf("marshal filters: %w", err)
	}
	q := `
		UPDATE message_views
		SET name = $3, description = $4,
		    pinned = $5, shared = $6, filters = $7,
		    scope_integration_id = $8, scope_service_id = $9,
		    last_edited_at = now(), updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING ` + listColumns
	row := s.pool.QueryRow(ctx, q,
		orgID, id, req.Name, nilIfEmpty(req.Description),
		req.Pinned, req.Shared, bytes,
		uuidOrNil(req.Scope.IntegrationID), nilIfEmpty(req.Scope.ServiceID),
	)
	v, err := scanView(row, ownerUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("update message view: %w", err)
	}
	return v, nil
}

// SetResultCount updates the cached result count for a view. Called by
// the search handler when a saved view is run, so the rail shows a
// reasonable hint without re-running the query.
func (s *Store) SetResultCount(ctx context.Context, orgID, id uuid.UUID, count int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE message_views SET last_result_count = $3
		 WHERE organization_id = $1 AND id = $2`,
		orgID, id, count,
	)
	if err != nil {
		return fmt.Errorf("update message view count: %w", err)
	}
	return nil
}

// Delete removes a view.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM message_views WHERE organization_id = $1 AND id = $2`,
		orgID, id,
	)
	if err != nil {
		return fmt.Errorf("delete message view: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// scanner is the subset of pgx.Row / pgx.Rows used by scanView. Lets
// the same scan logic feed both Get and List without duplication.
type scanner interface {
	Scan(dest ...any) error
}

func scanView(s scanner, callerID *uuid.UUID) (View, error) {
	var (
		v              View
		ownerNullable  *uuid.UUID
		filtersBytes   []byte
		scopeIntegrate *uuid.UUID
		scopeService   *string
		resultCount    *int64
		lastEdited     time.Time
		createdAt      time.Time
		updatedAt      time.Time
	)
	if err := s.Scan(
		&v.ID, &v.OrganizationID, &ownerNullable, &v.Name,
		&v.Description, &v.Pinned, &v.Shared,
		&filtersBytes, &scopeIntegrate, &scopeService,
		&resultCount,
		&lastEdited, &createdAt, &updatedAt,
	); err != nil {
		return View{}, err
	}
	v.OwnerUserID = ownerNullable
	v.ResultCount = resultCount
	v.LastEditedAt = lastEdited
	v.CreatedAt = createdAt
	v.UpdatedAt = updatedAt
	if scopeIntegrate != nil {
		v.Scope.IntegrationID = scopeIntegrate.String()
	}
	if scopeService != nil {
		v.Scope.ServiceID = *scopeService
	}
	if len(filtersBytes) > 0 {
		if err := json.Unmarshal(filtersBytes, &v.Filters); err != nil {
			return View{}, fmt.Errorf("unmarshal filters: %w", err)
		}
	}
	if v.Filters == nil {
		v.Filters = []Filter{}
	}
	// "Mine" is computed per request: a view belongs to the caller
	// when the owner_user_id column matches. Until auth lands and the
	// caller has an identity, this stays false everywhere.
	if callerID != nil && ownerNullable != nil && *ownerNullable == *callerID {
		v.Mine = true
	}
	return v, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// uuidOrNil parses an optional UUID string for binding to a UUID
// column. An empty string maps to NULL; a malformed value also maps to
// NULL on the assumption that the API handler has already validated
// it (the store is internal; bad input here is a programmer error and
// silently treating it as "no scope" is safer than panicking).
func uuidOrNil(s string) any {
	if s == "" {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return id
}
