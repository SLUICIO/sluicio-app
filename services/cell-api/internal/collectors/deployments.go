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
	// DeploymentProposed waits for approval and is never delivered until
	// it is approved (Release turns it pending).
	DeploymentProposed DeploymentStatus = "proposed"
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
	// DeploymentHeld: a permanent change sent while a temporary one is
	// active. It is the base the collector returns to, delivered when the
	// temporary change ends.
	DeploymentHeld DeploymentStatus = "held"
	// DeploymentEnded: a temporary deployment whose time is up, or that
	// was ended early; the collector has been sent its base again.
	DeploymentEnded DeploymentStatus = "ended"
)

// ErrTemporaryActive: a temporary change is already active on the
// collector; end it before sending another.
var ErrTemporaryActive = errors.New("collectors: a temporary change is already active")

// Limits on a temporary change's duration.
const (
	MinTemporary = 5 * time.Minute
	MaxTemporary = 7 * 24 * time.Hour
)

// Deployment is one configuration sent, or to be sent, to a collector.
type Deployment struct {
	ID            uuid.UUID        `json:"id"`
	CollectorID   uuid.UUID        `json:"collector_id"`
	Hash          string           `json:"hash"`
	Status        DeploymentStatus `json:"status"`
	ErrorMessage  string           `json:"error_message,omitempty"`
	Message       string           `json:"message,omitempty"`
	CreatedBy     *uuid.UUID       `json:"-"`
	CreatedByName string           `json:"created_by_name,omitempty"`
	Via           string           `json:"via"`
	ProposalID    *uuid.UUID       `json:"proposal_id,omitempty"`
	// ProposalState is the state of the proposal a proposed deployment
	// waits on (pending, rejected, expired, superseded). A rejected or
	// expired proposal never touches the deployment, so without this a
	// change nobody approved would read as still waiting.
	ProposalState  string     `json:"proposal_state,omitempty"`
	RolledBackFrom *uuid.UUID `json:"rolled_back_from,omitempty"`
	// AdvisorSuggestionID, AdvisorClass and AdvisorTarget: the change came
	// from an Advisor suggestion and drops this (stage 3).
	AdvisorSuggestionID *uuid.UUID     `json:"advisor_suggestion_id,omitempty"`
	AdvisorClass        string         `json:"advisor_class,omitempty"`
	AdvisorTarget       *AdvisorTarget `json:"advisor_target,omitempty"`
	// DurationSeconds is set on a temporary deployment. ExpiresAt is set
	// once the collector applied it; EndedAt and EndedReason (expired,
	// ended) when it was reverted.
	DurationSeconds *int       `json:"duration_seconds,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	EndedReason     string     `json:"ended_reason,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	SentAt          *time.Time `json:"sent_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

// AdvisorTarget is what an Advisor change drops or changes: a metric
// (T1), one service's log records below a severity (T2), or an attribute
// on one service's spans (T3 and T5 delete it, T6 hashes its value).
type AdvisorTarget struct {
	Metric  string `json:"metric,omitempty"`
	Service string `json:"service,omitempty"`
	// Below is the OTTL severity the records are dropped below: INFO or
	// WARN.
	Below string `json:"below,omitempty"`
	// Attribute is a span attribute's key, or with Prefix set the start
	// of a family of keys.
	Attribute string `json:"attribute,omitempty"`
	Prefix    bool   `json:"prefix,omitempty"`
	// Hash: the value is hashed rather than the key deleted.
	Hash bool `json:"hash,omitempty"`
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
	// AdvisorSuggestionID, AdvisorClass and AdvisorTarget record an
	// Advisor change.
	AdvisorSuggestionID *uuid.UUID
	AdvisorClass        string
	AdvisorTarget       *AdvisorTarget
	// Duration makes it temporary: the collector is sent its base again
	// this long after it applied it. Zero is permanent.
	Duration time.Duration
	// Proposed stores it for approval instead of for delivery.
	Proposed bool
}

const deploymentCols = `id, collector_id, config_hash, status, error_message, message, created_by, created_by_name, via,
	proposal_id, rolled_back_from, advisor_suggestion_id, advisor_class, advisor_target,
	duration_seconds, expires_at, ended_at, ended_reason, created_at, sent_at, finished_at`

// dest is where a row of deploymentCols scans into, in that order, with
// any extra columns after.
func (d *Deployment) dest(extra ...any) []any {
	return append([]any{&d.ID, &d.CollectorID, &d.Hash, &d.Status, &d.ErrorMessage, &d.Message, &d.CreatedBy, &d.CreatedByName, &d.Via,
		&d.ProposalID, &d.RolledBackFrom, &d.AdvisorSuggestionID, &d.AdvisorClass, &d.AdvisorTarget,
		&d.DurationSeconds, &d.ExpiresAt, &d.EndedAt, &d.EndedReason, &d.CreatedAt, &d.SentAt, &d.FinishedAt}, extra...)
}

func scanDeployment(row pgx.Row) (Deployment, error) {
	var d Deployment
	err := row.Scan(d.dest()...)
	return d, err
}

func deploymentFiles(body string) []ConfigFile {
	return []ConfigFile{{Name: "", ContentType: "text/yaml", Body: body}}
}

// CreateDeployment stores a configuration to send. Anything still waiting
// for the same collector is superseded: only the newest is delivered.
// While a temporary change is active, a permanent one is held as the base
// the collector returns to, and another temporary one is refused
// (ErrTemporaryActive).
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
	// A proposal waits beside whatever is being delivered; a deployment
	// to deliver supersedes what has not applied yet, or is held behind a
	// temporary change.
	status := DeploymentProposed
	if !nd.Proposed {
		if status, err = queue(ctx, tx, nd.CollectorID, nd.Duration > 0); err != nil {
			return Deployment{}, err
		}
	}
	via := nd.Via
	if via == "" {
		via = "ui"
	}
	var duration *int
	if nd.Duration > 0 {
		v := int(nd.Duration / time.Second)
		duration = &v
	}
	d, err := scanDeployment(tx.QueryRow(ctx, `
		INSERT INTO collector_deployments (collector_id, config_hash, config_enc, message, created_by, created_by_name, via, proposal_id, rolled_back_from, status,
		                                   advisor_suggestion_id, advisor_class, advisor_target, duration_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING `+deploymentCols,
		nd.CollectorID, hash, sealed, nd.Message, nd.CreatedBy, nd.CreatedByName, via, nd.ProposalID, nd.RolledBackFrom, string(status),
		nd.AdvisorSuggestionID, nd.AdvisorClass, nd.AdvisorTarget, duration))
	if err != nil {
		return Deployment{}, fmt.Errorf("collectors: create deployment: %w", err)
	}
	return d, tx.Commit(ctx)
}

// queue makes room for a deployment about to be delivered and says what
// status it gets: pending, superseding what has not applied yet; held,
// when a temporary change is active and this one is permanent (an older
// held one is superseded, the newest base wins); or ErrTemporaryActive
// for a second temporary change.
func queue(ctx context.Context, tx pgx.Tx, collectorID uuid.UUID, temporary bool) (DeploymentStatus, error) {
	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM collector_deployments
		               WHERE collector_id = $1 AND duration_seconds IS NOT NULL
		                 AND status IN ('pending', 'sent', 'applying', 'applied')
		               FOR UPDATE)`, collectorID).Scan(&active); err != nil {
		return "", fmt.Errorf("collectors: active temporary: %w", err)
	}
	switch {
	case active && temporary:
		return "", ErrTemporaryActive
	case active:
		if _, err := tx.Exec(ctx, `UPDATE collector_deployments SET status = 'superseded', finished_at = now()
			WHERE collector_id = $1 AND status = 'held'`, collectorID); err != nil {
			return "", fmt.Errorf("collectors: supersede held: %w", err)
		}
		return DeploymentHeld, nil
	}
	return DeploymentPending, supersede(ctx, tx, collectorID)
}

func supersede(ctx context.Context, tx pgx.Tx, collectorID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE collector_deployments SET status = 'superseded', finished_at = now()
		WHERE collector_id = $1 AND status IN ('pending', 'sent', 'applying', 'held')`, collectorID); err != nil {
		return fmt.Errorf("collectors: supersede: %w", err)
	}
	return nil
}

// LinkProposal records the proposal a proposed deployment waits on.
func (s *Store) LinkProposal(ctx context.Context, id, proposalID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE collector_deployments SET proposal_id = $2 WHERE id = $1`, id, proposalID)
	return err
}

// Release turns an approved proposed deployment into one to deliver,
// superseding whatever had not applied yet. ErrNotFound when it is not
// (or no longer) waiting for approval.
func (s *Store) Release(ctx context.Context, collectorID, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var temporary bool
	err = tx.QueryRow(ctx, `SELECT duration_seconds IS NOT NULL FROM collector_deployments
		WHERE id = $1 AND collector_id = $2 AND status = 'proposed' FOR UPDATE`, id, collectorID).Scan(&temporary)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	status, err := queue(ctx, tx, collectorID, temporary)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE collector_deployments SET status = $3 WHERE id = $1 AND collector_id = $2`, id, collectorID, string(status)); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
	err := row.Scan(d.dest(&sealed)...)
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
	// A temporary deployment's time starts when the collector applied it.
	_, err := s.pool.Exec(ctx, `
		UPDATE collector_deployments SET status = $3, error_message = $4,
		       sent_at = COALESCE(sent_at, now()),
		       finished_at = CASE WHEN $5 THEN now() ELSE finished_at END,
		       expires_at = CASE WHEN $3 = 'applied' AND duration_seconds IS NOT NULL
		                         THEN now() + make_interval(secs => duration_seconds) ELSE expires_at END
		WHERE id = (SELECT id FROM collector_deployments
		            WHERE collector_id = $1 AND config_hash = $2 AND status IN ('pending', 'sent', 'applying')
		            ORDER BY created_at DESC LIMIT 1)`,
		collectorID, hash, string(status), MaskText(errMsg), finished)
	return err
}

// Deployments lists a collector's deployments, newest first.
func (s *Store) Deployments(ctx context.Context, collectorID uuid.UUID, limit int) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deploymentCols+`,
		       COALESCE((SELECT p.state FROM proposals p WHERE p.id = collector_deployments.proposal_id), '')
		FROM collector_deployments
		WHERE collector_id = $1 ORDER BY created_at DESC LIMIT $2`, collectorID, limit)
	if err != nil {
		return nil, fmt.Errorf("collectors: deployments: %w", err)
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		var d Deployment
		if err := rows.Scan(d.dest(&d.ProposalState)...); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDeployment is one deployment of a collector, without its body.
func (s *Store) GetDeployment(ctx context.Context, collectorID, id uuid.UUID) (Deployment, error) {
	d, err := scanDeployment(s.pool.QueryRow(ctx, `SELECT `+deploymentCols+` FROM collector_deployments
		WHERE collector_id = $1 AND id = $2`, collectorID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	return d, err
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
		Scan(d.dest(&sealed)...)
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

// LatestRawOnly is the newest configuration as written, unmasked, and
// ErrNotFound when the collector reports only the effective one. A change
// Sluicio writes into a configuration starts from what was written: the
// effective configuration leaves fields out (a receiver's scrapers), and
// sending it back would drop them.
func (s *Store) LatestRawOnly(ctx context.Context, collectorID uuid.UUID) (string, error) {
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
	for _, f := range files {
		if f.Name == RawConfigFile {
			return f.Body, nil
		}
	}
	return "", ErrNotFound
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
