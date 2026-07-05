// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package secretcrypto encrypts "replayable" credentials at rest — secrets
// the cell must hand back out in the clear to authenticate to a third party
// (the SMTP relay password, an OIDC client secret), so they cannot be hashed
// like a user password. It wraps AES-256-GCM with a versioned, self-describing
// text encoding so a value can be told apart from a legacy plaintext one that
// was written before encryption-at-rest existed.
//
// The 32-byte key is the same cell secret used for MFA TOTP encryption
// (SLUICIO_MFA_KEY, or the auto-generated system.mfa_key). Callers pass it in;
// this package holds no state.
package secretcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

// prefix marks a value produced by Encrypt. Its presence is how Decrypt
// distinguishes ciphertext from a legacy plaintext value; the version segment
// lets the encoding evolve without ambiguity.
const prefix = "enc:v1:"

// ErrNoKey is returned when encryption (or decryption of ciphertext) is
// requested without a 32-byte key.
var ErrNoKey = errors.New("secretcrypto: encryption key not configured")

// IsEncrypted reports whether stored was produced by Encrypt. Used by the
// one-time migrations to skip already-encrypted rows (idempotency).
func IsEncrypted(stored string) bool { return strings.HasPrefix(stored, prefix) }

// Encrypt returns prefix + base64(nonce||ciphertext) for a non-empty plaintext.
//
// An empty plaintext returns "" unchanged: there is nothing to protect, and
// callers depend on empty staying empty (e.g. the SSO UPDATE uses
// NULLIF(secret,”) to mean "keep the stored value").
func Encrypt(key []byte, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if len(key) != 32 {
		return "", ErrNoKey
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil) // nonce || ciphertext
	return prefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. A value without the prefix is treated as legacy
// plaintext and returned unchanged, so reads keep working before the one-time
// migration re-encrypts existing rows — and even on a cell with no key. A
// prefixed value that fails to decrypt is a real error (wrong key / tampering),
// never silently returned.
func Decrypt(key []byte, stored string) (string, error) {
	if !IsEncrypted(stored) {
		return stored, nil
	}
	if len(key) != 32 {
		return "", ErrNoKey
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, prefix))
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("secretcrypto: ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
