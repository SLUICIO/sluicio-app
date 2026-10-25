// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Metric and log sources of an integration (#43).
//
// Membership answers which services carry an integration's messages. A
// SOURCE answers which telemetry is evidence about it: a broker's metrics
// where the queue is the integration's, a runtime's logs where the flow
// is. A source is never a member. It is not in the flow graph, the
// message counts or the one-trace gate; it feeds the integration's
// Metrics and Logs, its metric and log checks, and what a grant on the
// integration reaches of those signals.

package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Source signals and origin kinds.
const (
	SourceMetrics = "metrics"
	SourceLogs    = "logs"

	OriginService = "service"
	OriginSystem  = "system"
)

// MaxSources bounds an integration's sources; each is a clause on every
// metrics and logs read of the integration.
const MaxSources = 20

// Numeric operators a source condition takes beyond a matcher's: the
// attribute read as a number ("http.response.status_code" >= 500). A row
// whose attribute is not a number does not match. Not matcher operators:
// an integration's rules compare text, and these never reach the
// matcher_operator type.
const (
	SourceOpGt  Operator = "gt"
	SourceOpGte Operator = "gte"
	SourceOpLt  Operator = "lt"
	SourceOpLte Operator = "lte"
)

// IsNumeric reports whether op compares the attribute as a number.
func (op Operator) IsNumeric() bool {
	switch op {
	case SourceOpGt, SourceOpGte, SourceOpLt, SourceOpLte:
		return true
	}
	return false
}

// SourceCondition is one condition on a source's rows: a matcher's
// operators, plus the numeric ones.
type SourceCondition struct {
	Attribute string   `json:"attribute"`
	Operator  Operator `json:"operator"`
	Value     string   `json:"value"`
}

// Source is one metric or log source.
type Source struct {
	ID            uuid.UUID `json:"id"`
	IntegrationID uuid.UUID `json:"integration_id"`
	Signal        string    `json:"signal"`
	OriginKind    string    `json:"origin_kind"`
	// Origin is a service name, or a system id.
	Origin      string            `json:"origin"`
	Conditions  []SourceCondition `json:"conditions"`
	MetricNames []string          `json:"metric_names"`
	CreatedAt   time.Time         `json:"created_at"`
}

// Validate checks one source.
func (s Source) Validate() error {
	if s.Signal != SourceMetrics && s.Signal != SourceLogs {
		return errInvalid(`a source's signal is "metrics" or "logs"`)
	}
	switch s.OriginKind {
	case OriginService:
		if strings.TrimSpace(s.Origin) == "" || len(s.Origin) > 256 {
			return errInvalid("a source needs the service it reads, at most 256 characters")
		}
	case OriginSystem:
		if _, err := uuid.Parse(s.Origin); err != nil {
			return errInvalid("a system source names the system by id")
		}
	default:
		return errInvalid(`a source's origin is a "service" or a "system"`)
	}
	if len(s.Conditions) > 20 {
		return errInvalid("at most 20 conditions per source")
	}
	for _, c := range s.Conditions {
		if strings.TrimSpace(c.Attribute) == "" {
			return errInvalid("a source condition needs an attribute")
		}
		if c.Attribute == "service.name" {
			return errInvalid("a source's service is its origin, not a condition")
		}
		if c.Operator.IsNumeric() {
			if _, err := strconv.ParseFloat(strings.TrimSpace(c.Value), 64); err != nil {
				return errInvalid("a numeric comparison needs a number, got " + strconv.Quote(c.Value))
			}
			continue
		}
		if err := (Matcher{Attribute: c.Attribute, Operator: c.Operator, Value: c.Value}).Validate(); err != nil {
			return err
		}
	}
	if len(s.MetricNames) > 0 && s.Signal != SourceMetrics {
		return errInvalid("metric names apply to a metric source only")
	}
	if len(s.MetricNames) > 20 {
		return errInvalid("at most 20 metric names per source")
	}
	for _, n := range s.MetricNames {
		if strings.TrimSpace(n) == "" || len(n) > 256 {
			return errInvalid("a metric name is 1 to 256 characters (* matches any run)")
		}
	}
	return nil
}

// SourcesForIntegration returns an integration's sources in order.
func (s *Store) SourcesForIntegration(ctx context.Context, integrationID uuid.UUID) ([]Source, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, integration_id, signal, origin_kind, origin, conditions, metric_names, created_at
		FROM integration_sources
		WHERE integration_id = $1
		ORDER BY position, created_at`, integrationID)
	if err != nil {
		return nil, fmt.Errorf("sources for integration: %w", err)
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var src Source
		var conds []byte
		if err := rows.Scan(&src.ID, &src.IntegrationID, &src.Signal, &src.OriginKind, &src.Origin, &conds, &src.MetricNames, &src.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(conds, &src.Conditions); err != nil || src.Conditions == nil {
			src.Conditions = []SourceCondition{}
		}
		if src.MetricNames == nil {
			src.MetricNames = []string{}
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// ReplaceSources replaces an integration's sources with the given ones,
// in that order. The integration must be in orgID.
func (s *Store) ReplaceSources(ctx context.Context, orgID, integrationID uuid.UUID, sources []Source) error {
	if len(sources) > MaxSources {
		return errInvalid(fmt.Sprintf("at most %d sources per integration", MaxSources))
	}
	for i := range sources {
		sources[i].Origin = strings.TrimSpace(sources[i].Origin)
		if err := sources[i].Validate(); err != nil {
			return err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM integrations WHERE id = $1 AND organization_id = $2)`,
		integrationID, orgID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM integration_sources WHERE integration_id = $1`, integrationID); err != nil {
		return err
	}
	for i, src := range sources {
		conds := src.Conditions
		if conds == nil {
			conds = []SourceCondition{}
		}
		raw, err := json.Marshal(conds)
		if err != nil {
			return err
		}
		names := src.MetricNames
		if names == nil {
			names = []string{}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO integration_sources (integration_id, signal, origin_kind, origin, conditions, metric_names, position)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)`,
			integrationID, src.Signal, src.OriginKind, src.Origin, raw, names, i); err != nil {
			return fmt.Errorf("insert source: %w", err)
		}
	}
	return tx.Commit(ctx)
}
