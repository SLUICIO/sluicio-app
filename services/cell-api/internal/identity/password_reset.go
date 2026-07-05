// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrResetTokenInvalid is returned when a reset token is unknown, expired, or
// already used. Callers surface a single generic message either way so a
// caller can't distinguish "wrong" from "expired".
var ErrResetTokenInvalid = errors.New("identity: reset token is invalid or expired")

// CreatePasswordResetToken mints a single-use, time-boxed reset token for a
// user. It returns the RAW token (to email); only its SHA-256 hash is stored.
func (s *Store) CreatePasswordResetToken(ctx context.Context, userID uuid.UUID, ttl time.Duration) (string, error) {
	raw, err := NewSessionID() // 256-bit base64url random — ample entropy
	if err != nil {
		return "", fmt.Errorf("identity: reset token: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)`,
		HashToken(raw), userID, time.Now().Add(ttl))
	if err != nil {
		return "", fmt.Errorf("identity: store reset token: %w", err)
	}
	return raw, nil
}

// ConsumePasswordResetToken validates a raw token and, if good, marks it used
// and returns the user it belongs to. Single-use + atomic: the UPDATE only
// matches an unused, unexpired row, so a token can't be redeemed twice even
// under a race.
func (s *Store) ConsumePasswordResetToken(ctx context.Context, rawToken string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE password_reset_tokens
		SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id`, HashToken(rawToken)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrResetTokenInvalid
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity: consume reset token: %w", err)
	}
	return userID, nil
}

// DeleteSessionsForUser revokes every active session for a user. Called after
// a password reset so any attacker session is kicked out.
func (s *Store) DeleteSessionsForUser(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("identity: delete user sessions: %w", err)
	}
	return nil
}

// InvalidateResetTokensForUser deletes any outstanding reset tokens for a
// user — e.g. after a successful reset or password change.
func (s *Store) InvalidateResetTokensForUser(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM password_reset_tokens WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("identity: invalidate reset tokens: %w", err)
	}
	return nil
}
