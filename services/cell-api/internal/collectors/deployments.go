// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeploymentStatus is where a configuration sent to a collector stands.
type DeploymentStatus string

const (
	// DeploymentPending is stored and not yet delivered: the collector is
	// offline, or polls over HTTP and has not asked since.
	DeploymentPending DeploymentStatus = "pending"
	// DeploymentSent was delivered; the collector has not answered yet.
	DeploymentSent DeploymentStatus = "sent"
	// DeploymentApplying: the collector said it is applying it.
	DeploymentApplying DeploymentStatus = "applying"
	// DeploymentApplied: the collector runs it.
	DeploymentApplied DeploymentStatus = "applied"
	// DeploymentFailed: the collector refused or could not run it. The
	// supervisor keeps (or goes back to) what it ran before.
	DeploymentFailed DeploymentStatus = "failed"
	// DeploymentSuperseded: a newer deployment replaced it before it
	// applied.
	DeploymentSuperseded DeploymentStatus = "superseded"
)

// Deployment is one configuration sent, or to be sent, to a collector.
type Deployment struct {
	ID             uuid.UUID        `json:"id"`
	CollectorID    uuid.UUID        `json:"collector_id"`
	Hash           string           `json:"hash"`
	Status         DeploymentStatus `json:"status"`
	ErrorMessage   string           `json:"error_message,omitempty"`
	Message        string           `json:"message,omitempty"`
	CreatedByName  string           `json:"created_by_name,omitempty"`
	Via            string           `json:"via"`
	ProposalID     *uuid.UUID       `json:"proposal_id,omitempty"`
	RolledBackFrom *uuid.UUID       `json:"rolled_back_from,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	SentAt         *time.Time       `json:"sent_at,omitempty"`
	FinishedAt     *time.Time       `json:"finished_at,omitempty"`
}

// NewDeployment is what a push stores. Body is the configuration as it
// will be sent (secrets restored, PrepareRemote applied).
type NewDeployment struct {
	CollectorID    uuid.UUID
	Body           string
	Message        string
	CreatedBy      *uuid.UUID
	CreatedByName  string
	Via            string
	ProposalID     *uuid.UUID
	RolledBackFrom *uuid.UUID
}

const deploymentCols = `id, collector_id, config_hash, status, error_message, message, created_by_name, via,
	proposal_id, rolled_back_from, created_at, sent_at, finished_at`

func scanDeployment(row pgx.Row) (Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.CollectorID, &d.Hash, &d.Status, &d.ErrorMessage, &d.Message, &d.CreatedByName, &d.Via,
		&d.ProposalID, &d.RolledBackFrom, &d.CreatedAt, &d.SentAt, &d.FinishedAt)
	return d, err
}

func deploymentFiles(body string) []ConfigFile {
	return []ConfigFile{{Name: "", ContentType: "text/yaml", Body: body}}
}

// CreateDeployment stores a configuration to send. Anything still waiting
// for the same collector is superseded: only the newest is delivered.
func (s *Store) CreateDeployment(ctx context.Context, nd NewDeployment) (Deployment, error) {
	if s.keys == nil {
		return Deployment{}, ErrNoKey
	}
	hash, sealed, err := s.keys.Seal(nd.CollectorID, deploymentFiles(nd.Body))
	if err != nil {
		return Deployment{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE collector_deployments SET status = 'superseded', finished_at = now()
		WHERE collector_id = $1 AND status IN ('pending', 'sent', 'applying')`, nd.CollectorID); err != nil {
		return Deployment{}, fmt.Errorf("collectors: supersede: %w", err)
	}
	via := nd.Via
	if via == "" {
		via = "ui"
	}
	d, err := scanDeployment(tx.QueryRow(ctx, `
		INSERT INTO collector_deployments (collector_id, config_hash, config_enc, message, created_by, created_by_name, via, proposal_id, rolled_back_from)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+deploymentCols,
		nd.CollectorID, hash, sealed, nd.Message, nd.CreatedBy, nd.CreatedByName, via, nd.ProposalID, nd.RolledBackFrom))
	if err != nil {
		return Deployment{}, fmt.Errorf("collectors: create deployment: %w", err)
	}
	return d, tx.Commit(ctx)
}

// NextToDeliver is the newest deployment not yet answered for, with its
// body, or nil.
func (s *Store) NextToDeliver(ctx context.Context, collectorID uuid.UUID) (*Deployment, string, error) {
	if s.keys == nil {
		return nil, "", nil
	}
	var sealed string
	row := s.pool.QueryRow(ctx, `
		SELECT `+deploymentCols+`, config_enc FROM collector_deployments
		WHERE collector_id = $1 AND status IN ('pending', 'sent')
		ORDER BY created_at DESC LIMIT 1`, collectorID)
	var d Deployment
	err := row.Scan(&d.ID, &d.CollectorID, &d.Hash, &d.Status, &d.ErrorMessage, &d.Message, &d.CreatedByName, &d.Via,
		&d.ProposalID, &d.RolledBackFrom, &d.CreatedAt, &d.SentAt, &d.FinishedAt, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("collectors: next deployment: %w", err)
	}
	files, err := s.keys.Open(collectorID, d.Hash, sealed)
	if err != nil || len(files) != 1 {
		return nil, "", fmt.Errorf("collectors: open deployment: %w", err)
	}
	return &d, files[0].Body, nil
}

// MarkSent records that a deployment was delivered.
func (s *Store) MarkSent(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE collector_deployments SET status = 'sent', sent_at = COALESCE(sent_at, now())
		WHERE id = $1 AND status = 'pending'`, id)
	return err
}

// ReportStatus applies what a collector said about the configuration with
// this hash (OpAMP's remote config status). A status for a superseded or
// already finished deployment changes nothing.
func (s *Store) ReportStatus(ctx context.Context, collectorID uuid.UUID, hash string, status DeploymentStatus, errMsg string) error {
	finished := status == DeploymentApplied || status == DeploymentFailed
	_, err := s.pool.Exec(ctx, `
		UPDATE collector_deployments SET status = $3, error_message = $4,
		       sent_at = COALESCE(sent_at, now()),
		       finished_at = CASE WHEN $5 THEN now() ELSE finished_at END
		WHERE id = (SELECT id FROM collector_deployments
		            WHERE collector_id = $1 AND config_hash = $2 AND status IN ('pending', 'sent', 'applying')
		            ORDER BY created_at DESC LIMIT 1)`,
		collectorID, hash, string(status), MaskText(errMsg), finished)
	return err
}

// Deployments lists a collector's deployments, newest first.
func (s *Store) Deployments(ctx context.Context, collectorID uuid.UUID, limit int) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deploymentCols+` FROM collector_deployments
		WHERE collector_id = $1 ORDER BY created_at DESC LIMIT $2`, collectorID, limit)
	if err != nil {
		return nil, fmt.Errorf("collectors: deployments: %w", err)
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeploymentBody opens one deployment's configuration, unmasked, for a
// rollback that sends it again. Never returned to a caller.
func (s *Store) DeploymentBody(ctx context.Context, collectorID, id uuid.UUID) (Deployment, string, error) {
	if s.keys == nil {
		return Deployment{}, "", ErrNoKey
	}
	var sealed string
	var d Deployment
	err := s.pool.QueryRow(ctx, `SELECT `+deploymentCols+`, config_enc FROM collector_deployments
		WHERE collector_id = $1 AND id = $2`, collectorID, id).
		Scan(&d.ID, &d.CollectorID, &d.Hash, &d.Status, &d.ErrorMessage, &d.Message, &d.CreatedByName, &d.Via,
			&d.ProposalID, &d.RolledBackFrom, &d.CreatedAt, &d.SentAt, &d.FinishedAt, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, "", ErrNotFound
	}
	if err != nil {
		return Deployment{}, "", err
	}
	files, err := s.keys.Open(collectorID, d.Hash, sealed)
	if err != nil || len(files) != 1 {
		return Deployment{}, "", fmt.Errorf("collectors: open deployment: %w", err)
	}
	return d, files[0].Body, nil
}

// LastWorking is the newest applied deployment before the given one: what
// a rollback goes back to. ErrNotFound when there is none.
func (s *Store) LastWorking(ctx context.Context, collectorID, before uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM collector_deployments
		WHERE collector_id = $1 AND status = 'applied'
		  AND created_at < (SELECT created_at FROM collector_deployments WHERE id = $2)
		ORDER BY created_at DESC LIMIT 1`, collectorID, before).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

// LatestRaw is the newest stored configuration as written ("raw", or the
// effective one when no raw was reported), unmasked, for restoring the
// secrets of an edited copy. Never returned to a caller.
func (s *Store) LatestRaw(ctx context.Context, collectorID uuid.UUID) (string, error) {
	if s.keys == nil {
		return "", ErrNoKey
	}
	files, err := s.latestFiles(ctx, collectorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	var effective string
	for _, f := range files {
		if f.Name == RawConfigFile {
			return f.Body, nil
		}
		if f.Name == "" {
			effective = f.Body
		}
	}
	return effective, nil
}

// RequireApproval says whether the team's configuration changes must be
// approved before they are sent.
func (s *Store) RequireApproval(ctx context.Context, groupID uuid.UUID) (bool, error) {
	var v bool
	err := s.pool.QueryRow(ctx, `SELECT require_approval FROM collector_team_settings WHERE group_id = $1`, groupID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return v, err
}

// SetRequireApproval stores the team's setting.
func (s *Store) SetRequireApproval(ctx context.Context, orgID, groupID uuid.UUID, v bool) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO collector_team_settings (group_id, require_approval, updated_at)
		SELECT g.id, $3, now() FROM groups g WHERE g.id = $2 AND g.org_id = $1
		ON CONFLICT (group_id) DO UPDATE SET require_approval = EXCLUDED.require_approval, updated_at = now()`, orgID, groupID, v)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
