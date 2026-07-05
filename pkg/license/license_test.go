// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// signToken mints a token the way ee/cmd/sluicio-license does, but with a
// test-supplied private key so the test owns both halves of the keypair.
func signToken(t *testing.T, priv ed25519.PrivateKey, c Claims) string {
	t.Helper()
	payload, err := json.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sig := ed25519.Sign(priv, payload)
	return tokenPrefix +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

func newTestManager(t *testing.T) (*Manager, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return &Manager{pub: pub}, priv
}

// The effective integration cap must follow the license's lifecycle: in
// force (and during grace) it's the signed number; unlicensed or past grace
// it lifts to 0 = unlimited (Community behaviour), never locking a lapsed
// customer at their old cap.
func TestMaxIntegrationsLifecycle(t *testing.T) {
	m, priv := newTestManager(t)
	now := time.Now()

	if got := m.MaxIntegrations(); got != 0 {
		t.Fatalf("unlicensed cap = %d, want 0 (unlimited)", got)
	}

	inForce := signToken(t, priv, Claims{
		Plan:      "pro",
		Limits:    Limits{MaxIntegrations: 25},
		IssuedAt:  now.Unix(),
		NotBefore: now.Add(-time.Hour).Unix(),
		ExpiresAt: now.Add(365 * 24 * time.Hour).Unix(),
	})
	if err := m.Load(inForce); err != nil {
		t.Fatalf("load in-force: %v", err)
	}
	if got := m.MaxIntegrations(); got != 25 {
		t.Fatalf("in-force cap = %d, want 25", got)
	}

	inGrace := signToken(t, priv, Claims{
		Limits:    Limits{MaxIntegrations: 25},
		ExpiresAt: now.Add(-24 * time.Hour).Unix(), // expired yesterday, within grace
	})
	if err := m.Load(inGrace); err != nil {
		t.Fatalf("load in-grace: %v", err)
	}
	if got := m.MaxIntegrations(); got != 25 {
		t.Fatalf("in-grace cap = %d, want 25 (cap still applies during grace)", got)
	}

	pastGrace := signToken(t, priv, Claims{
		Limits:    Limits{MaxIntegrations: 25},
		ExpiresAt: now.Add(-graceWindow - 24*time.Hour).Unix(),
	})
	if err := m.Load(pastGrace); err != nil {
		t.Fatalf("load past-grace: %v", err)
	}
	if got := m.MaxIntegrations(); got != 0 {
		t.Fatalf("past-grace cap = %d, want 0 (lifts to unlimited)", got)
	}
}

// Water-proof: raising the cap in the payload without re-signing must be
// rejected — the signature, not the JSON, is the source of truth.
func TestTamperedCapRejected(t *testing.T) {
	m, priv := newTestManager(t)
	tok := signToken(t, priv, Claims{
		Plan:     "pro",
		Limits:   Limits{MaxIntegrations: 25},
		IssuedAt: time.Now().Unix(),
	})
	if err := m.Load(tok); err != nil {
		t.Fatalf("baseline load: %v", err)
	}

	parts := strings.SplitN(strings.TrimPrefix(tok, tokenPrefix), ".", 2)
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	c.Limits.MaxIntegrations = 1_000_000 // forge a bigger cap
	forged, _ := json.Marshal(&c)
	// Re-attach the ORIGINAL signature over the tampered payload.
	tampered := tokenPrefix + base64.RawURLEncoding.EncodeToString(forged) + "." + parts[1]

	if err := m.Load(tampered); err == nil {
		t.Fatal("tampered token with a raised cap was accepted — signature check is not water-proof")
	}
	// The previously loaded (valid) cap must remain in force after a rejected load.
	if got := m.MaxIntegrations(); got != 25 {
		t.Fatalf("after rejected tamper, cap = %d, want 25 (unchanged)", got)
	}
}
