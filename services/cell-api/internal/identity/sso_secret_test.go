// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package identity

import (
	"crypto/rand"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/secretcrypto"
)

// The store secret helpers gate encryption on the presence of a key and must
// keep empty empty — the SSO UPDATE relies on "" meaning "keep the stored
// secret" via COALESCE(NULLIF($6,”), client_secret).
func TestStoreSecretHelpers(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}

	t.Run("round trip with key", func(t *testing.T) {
		s := &Store{}
		s.SetMFAKey(key)

		enc, err := s.encryptSecret("oidc-client-secret")
		if err != nil {
			t.Fatalf("encryptSecret: %v", err)
		}
		if !secretcrypto.IsEncrypted(enc) {
			t.Fatalf("expected ciphertext, got %q", enc)
		}
		got, err := s.decryptSecret(enc)
		if err != nil {
			t.Fatalf("decryptSecret: %v", err)
		}
		if got != "oidc-client-secret" {
			t.Fatalf("round trip: got %q", got)
		}
	})

	t.Run("empty stays empty", func(t *testing.T) {
		s := &Store{}
		s.SetMFAKey(key)
		enc, err := s.encryptSecret("")
		if err != nil || enc != "" {
			t.Fatalf("encryptSecret(\"\") = %q, %v; want \"\", nil", enc, err)
		}
	})

	t.Run("no key passes through as plaintext", func(t *testing.T) {
		s := &Store{} // no mfaKey
		enc, err := s.encryptSecret("plain")
		if err != nil || enc != "plain" {
			t.Fatalf("keyless encryptSecret = %q, %v; want plaintext", enc, err)
		}
		got, err := s.decryptSecret("plain") // legacy value, no prefix
		if err != nil || got != "plain" {
			t.Fatalf("keyless decryptSecret = %q, %v; want plaintext", got, err)
		}
	})
}
