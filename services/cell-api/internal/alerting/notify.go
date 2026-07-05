// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package alerting

import (
	"context"
	"fmt"
	"net/http"
	"sort"
)

// Message is the rendered, channel-agnostic notification a Notifier
// sends. Subject + Body are pre-rendered upstream — today from the
// built-in summary (see messageFromJob), later from per-rule
// title/body templates. A Notifier's only job is to wrap these in its
// channel-specific envelope (SMTP headers, Slack JSON, a webhook
// payload, …); it never re-derives the wording.
type Message struct {
	State    string            // "firing" | "resolved"
	Severity Severity          // promoted from Labels["severity"] for convenience
	Subject  string            // one-line title (used as the email subject, etc.)
	Body     string            // main notification text (plaintext)
	BodyHTML string            // optional HTML body (email sends multipart when set)
	Payload  map[string]any    // optional structured payload (webhook sends this when set)
	Labels   map[string]string // denormalised rule context (rule_id, metric, …)
	Config   map[string]string // the channel's config (smtp_host, url, …)
}

// Notifier delivers a Message to one channel kind. Adding a new
// channel type is: implement this interface + Register() it in an
// init() — no central switch to edit. That's the extension point the
// SMTP/webhook/Slack/PagerDuty notifiers below all plug into, and the
// slot a future Teams / SendGrid notifier drops into.
type Notifier interface {
	// Kind is the channel.kind string this notifier handles ("email",
	// "webhook", …). Must be unique across the registry.
	Kind() string
	// Send delivers msg. A non-nil error is treated as retryable by the
	// delivery worker (it re-queues with backoff up to maxAttempts).
	Send(ctx context.Context, client *http.Client, msg Message) error
}

// notifiers is the kind → Notifier registry. Populated by Register()
// from each notifier file's init(). Reads are lock-free because
// registration only happens at init (before any delivery runs).
var notifiers = map[string]Notifier{}

// Register adds a Notifier to the registry. Panics on a duplicate kind
// — that's a programming error (two notifiers claiming one kind), and
// it surfaces at startup rather than silently shadowing one.
func Register(n Notifier) {
	kind := n.Kind()
	if _, dup := notifiers[kind]; dup {
		panic(fmt.Sprintf("alerting: duplicate notifier for kind %q", kind))
	}
	notifiers[kind] = n
}

// notifierFor looks up the registered notifier for a channel kind.
func notifierFor(kind string) (Notifier, bool) {
	n, ok := notifiers[kind]
	return n, ok
}

// SendMessageToChannels delivers a one-off, already-rendered message to
// each channel immediately, reusing the registered notifiers — bypassing
// the instance/job queue. Used by the error notifier, which fires outside
// the rule → instance → job pipeline. Best-effort: every channel is
// attempted and the first error returned; a channel kind with no
// registered notifier is skipped. severity drives the styling/subject.
func SendMessageToChannels(ctx context.Context, client *http.Client, channels []NotificationChannel, severity, subject, body string) error {
	var firstErr error
	labels := map[string]string{"severity": severity}
	for _, ch := range channels {
		n, ok := notifierFor(ch.Kind)
		if !ok {
			continue
		}
		msg := Message{
			State:    "firing",
			Severity: Severity(severity),
			Subject:  subject,
			Body:     body,
			Labels:   labels,
			Config:   ch.Config,
		}
		if err := n.Send(ctx, client, msg); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RegisteredKinds returns the channel kinds with a notifier, sorted.
// Lets the API surface "which channel types can I create?" from the
// registry rather than a duplicated hardcoded list.
func RegisteredKinds() []string {
	out := make([]string, 0, len(notifiers))
	for k := range notifiers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
