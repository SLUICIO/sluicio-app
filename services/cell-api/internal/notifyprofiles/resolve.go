// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package notifyprofiles

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ResolveProfile returns the profile that governs an alert scoped to the
// given integration / owning team, most-specific-first: the integration's
// assigned profile, else the team's default, else the org-wide default.
// Returns ErrNotFound only if nothing (not even an org default) exists.
func (s *Store) ResolveProfile(ctx context.Context, orgID uuid.UUID, integrationID, groupID *uuid.UUID) (Profile, error) {
	if integrationID != nil {
		var pid uuid.NullUUID
		err := s.pool.QueryRow(ctx,
			`SELECT notification_profile_id FROM integrations WHERE id = $1 AND organization_id = $2`,
			*integrationID, orgID).Scan(&pid)
		if err == nil && pid.Valid {
			return s.Get(ctx, orgID, pid.UUID)
		}
	}
	if groupID != nil {
		if id, ok, err := s.defaultProfileID(ctx, orgID, groupID); err != nil {
			return Profile{}, err
		} else if ok {
			return s.Get(ctx, orgID, id)
		}
	}
	if id, ok, err := s.defaultProfileID(ctx, orgID, nil); err != nil {
		return Profile{}, err
	} else if ok {
		return s.Get(ctx, orgID, id)
	}
	return Profile{}, ErrNotFound
}

// Resolve returns the channels an alert should deliver to — implements the
// alerting.ChannelResolver contract. A rule's explicit channels win;
// otherwise the resolved profile's channels.
func (s *Store) Resolve(ctx context.Context, orgID uuid.UUID, explicit []uuid.UUID, integrationID, groupID *uuid.UUID) ([]uuid.UUID, error) {
	if len(explicit) > 0 {
		return explicit, nil
	}
	p, err := s.ResolveProfile(ctx, orgID, integrationID, groupID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p.ChannelIDs, nil
}

// ResolveBehavior returns the delivery behaviour (grouping mode +
// re-notify interval in minutes) of the profile governing an alert in the
// given scope. Defaults to per-check / no-recurrence when no profile
// resolves, so an unconfigured org still behaves sanely. The signature is
// plain values (not a notifyprofiles type) so the alerting engine can
// depend on it structurally without importing this package.
func (s *Store) ResolveBehavior(ctx context.Context, orgID uuid.UUID, integrationID, groupID *uuid.UUID) (string, int, error) {
	p, err := s.ResolveProfile(ctx, orgID, integrationID, groupID)
	if errors.Is(err, ErrNotFound) {
		return GroupingPerCheck, 0, nil
	}
	if err != nil {
		return GroupingPerCheck, 0, err
	}
	grouping := p.Grouping
	if grouping == "" {
		grouping = GroupingPerCheck
	}
	return grouping, p.RenotifyMinutes, nil
}

func (s *Store) defaultProfileID(ctx context.Context, orgID uuid.UUID, groupID *uuid.UUID) (uuid.UUID, bool, error) {
	var id uuid.UUID
	var err error
	if groupID == nil {
		err = s.pool.QueryRow(ctx,
			`SELECT id FROM notification_profiles WHERE organization_id = $1 AND group_id IS NULL AND is_default LIMIT 1`,
			orgID).Scan(&id)
	} else {
		err = s.pool.QueryRow(ctx,
			`SELECT id FROM notification_profiles WHERE organization_id = $1 AND group_id = $2 AND is_default LIMIT 1`,
			orgID, *groupID).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("notifyprofiles: default profile: %w", err)
	}
	return id, true, nil
}

// AssignIntegrationProfile sets (or clears, with nil) an integration's
// assigned profile.
func (s *Store) AssignIntegrationProfile(ctx context.Context, orgID, integrationID uuid.UUID, profileID *uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE integrations SET notification_profile_id = $3 WHERE id = $1 AND organization_id = $2`,
		integrationID, orgID, profileID)
	if err != nil {
		return fmt.Errorf("notifyprofiles: assign integration profile: %w", err)
	}
	return nil
}

// IntegrationProfileID returns an integration's assigned profile id (nil
// if it inherits).
func (s *Store) IntegrationProfileID(ctx context.Context, orgID, integrationID uuid.UUID) (*uuid.UUID, error) {
	var pid uuid.NullUUID
	err := s.pool.QueryRow(ctx,
		`SELECT notification_profile_id FROM integrations WHERE id = $1 AND organization_id = $2`,
		integrationID, orgID).Scan(&pid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if pid.Valid {
		id := pid.UUID
		return &id, nil
	}
	return nil, nil
}

// ── error-notify watermark (service_error_notifications) ────────────────

// ErrorNotifyState is the per-service "we've already notified" watermark.
type ErrorNotifyState struct {
	NotifiedAt  time.Time
	LastErrorAt time.Time
}

// NotifiedStates returns every service's notify watermark, keyed by name.
func (s *Store) NotifiedStates(ctx context.Context, orgID uuid.UUID) (map[string]ErrorNotifyState, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT service_name, notified_at, last_error_at FROM service_error_notifications WHERE organization_id = $1`, orgID)
	if err != nil {
		return nil, fmt.Errorf("notifyprofiles: notified states: %w", err)
	}
	defer rows.Close()
	out := map[string]ErrorNotifyState{}
	for rows.Next() {
		var svc string
		var st ErrorNotifyState
		if err := rows.Scan(&svc, &st.NotifiedAt, &st.LastErrorAt); err != nil {
			return nil, err
		}
		out[svc] = st
	}
	return out, rows.Err()
}

// MarkNotified records that we've sent for a service's open errors.
func (s *Store) MarkNotified(ctx context.Context, orgID uuid.UUID, service string, lastErrorAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO service_error_notifications (organization_id, service_name, notified_at, last_error_at)
		VALUES ($1, $2, now(), $3)
		ON CONFLICT (organization_id, service_name) DO UPDATE
		SET notified_at = now(), last_error_at = EXCLUDED.last_error_at`, orgID, service, lastErrorAt)
	if err != nil {
		return fmt.Errorf("notifyprofiles: mark notified: %w", err)
	}
	return nil
}
