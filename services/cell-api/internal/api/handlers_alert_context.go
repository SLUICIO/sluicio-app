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
	"github.com/sluicio/sluicio-app/services/cell-api/internal/envctx"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
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
		// Which integrations the service is in, so a service-bound alert
		// still names the integration it concerns.
		if job.IntegrationID == nil && h.Resolver != nil {
			if integs, err := h.Resolver.IntegrationsFor(ctx, orgID, name); err == nil {
				for _, in := range integs {
					sf.Integrations = append(sf.Integrations, in.Name)
				}
			}
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
		h.resolveTriggeringMessage(ctx, job, excServices, excGroups, out)
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

// triggerLookbackFloor is the least a firing notification looks back for
// the message it is about: a check evaluated a minute ago over a one
// minute window may have counted a message from just before.
const triggerLookbackFloor = 15 * time.Minute

// resolveTriggeringMessage fills out.Message with the newest message in
// the rule's scope that met the rule's filter, and what it carried for
// each of the filter's keys. A per-environment alert reads its own
// environment; a check read across environments takes the environment
// from that message. Best-effort, like the rest of the enrichment.
func (h *Handlers) resolveTriggeringMessage(ctx context.Context, job alerting.DeliveryJob, services []string, groups [][]store.LogAttrFilter, out *alerting.AlertContext) {
	f, ok := alerting.RuleFilterOf(job.RuleSignal, job.RuleKind, job.RuleSpec)
	if !ok {
		return
	}
	// Keys go into the statement as text; a rule saved through the API
	// already passed this check, and one that did not is skipped rather
	// than trusted.
	var keys []string
	for _, k := range f.Keys() {
		if attrKeyRe.MatchString(k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	// A log rule on an integration reads its log sources too (#43), as
	// the check itself does.
	if f.Logs && job.IntegrationID != nil {
		if svcs, g, err := h.integrationSignalScope(ctx, *job.IntegrationID, services, identity.SignalLogs); err == nil {
			services, groups = svcs, g
		}
	}
	if env, ok := job.Labels[alerting.EnvironmentLabel]; ok {
		groups = store.WithEnvironment(groups, env)
	}
	window := f.Window
	if window < triggerLookbackFloor {
		window = triggerLookbackFloor
	}
	now := time.Now()
	q := store.TriggerQuery{
		Services: services, From: now.Add(-window), To: now,
		Attrs: ruleAttrsToStore(f.Attrs), Groups: groups, ErrorsOnly: f.ErrorsOnly, Keys: keys,
	}
	read := h.Store.LatestTriggeringSpan
	if f.Logs {
		read = h.Store.LatestTriggeringLog
	}
	rec, found, err := read(ctx, q)
	if err != nil {
		h.Logger.Warn("alert context: triggering message failed", "err", err)
		return
	}
	if !found || len(rec.Values) == 0 {
		return
	}
	out.Message = &alerting.MessageFacts{TraceID: rec.TraceID, Service: rec.ServiceName, Attributes: rec.Values}
	if _, perEnv := job.Labels[alerting.EnvironmentLabel]; !perEnv && rec.Environment != "" {
		out.Alert.Environment = envctx.Display(rec.Environment)
	}
}
