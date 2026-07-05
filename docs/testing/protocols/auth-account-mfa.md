<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Account, password & MFA

| Field | Value |
|-------|-------|
| **Area** | Account self-service + multi-factor auth |
| **Automation status** | Partial (login covered in [auth-login.md](auth-login.md); the rest manual) |
| **Automated by** | — |
| **Last reviewed** | 2026-06-20 |

## Preconditions

- Local stack up (`make dev-up`); signed in as the seed admin unless noted.
- Password-reset and SMTP-test cases need SMTP configured (Settings →
  cell SMTP, or `cell-settings/smtp`) — otherwise they're inspect-only.

## Cases

### Case 1 — View & edit profile
- **Actor:** any signed-in user · **Endpoint:** `GET/PATCH /api/v1/me`
- **Steps:** Open Account; change display name; save.
- **Expected:** Name persists across reload; `/me` returns the new name.
- **Automation:** Manual.

### Case 2 — Change own password
- **Actor:** any user · **Endpoint:** `POST /api/v1/me/password`
- **Steps:** Account → change password; enter current + new; save; log out; log in with the new password.
- **Expected:** Old password no longer works; new one does.
- **Automation:** Manual (mutates the seed admin password — do last, or on a throwaway user).

### Case 3 — Forgot password (request)
- **Actor:** anonymous · **Endpoint:** `POST /api/v1/auth/forgot-password`
- **Steps:** Login page → "Forgot password?"; enter email; submit.
- **Expected:** Generic "if that account exists…" confirmation (no user enumeration); with SMTP set, a reset email is sent.
- **Automation:** Manual (needs SMTP / mailbox to complete).

### Case 4 — Reset password (consume token)
- **Actor:** anonymous with a valid reset token · **Endpoint:** `POST /api/v1/auth/reset-password`
- **Steps:** Open the reset link; set a new password; submit.
- **Expected:** Password updated; token is single-use; login works with the new password.
- **Automation:** Manual (token arrives by email).

### Case 5 — Enroll in MFA (TOTP)
- **Actor:** any user · **Endpoint:** `POST /api/v1/account/mfa/setup` → `…/enable`
- **Steps:** Account → enable MFA; scan the QR / take the secret into an authenticator; enter a 6-digit code to confirm; save backup codes.
- **Expected:** `GET /api/v1/account/mfa` reports enabled; backup codes shown once.
- **Automation:** Partial — automatable if the test derives the TOTP code from the `setup` secret; otherwise Manual.

### Case 6 — Login with MFA
- **Actor:** a user with MFA enabled · **Endpoint:** `POST /api/v1/auth/login` → `POST /api/v1/auth/mfa-verify`
- **Steps:** Enter email+password; at the 2FA prompt enter a current TOTP code; submit.
- **Expected:** Password step returns `mfa_required` + a pending token; a valid code completes login and sets the session; an invalid code is rejected.
- **Automation:** Partial (needs a TOTP secret fixture).

### Case 7 — Login with a backup code
- **Actor:** MFA user · **Endpoint:** `POST /api/v1/auth/mfa-verify`
- **Steps:** At the 2FA prompt, enter a saved backup code instead of a TOTP.
- **Expected:** Login succeeds; that backup code is consumed (single-use).
- **Automation:** Manual.

### Case 8 — Disable MFA
- **Actor:** the MFA user · **Endpoint:** `POST /api/v1/account/mfa/disable`
- **Steps:** Account → disable MFA (re-auth/confirm as prompted).
- **Expected:** `GET /api/v1/account/mfa` reports disabled; next login is single-step.
- **Automation:** Manual.

## Notes
- The MFA encryption key is `SLUICIO_MFA_KEY` (auto-generated + persisted in `cell_settings` if unset). For repeatable automation, pin it.
