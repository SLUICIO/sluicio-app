// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package settings is the cell-wide configuration store. Backed by the
// cell_settings(key, value JSONB, ...) Postgres table. One row per
// knob; the value column is JSONB so each setting's shape can evolve
// without a schema migration.
//
// Two consumers today:
//
//	1. The retention enforcer (internal/retention) reads the three
//	   telemetry.retention.* settings on a timer and pushes the
//	   resulting TTL into ClickHouse.
//	2. The cell-settings HTTP surface (api/handlers_cell_settings.go)
//	   exposes GET/PATCH for the same three keys to the org-admin UI.
//
// Settings are cell-wide, not per-org — see the migration comment for
// the data-model rationale.

package settings

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/secretcrypto"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed cell_settings reader/writer.
type Store struct {
	pool *pgxpool.Pool
	// secretKey is the 32-byte AES-GCM key used to encrypt replayable
	// credentials at rest (currently the SMTP relay password). Set at
	// startup via SetSecretKey; empty on a cell with no key configured, in
	// which case those values are stored/read as plaintext (legacy behavior).
	secretKey []byte
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// SetSecretKey installs the cell secret-encryption key (see Store.secretKey).
// Uses the same key as identity MFA encryption so there's one key to manage.
func (s *Store) SetSecretKey(key []byte) { s.secretKey = key }

// encryptSecret encrypts a replayable credential for storage. With no key
// configured it returns the value unchanged, matching pre-encryption behavior
// so a keyless (degraded) cell still functions.
func (s *Store) encryptSecret(plain string) (string, error) {
	if len(s.secretKey) != 32 {
		return plain, nil
	}
	return secretcrypto.Encrypt(s.secretKey, plain)
}

// decryptSecret reverses encryptSecret. Legacy plaintext (no ciphertext
// prefix) is returned as-is, so this is safe even before the key is set.
func (s *Store) decryptSecret(stored string) (string, error) {
	return secretcrypto.Decrypt(s.secretKey, stored)
}

// ── retention ────────────────────────────────────────────────────────

// TelemetryType is the closed enum of retention domains. Each maps to
// one ClickHouse table the enforcer drives via ALTER TABLE … MODIFY TTL.
type TelemetryType string

const (
	TelemetryTraces  TelemetryType = "traces"
	TelemetryLogs    TelemetryType = "logs"
	TelemetryMetrics TelemetryType = "metrics"
)

// AllTelemetryTypes is the iteration order for the enforcer and the
// API response. Kept in one place so both stay in sync.
var AllTelemetryTypes = []TelemetryType{TelemetryTraces, TelemetryLogs, TelemetryMetrics}

// RetentionDaysBounds is the inclusive range we accept for a retention
// setting. The floor of 1 day matches the granularity of ClickHouse's
// daily partitioning (TTL granularity below a day has no effect — the
// whole day's partition is the smallest drop unit). The ceiling of
// 1825 days (5 years) is a safety bound; pushing past that means
// retaining data ClickHouse will struggle to evict on a single cell.
const (
	RetentionMinDays = 1
	RetentionMaxDays = 1825
)

// RetentionSetting is what we persist per telemetry type. JSON-shaped
// (e.g. {"days": 14}) so future qualifiers like downsample-after-N-days
// can land without a schema change.
type RetentionSetting struct {
	Days int `json:"days"`
}

// RetentionPolicy is the full read of all three retention settings +
// when the enforcer last pushed each into ClickHouse. The
// LastAppliedAt map keys are the same TelemetryType strings used in
// the URL surface.
type RetentionPolicy struct {
	Traces  RetentionSetting
	Logs    RetentionSetting
	Metrics RetentionSetting
	// LastAppliedAt[type] is the time the enforcer most recently set
	// the TTL on the corresponding CH table from the value above. Zero
	// means "never applied yet" (fresh install, or applied via raw
	// ClickHouse before this code shipped).
	LastAppliedAt map[TelemetryType]time.Time
}

// GetRetention loads the full policy. If a row is missing the
// migration seeded it, but we tolerate absence anyway by falling back
// to the 14-day default so an out-of-sync deployment still works.
func (s *Store) GetRetention(ctx context.Context) (RetentionPolicy, error) {
	out := RetentionPolicy{
		Traces:        RetentionSetting{Days: 14},
		Logs:          RetentionSetting{Days: 14},
		Metrics:       RetentionSetting{Days: 14},
		LastAppliedAt: map[TelemetryType]time.Time{},
	}
	// One round-trip: pull all four keys we care about.
	const q = `
		SELECT key, value
		FROM cell_settings
		WHERE key IN (
			'telemetry.retention.traces',
			'telemetry.retention.logs',
			'telemetry.retention.metrics',
			'telemetry.retention.last_applied'
		)`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return out, fmt.Errorf("settings: get retention: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return out, err
		}
		switch key {
		case "telemetry.retention.traces":
			_ = json.Unmarshal(raw, &out.Traces)
		case "telemetry.retention.logs":
			_ = json.Unmarshal(raw, &out.Logs)
		case "telemetry.retention.metrics":
			_ = json.Unmarshal(raw, &out.Metrics)
		case "telemetry.retention.last_applied":
			// The shape here is map[string]time.Time but the keys are
			// strings, not TelemetryType. Decode permissively.
			var m map[string]time.Time
			if err := json.Unmarshal(raw, &m); err == nil {
				for k, v := range m {
					out.LastAppliedAt[TelemetryType(k)] = v
				}
			}
		}
	}
	return out, rows.Err()
}

// SetRetentionDays writes one telemetry type's retention. Validates
// the bounds before touching the DB. Returns ErrInvalidRetention if
// the value is outside [RetentionMinDays, RetentionMaxDays].
func (s *Store) SetRetentionDays(ctx context.Context, kind TelemetryType, days int) error {
	if !validKind(kind) {
		return fmt.Errorf("settings: invalid telemetry type %q", kind)
	}
	if days < RetentionMinDays || days > RetentionMaxDays {
		return ErrInvalidRetention
	}
	key := "telemetry.retention." + string(kind)
	raw, _ := json.Marshal(RetentionSetting{Days: days})
	// UPSERT so a fresh install (where the migration ran) updates,
	// and an environment that's been provisioned past the migration
	// still works.
	const q = `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, key, raw); err != nil {
		return fmt.Errorf("settings: set retention: %w", err)
	}
	return nil
}

// RecordRetentionApplied is the enforcer's write-back: after pushing
// the new TTL into ClickHouse, update the last_applied map so the API
// can show "applied at 12:34 today" in the UI.
func (s *Store) RecordRetentionApplied(ctx context.Context, kind TelemetryType, at time.Time) error {
	if !validKind(kind) {
		return fmt.Errorf("settings: invalid telemetry type %q", kind)
	}
	// We merge into the existing map atomically with jsonb_set. The
	// outer COALESCE handles the case where the row was deleted (the
	// migration always seeds it, but defensive coding).
	const q = `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ('telemetry.retention.last_applied',
		        jsonb_build_object($1::text, to_jsonb($2::timestamptz)),
		        now())
		ON CONFLICT (key) DO UPDATE
		SET value = COALESCE(cell_settings.value, '{}'::jsonb)
		            || jsonb_build_object($1::text, to_jsonb($2::timestamptz)),
		    updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, string(kind), at); err != nil {
		return fmt.Errorf("settings: record retention applied: %w", err)
	}
	return nil
}

// validKind keeps the input enum honest. We can't use Go's type system
// to constrain TelemetryType to AllTelemetryTypes (strings are open),
// so the API layer / store both check.
func validKind(k TelemetryType) bool {
	switch k {
	case TelemetryTraces, TelemetryLogs, TelemetryMetrics:
		return true
	}
	return false
}

// ── system: environment ──────────────────────────────────────────────
//
// The environment label shown in the top nav (e.g. "production",
// "staging"). It's a cell-wide system setting, not per-org: one Sluicio
// instance serves one org/environment, so the org admin is effectively
// the system admin and owns this knob (see issue #27).

// DefaultEnvironment is returned when the setting has never been set,
// preserving the historical hardcoded label.
const DefaultEnvironment = "production"

// EnvironmentMaxLen bounds the free-text label so the nav chip stays sane.
const EnvironmentMaxLen = 40

type environmentSetting struct {
	Environment string `json:"environment"`
}

// GetEnvironment returns the cell's environment label, or
// DefaultEnvironment if unset.
func (s *Store) GetEnvironment(ctx context.Context) (string, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM cell_settings WHERE key = 'system.environment'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultEnvironment, nil
	}
	if err != nil {
		return DefaultEnvironment, fmt.Errorf("settings: get environment: %w", err)
	}
	var v environmentSetting
	if err := json.Unmarshal(raw, &v); err != nil || v.Environment == "" {
		return DefaultEnvironment, nil
	}
	return v.Environment, nil
}

// SetEnvironment writes the cell's environment label. Rejects empty or
// over-long values with ErrInvalidEnvironment.
func (s *Store) SetEnvironment(ctx context.Context, env string) error {
	if env == "" || len(env) > EnvironmentMaxLen {
		return ErrInvalidEnvironment
	}
	raw, _ := json.Marshal(environmentSetting{Environment: env})
	const q = `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ('system.environment', $1::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, raw); err != nil {
		return fmt.Errorf("settings: set environment: %w", err)
	}
	return nil
}

// ── Alert email template ─────────────────────────────────────────────
//
// The org-default Liquid email template (subject + HTML body) for alert
// notifications, used when a rule doesn't carry its own inline override. Empty
// = the alerting package's built-in default.

type alertEmailTemplateSetting struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// GetAlertEmailTemplate returns the org default alert email subject + body, or
// two empty strings when unset.
func (s *Store) GetAlertEmailTemplate(ctx context.Context) (subject, body string, err error) {
	var raw []byte
	qerr := s.pool.QueryRow(ctx,
		`SELECT value FROM cell_settings WHERE key = 'system.alert_email_template'`).Scan(&raw)
	if errors.Is(qerr, pgx.ErrNoRows) {
		return "", "", nil
	}
	if qerr != nil {
		return "", "", fmt.Errorf("settings: get alert email template: %w", qerr)
	}
	var v alertEmailTemplateSetting
	if json.Unmarshal(raw, &v) != nil {
		return "", "", nil
	}
	return v.Subject, v.Body, nil
}

// SetAlertEmailTemplate stores the org default alert email subject + body
// (either may be empty to fall back to the built-in default).
func (s *Store) SetAlertEmailTemplate(ctx context.Context, subject, body string) error {
	raw, _ := json.Marshal(alertEmailTemplateSetting{Subject: subject, Body: body})
	const q = `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ('system.alert_email_template', $1::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, raw); err != nil {
		return fmt.Errorf("settings: set alert email template: %w", err)
	}
	return nil
}

// ── Ingest base URL ──────────────────────────────────────────────────
//
// The external OTLP/HTTP base URL of this cell's ingest endpoint
// (cell-ingest), as reachable by customers' collectors — e.g.
// "https://ingest.acme.example.com". cell-api can't know this itself (it
// sits behind a reverse proxy and only knows its internal addr), so an
// admin sets it once and the Ingest Keys UI bakes it into the ready-to-
// paste exporter config. When unset the UI falls back to the browser's
// origin, which is correct for single-host deployments.

// IngestBaseURLMaxLen bounds the stored URL.
const IngestBaseURLMaxLen = 200

type ingestBaseURLSetting struct {
	URL string `json:"url"`
}

// GetIngestBaseURL returns the configured ingest base URL, or "" if unset.
func (s *Store) GetIngestBaseURL(ctx context.Context) (string, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM cell_settings WHERE key = 'system.ingest_base_url'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("settings: get ingest base url: %w", err)
	}
	var v ingestBaseURLSetting
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", nil
	}
	return v.URL, nil
}

// SetIngestBaseURL stores the ingest base URL. An empty string clears it
// (the UI reverts to deriving the host from the browser). A non-empty
// value must be a plausible absolute http(s) URL; a trailing slash is
// trimmed so the OTLP SDK's "/v1/…" suffix joins cleanly.
func (s *Store) SetIngestBaseURL(ctx context.Context, raw string) error {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if u != "" {
		if len(u) > IngestBaseURLMaxLen ||
			!(strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
			return ErrInvalidIngestBaseURL
		}
	}
	val, _ := json.Marshal(ingestBaseURLSetting{URL: u})
	const q = `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ('system.ingest_base_url', $1::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, val); err != nil {
		return fmt.Errorf("settings: set ingest base url: %w", err)
	}
	return nil
}

// ── SMTP / transactional email ───────────────────────────────────────
//
// The global mail transport used for password resets (and future account
// email). Stored as one JSON row under 'system.smtp'. Env vars
// (SLUICIO_SMTP_*) act as defaults; non-empty values here override them.
// The password is stored as-is for the admin to manage; the HTTP layer
// masks it on read.

type SMTPSettings struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	FromName string `json:"from_name"`
}

// GetSMTP returns the stored SMTP settings, or a zero value if unset.
func (s *Store) GetSMTP(ctx context.Context) (SMTPSettings, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM cell_settings WHERE key = 'system.smtp'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return SMTPSettings{}, nil
	}
	if err != nil {
		return SMTPSettings{}, fmt.Errorf("settings: get smtp: %w", err)
	}
	var v SMTPSettings
	if err := json.Unmarshal(raw, &v); err != nil {
		return SMTPSettings{}, nil
	}
	// Password is stored encrypted (or legacy plaintext); callers — the
	// mailer and the masked HTTP response — expect the cleartext value.
	pw, err := s.decryptSecret(v.Password)
	if err != nil {
		return SMTPSettings{}, fmt.Errorf("settings: decrypt smtp password: %w", err)
	}
	v.Password = pw
	return v, nil
}

// SetSMTP writes the SMTP settings, encrypting the password at rest.
func (s *Store) SetSMTP(ctx context.Context, v SMTPSettings) error {
	enc, err := s.encryptSecret(v.Password)
	if err != nil {
		return fmt.Errorf("settings: encrypt smtp password: %w", err)
	}
	v.Password = enc
	raw, _ := json.Marshal(v)
	const q = `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ('system.smtp', $1::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, raw); err != nil {
		return fmt.Errorf("settings: set smtp: %w", err)
	}
	return nil
}

// EncryptSMTPSecretAtRest is the one-time migration that re-stores the SMTP
// password encrypted if it was written as plaintext before encryption-at-rest.
// Idempotent: an already-encrypted (or empty) password is left untouched.
// No-op when no key is configured.
func (s *Store) EncryptSMTPSecretAtRest(ctx context.Context) (bool, error) {
	if len(s.secretKey) != 32 {
		return false, nil
	}
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM cell_settings WHERE key = 'system.smtp'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("settings: migrate smtp: read: %w", err)
	}
	var v SMTPSettings
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, nil // unreadable row; leave it for the admin to re-save
	}
	if v.Password == "" || secretcrypto.IsEncrypted(v.Password) {
		return false, nil
	}
	enc, err := secretcrypto.Encrypt(s.secretKey, v.Password)
	if err != nil {
		return false, fmt.Errorf("settings: migrate smtp: encrypt: %w", err)
	}
	v.Password = enc
	next, _ := json.Marshal(v)
	if _, err := s.pool.Exec(ctx,
		`UPDATE cell_settings SET value = $1::jsonb, updated_at = now() WHERE key = 'system.smtp'`,
		next); err != nil {
		return false, fmt.Errorf("settings: migrate smtp: write: %w", err)
	}
	return true, nil
}

// ── MFA encryption key ────────────────────────────────────────────────

// GetOrCreateMFAKey returns the persisted 32-byte AES key used to encrypt
// TOTP secrets, generating + storing one on first use. This keeps MFA
// working out-of-box for self-hosters who don't set SLUICIO_MFA_KEY; for
// stronger key separation, set that env var (it takes precedence — the
// caller checks it first).
func (s *Store) GetOrCreateMFAKey(ctx context.Context) ([]byte, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM cell_settings WHERE key = 'system.mfa_key'`).Scan(&raw)
	if err == nil {
		var v struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(raw, &v) == nil {
			if b, err := base64.StdEncoding.DecodeString(v.Key); err == nil && len(b) == 32 {
				return b, nil
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("settings: get mfa key: %w", err)
	}
	// Generate + persist.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("settings: gen mfa key: %w", err)
	}
	enc, _ := json.Marshal(struct {
		Key string `json:"key"`
	}{base64.StdEncoding.EncodeToString(key)})
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ('system.mfa_key', $1::jsonb, now())
		ON CONFLICT (key) DO NOTHING`, enc); err != nil {
		return nil, fmt.Errorf("settings: store mfa key: %w", err)
	}
	// Re-read in case a concurrent boot won the INSERT.
	if err := s.pool.QueryRow(ctx, `SELECT value FROM cell_settings WHERE key = 'system.mfa_key'`).Scan(&raw); err == nil {
		var v struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(raw, &v) == nil {
			if b, err := base64.StdEncoding.DecodeString(v.Key); err == nil && len(b) == 32 {
				return b, nil
			}
		}
	}
	return key, nil
}

// ── security policy ───────────────────────────────────────────────────

// GetMFARequired reports whether org-wide MFA enforcement is on (every
// member must enrol). Defaults false.
func (s *Store) GetMFARequired(ctx context.Context) (bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM cell_settings WHERE key = 'security.mfa_required'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("settings: get mfa_required: %w", err)
	}
	var v struct {
		Required bool `json:"required"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Required, nil
}

// SetMFARequired toggles org-wide MFA enforcement.
func (s *Store) SetMFARequired(ctx context.Context, required bool) error {
	raw, _ := json.Marshal(struct {
		Required bool `json:"required"`
	}{required})
	const q = `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ('security.mfa_required', $1::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, raw); err != nil {
		return fmt.Errorf("settings: set mfa_required: %w", err)
	}
	return nil
}

// ── ingest normalization ──────────────────────────────────────────────

// GetMapHTTP5xxToError reports whether the cell normalizes span status at
// ingest: spans carrying http.response.status_code >= 500 but a non-Error
// span status are stored as Error spans (OTel semconv says servers SHOULD
// mark 5xx as Error; some emitters — API gateways notably — don't).
// Defaults false. cell-ingest reads the same key with its own cached
// reader; this accessor backs the settings API.
func (s *Store) GetMapHTTP5xxToError(ctx context.Context) (bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM cell_settings WHERE key = 'ingest.map_http_5xx_to_error'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("settings: get map_http_5xx_to_error: %w", err)
	}
	var v struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Enabled, nil
}

// SetMapHTTP5xxToError toggles 5xx→Error span-status normalization.
// Takes effect for newly ingested spans within the ingest cache TTL
// (~30s); already-stored spans keep their status.
func (s *Store) SetMapHTTP5xxToError(ctx context.Context, enabled bool) error {
	raw, _ := json.Marshal(struct {
		Enabled bool `json:"enabled"`
	}{enabled})
	const q = `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ('ingest.map_http_5xx_to_error', $1::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, raw); err != nil {
		return fmt.Errorf("settings: set map_http_5xx_to_error: %w", err)
	}
	return nil
}

// GetForbidOrgWideServiceAccounts reports whether this cell refuses
// org-wide service accounts (compliance posture — see
// docs/service-account-scoping-design.md). When enabled: creating or
// switching an SA to scope='org_wide' is rejected, and any existing
// org-wide SA resolves visibility as if scoped (group memberships
// only) — defense in depth for SAs created before the toggle.
// Defaults false.
func (s *Store) GetForbidOrgWideServiceAccounts(ctx context.Context) (bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM cell_settings WHERE key = 'rbac.forbid_org_wide_service_accounts'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("settings: get forbid_org_wide_service_accounts: %w", err)
	}
	var v struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Enabled, nil
}

// SetForbidOrgWideServiceAccounts toggles the org-wide-SA prohibition.
func (s *Store) SetForbidOrgWideServiceAccounts(ctx context.Context, enabled bool) error {
	raw, _ := json.Marshal(struct {
		Enabled bool `json:"enabled"`
	}{enabled})
	const q = `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ('rbac.forbid_org_wide_service_accounts', $1::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, raw); err != nil {
		return fmt.Errorf("settings: set forbid_org_wide_service_accounts: %w", err)
	}
	return nil
}

// ── audit retention ─────────────────────────────────────────────────
//
// How long audit_log rows are kept before the retention enforcer prunes
// them (chain-safely — see ee/audit Prune). Unlike telemetry retention
// this is a Postgres DELETE, not a ClickHouse TTL. The free default is
// two weeks; raising it is gated on the audit_log entitlement at the API
// layer. The ceiling is 10 years — audit rows are tiny, and compliance
// horizons run long.

const (
	AuditRetentionDefaultDays = 14
	AuditRetentionMinDays     = 1
	AuditRetentionMaxDays     = 3650
)

// GetAuditRetentionDays returns the configured audit retention, falling
// back to the default when the key was never set.
func (s *Store) GetAuditRetentionDays(ctx context.Context) (int, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM cell_settings WHERE key = 'audit.retention'`).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuditRetentionDefaultDays, nil
		}
		return AuditRetentionDefaultDays, fmt.Errorf("settings: get audit retention: %w", err)
	}
	var v RetentionSetting
	if err := json.Unmarshal(raw, &v); err != nil || v.Days == 0 {
		return AuditRetentionDefaultDays, nil
	}
	return v.Days, nil
}

// SetAuditRetentionDays persists the audit retention. Bounds-checked here;
// the entitlement gate (free cap vs Enterprise) lives at the API layer.
func (s *Store) SetAuditRetentionDays(ctx context.Context, days int) error {
	if days < AuditRetentionMinDays || days > AuditRetentionMaxDays {
		return ErrInvalidAuditRetention
	}
	raw, _ := json.Marshal(RetentionSetting{Days: days})
	const q = `
		INSERT INTO cell_settings (key, value, updated_at)
		VALUES ('audit.retention', $1::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, raw); err != nil {
		return fmt.Errorf("settings: set audit retention: %w", err)
	}
	return nil
}

// ── error sentinels ──────────────────────────────────────────────────

var (
	ErrInvalidRetention = errors.New("settings: retention days must be between 1 and 1825")
	// ErrInvalidAuditRetention bounds the audit-log retention knob.
	ErrInvalidAuditRetention = errors.New("settings: audit retention days must be between 1 and 3650")
	// ErrInvalidEnvironment is returned for an empty or over-long
	// environment label.
	ErrInvalidEnvironment = errors.New("settings: environment must be 1–40 characters")
	// ErrInvalidIngestBaseURL is returned for a non-empty ingest base URL
	// that isn't a plausible absolute http(s) URL (or is over-long).
	ErrInvalidIngestBaseURL = errors.New("settings: ingest base URL must be an http(s):// URL")
	// ErrNotFound surfaces from operations that look up a specific key
	// that doesn't exist. Unused today (the retention API uses defaults
	// on missing rows) but reserved for future per-key reads.
	ErrNotFound = pgx.ErrNoRows
)
