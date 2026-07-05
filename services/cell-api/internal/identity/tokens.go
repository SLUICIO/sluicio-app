// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Token format + storage. Conduit's API tokens look like:
//
//   con_pat_<22-char base64url>   personal access token (a user's PAT)
//   con_sa_<22-char base64url>    service account token
//
// 22 base64url chars = 132 bits of entropy. Far above any reasonable
// brute-force floor; SHA-256 verification is fine — no KDF needed.
// (We use argon2id for user passwords because those are low-entropy.
// Random-issued tokens don't have that problem.)
//
// The first 12 chars of the encoded token are the visible "prefix"
// users see in their token list, e.g. "con_pat_aXJj…". The full
// token is shown exactly once at mint time and never persisted.

package identity

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// Token-format constants. Kept here so the API and the middleware
// agree on what "looks like a token" means without duplication.
const (
	// TokenKindPersonal is the prefix for personal access tokens
	// (owner_type='user' in api_tokens).
	TokenKindPersonal = "con_pat_"
	// TokenKindServiceAccount is the prefix for service-account
	// tokens (owner_type='service_account').
	TokenKindServiceAccount = "con_sa_"
	// tokenSecretBytes is the random byte count we mint per token.
	// 16 bytes → 22 base64url chars → 128 bits, comfortably above
	// the brute-force floor for any realistic attacker.
	tokenSecretBytes = 16
	// PrefixLen is the number of leading characters of an encoded
	// token we store in `api_tokens.prefix` for indexed lookup +
	// user-visible display. Includes the kind prefix.
	PrefixLen = 12
)

// MintedToken bundles the three values a token-mint operation needs:
// the plaintext we hand back to the user (once), the prefix we store
// for indexed lookup + display, and the hash we store as the
// verification witness. Callers persist Prefix + Hash and return
// Plaintext to the client.
type MintedToken struct {
	Plaintext string
	Prefix    string
	Hash      string
}

// NewToken mints a fresh token of the given kind. Returns the parts
// the caller stores (Prefix + Hash) plus the Plaintext to return to
// the user.
//
// kind must be one of TokenKindPersonal / TokenKindServiceAccount.
func NewToken(kind string) (MintedToken, error) {
	if kind != TokenKindPersonal && kind != TokenKindServiceAccount {
		return MintedToken{}, errors.New("identity: invalid token kind")
	}
	secret, err := randomBytes(tokenSecretBytes)
	if err != nil {
		return MintedToken{}, err
	}
	plaintext := kind + base64.RawURLEncoding.EncodeToString(secret)
	return MintedToken{
		Plaintext: plaintext,
		Prefix:    plaintext[:PrefixLen],
		Hash:      HashToken(plaintext),
	}, nil
}

// HashToken returns the lowercase-hex SHA-256 of the token. Used both
// at mint time (stored in api_tokens.token_hash) and at verification
// time (compared constant-time against the stored hash).
//
// SHA-256 is sufficient because tokens are 128-bit random; an
// attacker who can brute-force a SHA-256 preimage of a 128-bit
// input has bigger problems than us. We do NOT use argon2id here —
// that's for low-entropy passwords.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// VerifyTokenHash returns true iff HashToken(plaintext) equals the
// stored hash, compared in constant time.
func VerifyTokenHash(plaintext, storedHex string) bool {
	got := HashToken(plaintext)
	return subtle.ConstantTimeCompare([]byte(got), []byte(storedHex)) == 1
}

// PrefixOf returns the first PrefixLen chars of a token, or the
// whole string if it's shorter (so a malformed token still produces
// a non-panicking lookup that simply won't match anything).
func PrefixOf(token string) string {
	if len(token) < PrefixLen {
		return token
	}
	return token[:PrefixLen]
}

// LooksLikeToken returns true if s starts with one of our known
// token kind prefixes. The middleware uses this to discriminate
// "Authorization: Bearer <opaque>" from other bearer schemes that
// might pass through (none today, but defensive).
func LooksLikeToken(s string) bool {
	return strings.HasPrefix(s, TokenKindPersonal) ||
		strings.HasPrefix(s, TokenKindServiceAccount)
}
