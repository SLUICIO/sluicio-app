// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/pkg/license"
	"github.com/integration-monitor/integration-monitor/pkg/mail"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/settings"
)

// freeRetentionCapDays is the longest retention a non-Enterprise install may
// configure — two weeks. The `retention_long` entitlement lifts it up to the
// server's hard RetentionMaxDays (or the license's own max_retention_days, if
// lower).
const freeRetentionCapDays = 14

// effectiveRetentionMaxDays returns the retention ceiling that applies right
// now, and whether long retention is unlocked. Without the entitlement the
// ceiling is the free cap; with it, the server max (optionally tightened by
// a license limit).
func (h *Handlers) effectiveRetentionMaxDays() (maxDays int, longUnlocked bool) {
	if h.featureEntitled(license.FeatureRetentionLong) {
		if lim := h.License.Limits().MaxRetentionDays; lim > 0 && lim < settings.RetentionMaxDays {
			return lim, true
		}
		return settings.RetentionMaxDays, true
	}
	if settings.RetentionMaxDays < freeRetentionCapDays {
		return settings.RetentionMaxDays, false
	}
	return freeRetentionCapDays, false
}

// Cell-wide settings surface. v1 only exposes telemetry retention; the
// shape (a small JSON object per setting) is meant to grow as more
// cell knobs come online (default windows, query timeouts, sampling…).
//
// All endpoints here mutate cell-level state, so the mutating routes
// are gated to org admin (RequireRole at the mux level). Reading is
// open to any signed-in user — the values are useful in the UI even
// for viewers (e.g. "how far back can I scroll the Logs page?").

// retentionResponse is the GET shape. Each telemetry type carries its
// configured retention + when the enforcer last applied it to
// ClickHouse. last_applied_at may be null if the enforcer hasn't run
// yet (very fresh install) or if the setting was edited but the
// synchronous apply failed.
type retentionResponse struct {
	Traces  retentionEntry `json:"traces"`
	Logs    retentionEntry `json:"logs"`
	Metrics retentionEntry `json:"metrics"`
	// Min/Max are echoed so the frontend's input range matches the
	// server's accepted bounds. Avoids the UI hard-coding limits that
	// would drift if the server's RetentionMinDays / MaxDays change.
	// MaxDays is the *effective* ceiling: capped to the free tier unless
	// the retention_long entitlement is active.
	MinDays int `json:"min_days"`
	MaxDays int `json:"max_days"`
	// LongRetention reports whether the Enterprise long-retention
	// entitlement is active. When false, MaxDays is the free-tier cap and
	// the UI shows an upgrade prompt.
	LongRetention bool `json:"long_retention"`
	// Audit-log retention (Postgres prune, not a CH TTL). AuditMaxDays is
	// the effective ceiling: the free cap without the audit_log
	// entitlement, 10 years with it. AuditConfigurable mirrors the
	// entitlement so the UI knows whether to offer the field or an
	// upgrade prompt.
	AuditDays         int  `json:"audit_days"`
	AuditMaxDays      int  `json:"audit_max_days"`
	AuditConfigurable bool `json:"audit_configurable"`
}

type retentionEntry struct {
	Days          int        `json:"days"`
	LastAppliedAt *time.Time `json:"last_applied_at,omitempty"`
}

// retentionRequest is the PATCH body. Each field is optional —
// omitting it leaves that telemetry type alone, sending it changes
// just that one. Lets the UI ship single-field saves without having
// to round-trip the whole policy.
type retentionRequest struct {
	Traces  *int `json:"traces_days,omitempty"`
	Logs    *int `json:"logs_days,omitempty"`
	Metrics *int `json:"metrics_days,omitempty"`
	Audit   *int `json:"audit_days,omitempty"`
}

// effectiveAuditRetentionMaxDays mirrors effectiveRetentionMaxDays for the
// audit knob: free installs are pinned to the 14-day cap, the audit_log
// entitlement unlocks up to 10 years.
func (h *Handlers) effectiveAuditRetentionMaxDays() (maxDays int, unlocked bool) {
	if h.featureEntitled(license.FeatureAuditLog) {
		return settings.AuditRetentionMaxDays, true
	}
	return freeRetentionCapDays, false
}

// getRetention: GET /api/v1/cell-settings/retention
func (h *Handlers) getRetention(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "settings store unavailable")
		return
	}
	pol, err := h.Settings.GetRetention(r.Context())
	if err != nil {
		h.Logger.Error("get retention failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	maxDays, longUnlocked := h.effectiveRetentionMaxDays()
	auditDays, err := h.Settings.GetAuditRetentionDays(r.Context())
	if err != nil {
		h.Logger.Warn("get audit retention failed", "err", err)
		auditDays = settings.AuditRetentionDefaultDays
	}
	auditMax, auditUnlocked := h.effectiveAuditRetentionMaxDays()
	httpserver.WriteJSON(w, http.StatusOK, retentionResponse{
		Traces:            toEntry(pol.Traces.Days, pol.LastAppliedAt[settings.TelemetryTraces]),
		Logs:              toEntry(pol.Logs.Days, pol.LastAppliedAt[settings.TelemetryLogs]),
		Metrics:           toEntry(pol.Metrics.Days, pol.LastAppliedAt[settings.TelemetryMetrics]),
		MinDays:           settings.RetentionMinDays,
		MaxDays:           maxDays,
		LongRetention:     longUnlocked,
		AuditDays:         auditDays,
		AuditMaxDays:      auditMax,
		AuditConfigurable: auditUnlocked,
	})
}

func toEntry(days int, last time.Time) retentionEntry {
	e := retentionEntry{Days: days}
	if !last.IsZero() {
		e.LastAppliedAt = &last
	}
	return e
}

// patchRetention: PATCH /api/v1/cell-settings/retention
//
// Admin-only (mux gate). Persists any non-nil field, then asks the
// retention enforcer to push the new values into ClickHouse
// synchronously so the user's change is reflected before the response
// returns.
//
// Synchronous-apply is best-effort: the Postgres write is the source
// of truth. If the ClickHouse ALTER fails (network blip, CH overload),
// we still 200 — the enforcer's periodic loop will retry. The
// response then carries an `apply_warning` field so the UI can
// communicate "saved, but ClickHouse hasn't picked it up yet."
func (h *Handlers) patchRetention(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "settings store unavailable")
		return
	}
	var body retentionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Traces == nil && body.Logs == nil && body.Metrics == nil && body.Audit == nil {
		httpserver.WriteError(w, http.StatusBadRequest, "at least one of traces_days, logs_days, metrics_days, audit_days is required")
		return
	}
	// Enterprise gate: a non-Enterprise install can't set retention beyond
	// the free cap. Reject with 402 so the UI can show an upgrade prompt
	// rather than a generic validation error.
	maxDays, longUnlocked := h.effectiveRetentionMaxDays()
	for _, v := range []*int{body.Traces, body.Logs, body.Metrics} {
		if v == nil || *v <= maxDays {
			continue
		}
		if !longUnlocked {
			httpserver.WriteJSON(w, http.StatusPaymentRequired, map[string]any{
				"error":    "enterprise_feature",
				"feature":  string(license.FeatureRetentionLong),
				"message":  "Retention beyond 2 weeks (the free-tier cap) requires a Sluicio Enterprise license.",
				"max_days": maxDays,
			})
			return
		}
		// Entitled, but the license carries its own max_retention_days cap
		// below the server maximum.
		httpserver.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("retention is capped at %d days by your license", maxDays))
		return
	}
	updates := []struct {
		kind  settings.TelemetryType
		value *int
	}{
		{settings.TelemetryTraces, body.Traces},
		{settings.TelemetryLogs, body.Logs},
		{settings.TelemetryMetrics, body.Metrics},
	}
	for _, u := range updates {
		if u.value == nil {
			continue
		}
		if err := h.Settings.SetRetentionDays(r.Context(), u.kind, *u.value); err != nil {
			if errors.Is(err, settings.ErrInvalidRetention) {
				httpserver.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			h.Logger.Error("set retention failed", "err", err, "kind", u.kind)
			httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
			return
		}
	}
	if body.Audit != nil {
		auditMax, auditUnlocked := h.effectiveAuditRetentionMaxDays()
		if !auditUnlocked && *body.Audit > auditMax {
			httpserver.WriteJSON(w, http.StatusPaymentRequired, map[string]any{
				"error":    "enterprise_feature",
				"feature":  string(license.FeatureAuditLog),
				"message":  "Audit retention beyond 2 weeks requires a Sluicio Enterprise license.",
				"max_days": auditMax,
			})
			return
		}
		if *body.Audit > auditMax {
			httpserver.WriteError(w, http.StatusBadRequest, "audit_days exceeds the maximum")
			return
		}
		if err := h.Settings.SetAuditRetentionDays(r.Context(), *body.Audit); err != nil {
			if errors.Is(err, settings.ErrInvalidAuditRetention) {
				httpserver.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			h.Logger.Error("set audit retention failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
			return
		}
	}
	h.recordAudit(r, "retention.update", "cell_settings", "retention", auditRetentionMeta(body))

	// Push into ClickHouse synchronously. If this fails, we still
	// return success — the periodic enforcer will repair. The UI
	// shows the apply_warning so the user knows there's lag.
	var applyWarning string
	if h.RetentionEnforcer != nil {
		if err := h.RetentionEnforcer.ApplyOnce(r.Context()); err != nil {
			h.Logger.Warn("retention apply failed after PATCH", "err", err)
			applyWarning = "Saved. ClickHouse hasn't accepted the new TTL yet; the background enforcer will retry within the hour."
		}
	}
	// Re-read so the response carries the up-to-date last_applied_at.
	pol, err := h.Settings.GetRetention(r.Context())
	if err != nil {
		h.Logger.Warn("re-read after patch failed", "err", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	auditDays, err := h.Settings.GetAuditRetentionDays(r.Context())
	if err != nil {
		h.Logger.Warn("get audit retention failed", "err", err)
		auditDays = settings.AuditRetentionDefaultDays
	}
	auditMax, auditUnlocked := h.effectiveAuditRetentionMaxDays()
	resp := struct {
		retentionResponse
		ApplyWarning string `json:"apply_warning,omitempty"`
	}{
		retentionResponse: retentionResponse{
			Traces:            toEntry(pol.Traces.Days, pol.LastAppliedAt[settings.TelemetryTraces]),
			Logs:              toEntry(pol.Logs.Days, pol.LastAppliedAt[settings.TelemetryLogs]),
			Metrics:           toEntry(pol.Metrics.Days, pol.LastAppliedAt[settings.TelemetryMetrics]),
			MinDays:           settings.RetentionMinDays,
			MaxDays:           maxDays,
			LongRetention:     longUnlocked,
			AuditDays:         auditDays,
			AuditMaxDays:      auditMax,
			AuditConfigurable: auditUnlocked,
		},
		ApplyWarning: applyWarning,
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

// ── system settings ───────────────────────────────────────────────────

// systemSettingsResponse is the GET/PATCH shape for cell-wide system
// knobs: the environment label shown in the top nav, and the external
// ingest base URL the Ingest Keys UI bakes into exporter config.
type systemSettingsResponse struct {
	Environment   string `json:"environment"`
	IngestBaseURL string `json:"ingest_base_url"`
}

// systemSettingsRequest is the PATCH body. Every field is optional so
// knobs can be patched independently — supply only what you're changing.
type systemSettingsRequest struct {
	Environment   *string `json:"environment,omitempty"`
	IngestBaseURL *string `json:"ingest_base_url,omitempty"`
}

// systemSettings reads the current system settings into the response shape.
func (h *Handlers) systemSettings(r *http.Request) (systemSettingsResponse, error) {
	env, err := h.Settings.GetEnvironment(r.Context())
	if err != nil {
		return systemSettingsResponse{}, err
	}
	ingest, err := h.Settings.GetIngestBaseURL(r.Context())
	if err != nil {
		return systemSettingsResponse{}, err
	}
	return systemSettingsResponse{Environment: env, IngestBaseURL: ingest}, nil
}

// getSystemSettings: GET /api/v1/cell-settings/system — open to any
// signed-in user (the nav chip + Ingest Keys snippets need it).
func (h *Handlers) getSystemSettings(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "settings store unavailable")
		return
	}
	resp, err := h.systemSettings(r)
	if err != nil {
		h.Logger.Error("get system settings failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

// patchSystemSettings: PATCH /api/v1/cell-settings/system — admin-only
// (mux gate). Patches whichever knobs are present in the body.
func (h *Handlers) patchSystemSettings(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "settings store unavailable")
		return
	}
	var body systemSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Environment == nil && body.IngestBaseURL == nil {
		httpserver.WriteError(w, http.StatusBadRequest, "no settings to update")
		return
	}
	audit := map[string]any{}
	if body.Environment != nil {
		env := strings.TrimSpace(*body.Environment)
		if err := h.Settings.SetEnvironment(r.Context(), env); err != nil {
			if errors.Is(err, settings.ErrInvalidEnvironment) {
				httpserver.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			h.Logger.Error("set environment failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
			return
		}
		audit["environment"] = env
	}
	if body.IngestBaseURL != nil {
		url := strings.TrimSpace(*body.IngestBaseURL)
		if err := h.Settings.SetIngestBaseURL(r.Context(), url); err != nil {
			if errors.Is(err, settings.ErrInvalidIngestBaseURL) {
				httpserver.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			h.Logger.Error("set ingest base url failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
			return
		}
		audit["ingest_base_url"] = url
	}
	h.recordAudit(r, "system_settings.update", "cell_settings", "system", audit)
	resp, err := h.systemSettings(r)
	if err != nil {
		h.Logger.Error("re-read system settings failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

// ── SMTP (global transactional email) ─────────────────────────────────

// smtpResponse is the GET shape. The password is never returned — only
// whether one is set. `configured` reflects the *effective* transport
// (env + settings), so the UI can tell the admin whether email will work.
type smtpResponse struct {
	Host        string `json:"host"`
	Port        string `json:"port"`
	Username    string `json:"username"`
	From        string `json:"from"`
	FromName    string `json:"from_name"`
	PasswordSet bool   `json:"password_set"`
	Configured  bool   `json:"configured"`
}

// smtpRequest is the PATCH body. Password is a pointer: omitted = keep the
// stored one, "" = clear it, a value = set it. The rest replace outright.
type smtpRequest struct {
	Host     string  `json:"host"`
	Port     string  `json:"port"`
	Username string  `json:"username"`
	Password *string `json:"password"`
	From     string  `json:"from"`
	FromName string  `json:"from_name"`
}

// getSMTP: GET /api/v1/cell-settings/smtp (admin).
func (h *Handlers) getSMTP(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "settings store unavailable")
		return
	}
	s, err := h.Settings.GetSMTP(r.Context())
	if err != nil {
		h.Logger.Error("get smtp failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, smtpResponse{
		Host:        s.Host,
		Port:        s.Port,
		Username:    s.Username,
		From:        s.From,
		FromName:    s.FromName,
		PasswordSet: s.Password != "",
		Configured:  h.Mail != nil && h.Mail.Configured(r.Context()),
	})
}

// patchSMTP: PATCH /api/v1/cell-settings/smtp (admin).
func (h *Handlers) patchSMTP(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "settings store unavailable")
		return
	}
	var body smtpRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	cur, err := h.Settings.GetSMTP(r.Context())
	if err != nil {
		h.Logger.Error("get smtp failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	next := settings.SMTPSettings{
		Host:     strings.TrimSpace(body.Host),
		Port:     strings.TrimSpace(body.Port),
		Username: strings.TrimSpace(body.Username),
		From:     strings.TrimSpace(body.From),
		FromName: strings.TrimSpace(body.FromName),
		Password: cur.Password, // keep by default
	}
	if body.Password != nil {
		next.Password = *body.Password // set or clear
	}
	if err := h.Settings.SetSMTP(r.Context(), next); err != nil {
		h.Logger.Error("set smtp failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "smtp.update", "cell_settings", "smtp", map[string]any{"host": next.Host, "from": next.From})
	httpserver.WriteJSON(w, http.StatusOK, smtpResponse{
		Host:        next.Host,
		Port:        next.Port,
		Username:    next.Username,
		From:        next.From,
		FromName:    next.FromName,
		PasswordSet: next.Password != "",
		Configured:  h.Mail != nil && h.Mail.Configured(r.Context()),
	})
}

// testSMTP: POST /api/v1/cell-settings/smtp/test (admin). Sends a test
// message to the given address (or the caller's email) using the effective
// transport, so an admin can confirm SMTP works before relying on it.
func (h *Handlers) testSMTP(w http.ResponseWriter, r *http.Request) {
	if h.Mail == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "mailer unavailable")
		return
	}
	var body struct {
		To string `json:"to"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	to := strings.TrimSpace(body.To)
	if to == "" {
		to = middleware.Principal(r).Email
	}
	if to == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "no recipient — pass {\"to\":\"…\"}")
		return
	}
	err := h.Mail.Send(r.Context(), []string{to},
		"Sluicio SMTP test",
		"This is a test email from Sluicio. If you received it, your SMTP settings are working.")
	if err != nil {
		if errors.Is(err, mail.ErrNotConfigured) {
			httpserver.WriteError(w, http.StatusBadRequest, "SMTP is not configured")
			return
		}
		h.Logger.Warn("smtp test send failed", "err", err)
		httpserver.WriteError(w, http.StatusBadGateway, "send failed: "+err.Error())
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"sent": true, "to": to})
}

// ── security policy (org-wide MFA enforcement, Enterprise) ────────────

// getSecuritySettings: GET /api/v1/cell-settings/security (admin). Returns
// the MFA-required policy + whether the Enterprise entitlement that gates it
// is active (so the UI can show an upsell).
func (h *Handlers) getSecuritySettings(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "settings store unavailable")
		return
	}
	req, err := h.Settings.GetMFARequired(r.Context())
	if err != nil {
		h.Logger.Error("get security settings failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"mfa_required":        req,
		"mfa_policy_entitled": h.featureEntitled(license.FeatureMFAPolicy),
	})
}

// patchSecuritySettings: PATCH /api/v1/cell-settings/security (admin +
// mfa_policy entitlement gated at the route). Toggles MFA enforcement.
func (h *Handlers) patchSecuritySettings(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "settings store unavailable")
		return
	}
	var body struct {
		MFARequired *bool `json:"mfa_required"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.MFARequired == nil {
		httpserver.WriteError(w, http.StatusBadRequest, "mfa_required is required")
		return
	}
	// Enabling the requirement while not enrolled yourself would lock you
	// out the moment the response lands (the enforcement wrap gates every
	// endpoint, this one included). Make the operator go first.
	if *body.MFARequired {
		p := middleware.Principal(r)
		if p.UserID != nil {
			if st, err := h.Identity.MFAStatus(r.Context(), *p.UserID); err == nil && !st.Enabled {
				httpserver.WriteError(w, http.StatusBadRequest,
					"enable two-factor authentication on your own account first — otherwise this policy would lock you out immediately")
				return
			}
		}
	}
	if err := h.Settings.SetMFARequired(r.Context(), *body.MFARequired); err != nil {
		h.Logger.Error("set mfa_required failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.setMFAPolicyCache(*body.MFARequired)
	h.recordAudit(r, "security.mfa_required.update", "cell_settings", "security", map[string]any{"mfa_required": *body.MFARequired})
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"mfa_required":        *body.MFARequired,
		"mfa_policy_entitled": true,
	})
}

// mfaEnrollmentRequired reports whether the given user must enrol in MFA
// before using the app: org policy is on, the entitlement is active, and the
// user hasn't enabled MFA yet. Fails open (false) on any error so a glitch
// never locks everyone out.
func (h *Handlers) mfaEnrollmentRequired(ctx context.Context, userID uuid.UUID) bool {
	if h.Settings == nil || !h.featureEntitled(license.FeatureMFAPolicy) {
		return false
	}
	if !h.mfaPolicyOn(ctx) {
		return false
	}
	st, err := h.Identity.MFAStatus(ctx, userID)
	if err != nil {
		return false
	}
	return !st.Enabled
}

// auditRetentionMeta captures which retention fields a PATCH changed, for the
// audit entry. Only the fields actually present in the request are recorded.
func auditRetentionMeta(body retentionRequest) map[string]any {
	m := map[string]any{}
	if body.Traces != nil {
		m["traces_days"] = *body.Traces
	}
	if body.Logs != nil {
		m["logs_days"] = *body.Logs
	}
	if body.Metrics != nil {
		m["metrics_days"] = *body.Metrics
	}
	if body.Audit != nil {
		m["audit_days"] = *body.Audit
	}
	return m
}
