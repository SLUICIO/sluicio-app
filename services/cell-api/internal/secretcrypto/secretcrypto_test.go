// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package secretcrypto

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestRoundTrip(t *testing.T) {
	key := newKey(t)
	for _, plain := range []string{"hunter2", "sk-abcDEF-0123", "with spaces & symbols: €@!"} {
		enc, err := Encrypt(key, plain)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", plain, err)
		}
		if !IsEncrypted(enc) {
			t.Fatalf("Encrypt(%q) = %q, missing prefix", plain, enc)
		}
		if strings.Contains(enc, plain) {
			t.Fatalf("ciphertext %q leaks plaintext %q", enc, plain)
		}
		got, err := Decrypt(key, enc)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != plain {
			t.Fatalf("round trip: got %q want %q", got, plain)
		}
	}
}

func TestNoncePerCall(t *testing.T) {
	key := newKey(t)
	a, _ := Encrypt(key, "same")
	b, _ := Encrypt(key, "same")
	if a == b {
		t.Fatalf("expected distinct ciphertexts for repeated plaintext (nonce reuse?)")
	}
}

func TestEmptyStaysEmpty(t *testing.T) {
	key := newKey(t)
	enc, err := Encrypt(key, "")
	if err != nil || enc != "" {
		t.Fatalf("Encrypt(\"\") = %q, %v; want \"\", nil", enc, err)
	}
	got, err := Decrypt(key, "")
	if err != nil || got != "" {
		t.Fatalf("Decrypt(\"\") = %q, %v; want \"\", nil", got, err)
	}
}

func TestLegacyPlaintextPassthrough(t *testing.T) {
	// A value written before encryption-at-rest has no prefix; Decrypt must
	// return it verbatim, even with no key, so reads keep working pre-migration.
	const legacy = "plaintext-smtp-password"
	got, err := Decrypt(nil, legacy)
	if err != nil || got != legacy {
		t.Fatalf("Decrypt(legacy) = %q, %v; want %q, nil", got, err, legacy)
	}
}

func TestEncryptRequiresKey(t *testing.T) {
	if _, err := Encrypt(nil, "secret"); err != ErrNoKey {
		t.Fatalf("Encrypt with no key: got %v want ErrNoKey", err)
	}
	if _, err := Encrypt(make([]byte, 16), "secret"); err != ErrNoKey {
		t.Fatalf("Encrypt with 16-byte key: got %v want ErrNoKey", err)
	}
}

func TestWrongKeyFails(t *testing.T) {
	enc, err := Encrypt(newKey(t), "secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(newKey(t), enc); err == nil {
		t.Fatal("Decrypt with wrong key: expected error, got nil")
	}
}

func TestTamperFails(t *testing.T) {
	key := newKey(t)
	enc, _ := Encrypt(key, "secret")
	// Decode to raw bytes and flip a bit in the final byte (part of the GCM
	// auth tag), then re-encode. Mutating at the byte level is deterministic —
	// flipping the last *base64* char can be a no-op, since the trailing char
	// of a partial group carries unused low bits.
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(enc, prefix))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[len(raw)-1] ^= 0x01
	tampered := prefix + base64.RawStdEncoding.EncodeToString(raw)
	if _, err := Decrypt(key, tampered); err == nil {
		t.Fatal("Decrypt of tampered ciphertext: expected error, got nil")
	}
}

func TestPrefixedButUndecodable(t *testing.T) {
	// Prefix present but the payload isn't valid ciphertext → must error,
	// never silently return the raw bytes.
	if _, err := Decrypt(newKey(t), prefix+"!!!not-base64!!!"); err == nil {
		t.Fatal("expected error for prefixed garbage, got nil")
	}
}
