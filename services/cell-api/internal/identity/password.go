// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// HashPassword returns a PHC-formatted argon2id hash of plaintext.
// The output is the canonical
//
//	$argon2id$v=19$m=65536,t=3,p=4$<salt-b64>$<hash-b64>
//
// string suitable for storage in users.password_hash and consumption
// by VerifyPassword.
//
// Params chosen for an interactive login on commodity hardware in
// 2025: 64 MB memory, 3 time-iterations, 4 lanes. OWASP currently
// recommends these as a sensible default; bump them in a future
// migration when the floor moves.
func HashPassword(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("identity: password is empty")
	}
	salt, err := randomBytes(saltLength)
	if err != nil {
		return "", fmt.Errorf("identity: salt: %w", err)
	}
	hash := argon2.IDKey([]byte(plaintext), salt, argonTime, argonMemory, argonThreads, keyLength)
	return formatPHC(salt, hash), nil
}

// VerifyPassword constant-time checks plaintext against a previously
// stored PHC-formatted hash. Returns (true, nil) on match,
// (false, nil) on mismatch, and an error only when the encoded hash
// is malformed (treat the latter like a security incident — it
// shouldn't happen against our own writes).
func VerifyPassword(plaintext, encoded string) (bool, error) {
	if encoded == "" {
		// Caller decides whether "no password set" is a hard fail or
		// a fall-through to SSO; we just say "not a match."
		return false, nil
	}
	parsed, err := parsePHC(encoded)
	if err != nil {
		return false, err
	}
	candidate := argon2.IDKey([]byte(plaintext), parsed.salt, parsed.time, parsed.memory, parsed.threads, uint32(len(parsed.hash)))
	if subtle.ConstantTimeCompare(candidate, parsed.hash) == 1 {
		return true, nil
	}
	return false, nil
}

// argon2id parameters. Centralised here so a future tuning bump
// can flow through every call site (and through any rehash-on-login
// upgrade path we add later).
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB → 64 MiB
	argonThreads uint8  = 4
	saltLength          = 16
	keyLength    uint32 = 32
)

// Internal PHC representation. We only need to round-trip the params
// the verifier consumed, so we don't bother with anything the format
// allows but we don't emit (data, secret, etc.).
type phcHash struct {
	time    uint32
	memory  uint32
	threads uint8
	salt    []byte
	hash    []byte
}

func formatPHC(salt, hash []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash))
}

// parsePHC decodes one of our own emitted strings. The format is
// fixed (argon2id, v=19, m/t/p in that order); we don't try to
// support arbitrary PHC dialects since we don't emit them.
func parsePHC(encoded string) (phcHash, error) {
	parts := strings.Split(encoded, "$")
	// "$argon2id$v=N$m=…,t=…,p=…$salt$hash" → 6 parts (incl. leading empty)
	if len(parts) != 6 {
		return phcHash{}, fmt.Errorf("identity: bad hash format")
	}
	if parts[1] != "argon2id" {
		return phcHash{}, fmt.Errorf("identity: unsupported algo %q", parts[1])
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return phcHash{}, fmt.Errorf("identity: bad version: %w", err)
	}
	if version != argon2.Version {
		return phcHash{}, fmt.Errorf("identity: incompatible argon2 version %d", version)
	}
	var p phcHash
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return phcHash{}, fmt.Errorf("identity: bad params: %w", err)
	}
	var err error
	if p.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return phcHash{}, fmt.Errorf("identity: bad salt: %w", err)
	}
	if p.hash, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return phcHash{}, fmt.Errorf("identity: bad hash: %w", err)
	}
	return p, nil
}

// randomBytes returns n cryptographically-random bytes or an error
// if the OS PRNG isn't available (essentially never).
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// NewSessionID returns a 32-byte base64url-encoded random string
// suitable for inserting into sessions.id and copying into the
// cookie. 256 bits of entropy makes brute-forcing irrelevant
// regardless of session_count.
func NewSessionID() (string, error) {
	b, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
