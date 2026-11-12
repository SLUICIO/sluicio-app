// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package errornotify sends one notification when a service's
// unacknowledged errors open, so an error isn't just persisted on the
// Errors page but actually reaches someone. It runs on a periodic loop:
// scan error traces per service, and for any service whose latest error
// post-dates both its clear-errors watermark (so it's unacknowledged) and
// its notify watermark (so we haven't already paged about this batch),
// resolve channels through the profiles of the service's integrations that
// asked for it (notify_unacknowledged_errors, off by default) and deliver.
// Acknowledging (clearing) a service lets the next NEW error notify again.
package errornotify

import (
	"context"
	"fmt"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/cellhealth"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/catalog"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/erroracks"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/notifyprofiles"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
)

// Notifier wires the per-service error scan to the routing + delivery.
type Notifier struct {
	ch       *store.Store
	acks     *erroracks.Store
	routes   *notifyprofiles.Store
	channels *alerting.Store
	org      uuid.UUID
	log      *slog.Logger
	interval time.Duration
	lookback time.Duration
	client   *http.Client
	// EmitEvent, when set, publishes com.sluicio.errors.opened for each
	// page this notifier sends — the event-subscriptions hook (issue #4).
	// Optional; fire-and-forget.
	EmitEvent func(ctx context.Context, serviceName string, errorTraces uint64, integrationID *uuid.UUID)
	// IntegrationsFor, when set, says which integrations a service
	// belongs to from the integrations' service rules, at once. The
	// catalog's membership (the fallback) is written by its reconciler,
	// up to a pass after a service first sends; a brand-new service's
	// first errors scanned in between were routed without its
	// integration, to the org default (and marked sent, so its
	// integration's channels never heard) or to nobody until a later tick.
	IntegrationsFor func(ctx context.Context, serviceName string) ([]uuid.UUID, error)
	// NotifyingIntegrations, when set, keeps the integrations that have
	// asked to be notified about unacknowledged errors
	// (notify_unacknowledged_errors). Off by default: unset, or for an
	// integration that has not turned it on, nobody is notified, and the
	// errors stay on the Errors page.
	NotifyingIntegrations func(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error)
	// catalogIntegrations is the catalog's membership of a service.
	catalogIntegrations func(ctx context.Context, serviceName string) ([]uuid.UUID, error)
}

func New(ch *store.Store, acks *erroracks.Store, routes *notifyprofiles.Store, channels *alerting.Store, cat *catalog.Store, org uuid.UUID, log *slog.Logger, interval time.Duration) *Notifier {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Notifier{
		ch: ch, acks: acks, routes: routes, channels: channels,
		org: org, log: log, interval: interval,
		lookback: 30 * 24 * time.Hour,
		client:   &http.Client{Timeout: 10 * time.Second},
		catalogIntegrations: func(ctx context.Context, service string) ([]uuid.UUID, error) {
			return cat.IntegrationsForService(ctx, org, service)
		},
	}
}

// Run evaluates once on start, then on the interval, until ctx is done.
func (n *Notifier) Run(ctx context.Context) {
	n.once(ctx)
	t := time.NewTicker(n.interval)
	cellhealth.Register("error-notifier", n.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.once(ctx)
			// End of cycle, not start: a loop wedged inside its
			// own body is exactly what this catches.
			cellhealth.Beat("error-notifier")
		}
	}
}

// integrationsOf are the integrations a service belongs to: those its
// service rules put it in, else the catalog's membership; none for a
// service in no integration.
func (n *Notifier) integrationsOf(ctx context.Context, service string) []uuid.UUID {
	if n.IntegrationsFor != nil {
		if ids, err := n.IntegrationsFor(ctx, service); err == nil && len(ids) > 0 {
			return ids
		} else if err != nil {
			n.log.Warn("errornotify: integration rules failed; using the catalog", "service", service, "err", err)
		}
	}
	if ids, err := n.catalogIntegrations(ctx, service); err == nil && len(ids) > 0 {
		return ids
	}
	return nil
}

// notifying are those of ids that asked to be notified about
// unacknowledged errors. ok is false when the question cannot be answered,
// so the batch waits for a later tick rather than being settled either way.
func (n *Notifier) notifying(ctx context.Context, ids []uuid.UUID) (on []uuid.UUID, ok bool) {
	if len(ids) == 0 || n.NotifyingIntegrations == nil {
		return nil, true
	}
	on, err := n.NotifyingIntegrations(ctx, ids)
	if err != nil {
		n.log.Warn("errornotify: reading the integrations' notify setting failed", "err", err)
		return nil, false
	}
	return on, true
}

func (n *Notifier) once(ctx context.Context) {
	to := time.Now().UTC()
	from := to.Add(-n.lookback)
	stats, err := n.ch.ErrorTraceStatsByService(ctx, from, to)
	if err != nil {
		n.log.Warn("errornotify: error-stats scan failed", "err", err)
		return
	}
	if len(stats) == 0 {
		return
	}
	acks, err := n.acks.GetAll(ctx, n.org)
	if err != nil {
		n.log.Warn("errornotify: load acks failed", "err", err)
		return
	}
	notified, err := n.routes.NotifiedStates(ctx, n.org)
	if err != nil {
		n.log.Warn("errornotify: load notify watermarks failed", "err", err)
		return
	}
	channels, err := n.channels.ListChannels(ctx, n.org)
	if err != nil {
		n.log.Warn("errornotify: list channels failed", "err", err)
		return
	}
	chByID := make(map[uuid.UUID]alerting.NotificationChannel, len(channels))
	for _, c := range channels {
		chByID[c.ID] = c
	}

	for _, st := range stats {
		watermark := acks[st.ServiceName].AcknowledgedUntil // zero if never cleared
		// Acknowledged / no new error → not open.
		if !st.LastErrorAt.After(watermark) {
			continue
		}
		// Already notified about this open batch (notified after the last
		// clear) → don't re-page until it's acknowledged + re-opens.
		if ns, ok := notified[st.ServiceName]; ok && ns.NotifiedAt.After(watermark) {
			continue
		}
		// Only integrations that asked to be told (off by default), through
		// each one's profile. Nobody asked: the batch is marked so it is
		// not reconsidered every tick, and the errors stay on the Errors
		// page; a later new error after acknowledging is weighed afresh.
		all := n.integrationsOf(ctx, st.ServiceName)
		on, ok := n.notifying(ctx, all)
		if !ok {
			continue
		}
		var first *uuid.UUID
		if len(all) > 0 {
			first = &all[0]
		}
		if len(on) == 0 {
			if n.EmitEvent != nil {
				n.EmitEvent(ctx, st.ServiceName, st.ErrorTraces, first)
			}
			if mErr := n.routes.MarkNotified(ctx, n.org, st.ServiceName, st.LastErrorAt); mErr != nil {
				n.log.Warn("errornotify: mark seen failed", "service", st.ServiceName, "err", mErr)
			}
			continue
		}
		integ := &on[0]
		seen := map[uuid.UUID]bool{}
		var targets []alerting.NotificationChannel
		resolveFailed := false
		for i := range on {
			chIDs, rerr := n.routes.Resolve(ctx, n.org, nil, &on[i], nil)
			if rerr != nil {
				n.log.Warn("errornotify: resolve channels failed", "service", st.ServiceName, "err", rerr)
				resolveFailed = true
				break
			}
			for _, id := range chIDs {
				if c, ok := chByID[id]; ok && !seen[id] {
					seen[id] = true
					targets = append(targets, c)
				}
			}
		}
		if resolveFailed || len(targets) == 0 {
			// Nothing configured: leave un-notified so it pages as soon as
			// a channel is set up.
			continue
		}
		// Lead the title + body with the deployment context (environment +
		// company), matching the alert-engine notifications:
		//   {product} {env} - Errors detected on {service} - {company}
		// product is the cell's wordmark: these mails leave the building,
		// so on a white-labelled cell they carry the partner's name.
		env, company := alerting.DeploymentContext(ctx)
		product := alerting.ProductName(ctx)
		subject := alerting.NotifSubject(product, env, company, fmt.Sprintf("Errors detected on %s", st.ServiceName))
		body := fmt.Sprintf(
			"%d unacknowledged error trace(s) on service %q. Latest at %s. Review and acknowledge on the Errors page in %s.",
			st.ErrorTraces, st.ServiceName, st.LastErrorAt.UTC().Format(time.RFC1123), product,
		)
		if header := alerting.ContextHeader(env, company); header != "" {
			body = header + "\n\n" + body
		}
		// Deep-link to the integration's Errors page when we know it, else
		// the global stuck-messages view. Omitted if no public URL is set.
		linkPath := "/stuck"
		if integ != nil {
			linkPath = "/integrations/" + integ.String() + "/errors"
		}
		if link := alerting.Link(linkPath); link != "" {
			body += "\n\nView in " + product + ": " + link
		}
		if n.EmitEvent != nil {
			n.EmitEvent(ctx, st.ServiceName, st.ErrorTraces, integ)
		}
		if sendErr := alerting.SendMessageToChannels(ctx, n.client, targets, "critical", subject, body); sendErr != nil {
			// Don't watermark on failure — retry next tick.
			n.log.Warn("errornotify: send failed", "service", st.ServiceName, "err", sendErr)
			continue
		}
		if mErr := n.routes.MarkNotified(ctx, n.org, st.ServiceName, st.LastErrorAt); mErr != nil {
			n.log.Warn("errornotify: mark notified failed", "service", st.ServiceName, "err", mErr)
		}
		n.log.Info("errornotify: sent", "service", st.ServiceName, "errors", st.ErrorTraces, "channels", len(targets))
	}
}
