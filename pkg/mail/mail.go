// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package mail is a small SMTP sender for transactional email (password
// resets, and later invites / notifications). It's separate from the
// per-channel SMTP in the alerting package: that one is user-configured per
// alert channel; this one is the single global mail transport for the
// product itself.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Config is the global SMTP transport configuration. Host + From are the
// minimum needed to send; Username/Password enable PLAIN auth (over STARTTLS
// when the server advertises it).
type Config struct {
	Host     string
	Port     string // default "587"
	Username string
	Password string
	From     string // envelope + header From address
	FromName string // optional display name
}

// Configured reports whether enough is set to attempt a send.
func (c Config) Configured() bool {
	return strings.TrimSpace(c.Host) != "" && strings.TrimSpace(c.From) != ""
}

// Sender resolves the effective config at send time (so settings edited in
// the UI take effect without a restart) and delivers messages over SMTP.
type Sender struct {
	resolve func(context.Context) (Config, error)
}

// NewSender builds a Sender around a config resolver.
func NewSender(resolve func(context.Context) (Config, error)) *Sender {
	return &Sender{resolve: resolve}
}

// ErrNotConfigured is returned when SMTP isn't set up. Callers translate it
// to a clear "email isn't configured" message rather than a generic 500.
var ErrNotConfigured = fmt.Errorf("mail: SMTP is not configured")

// Configured reports whether a usable transport is currently configured.
func (s *Sender) Configured(ctx context.Context) bool {
	if s == nil || s.resolve == nil {
		return false
	}
	cfg, err := s.resolve(ctx)
	return err == nil && cfg.Configured()
}

// Send delivers a plain-text UTF-8 email to one or more recipients.
func (s *Sender) Send(ctx context.Context, to []string, subject, body string) error {
	if s == nil || s.resolve == nil {
		return ErrNotConfigured
	}
	cfg, err := s.resolve(ctx)
	if err != nil {
		return fmt.Errorf("mail: resolve config: %w", err)
	}
	if !cfg.Configured() {
		return ErrNotConfigured
	}
	recipients := make([]string, 0, len(to))
	for _, r := range to {
		if r = strings.TrimSpace(r); r != "" {
			recipients = append(recipients, r)
		}
	}
	if len(recipients) == 0 {
		return fmt.Errorf("mail: no recipients")
	}
	port := strings.TrimSpace(cfg.Port)
	if port == "" {
		port = "587"
	}
	host := strings.TrimSpace(cfg.Host)
	var auth smtp.Auth
	if u := strings.TrimSpace(cfg.Username); u != "" {
		auth = smtp.PlainAuth("", u, cfg.Password, host)
	}
	raw := buildMessage(cfg, recipients, subject, body)
	if err := sendMail(ctx, net.JoinHostPort(host, port), host, auth, cfg.From, recipients, raw); err != nil {
		return fmt.Errorf("mail: smtp send: %w", err)
	}
	return nil
}

// dialTimeout bounds how long a send may spend waiting on the SMTP server.
//
// smtp.SendMail has no timeout at all: point it at a host that accepts the
// connection and then says nothing and the call hangs indefinitely, taking
// the HTTP handler with it. The "Send test email" request then sits until a
// reverse proxy gives up and answers 504 — the admin sees nothing at all,
// having had no feedback that anything was happening either.
//
// Well under the 60s a proxy typically allows, so the API answers with a
// real error rather than the proxy answering for it.
const dialTimeout = 20 * time.Second

// sendMail is smtp.SendMail with a deadline and context cancellation.
//
// Reimplemented rather than wrapped because the stdlib helper exposes no
// dialer, and a mail transport that can hang forever is not something a
// request handler can defend against from the outside.
func sendMail(ctx context.Context, addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	// Covers the whole conversation, not just the dial: a server that
	// accepts and then stalls mid-DATA is the case a dial timeout misses.
	deadline := time.Now().Add(dialTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return err
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// buildMessage renders a minimal RFC 822 text/plain message.
func buildMessage(cfg Config, to []string, subject, body string) []byte {
	from := cfg.From
	if n := strings.TrimSpace(cfg.FromName); n != "" {
		from = fmt.Sprintf("%s <%s>", n, cfg.From)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	// Message-ID is required of a well-formed message (RFC 5322) and is
	// what receiving systems use to recognise a duplicate. Without it, a
	// message delivered more than once — a relay retrying after a slow
	// or half-finished conversation, say — arrives as several separate
	// mails with nothing tying them together.
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", messageID(), messageIDDomain(cfg.From))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	return b.Bytes()
}

// messageID returns the unique left-hand side of a Message-ID.
func messageID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Randomness is unavailable only in situations where the send is
		// about to fail anyway; a time-based id still beats no header.
		return fmt.Sprintf("%d.sluicio", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:]) + ".sluicio"
}

// messageIDDomain takes the domain from the From address, falling back to
// a literal so the header is always well-formed.
func messageIDDomain(from string) string {
	if i := strings.LastIndex(from, "@"); i >= 0 && i+1 < len(from) {
		return strings.TrimSpace(from[i+1:])
	}
	return "sluicio.local"
}
