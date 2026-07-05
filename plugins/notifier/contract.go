// SPDX-License-Identifier: Apache-2.0
//
// Package notifier defines the contract that notification channel plugins
// implement. The contract is intentionally small and free of internal
// product types so third-party implementations can depend on this package
// without pulling in the rest of Integration Monitor.
//
// In v1 only built-in channels (email, webhook, AMQP, Kafka) are compiled
// into the binary. A future revision is expected to expose the same
// contract over gRPC using the HashiCorp go-plugin pattern, allowing
// external plugins as separate processes. The shape of the Notifier
// interface is designed for that future without committing to it yet.
package notifier

import (
	"context"
	"time"
)

// Severity is the alert severity level a notifier may use for routing
// or formatting decisions.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// State is the alert state at the moment the notification is dispatched.
type State string

const (
	StateFiring   State = "firing"
	StateResolved State = "resolved"
)

// Alert is the data a notifier receives. It is intentionally a plain
// data record without methods so it can be marshaled across a gRPC
// boundary later without behavior changes.
type Alert struct {
	// ID is a stable identifier for the alert instance.
	ID string

	// RuleID is the rule that fired this alert.
	RuleID string

	// RuleName is a human-readable name for the rule.
	RuleName string

	// IntegrationName is the integration the alert is scoped to, if any.
	IntegrationName string

	// Severity is the alert severity.
	Severity Severity

	// State is the current alert state at dispatch time.
	State State

	// StartedAt is when the alert entered the firing state.
	StartedAt time.Time

	// EndedAt is when the alert was resolved. Zero while firing.
	EndedAt time.Time

	// Summary is a short human-readable description.
	Summary string

	// Description is an optional longer description.
	Description string

	// Labels are key/value pairs attached to the alert (e.g. service,
	// host, queue name).
	Labels map[string]string

	// Annotations are key/value pairs for additional context that is not
	// part of the alert's identity (e.g. runbook URL, dashboard link).
	Annotations map[string]string

	// SourceURL is a deep-link back into the Integration Monitor UI
	// for the alert.
	SourceURL string
}

// Notifier is implemented by every notification channel.
//
// Implementations MUST be safe to call concurrently.
//
// Send is expected to be idempotent on (Alert.ID, Alert.State). The
// alerting engine may retry Send if it returns a transient error; the
// implementation should not produce duplicate user-visible notifications
// when retried with the same alert and state.
type Notifier interface {
	// Name returns a short identifier for this notifier kind, e.g.
	// "email", "webhook", "amqp", "kafka". This is the value used in
	// rule routing configuration.
	Name() string

	// Describe returns metadata for the UI (display name, description,
	// the JSON schema of its configuration).
	Describe() Descriptor

	// Validate checks the supplied configuration against the
	// notifier's schema and returns a list of validation errors. It
	// must not make network calls.
	Validate(config map[string]any) []ValidationError

	// Send delivers a single alert. It must return a non-nil error if
	// delivery failed and should be retried, and nil if the alert was
	// accepted by the destination (or rejected for a non-retryable
	// reason that has been logged).
	Send(ctx context.Context, config map[string]any, alert Alert) error
}

// Descriptor is the metadata a notifier exposes for the UI and config
// validation.
type Descriptor struct {
	Name        string
	DisplayName string
	Description string

	// ConfigSchema is a JSON Schema (draft 2020-12) describing the
	// configuration this notifier accepts.
	ConfigSchema []byte
}

// ValidationError describes a single config validation failure.
type ValidationError struct {
	Field   string
	Message string
}
