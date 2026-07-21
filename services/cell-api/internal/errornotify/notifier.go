// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package errornotify sends one notification when a service's
// unacknowledged errors open, so an error isn't just persisted on the
// Errors page but actually reaches someone. It runs on a periodic loop:
// scan error traces per service, and for any service whose latest error
// post-dates both its clear-errors watermark (so it's unacknowledged) and
// its notify watermark (so we haven't already paged about this batch),
// resolve channels via routing (integration → global default) and deliver.
// Acknowledging (clearing) a service lets the next NEW error notify again.
package errornotify

import (
	"context"
	"fmt"
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
	catalog  *catalog.Store
	org      uuid.UUID
	log      *slog.Logger
	interval time.Duration
	lookback time.Duration
	client   *http.Client
}

func New(ch *store.Store, acks *erroracks.Store, routes *notifyprofiles.Store, channels *alerting.Store, cat *catalog.Store, org uuid.UUID, log *slog.Logger, interval time.Duration) *Notifier {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Notifier{
		ch: ch, acks: acks, routes: routes, channels: channels, catalog: cat,
		org: org, log: log, interval: interval,
		lookback: 30 * 24 * time.Hour,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Run evaluates once on start, then on the interval, until ctx is done.
func (n *Notifier) Run(ctx context.Context) {
	n.once(ctx)
	t := time.NewTicker(n.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.once(ctx)
		}
	}
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
		// Route: the service's integration (first), else the global default.
		var integ *uuid.UUID
		if ids, ierr := n.catalog.IntegrationsForService(ctx, n.org, st.ServiceName); ierr == nil && len(ids) > 0 {
			integ = &ids[0]
		}
		chIDs, rerr := n.routes.Resolve(ctx, n.org, nil, integ, nil)
		if rerr != nil {
			n.log.Warn("errornotify: resolve channels failed", "service", st.ServiceName, "err", rerr)
			continue
		}
		if len(chIDs) == 0 {
			// Nothing configured anywhere — leave un-notified so it pages as
			// soon as a default/integration channel is set up.
			continue
		}
		targets := make([]alerting.NotificationChannel, 0, len(chIDs))
		for _, id := range chIDs {
			if c, ok := chByID[id]; ok {
				targets = append(targets, c)
			}
		}
		if len(targets) == 0 {
			continue
		}
		// Lead the title + body with the deployment context (environment +
		// company), matching the alert-engine notifications:
		//   Sluicio {env} - Errors detected on {service} - {company}
		env, company := alerting.DeploymentContext(ctx)
		subject := alerting.NotifSubject(env, company, fmt.Sprintf("Errors detected on %s", st.ServiceName))
		body := fmt.Sprintf(
			"%d unacknowledged error trace(s) on service %q. Latest at %s. Review and acknowledge on the Errors page in Sluicio.",
			st.ErrorTraces, st.ServiceName, st.LastErrorAt.UTC().Format(time.RFC1123),
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
			body += "\n\nView in Sluicio: " + link
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
