// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// exceptionLookback is how far back a firing notification looks for the
// exception to name (#42). Recent enough to belong to this firing, wide
// enough to cover a check's evaluation window.
const exceptionLookback = 15 * time.Minute

// ResolveAlertContext is the alerting.SetAlertContextResolver implementation:
// it enriches a firing notification with the live service / integration
// details + their user-defined metadata, read fresh at delivery time, and
// for a firing notification the latest exception recorded in the rule's
// scope. Only Service / Integration / Exception are returned (delivery.go
// owns Alert / Rule / Org / Check). Best-effort — a failed lookup just leaves that block nil, never
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

	// Exception: what the scope's failing spans last recorded. Scoped
	// like the rule: a service-bound rule reads that service, an
	// integration-bound one its member services through its slice, and
	// an org-wide rule names nothing rather than an exception from an
	// arbitrary corner of the org.
	var (
		excServices []string
		excGroups   [][]store.LogAttrFilter
	)

	// Integration block — when the rule is bound to an integration.
	if job.IntegrationID != nil {
		if integ, err := h.Integrations.Get(ctx, orgID, *job.IntegrationID); err == nil {
			inf := &alerting.IntegrationFacts{Name: integ.Name, Slug: integ.Slug}
			if members, mErr := h.Catalog.IntegrationServices(ctx, *job.IntegrationID); mErr == nil {
				inf.Services = members
				excServices = members
				excGroups = h.integrationGroups(ctx, *job.IntegrationID)
			}
			if meta, mErr := h.integrationMetadataValues(ctx, *job.IntegrationID); mErr == nil && len(meta) > 0 {
				inf.Metadata = meta
			}
			out.Integration = inf
		}
	} else if out.Service != nil {
		excServices = []string{out.Service.Name}
	}
	if job.State == "firing" && h.Store != nil && len(excServices) > 0 {
		now := time.Now()
		if ex, ok, err := h.Store.LatestException(ctx, excServices, now.Add(-exceptionLookback), now, excGroups); err != nil {
			h.Logger.Warn("alert context: latest exception failed", "err", err)
		} else if ok {
			out.Exception = &alerting.ExceptionFacts{
				Type:       ex.Type,
				Message:    ex.Message,
				Service:    ex.ServiceName,
				TraceID:    ex.TraceID,
				Stacktrace: ex.Stacktrace,
			}
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
		// BodyTemplate previews a webhook channel's structural body
		// template. Ignored for the other kinds.
		BodyTemplate string `json:"body_template"`
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
	if err := alerting.ValidateLiquid(req.Content.SlackTitle); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid slack title: "+err.Error())
		return
	}
	if err := alerting.ValidateLiquid(req.Content.SlackBody); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid slack body: "+err.Error())
		return
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = alerting.ChannelEmail
	}
	if kind == alerting.ChannelWebhook {
		// The body template has its own grammar (structural JSON, not
		// Liquid), and its errors are the ones an author most needs to
		// see: a bad path here means the receiver gets null.
		body, err := alerting.RenderWebhookPreview(r.Context(), req.BodyTemplate, req.Content, alerting.SampleAlertContext())
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		httpserver.WriteJSON(w, http.StatusOK, map[string]string{"subject": "", "body": body})
		return
	}
	subject, body := alerting.RenderForPreview(r.Context(), kind, req.Content, alerting.SampleAlertContext())
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"subject": subject, "body": body})
}
