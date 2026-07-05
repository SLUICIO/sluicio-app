// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package servicemeta stores the human-owned, editable metadata for a
// service (description, owner, on-call, team, repo, runbook). The
// service itself is discovered from telemetry; this is the descriptive
// layer the Service detail page's Identity form edits.
package servicemeta

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Metadata is the editable descriptive layer for a service.
type Metadata struct {
	ServiceName string    `json:"service_name"`
	Description string    `json:"description"`
	Owner       string    `json:"owner"`
	OnCall      string    `json:"on_call"`
	Team        string    `json:"team"`
	Repository  string    `json:"repository"`
	RunbookURL  string    `json:"runbook_url"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// Store is the Postgres-backed CRUD layer for service metadata.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Get returns the metadata for a service, or a zero-valued Metadata
// (only ServiceName set) if none has been saved yet.
func (s *Store) Get(ctx context.Context, orgID uuid.UUID, service string) (Metadata, error) {
	const q = `
		SELECT description, owner, on_call, team, repository, runbook_url, updated_at
		FROM service_metadata WHERE organization_id = $1 AND service_name = $2`
	m := Metadata{ServiceName: service}
	err := s.pool.QueryRow(ctx, q, orgID, service).Scan(
		&m.Description, &m.Owner, &m.OnCall, &m.Team, &m.Repository, &m.RunbookURL, &m.UpdatedAt)
	if err == pgx.ErrNoRows {
		return m, nil
	}
	if err != nil {
		return Metadata{}, fmt.Errorf("get service metadata: %w", err)
	}
	return m, nil
}

// Upsert writes the editable fields for a service.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, service string, m Metadata) (Metadata, error) {
	const q = `
		INSERT INTO service_metadata
			(organization_id, service_name, description, owner, on_call, team, repository, runbook_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (organization_id, service_name) DO UPDATE SET
			description = EXCLUDED.description,
			owner       = EXCLUDED.owner,
			on_call     = EXCLUDED.on_call,
			team        = EXCLUDED.team,
			repository  = EXCLUDED.repository,
			runbook_url = EXCLUDED.runbook_url,
			updated_at  = now()
		RETURNING description, owner, on_call, team, repository, runbook_url, updated_at`
	out := Metadata{ServiceName: service}
	err := s.pool.QueryRow(ctx, q, orgID, service,
		m.Description, m.Owner, m.OnCall, m.Team, m.Repository, m.RunbookURL).Scan(
		&out.Description, &out.Owner, &out.OnCall, &out.Team, &out.Repository, &out.RunbookURL, &out.UpdatedAt)
	if err != nil {
		return Metadata{}, fmt.Errorf("upsert service metadata: %w", err)
	}
	return out, nil
}
