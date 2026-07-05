// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package license verifies Sluicio license keys and answers the single
// question the rest of the app asks: "is feature X entitled right now?".
//
// Verification is part of the OPEN CORE (FSL) and fully inspectable: it only
// needs the embedded *public* key. The matching private key and the minting
// tool stay proprietary (under ee/), so only Sluicio can issue valid
// licenses — but anyone can audit exactly how they're checked. Keys are
// offline, Ed25519-signed tokens verified locally, so gating works fully
// air-gapped with no phone-home.
//
// Design rules that must not regress:
//   - The core product runs with NO license. An absent, malformed, or expired
//     key disables EE features but never blocks login, admin, or core flows.
//   - Verification is offline and deterministic against the embedded public
//     key. The matching private key signs licenses and never ships.
package license

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// tokenPrefix tags our license strings so they're recognisable and so a
// stray value (e.g. a JWT) is rejected early.
const tokenPrefix = "sluicio_lic_"

// graceWindow keeps EE features working for a while after expiry, with a
// loud warning, so a lapsed renewal degrades gracefully instead of cutting
// a self-hosted customer off at midnight.
const graceWindow = 14 * 24 * time.Hour

// embeddedPublicKey is the base64 (std) encoding of the 32-byte Ed25519
// public key. The private counterpart signs licenses and is kept out of the
// repo entirely (see ee/cmd/sluicio-license).
//
//go:embed sluicio_license_ed25519.pub
var embeddedPublicKeyB64 string

// Feature is one entitlement gate. Add a constant here and reference it from
// the gated route/logic; never gate on a bare string.
type Feature string

const (
	FeatureSSO           Feature = "sso"
	FeatureRBACAdvanced  Feature = "rbac_advanced"
	FeatureAuditLog      Feature = "audit_log"
	FeatureRetentionLong Feature = "retention_long"
	// FeatureMFAPolicy gates org-wide MFA *enforcement* (requiring every
	// member to enrol). Per-user MFA itself is free/core — only the
	// compliance knob is Enterprise.
	FeatureMFAPolicy Feature = "mfa_policy"
)

// AllFeatures is the canonical list, used to render the features map in the
// status response so the frontend always sees every gate.
var AllFeatures = []Feature{FeatureSSO, FeatureRBACAdvanced, FeatureAuditLog, FeatureRetentionLong, FeatureMFAPolicy}

// Limits are optional numeric caps carried by a license. Zero means "no
// explicit limit from the license" (callers apply their own free-tier
// default). These ride inside the Ed25519-signed Claims, so they can't be
// tampered with without invalidating the signature.
type Limits struct {
	MaxRetentionDays int `json:"max_retention_days,omitempty"`
	// MaxIntegrations caps how many integrations a plan covers (Pro 25,
	// Business 75). Zero = unlimited (Enterprise, and unlicensed/Community).
	MaxIntegrations int `json:"max_integrations,omitempty"`
}

// Claims is the signed payload of a license token. Times are unix seconds.
// ExpiresAt == 0 means perpetual.
type Claims struct {
	LicenseID    string   `json:"license_id"`
	Customer     string   `json:"customer"`
	Plan         string   `json:"plan"`
	Entitlements []string `json:"entitlements"`
	Limits       Limits   `json:"limits,omitempty"`
	IssuedAt     int64    `json:"issued_at"`
	NotBefore    int64    `json:"not_before,omitempty"`
	ExpiresAt    int64    `json:"expires_at,omitempty"`
}

func (c *Claims) hasEntitlement(f Feature) bool {
	for _, e := range c.Entitlements {
		if Feature(e) == f {
			return true
		}
	}
	return false
}

// Status is the read model handed to the frontend (and logs). It never
// contains the raw token or any secret.
type Status struct {
	Licensed     bool            `json:"licensed"`
	Plan         string          `json:"plan,omitempty"`
	Customer     string          `json:"customer,omitempty"`
	LicenseID    string          `json:"license_id,omitempty"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
	Expired      bool            `json:"expired"`
	InGrace      bool            `json:"in_grace"`
	Entitlements []string        `json:"entitlements"`
	Features     map[string]bool `json:"features"`
	Limits       Limits          `json:"limits,omitempty"`
	// Warning is a human-readable heads-up (expiring soon, in grace, …),
	// empty when all is well.
	Warning string `json:"warning,omitempty"`
}

// Manager holds the verified license (if any) and answers entitlement
// questions. Safe for concurrent use; a license can be hot-reloaded via
// Load without a restart.
type Manager struct {
	mu     sync.RWMutex
	pub    ed25519.PublicKey
	claims *Claims // nil when unlicensed
}

// NewManager builds a Manager around the embedded public key. It returns an
// error only if the embedded key itself is unparseable (a build/release
// defect), never for the absence of a license.
func NewManager() (*Manager, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(embeddedPublicKeyB64))
	if err != nil {
		return nil, fmt.Errorf("license: bad embedded public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license: embedded public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return &Manager{pub: ed25519.PublicKey(raw)}, nil
}

// LoadFromEnv loads a license from SLUICIO_LICENSE_KEY (inline token) or
// SLUICIO_LICENSE_FILE (path to a token). It returns nil when neither is set
// (unlicensed is a valid, supported state) and an error only when a key is
// present but invalid — the caller logs it and continues unlicensed.
func (m *Manager) LoadFromEnv() error {
	if tok := strings.TrimSpace(os.Getenv("SLUICIO_LICENSE_KEY")); tok != "" {
		return m.Load(tok)
	}
	if path := strings.TrimSpace(os.Getenv("SLUICIO_LICENSE_FILE")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("license: read %s: %w", path, err)
		}
		return m.Load(string(b))
	}
	return nil
}

// Load verifies a token's signature and stores its claims. On any error the
// previous license state is left untouched, so a bad hot-reload can't
// silently disable a working license.
func (m *Manager) Load(token string) error {
	claims, err := m.verify(token)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.claims = claims
	m.mu.Unlock()
	return nil
}

// verify decodes "sluicio_lic_<b64url(payload)>.<b64url(sig)>", checks the
// Ed25519 signature against the embedded public key, and returns the claims.
// It does NOT check expiry — that's a runtime question answered by Entitled
// so a clock change or a long-running process re-evaluates each call.
func (m *Manager) verify(token string) (*Claims, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, tokenPrefix) {
		return nil, errors.New("license: not a Sluicio license token")
	}
	body := strings.TrimPrefix(token, tokenPrefix)
	parts := strings.SplitN(body, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("license: malformed token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("license: bad payload encoding: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("license: bad signature encoding: %w", err)
	}
	if !ed25519.Verify(m.pub, payload, sig) {
		return nil, errors.New("license: signature verification failed")
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("license: bad payload json: %w", err)
	}
	return &c, nil
}

// Entitled reports whether feature f may run right now: a valid license is
// loaded, the current time is within [not_before, expires_at + grace], and
// the feature is in the license's entitlements. Unlicensed or expired-past-
// grace ⇒ false for everything.
func (m *Manager) Entitled(f Feature) bool {
	m.mu.RLock()
	c := m.claims
	m.mu.RUnlock()
	if c == nil {
		return false
	}
	now := time.Now()
	if c.NotBefore != 0 && now.Before(time.Unix(c.NotBefore, 0)) {
		return false
	}
	if c.ExpiresAt != 0 && now.After(time.Unix(c.ExpiresAt, 0).Add(graceWindow)) {
		return false
	}
	return c.hasEntitlement(f)
}

// Limits returns the license's numeric limits (zero-valued when unlicensed).
func (m *Manager) Limits() Limits {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.claims == nil {
		return Limits{}
	}
	return m.claims.Limits
}

// MaxIntegrations returns the *effective* integration cap right now: the
// signed limit while the license is in force (including its grace window),
// or 0 = unlimited when unlicensed, not-yet-valid, or expired past grace —
// so a lapsed plan gracefully falls back to the uncapped Community
// behaviour rather than locking a customer at their old cap. Authoritative
// and tamper-proof: the number comes from the signed Claims.
func (m *Manager) MaxIntegrations() int {
	m.mu.RLock()
	c := m.claims
	m.mu.RUnlock()
	if c == nil {
		return 0
	}
	now := time.Now()
	if c.NotBefore != 0 && now.Before(time.Unix(c.NotBefore, 0)) {
		return 0
	}
	if c.ExpiresAt != 0 && now.After(time.Unix(c.ExpiresAt, 0).Add(graceWindow)) {
		return 0
	}
	return c.Limits.MaxIntegrations
}

// Status builds the read model for /api/v1/license. Always safe to call;
// returns an unlicensed status when no valid key is loaded.
func (m *Manager) Status() Status {
	m.mu.RLock()
	c := m.claims
	m.mu.RUnlock()

	st := Status{Features: map[string]bool{}}
	for _, f := range AllFeatures {
		st.Features[string(f)] = m.Entitled(f)
	}
	if c == nil {
		st.Entitlements = []string{}
		return st
	}

	now := time.Now()
	st.Plan = c.Plan
	st.Customer = c.Customer
	st.LicenseID = c.LicenseID
	st.Entitlements = c.Entitlements
	st.Limits = c.Limits
	if st.Entitlements == nil {
		st.Entitlements = []string{}
	}
	if c.ExpiresAt != 0 {
		exp := time.Unix(c.ExpiresAt, 0)
		st.ExpiresAt = &exp
		switch {
		case now.After(exp.Add(graceWindow)):
			st.Expired = true
			st.Warning = "License expired and the grace period has ended — Enterprise features are disabled. Renew to restore them."
		case now.After(exp):
			st.Expired = true
			st.InGrace = true
			st.Warning = fmt.Sprintf("License expired on %s — running in a grace period. Renew before it ends.", exp.Format("2006-01-02"))
		case now.After(exp.Add(-14 * 24 * time.Hour)):
			st.Warning = fmt.Sprintf("License expires on %s — renew soon to avoid interruption.", exp.Format("2006-01-02"))
		}
	}
	// "Licensed" means a valid, in-force license (signature good and not
	// past grace). Features may still be individually off if not entitled.
	st.Licensed = !(st.Expired && !st.InGrace)
	if c.NotBefore != 0 && now.Before(time.Unix(c.NotBefore, 0)) {
		st.Licensed = false
		st.Warning = "License is not yet valid (not-before date in the future)."
	}
	return st
}
