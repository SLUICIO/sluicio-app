// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrMFAUnavailable is returned when no encryption key is configured, so the
// handlers can tell the admin to set SLUICIO_MFA_KEY rather than silently
// store secrets in the clear.
var ErrMFAUnavailable = errors.New("identity: MFA encryption key not configured")

// ErrMFAPendingInvalid is returned for a bad/expired MFA login token.
var ErrMFAPendingInvalid = errors.New("identity: MFA login token invalid or expired")

// MFAState describes a user's MFA enrollment.
type MFAState struct {
	Enrolled bool // a row exists (enabled or mid-enrollment)
	Enabled  bool // enabled_at is set — MFA is enforced at login
}

// MFAStatus reports whether a user has MFA enabled / pending.
func (s *Store) MFAStatus(ctx context.Context, userID uuid.UUID) (MFAState, error) {
	var enabledAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT enabled_at FROM user_mfa WHERE user_id = $1`, userID).Scan(&enabledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MFAState{}, nil
	}
	if err != nil {
		return MFAState{}, fmt.Errorf("identity: mfa status: %w", err)
	}
	return MFAState{Enrolled: true, Enabled: enabledAt != nil}, nil
}

// StartMFAEnrollment generates a new TOTP secret, stores it encrypted with
// enabled_at NULL (pending), and returns the raw base32 secret for the
// provisioning URI. Re-enrolling overwrites any pending/old secret.
func (s *Store) StartMFAEnrollment(ctx context.Context, userID uuid.UUID, secret string) error {
	enc, err := s.encrypt([]byte(secret))
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO user_mfa (user_id, secret_enc, enabled_at, backup_code_hashes, updated_at)
		VALUES ($1, $2, NULL, '[]'::jsonb, now())
		ON CONFLICT (user_id) DO UPDATE
		SET secret_enc = EXCLUDED.secret_enc, enabled_at = NULL,
		    backup_code_hashes = '[]'::jsonb, updated_at = now()`,
		userID, enc)
	if err != nil {
		return fmt.Errorf("identity: start mfa: %w", err)
	}
	return nil
}

// MFASecret decrypts and returns a user's TOTP secret + whether MFA is
// enabled. Used to verify codes at enable time and login.
func (s *Store) MFASecret(ctx context.Context, userID uuid.UUID) (secret string, enabled bool, err error) {
	var enc []byte
	var enabledAt *time.Time
	err = s.pool.QueryRow(ctx, `SELECT secret_enc, enabled_at FROM user_mfa WHERE user_id = $1`, userID).Scan(&enc, &enabledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, fmt.Errorf("identity: mfa secret: %w", err)
	}
	plain, err := s.decrypt(enc)
	if err != nil {
		return "", false, err
	}
	return string(plain), enabledAt != nil, nil
}

// EnableMFA marks a pending enrollment active and stores the backup-code
// hashes (SHA-256 hex of each code).
func (s *Store) EnableMFA(ctx context.Context, userID uuid.UUID, backupHashes []string) error {
	raw, _ := json.Marshal(backupHashes)
	ct, err := s.pool.Exec(ctx, `
		UPDATE user_mfa SET enabled_at = now(), backup_code_hashes = $2::jsonb, updated_at = now()
		WHERE user_id = $1`, userID, raw)
	if err != nil {
		return fmt.Errorf("identity: enable mfa: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DisableMFA removes a user's MFA entirely.
func (s *Store) DisableMFA(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM user_mfa WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("identity: disable mfa: %w", err)
	}
	return nil
}

// ConsumeBackupCode checks a raw backup code against the stored hashes and,
// if it matches, removes it (single-use). Returns true on a successful match.
func (s *Store) ConsumeBackupCode(ctx context.Context, userID uuid.UUID, rawCode string) (bool, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT backup_code_hashes FROM user_mfa WHERE user_id = $1`, userID).Scan(&raw); err != nil {
		return false, fmt.Errorf("identity: backup codes: %w", err)
	}
	var hashes []string
	_ = json.Unmarshal(raw, &hashes)
	want := HashToken(strings.TrimSpace(rawCode))
	kept := make([]string, 0, len(hashes))
	matched := false
	for _, h := range hashes {
		if !matched && subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			matched = true
			continue // drop it (consumed)
		}
		kept = append(kept, h)
	}
	if !matched {
		return false, nil
	}
	out, _ := json.Marshal(kept)
	if _, err := s.pool.Exec(ctx, `UPDATE user_mfa SET backup_code_hashes = $2::jsonb, updated_at = now() WHERE user_id = $1`, userID, out); err != nil {
		return false, fmt.Errorf("identity: consume backup code: %w", err)
	}
	return true, nil
}

// HasMFAKey reports whether an encryption key is configured (MFA usable).
func (s *Store) HasMFAKey() bool { return len(s.mfaKey) == 32 }

// ── MFA pending-login token (stateless, HMAC-signed) ──────────────────
//
// After a correct password for an MFA-enabled user, we issue a short-lived
// token instead of a session. The /auth/mfa-verify step exchanges it (plus a
// valid TOTP/backup code) for a real session. The token is HMAC-signed with
// the MFA key so it needs no storage.

const mfaPendingTTL = 5 * time.Minute

// IssueMFAPendingToken returns "userID.expiryUnix.hmac" signed with the MFA
// key.
func (s *Store) IssueMFAPendingToken(userID uuid.UUID) (string, error) {
	if len(s.mfaKey) != 32 {
		return "", ErrMFAUnavailable
	}
	exp := time.Now().Add(mfaPendingTTL).Unix()
	payload := fmt.Sprintf("%s.%d", userID.String(), exp)
	mac := s.mfaHMAC(payload)
	return payload + "." + mac, nil
}

// VerifyMFAPendingToken validates the token and returns the user id.
func (s *Store) VerifyMFAPendingToken(token string) (uuid.UUID, error) {
	if len(s.mfaKey) != 32 {
		return uuid.Nil, ErrMFAUnavailable
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return uuid.Nil, ErrMFAPendingInvalid
	}
	payload := parts[0] + "." + parts[1]
	if subtle.ConstantTimeCompare([]byte(s.mfaHMAC(payload)), []byte(parts[2])) != 1 {
		return uuid.Nil, ErrMFAPendingInvalid
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return uuid.Nil, ErrMFAPendingInvalid
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, ErrMFAPendingInvalid
	}
	return id, nil
}

func (s *Store) mfaHMAC(payload string) string {
	h := hmac.New(sha256.New, s.mfaKey)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// ── AES-GCM secret encryption ─────────────────────────────────────────

func (s *Store) encrypt(plain []byte) ([]byte, error) {
	if len(s.mfaKey) != 32 {
		return nil, ErrMFAUnavailable
	}
	block, err := aes.NewCipher(s.mfaKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil // nonce || ciphertext
}

func (s *Store) decrypt(enc []byte) ([]byte, error) {
	if len(s.mfaKey) != 32 {
		return nil, ErrMFAUnavailable
	}
	block, err := aes.NewCipher(s.mfaKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(enc) < gcm.NonceSize() {
		return nil, errors.New("identity: ciphertext too short")
	}
	nonce, ct := enc[:gcm.NonceSize()], enc[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// GenerateBackupCodes returns n random human-friendly codes plus their
// SHA-256 hashes (for storage). The raw codes are shown once.
func GenerateBackupCodes(n int) (codes []string, hashes []string, err error) {
	for i := 0; i < n; i++ {
		b := make([]byte, 5)
		if _, err := io.ReadFull(rand.Reader, b); err != nil {
			return nil, nil, err
		}
		// 10 hex chars, grouped as XXXXX-XXXXX for readability.
		h := hex.EncodeToString(b)
		code := h[:5] + "-" + h[5:]
		codes = append(codes, code)
		hashes = append(hashes, HashToken(code))
	}
	return codes, hashes, nil
}
