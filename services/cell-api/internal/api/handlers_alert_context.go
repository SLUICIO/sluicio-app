// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/alerting"
)

// ResolveAlertContext is the alerting.SetAlertContextResolver implementation:
// it enriches a firing notification with the live service / integration
// details + their user-defined metadata, read fresh at delivery time. Only
// Service / Integration are returned (delivery.go owns Alert / Rule / Org /
// Check). Best-effort — a failed lookup just leaves that block nil, never
// blocking delivery.
func (h *Handlers) ResolveAlertContext(ctx context.Context, job alerting.DeliveryJob) (*alerting.AlertContext, error) {
	out := &alerting.AlertContext{}
	orgID := job.Channel.OrganizationID

	// Service block — when the rule is bound to a service.
	if name := strings.TrimSpace(job.Labels["service_name"]); name != "" {
		sf := &alerting.ServiceFacts{Name: name, Status: job.Labels["service_status"]}
		if meta, err := h.serviceMetadataValues(ctx, name); err == nil && len(meta) > 0 {
			sf.Metadata = meta
		}
		out.Service = sf
	}

	// Integration block — when the rule is bound to an integration.
	if job.IntegrationID != nil {
		if integ, err := h.Integrations.Get(ctx, orgID, *job.IntegrationID); err == nil {
			inf := &alerting.IntegrationFacts{Name: integ.Name, Slug: integ.Slug}
			if members, mErr := h.Catalog.IntegrationServices(ctx, *job.IntegrationID); mErr == nil {
				inf.Services = members
			}
			if meta, mErr := h.integrationMetadataValues(ctx, *job.IntegrationID); mErr == nil && len(meta) > 0 {
				inf.Metadata = meta
			}
			out.Integration = inf
		}
	}
	return out, nil
}

// defaultEmailTemplate returns the org's saved default email subject + body
// (Liquid) from cell-settings, or two empty strings when none is set (then the
// alerting package falls back to its built-in default). Wired via
// alerting.SetDefaultEmailTemplateResolver.
// DefaultEmailTemplate is the alerting.SetDefaultEmailTemplateResolver
// implementation: the org's saved default email subject/body, or empty (the
// alerting package then uses its built-in default).
func (h *Handlers) DefaultEmailTemplate(ctx context.Context) (subject, body string) {
	if h.Settings == nil {
		return "", ""
	}
	subject, body, _ = h.Settings.GetAlertEmailTemplate(ctx)
	return subject, body
}

// previewAlertTemplate: POST /api/v1/alert-templates/preview
//
// Renders a notification (email HTML or webhook JSON) against a representative
// sample firing context, applying the given content toggles + optional inline
// email template. No send — powers the template editor's live preview.
func (h *Handlers) previewAlertTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind    string                       `json:"kind"`
		Content alerting.NotificationContent `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := alerting.ValidateLiquid(req.Content.EmailSubject); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid email subject: "+err.Error())
		return
	}
	if err := alerting.ValidateLiquid(req.Content.EmailBody); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid email body: "+err.Error())
		return
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = alerting.ChannelEmail
	}
	subject, body := alerting.RenderForPreview(r.Context(), kind, req.Content, alerting.SampleAlertContext())
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"subject": subject, "body": body})
}

// getAlertEmailTemplate: GET /api/v1/alert-email-template
// Returns the org default email subject/body and the built-in defaults so the
// editor can show what's in effect + offer a reset.
func (h *Handlers) getAlertEmailTemplate(w http.ResponseWriter, r *http.Request) {
	sub, body, _ := h.Settings.GetAlertEmailTemplate(r.Context())
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{
		"subject":         sub,
		"body":            body,
		"default_subject": alerting.DefaultEmailSubject,
		"default_body":    alerting.DefaultEmailBody,
	})
}

// putAlertEmailTemplate: PUT /api/v1/alert-email-template
// Persists the org default email subject/body (Liquid; either may be empty to
// fall back to the built-in default).
func (h *Handlers) putAlertEmailTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := alerting.ValidateLiquid(req.Subject); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid subject: "+err.Error())
		return
	}
	if err := alerting.ValidateLiquid(req.Body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := h.Settings.SetAlertEmailTemplate(r.Context(), req.Subject, req.Body); err != nil {
		h.Logger.Error("set alert email template failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "alert_email_template.updated", "cell_settings", "alert_email_template", nil)
	w.WriteHeader(http.StatusNoContent)
}
