// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// HTTPOnlineWindow is how long after its last poll an agent on plain HTTP
// still counts as online. A WebSocket agent is online while its
// connection is open; a polling one is never "connected" in between.
const HTTPOnlineWindow = 90 * time.Second

// Health is the agent's own report, as OpAMP's ComponentHealth.
type Health struct {
	Healthy    bool              `json:"healthy"`
	Status     string            `json:"status,omitempty"`
	LastError  string            `json:"last_error,omitempty"`
	StartedAt  *time.Time        `json:"started_at,omitempty"`
	Components map[string]Health `json:"components,omitempty"`
}

// Observation is what one AgentToServer message tells the registry. OpAMP
// agents send only what changed, so a nil field means "not reported this
// time" and leaves the stored value alone.
type Observation struct {
	OrgID        uuid.UUID
	GroupID      uuid.UUID
	TokenID      uuid.UUID
	InstanceUID  string
	Transport    string
	RemoteAddr   string
	Capabilities *uint64
	Description  map[string]string
	Health       *Health
}

// Collector is one registered agent.
type Collector struct {
	ID           uuid.UUID         `json:"id"`
	GroupID      uuid.UUID         `json:"group_id"`
	GroupName    string            `json:"group_name"`
	InstanceUID  string            `json:"instance_uid"`
	Kind         Kind              `json:"kind"`
	Capabilities []string          `json:"capabilities"`
	Description  map[string]string `json:"description"`
	Healthy      *bool             `json:"healthy"`
	Health       *Health           `json:"health,omitempty"`
	Online       bool              `json:"online"`
	Transport    string            `json:"transport"`
	RemoteAddr   string            `json:"remote_addr"`
	FirstSeenAt  time.Time         `json:"first_seen_at"`
	LastSeenAt   time.Time         `json:"last_seen_at"`
	// ConfigHash and ConfigReceivedAt describe the newest stored version;
	// empty when none is stored (not reported, or no key).
	ConfigHash       string     `json:"config_hash,omitempty"`
	ConfigReceivedAt *time.Time `json:"config_received_at,omitempty"`
}

// Observe records a message and returns the collector's id. created says
// whether this was the first message from this agent in this team, which
// is when the server asks for the full state.
func (s *Store) Observe(ctx context.Context, o Observation) (uuid.UUID, bool, error) {
	var caps *int64
	var kind *string
	if o.Capabilities != nil {
		c := int64(*o.Capabilities)
		k := string(DetectKind(*o.Capabilities))
		caps, kind = &c, &k
	}
	var desc, health []byte
	if o.Description != nil {
		desc, _ = json.Marshal(o.Description)
	}
	var healthy *bool
	if o.Health != nil {
		health, _ = json.Marshal(o.Health)
		healthy = &o.Health.Healthy
	}
	var id uuid.UUID
	var created bool
	err := s.pool.QueryRow(ctx, `
		INSERT INTO collectors (organization_id, group_id, instance_uid, capabilities, kind, description,
		                        healthy, health, connected, transport, remote_addr, last_token_id)
		VALUES ($1, $2, $3, COALESCE($4, 0), COALESCE($5, 'extension'), COALESCE($6::jsonb, '{}'::jsonb),
		        $7, $8::jsonb, true, $9, $10, $11)
		ON CONFLICT (group_id, instance_uid) DO UPDATE SET
		    capabilities  = COALESCE($4, collectors.capabilities),
		    kind          = COALESCE($5, collectors.kind),
		    description   = COALESCE($6::jsonb, collectors.description),
		    healthy       = CASE WHEN $8::jsonb IS NULL THEN collectors.healthy ELSE $7 END,
		    health        = COALESCE($8::jsonb, collectors.health),
		    connected     = true,
		    transport     = $9,
		    remote_addr   = $10,
		    last_token_id = $11,
		    last_seen_at  = now()
		RETURNING id, (xmax = 0)`,
		o.OrgID, o.GroupID, o.InstanceUID, caps, kind, nullJSON(desc), healthy, nullJSON(health),
		o.Transport, o.RemoteAddr, o.TokenID).Scan(&id, &created)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("collectors: observe: %w", err)
	}
	return id, created, nil
}

func nullJSON(b []byte) *string {
	if b == nil {
		return nil
	}
	s := string(b)
	return &s
}

// Disconnected marks a WebSocket agent's connection closed.
func (s *Store) Disconnected(ctx context.Context, groupID uuid.UUID, instanceUID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE collectors SET connected = false WHERE group_id = $1 AND instance_uid = $2`, groupID, instanceUID)
	return err
}

// ResetConnections clears every connected flag. cell-api runs as one
// replica and holds the connections in memory, so at start none is open.
func (s *Store) ResetConnections(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE collectors SET connected = false WHERE connected`)
	return err
}

// RecordConfig stores the configuration if it differs from the newest
// stored version. Without a key it stores nothing and reports ErrNoKey.
func (s *Store) RecordConfig(ctx context.Context, collectorID uuid.UUID, files []ConfigFile) (bool, error) {
	if s.keys == nil {
		return false, ErrNoKey
	}
	hash, sealed, err := s.keys.Seal(collectorID, files)
	if err != nil {
		return false, err
	}
	// Insert only when the newest stored hash differs, in one statement so
	// two messages racing on one collector cannot both decide "changed".
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO collector_configs (collector_id, source, config_hash, config_enc)
		SELECT $1, 'effective', $2, $3
		WHERE COALESCE((SELECT config_hash FROM collector_configs WHERE collector_id = $1
		                ORDER BY received_at DESC, id DESC LIMIT 1), '') <> $2`,
		collectorID, hash, sealed)
	if err != nil {
		return false, fmt.Errorf("collectors: record config: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Scope says which collectors a caller may read: every team's in the
// organization, or only the listed teams'. An empty Groups with All unset
// reads nothing (access fails closed).
type Scope struct {
	OrgID  uuid.UUID
	All    bool
	Groups []uuid.UUID
}

const collectorCols = `
	c.id, c.group_id, g.name, c.instance_uid, c.kind, c.capabilities, c.description, c.healthy, c.health,
	c.connected, c.transport, c.remote_addr, c.first_seen_at, c.last_seen_at, cfg.config_hash, cfg.received_at`

const collectorFrom = `
	FROM collectors c
	JOIN groups g ON g.id = c.group_id
	LEFT JOIN LATERAL (
	    SELECT config_hash, received_at FROM collector_configs
	    WHERE collector_id = c.id ORDER BY received_at DESC, id DESC LIMIT 1
	) cfg ON true`

func scanCollector(row pgx.Row, now time.Time) (Collector, error) {
	var c Collector
	var caps int64
	var desc, health []byte
	var connected bool
	var hash *string
	if err := row.Scan(&c.ID, &c.GroupID, &c.GroupName, &c.InstanceUID, &c.Kind, &caps, &desc, &c.Healthy,
		&health, &connected, &c.Transport, &c.RemoteAddr, &c.FirstSeenAt, &c.LastSeenAt, &hash, &c.ConfigReceivedAt); err != nil {
		return Collector{}, err
	}
	c.Capabilities = CapabilityNames(uint64(caps))
	c.Description = map[string]string{}
	_ = json.Unmarshal(desc, &c.Description)
	if health != nil {
		var h Health
		if json.Unmarshal(health, &h) == nil {
			c.Health = &h
		}
	}
	if hash != nil {
		c.ConfigHash = *hash
	}
	if c.Transport == "http" {
		c.Online = now.Sub(c.LastSeenAt) < HTTPOnlineWindow
	} else {
		c.Online = connected
	}
	return c, nil
}

// List returns the collectors the scope may read, team by team.
func (s *Store) List(ctx context.Context, scope Scope) ([]Collector, error) {
	if !scope.All && len(scope.Groups) == 0 {
		return []Collector{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+collectorCols+collectorFrom+`
		WHERE c.organization_id = $1 AND ($2 OR c.group_id = ANY($3))
		ORDER BY g.name, c.description->>'host.name', c.instance_uid`, scope.OrgID, scope.All, scope.Groups)
	if err != nil {
		return nil, fmt.Errorf("collectors: list: %w", err)
	}
	defer rows.Close()
	now := time.Now()
	out := []Collector{}
	for rows.Next() {
		c, err := scanCollector(rows, now)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get returns one collector if the scope may read it.
func (s *Store) Get(ctx context.Context, scope Scope, id uuid.UUID) (Collector, error) {
	if !scope.All && len(scope.Groups) == 0 {
		return Collector{}, ErrNotFound
	}
	c, err := scanCollector(s.pool.QueryRow(ctx, `SELECT `+collectorCols+collectorFrom+`
		WHERE c.id = $4 AND c.organization_id = $1 AND ($2 OR c.group_id = ANY($3))`,
		scope.OrgID, scope.All, scope.Groups, id), time.Now())
	if errors.Is(err, pgx.ErrNoRows) {
		return Collector{}, ErrNotFound
	}
	return c, err
}

// ConfigVersion is one stored configuration, newest first in a history.
type ConfigVersion struct {
	ID         uuid.UUID `json:"id"`
	Source     string    `json:"source"`
	Hash       string    `json:"hash"`
	ReceivedAt time.Time `json:"received_at"`
}

// History lists a collector's stored versions without opening them.
// Callers check access with Get first.
func (s *Store) History(ctx context.Context, collectorID uuid.UUID, limit int) ([]ConfigVersion, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, source, config_hash, received_at FROM collector_configs
		WHERE collector_id = $1 ORDER BY received_at DESC, id DESC LIMIT $2`, collectorID, limit)
	if err != nil {
		return nil, fmt.Errorf("collectors: history: %w", err)
	}
	defer rows.Close()
	out := []ConfigVersion{}
	for rows.Next() {
		var v ConfigVersion
		if err := rows.Scan(&v.ID, &v.Source, &v.Hash, &v.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// MaskedFile is a configuration file as shown: secrets masked, or hidden
// altogether when it cannot be parsed to mask it.
type MaskedFile struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Body        string `json:"body"`
	Hidden      bool   `json:"hidden,omitempty"`
}

// MaskedConfig opens one stored version and masks it. versionID nil means
// the newest. This is the only way a stored configuration leaves the
// store; there is no unmasked read.
func (s *Store) MaskedConfig(ctx context.Context, collectorID uuid.UUID, versionID *uuid.UUID) (ConfigVersion, []MaskedFile, error) {
	if s.keys == nil {
		return ConfigVersion{}, nil, ErrNoKey
	}
	var v ConfigVersion
	var sealed string
	err := s.pool.QueryRow(ctx, `
		SELECT id, source, config_hash, received_at, config_enc FROM collector_configs
		WHERE collector_id = $1 AND ($2::uuid IS NULL OR id = $2)
		ORDER BY received_at DESC, id DESC LIMIT 1`, collectorID, versionID).
		Scan(&v.ID, &v.Source, &v.Hash, &v.ReceivedAt, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConfigVersion{}, nil, ErrNotFound
	}
	if err != nil {
		return ConfigVersion{}, nil, fmt.Errorf("collectors: config: %w", err)
	}
	files, err := s.keys.Open(collectorID, v.Hash, sealed)
	if err != nil {
		return ConfigVersion{}, nil, err
	}
	out := make([]MaskedFile, 0, len(files))
	for _, f := range files {
		body, ok := MaskConfig(f.Body)
		out = append(out, MaskedFile{Name: f.Name, ContentType: f.ContentType, Body: body, Hidden: !ok})
	}
	return v, out, nil
}

// CountForGroup says how many collectors a team owns; a team that owns any
// cannot be deleted.
func (s *Store) CountForGroup(ctx context.Context, groupID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM collectors WHERE group_id = $1`, groupID).Scan(&n)
	return n, err
}
