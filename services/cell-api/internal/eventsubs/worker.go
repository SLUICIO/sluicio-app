// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package eventsubs

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
)

// Worker drains event_jobs to their webhook destinations — the same
// claim/backoff loop shape as the alert delivery worker, sharing the
// exact webhook contract (HMAC signing, CloudEvents envelope) via
// alerting's exported helpers. The channel's config.format decides the
// payload shape: "cloudevents" → CE 1.0 structured mode; default → the
// canonical flat event JSON.
type Worker struct {
	Store *Store
	// ResolveChannel loads a subscription's destination channel (the
	// subscription row only stores the id; config lives on the channel).
	ResolveChannel func(ctx context.Context, subscriptionID uuid.UUID) (url, secret, format string, err error)
	Log            *slog.Logger

	// Poll is the queue poll interval (default 5s, like alert delivery).
	Poll time.Duration
	// Retention for finished jobs (default 72h).
	Retention time.Duration

	client    *http.Client
	lastPrune time.Time
}

// Run loops until the context ends.
func (w *Worker) Run(ctx context.Context) {
	if w.Poll <= 0 {
		w.Poll = 5 * time.Second
	}
	if w.Retention <= 0 {
		w.Retention = 72 * time.Hour
	}
	w.client = &http.Client{Timeout: 15 * time.Second}
	t := time.NewTicker(w.Poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.sweep(ctx)
		}
	}
}

func (w *Worker) sweep(ctx context.Context) {
	jobs, err := w.Store.ClaimDue(ctx, 50)
	if err != nil {
		w.Log.Warn("event worker: claim failed", "err", err)
		return
	}
	for _, j := range jobs {
		err := w.deliver(ctx, j)
		if merr := w.Store.MarkResult(ctx, j.ID, j.Attempts+1, err); merr != nil {
			w.Log.Warn("event worker: mark failed", "job", j.ID, "err", merr)
		}
		if err != nil {
			w.Log.Info("event delivery failed", "job", j.ID, "type", j.EventType, "attempt", j.Attempts+1, "err", err)
		}
	}
	// Opportunistic hourly prune — events are notifications, not records.
	if time.Since(w.lastPrune) > time.Hour {
		w.lastPrune = time.Now()
		if n, err := w.Store.PruneFinished(ctx, w.Retention); err == nil && n > 0 {
			w.Log.Info("event jobs pruned", "removed", n)
		}
	}
}

func (w *Worker) deliver(ctx context.Context, j Job) error {
	url, secret, format, err := w.ResolveChannel(ctx, j.SubscriptionID)
	if err != nil {
		return fmt.Errorf("resolve channel: %w", err)
	}
	occurred := j.OccurredAt.UTC().Format(time.RFC3339)
	var body any
	contentType := ""
	if strings.EqualFold(strings.TrimSpace(format), alerting.FormatCloudEvents) {
		body = alerting.CloudEventEnvelope(j.EventID, j.EventType, j.Subject, occurred, j.Payload)
		contentType = alerting.CloudEventsContentType
	} else {
		// Canonical flat shape — same fields, no envelope ceremony.
		body = map[string]any{
			"event":   j.EventType,
			"id":      j.EventID,
			"time":    occurred,
			"subject": j.Subject,
			"source":  "sluicio",
			"data":    j.Payload,
		}
	}
	return alerting.PostJSONSigned(ctx, w.client, url, body, secret, contentType)
}
