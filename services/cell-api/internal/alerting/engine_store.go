// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Instance/job persistence used by the evaluator + delivery worker. The
// state lives in alert_instances (one open instance per rule in v1) and
// notification_jobs (a durable, retried outbound queue).

// AlertInstance is one open/closed occurrence of a rule breaching.
type AlertInstance struct {
	ID              uuid.UUID
	AlertRuleID     uuid.UUID
	State           string
	StartedAt       time.Time
	LastEvaluatedAt time.Time
	Fingerprint     string
	Summary         string
	Labels          map[string]string
	// HandledAt is set once a user acknowledges the instance ("being
	// worked on"). While set, the engine stops sending notifications for
	// this instance — the operator is already on it.
	HandledAt *time.Time
	// SuppressedBy is the maintenance window that muted this instance's
	// firing notification. Muted at birth ⇒ its resolve stays silent too
	// (nobody was told it fired). Instances that fired *before* a window
	// keep nil and resolve loudly as usual.
	SuppressedBy *uuid.UUID
}

// ActiveInstance returns the rule's currently-firing instance, or nil.
func (s *Store) ActiveInstance(ctx context.Context, ruleID uuid.UUID) (*AlertInstance, error) {
	const q = `
		SELECT id, alert_rule_id, state, started_at, last_evaluated_at, fingerprint, COALESCE(summary, ''), handled_at, suppressed_by
		FROM alert_instances
		WHERE alert_rule_id = $1 AND state = 'firing'
		ORDER BY started_at DESC
		LIMIT 1`
	var a AlertInstance
	err := s.pool.QueryRow(ctx, q, ruleID).Scan(
		&a.ID, &a.AlertRuleID, &a.State, &a.StartedAt, &a.LastEvaluatedAt, &a.Fingerprint, &a.Summary, &a.HandledAt, &a.SuppressedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("active instance: %w", err)
	}
	return &a, nil
}

// ActiveInstanceByFingerprint returns the currently-firing instance
// for one rule whose fingerprint matches, or nil. Used by
// trace-completion evaluation where one rule fans out across many
// concurrent traces (fingerprint = trace_id) and a per-rule lookup
// would just return the first one. Metric alerts that only ever
// have one fingerprint per rule should keep using ActiveInstance.
func (s *Store) ActiveInstanceByFingerprint(ctx context.Context, ruleID uuid.UUID, fingerprint string) (*AlertInstance, error) {
	const q = `
		SELECT id, alert_rule_id, state, started_at, last_evaluated_at, fingerprint, COALESCE(summary, ''), handled_at, suppressed_by
		FROM alert_instances
		WHERE alert_rule_id = $1 AND fingerprint = $2 AND state = 'firing'
		ORDER BY started_at DESC
		LIMIT 1`
	var a AlertInstance
	err := s.pool.QueryRow(ctx, q, ruleID, fingerprint).Scan(
		&a.ID, &a.AlertRuleID, &a.State, &a.StartedAt, &a.LastEvaluatedAt, &a.Fingerprint, &a.Summary, &a.HandledAt, &a.SuppressedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("active instance by fingerprint: %w", err)
	}
	return &a, nil
}

// AcknowledgeInstance marks a firing instance as being worked on (sets
// handled_at). Org-scoped via the rule join so a caller can't ack another
// org's instance. No-op if the instance isn't firing or isn't theirs.
// InstanceServiceName returns the bound service of the rule behind one
// alert instance ("" when the rule isn't service-scoped). Backs the
// scoped-manage gate on ack/resolve (RBAC v2 §5.2).
func (s *Store) InstanceServiceName(ctx context.Context, orgID, instanceID uuid.UUID) (string, error) {
	var name string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(r.service_name, '')
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE i.id = $2 AND r.organization_id = $1`, orgID, instanceID).Scan(&name)
	if err != nil {
		return "", err
	}
	return name, nil
}

func (s *Store) AcknowledgeInstance(ctx context.Context, orgID, instanceID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE alert_instances i
		SET handled_at = now()
		FROM alert_rules r
		WHERE i.alert_rule_id = r.id
		  AND i.id = $2 AND r.organization_id = $1
		  AND i.state = 'firing' AND i.handled_at IS NULL`, orgID, instanceID)
	if err != nil {
		return fmt.Errorf("acknowledge instance: %w", err)
	}
	return nil
}

// ResolveInstanceManual closes an instance on user request (state ->
// resolved), stamping handled_at too so the engine treats it as handled
// and won't re-notify while the underlying condition persists. Org-scoped.
func (s *Store) ResolveInstanceManual(ctx context.Context, orgID, instanceID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE alert_instances i
		SET state = 'resolved', ended_at = now(), last_evaluated_at = now(),
		    handled_at = COALESCE(i.handled_at, now())
		FROM alert_rules r
		WHERE i.alert_rule_id = r.id
		  AND i.id = $2 AND r.organization_id = $1
		  AND i.state = 'firing'`, orgID, instanceID)
	if err != nil {
		return fmt.Errorf("resolve instance: %w", err)
	}
	return nil
}

// ResolveErrorTraceInstancesForService closes any firing failed-trace
// (trace_error) health-check instances bound to a single service. It's the
// hook behind "Clear errors": clearing a service's errors should clear its
// error-trace health check too, the same way it clears the built-in
// open-error signal and the window error count — otherwise a sticky
// (manual-resolve) check would keep the service red after the operator has
// reviewed the failures. handled_at is stamped so the engine treats them as
// handled and won't re-notify. Org-scoped via the rule join. Returns the
// number of instances closed.
func (s *Store) ResolveErrorTraceInstancesForService(ctx context.Context, orgID uuid.UUID, service string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE alert_instances i
		SET state = 'resolved', ended_at = now(), last_evaluated_at = now(),
		    handled_at = COALESCE(i.handled_at, now())
		FROM alert_rules r
		WHERE i.alert_rule_id = r.id
		  AND r.organization_id = $1
		  AND r.service_name = $2
		  AND r.signal = 'trace'
		  AND r.rule_spec->>'kind' = '`+TraceErrorSpecKind+`'
		  AND i.state = 'firing'`, orgID, service)
	if err != nil {
		return 0, fmt.Errorf("resolve error-trace instances for service: %w", err)
	}
	return tag.RowsAffected(), nil
}

// OpenInstance records a new firing instance for a rule.
func (s *Store) OpenInstance(ctx context.Context, ruleID uuid.UUID, fingerprint string, labels map[string]string, summary string) (AlertInstance, error) {
	lbl, _ := json.Marshal(labels)
	const q = `
		INSERT INTO alert_instances (alert_rule_id, state, fingerprint, labels, summary)
		VALUES ($1, 'firing', $2, $3, $4)
		RETURNING id, alert_rule_id, state, started_at, last_evaluated_at, fingerprint, COALESCE(summary, '')`
	var a AlertInstance
	err := s.pool.QueryRow(ctx, q, ruleID, fingerprint, lbl, summary).Scan(
		&a.ID, &a.AlertRuleID, &a.State, &a.StartedAt, &a.LastEvaluatedAt, &a.Fingerprint, &a.Summary)
	if err != nil {
		return AlertInstance{}, fmt.Errorf("open instance: %w", err)
	}
	a.Labels = labels
	return a, nil
}

// TouchInstance bumps last_evaluated_at on a still-firing instance.
func (s *Store) TouchInstance(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE alert_instances SET last_evaluated_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("touch instance: %w", err)
	}
	return nil
}

// RefreshInstance bumps last_evaluated_at and rewrites the labels +
// summary of a still-firing instance. Used by split-by metric rules,
// whose breakdown (which values are breaching, and by how much) changes
// between evaluations while the instance stays open.
func (s *Store) RefreshInstance(ctx context.Context, id uuid.UUID, labels map[string]string, summary string) error {
	lbl, _ := json.Marshal(labels)
	_, err := s.pool.Exec(ctx,
		`UPDATE alert_instances SET last_evaluated_at = now(), labels = $2, summary = $3 WHERE id = $1`,
		id, lbl, summary)
	if err != nil {
		return fmt.Errorf("refresh instance: %w", err)
	}
	return nil
}

// ResolveInstance closes a firing instance.
func (s *Store) ResolveInstance(ctx context.Context, id uuid.UUID, summary string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE alert_instances SET state = 'resolved', ended_at = now(), last_evaluated_at = now(), summary = $2 WHERE id = $1`,
		id, summary)
	if err != nil {
		return fmt.Errorf("resolve instance: %w", err)
	}
	return nil
}

// EnqueueJobs adds one pending delivery job per channel for an instance.
func (s *Store) EnqueueJobs(ctx context.Context, instanceID uuid.UUID, channelIDs []uuid.UUID) error {
	for _, ch := range dedupe(channelIDs) {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO notification_jobs (alert_instance_id, channel_id) VALUES ($1, $2)`, instanceID, ch); err != nil {
			return fmt.Errorf("enqueue job: %w", err)
		}
	}
	return nil
}

// MarkInstanceNotified stamps last_notified_at on an instance, recording
// that a firing notification just went out. Drives the re-notify interval
// and per-integration coalescing in the engine's renotify loop.
func (s *Store) MarkInstanceNotified(ctx context.Context, instanceID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE alert_instances SET last_notified_at = now() WHERE id = $1`, instanceID)
	if err != nil {
		return fmt.Errorf("mark instance notified: %w", err)
	}
	return nil
}

// FiringUnackedInstance is one open, unacknowledged alert the renotify
// loop considers for re-notification / per-integration coalescing, plus
// the rule id needed to resolve its channels + delivery behaviour.
type FiringUnackedInstance struct {
	InstanceID     uuid.UUID
	RuleID         uuid.UUID
	StartedAt      time.Time
	LastNotifiedAt *time.Time
	// SuppressedBy set = this instance fired inside a maintenance window
	// and its first page never went out. Once no window covers the rule
	// anymore, the renotify loop sends that overdue first page.
	SuppressedBy *uuid.UUID
}

// FiringUnackedInstances returns every firing, unacknowledged instance in
// the org. The renotify loop drives recurrence (re-send on the profile's
// interval) and per-integration coalescing off this set; acknowledged
// instances are excluded because an operator is already on them.
func (s *Store) FiringUnackedInstances(ctx context.Context, orgID uuid.UUID) ([]FiringUnackedInstance, error) {
	const q = `
		SELECT i.id, i.alert_rule_id, i.started_at, i.last_notified_at, i.suppressed_by
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1 AND i.state = 'firing' AND i.handled_at IS NULL`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("firing unacked instances: %w", err)
	}
	defer rows.Close()
	var out []FiringUnackedInstance
	for rows.Next() {
		var fi FiringUnackedInstance
		if err := rows.Scan(&fi.InstanceID, &fi.RuleID, &fi.StartedAt, &fi.LastNotifiedAt, &fi.SuppressedBy); err != nil {
			return nil, err
		}
		out = append(out, fi)
	}
	return out, rows.Err()
}

// ── maintenance suppression ──────────────────────────────────────────

// MaintenanceWindow is the engine's minimal read model of an active
// window: just enough to decide whether a rule's delivery is muted.
// ServiceNames carries both the explicit names and the write-time system
// expansion (see the maintenance package).
type MaintenanceWindow struct {
	ID             uuid.UUID
	ScopeKind      string // all_org | entities | group
	IntegrationIDs map[uuid.UUID]struct{}
	ServiceNames   map[string]struct{}
	GroupID        *uuid.UUID
}

// Covers reports whether this window silences the given rule.
func (w MaintenanceWindow) Covers(rule AlertRule) bool {
	switch w.ScopeKind {
	case "all_org":
		return true
	case "group":
		return w.GroupID != nil && rule.GroupID != nil && *w.GroupID == *rule.GroupID
	case "entities":
		if rule.IntegrationID != nil {
			if _, ok := w.IntegrationIDs[*rule.IntegrationID]; ok {
				return true
			}
		}
		if rule.ServiceName != "" {
			if _, ok := w.ServiceNames[rule.ServiceName]; ok {
				return true
			}
		}
	}
	return false
}

// ActiveMaintenanceWindows returns the org's currently-active windows in
// the engine's read model. Called on a short cache TTL from the engine.
func (s *Store) ActiveMaintenanceWindows(ctx context.Context, orgID uuid.UUID) ([]MaintenanceWindow, error) {
	const q = `
		SELECT id, scope FROM maintenance_windows
		WHERE org_id = $1 AND starts_at <= now() AND ends_at > now()`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("active maintenance windows: %w", err)
	}
	defer rows.Close()
	var out []MaintenanceWindow
	for rows.Next() {
		var id uuid.UUID
		var scopeJSON []byte
		if err := rows.Scan(&id, &scopeJSON); err != nil {
			return nil, err
		}
		var scope struct {
			Kind                 string      `json:"kind"`
			IntegrationIDs       []uuid.UUID `json:"integration_ids"`
			ServiceNames         []string    `json:"service_names"`
			ServiceNamesExpanded []string    `json:"service_names_expanded"`
			GroupID              *uuid.UUID  `json:"group_id"`
		}
		if err := json.Unmarshal(scopeJSON, &scope); err != nil {
			// A window we can't parse must not silently mute alerts —
			// skip it (fail toward delivery).
			continue
		}
		w := MaintenanceWindow{
			ID:             id,
			ScopeKind:      scope.Kind,
			IntegrationIDs: map[uuid.UUID]struct{}{},
			ServiceNames:   map[string]struct{}{},
			GroupID:        scope.GroupID,
		}
		for _, iid := range scope.IntegrationIDs {
			w.IntegrationIDs[iid] = struct{}{}
		}
		for _, n := range scope.ServiceNames {
			w.ServiceNames[n] = struct{}{}
		}
		for _, n := range scope.ServiceNamesExpanded {
			w.ServiceNames[n] = struct{}{}
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// MarkInstanceSuppressed stamps the window that muted an instance's
// firing notification. First writer wins — the stamp is birth metadata.
func (s *Store) MarkInstanceSuppressed(ctx context.Context, instanceID, windowID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE alert_instances SET suppressed_by = $2
		WHERE id = $1 AND suppressed_by IS NULL`, instanceID, windowID)
	return err
}

// DeliveryJob is a claimed job plus the context needed to render and
// send the notification.
type DeliveryJob struct {
	JobID           uuid.UUID
	AlertInstanceID uuid.UUID // for building a deep link to the alert
	Attempts        int
	Channel         NotificationChannel
	State           string // instance state: "firing" | "resolved"
	Summary         string
	Labels          map[string]string
	// TitleTemplate + BodyTemplate are the owning rule's optional Go
	// text/template strings. Rendered against the firing context at
	// send time (messageFromJob); empty = fall back to Summary.
	TitleTemplate string
	BodyTemplate  string
	// Owning rule's signal + scope, read at claim time so the delivery
	// layer can point the deep link at the most useful destination — e.g.
	// a failed-trace rule links to the Errors page rather than /alerts.
	RuleSignal    string     // "metric" | "log" | "trace"
	RuleKind      string     // rule_spec->>'kind' for trace rules ("trace_error"|"trace_latency")
	IntegrationID *uuid.UUID // rule's integration scope, if any
	// RuleGroupID is the owning rule's team (nil = org-wide) — read at
	// claim time so the delivery worker resolves the message-template
	// ladder without re-fetching the rule (same pattern as Content).
	RuleGroupID *uuid.UUID
	// Content is the owning rule's notification config (which enrichment
	// blocks to include + optional inline email template). Read at claim time
	// so the worker renders without re-fetching the rule.
	Content NotificationContent
}

// ClaimDueJobs atomically claims up to `limit` pending jobs whose
// next_attempt_at has passed, flipping them to 'running' so a second
// worker won't pick them up (FOR UPDATE SKIP LOCKED). Delivery happens
// after the claim, outside the lock.
func (s *Store) ClaimDueJobs(ctx context.Context, limit int) ([]DeliveryJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT j.id, j.alert_instance_id, j.attempts,
		       c.id, c.organization_id, c.name, c.kind, c.config, c.created_at, c.updated_at,
		       i.state, COALESCE(i.summary, ''), i.labels,
		       COALESCE(r.title_template, ''), COALESCE(r.body_template, ''),
		       COALESCE(r.signal::text, ''), COALESCE(r.rule_spec->>'kind', ''), r.integration_id,
		       r.group_id, COALESCE(r.notification_config, '{}'::jsonb)
		FROM notification_jobs j
		JOIN notification_channels c ON c.id = j.channel_id
		JOIN alert_instances i ON i.id = j.alert_instance_id
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE j.state = 'pending' AND j.next_attempt_at <= now()
		ORDER BY j.next_attempt_at
		LIMIT $1
		FOR UPDATE OF j SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim jobs: %w", err)
	}

	var jobs []DeliveryJob
	var ids []uuid.UUID
	for rows.Next() {
		var (
			j   DeliveryJob
			cfg []byte
			lbl []byte
			nc  []byte
		)
		if err := rows.Scan(
			&j.JobID, &j.AlertInstanceID, &j.Attempts,
			&j.Channel.ID, &j.Channel.OrganizationID, &j.Channel.Name, &j.Channel.Kind, &cfg, &j.Channel.CreatedAt, &j.Channel.UpdatedAt,
			&j.State, &j.Summary, &lbl,
			&j.TitleTemplate, &j.BodyTemplate,
			&j.RuleSignal, &j.RuleKind, &j.IntegrationID, &j.RuleGroupID, &nc,
		); err != nil {
			rows.Close()
			return nil, err
		}
		j.Channel.Config = map[string]string{}
		if len(cfg) > 0 {
			_ = json.Unmarshal(cfg, &j.Channel.Config)
		}
		if len(nc) > 0 {
			_ = json.Unmarshal(nc, &j.Content)
		}
		j.Labels = map[string]string{}
		if len(lbl) > 0 {
			_ = json.Unmarshal(lbl, &j.Labels)
		}
		jobs = append(jobs, j)
		ids = append(ids, j.JobID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for _, id := range ids {
		if _, err := tx.Exec(ctx,
			`UPDATE notification_jobs SET state = 'running', updated_at = now() WHERE id = $1`, id); err != nil {
			return nil, fmt.Errorf("mark running: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}

// MarkJobSucceeded closes a job after a successful delivery, recording
// the rendered subject/body that was actually sent (for the
// delivery-history view).
func (s *Store) MarkJobSucceeded(ctx context.Context, jobID uuid.UUID, subject, body string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE notification_jobs
		 SET state = 'succeeded', attempts = attempts + 1, last_error = NULL,
		     sent_subject = $2, sent_body = $3, updated_at = now()
		 WHERE id = $1`,
		jobID, subject, body)
	return err
}

// MarkJobFailed records a failed attempt: re-queue with a backoff, or
// give up as 'failed' once attempts reach maxAttempts.
func (s *Store) MarkJobFailed(ctx context.Context, jobID uuid.UUID, attempts, maxAttempts int, errMsg string, backoff time.Duration) error {
	next := attempts + 1
	if next >= maxAttempts {
		_, err := s.pool.Exec(ctx,
			`UPDATE notification_jobs SET state = 'failed', attempts = $2, last_error = $3, updated_at = now() WHERE id = $1`,
			jobID, next, errMsg)
		return err
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE notification_jobs SET state = 'pending', attempts = $2, last_error = $3, next_attempt_at = now() + $4, updated_at = now() WHERE id = $1`,
		jobID, next, errMsg, backoff)
	return err
}

// RecentInstances returns the most recent alert instances in the org
// (firing first), joined to their rule name + severity — backs the
// Alerts page.
func (s *Store) RecentInstances(ctx context.Context, orgID uuid.UUID, limit int) ([]InstanceView, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT i.id, i.alert_rule_id, r.name, r.severity, i.state,
		       i.started_at, i.ended_at, COALESCE(i.summary, ''), i.handled_at, r.group_id,
		       COALESCE(r.service_name, ''), r.integration_id
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1
		ORDER BY (i.state = 'firing') DESC, i.started_at DESC
		LIMIT $2`
	rows, err := s.pool.Query(ctx, q, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("recent instances: %w", err)
	}
	defer rows.Close()
	out := []InstanceView{}
	for rows.Next() {
		var v InstanceView
		var ended *time.Time
		var grp uuid.NullUUID
		var intg uuid.NullUUID
		if err := rows.Scan(&v.ID, &v.AlertRuleID, &v.RuleName, &v.Severity, &v.State, &v.StartedAt, &ended, &v.Summary, &v.HandledAt, &grp, &v.ServiceName, &intg); err != nil {
			return nil, err
		}
		v.EndedAt = ended
		if grp.Valid {
			id := grp.UUID
			v.GroupID = &id
		}
		if intg.Valid {
			id := intg.UUID
			v.IntegrationID = &id
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// FiringInstance is a currently-firing alert instance joined to the
// target its rule is bound to (a service or an integration). Backs the
// Errors page "failing health checks" feed: each firing health check
// attributed to the entity it guards. ServiceName is "" and
// IntegrationID nil for an unbound (org-wide) rule.
type FiringInstance struct {
	ID            uuid.UUID  `json:"id"`
	RuleID        uuid.UUID  `json:"rule_id"`
	RuleName      string     `json:"rule_name"`
	Severity      Severity   `json:"severity"`
	StartedAt     time.Time  `json:"started_at"`
	HandledAt     *time.Time `json:"handled_at,omitempty"`
	Summary       string     `json:"summary,omitempty"`
	ServiceName   string     `json:"service_name,omitempty"`
	IntegrationID *uuid.UUID `json:"integration_id,omitempty"`
	// GroupID is the owning team of the rule (nil = org-wide). Drives the
	// same team access-control filter as rules/instances.
	GroupID *uuid.UUID `json:"group_id,omitempty"`
}

// FiringInstances returns every currently-firing alert instance in the
// org, joined to its rule's target + owning team, most-recently-started
// first. The caller applies the team access-control filter on GroupID.
func (s *Store) FiringInstances(ctx context.Context, orgID uuid.UUID) ([]FiringInstance, error) {
	const q = `
		SELECT i.id, r.id, r.name, r.severity, i.started_at, i.handled_at,
		       COALESCE(i.summary, ''), COALESCE(r.service_name, ''),
		       r.integration_id, r.group_id
		FROM alert_instances i
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE r.organization_id = $1 AND i.state = 'firing'
		ORDER BY i.started_at DESC`
	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("firing instances: %w", err)
	}
	defer rows.Close()
	out := []FiringInstance{}
	for rows.Next() {
		var (
			v    FiringInstance
			intg uuid.NullUUID
			grp  uuid.NullUUID
		)
		if err := rows.Scan(&v.ID, &v.RuleID, &v.RuleName, &v.Severity, &v.StartedAt, &v.HandledAt, &v.Summary, &v.ServiceName, &intg, &grp); err != nil {
			return nil, err
		}
		if intg.Valid {
			id := intg.UUID
			v.IntegrationID = &id
		}
		if grp.Valid {
			id := grp.UUID
			v.GroupID = &id
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// InstanceView is the read model for the Alerts page.
type InstanceView struct {
	ID          uuid.UUID  `json:"id"`
	AlertRuleID uuid.UUID  `json:"alert_rule_id"`
	RuleName    string     `json:"rule_name"`
	Severity    Severity   `json:"severity"`
	State       string     `json:"state"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	Summary     string     `json:"summary"`
	// HandledAt is set when a user has acknowledged the alert.
	HandledAt *time.Time `json:"handled_at,omitempty"`
	// GroupID is the owning team of the instance's rule (nil =
	// org-wide). Drives the same team access-control filter as rules.
	GroupID *uuid.UUID `json:"group_id,omitempty"`
	// ServiceName / IntegrationID are the rule's bound target. They drive
	// the telemetry-visibility filter (a service-scoped instance must not
	// leak to someone who can't see the service). "" / nil = unbound.
	ServiceName   string     `json:"service_name,omitempty"`
	IntegrationID *uuid.UUID `json:"integration_id,omitempty"`
}

// DeliveryView is one row of the "what's been sent" history: a
// notification job joined to its channel + the rule/instance it
// notified about. Backs the Alerts page "Sent" view.
type DeliveryView struct {
	JobID       uuid.UUID `json:"job_id"`
	ChannelName string    `json:"channel_name"`
	ChannelKind string    `json:"channel_kind"`
	RuleName    string    `json:"rule_name"`
	Severity    Severity  `json:"severity"`
	AlertState  string    `json:"alert_state"` // instance state: firing | resolved
	JobState    string    `json:"job_state"`   // pending | running | succeeded | failed
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"last_error,omitempty"`
	Subject     string    `json:"subject,omitempty"` // rendered, as sent
	Body        string    `json:"body,omitempty"`
	Summary     string    `json:"summary"` // instance summary (fallback context)
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"` // delivery / last-attempt time
	// GroupID is the owning team of the underlying rule (nil =
	// org-wide); drives the team access-control filter.
	GroupID *uuid.UUID `json:"group_id,omitempty"`
	// ServiceName / IntegrationID are the rule's bound target, for the
	// telemetry-visibility filter. "" / nil = unbound.
	ServiceName   string     `json:"service_name,omitempty"`
	IntegrationID *uuid.UUID `json:"integration_id,omitempty"`
}

// ListDeliveries returns recent notification jobs in the org,
// newest-first, joined to their channel + rule + instance. Backs the
// delivery-history view ("what alerts have been sent, where, and did
// it work"). The caller applies the team access-control filter on the
// returned GroupID.
// DeliveryFilter narrows the delivery history for the Sent-notifications
// view. Zero values mean "no filter" for that field.
type DeliveryFilter struct {
	From, To      time.Time // window on the delivery time (j.updated_at)
	Limit         int
	ServiceName   string    // rule bound to exactly this service
	IntegrationID uuid.UUID // rule bound to this integration
	SystemID      uuid.UUID // rule's service is a member of this system
	Name          string    // rule name contains (case-insensitive)
}

func (s *Store) ListDeliveries(ctx context.Context, orgID uuid.UUID, f DeliveryFilter) ([]DeliveryView, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Dynamic WHERE — $1 is always orgID; the limit is the final arg.
	args := []any{orgID}
	where := []string{"r.organization_id = $1"}
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if !f.From.IsZero() {
		add("j.updated_at >= $%d", f.From)
	}
	if !f.To.IsZero() {
		add("j.updated_at <= $%d", f.To)
	}
	if f.ServiceName != "" {
		add("r.service_name = $%d", f.ServiceName)
	}
	if f.IntegrationID != uuid.Nil {
		add("r.integration_id = $%d", f.IntegrationID)
	}
	if f.SystemID != uuid.Nil {
		// The rule's bound service is a member of the given system.
		add("r.service_name IN (SELECT service_name FROM services WHERE organization_id = $1 AND system_id = $%d)", f.SystemID)
	}
	if f.Name != "" {
		add("r.name ILIKE '%%' || $%d || '%%'", f.Name)
	}
	args = append(args, limit)
	q := `
		SELECT j.id, c.name, c.kind, r.name, r.severity,
		       i.state, j.state, j.attempts, COALESCE(j.last_error, ''),
		       COALESCE(j.sent_subject, ''), COALESCE(j.sent_body, ''), COALESCE(i.summary, ''),
		       j.created_at, j.updated_at, r.group_id,
		       COALESCE(r.service_name, ''), r.integration_id
		FROM notification_jobs j
		JOIN notification_channels c ON c.id = j.channel_id
		JOIN alert_instances i ON i.id = j.alert_instance_id
		JOIN alert_rules r ON r.id = i.alert_rule_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY j.updated_at DESC
		LIMIT $` + fmt.Sprintf("%d", len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()
	out := []DeliveryView{}
	for rows.Next() {
		var d DeliveryView
		var grp uuid.NullUUID
		var intg uuid.NullUUID
		if err := rows.Scan(
			&d.JobID, &d.ChannelName, &d.ChannelKind, &d.RuleName, &d.Severity,
			&d.AlertState, &d.JobState, &d.Attempts, &d.LastError,
			&d.Subject, &d.Body, &d.Summary,
			&d.CreatedAt, &d.UpdatedAt, &grp,
			&d.ServiceName, &intg,
		); err != nil {
			return nil, err
		}
		if grp.Valid {
			id := grp.UUID
			d.GroupID = &id
		}
		if intg.Valid {
			id := intg.UUID
			d.IntegrationID = &id
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
