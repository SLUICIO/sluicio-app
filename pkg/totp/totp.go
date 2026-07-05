// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package totp implements RFC 6238 time-based one-time passwords (the
// authenticator-app standard) with no external dependencies: HMAC-SHA1,
// 6 digits, 30-second period — the defaults every authenticator app uses.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	period      = 30 // seconds per code
	digits      = 6
	secretBytes = 20 // 160-bit secret, per RFC 4226 recommendation
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret returns a new random base32-encoded TOTP secret.
func GenerateSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// code computes the 6-digit TOTP for a counter value.
func code(secret string, counter uint64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("totp: bad secret: %w", err)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	h := hmac.New(sha1.New, key)
	h.Write(buf[:])
	sum := h.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	val := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%0*d", digits, val%1_000_000), nil
}

// Generate returns the current TOTP code for a secret at time t. Useful for
// tests and tooling; Validate is what the login path uses.
func Generate(secret string, t time.Time) (string, error) {
	return code(secret, uint64(int64(t.Unix())/period))
}

// Validate reports whether input is a valid code for secret at time t,
// accepting the adjacent ±1 windows to tolerate clock skew. Constant-time
// comparison avoids leaking timing information.
func Validate(secret, input string, t time.Time) bool {
	input = strings.TrimSpace(input)
	if len(input) != digits {
		return false
	}
	counter := int64(t.Unix()) / period
	for _, d := range []int64{0, -1, 1} {
		want, err := code(secret, uint64(counter+d))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(input)) == 1 {
			return true
		}
	}
	return false
}

// ProvisioningURI builds the otpauth:// URI an authenticator app reads from
// a QR code. account is typically the user's email; issuer is the app name.
func ProvisioningURI(secret, account, issuer string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", digits))
	q.Set("period", fmt.Sprintf("%d", period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}
