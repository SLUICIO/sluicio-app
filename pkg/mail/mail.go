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
	if err := smtp.SendMail(net.JoinHostPort(host, port), auth, cfg.From, recipients, raw); err != nil {
		return fmt.Errorf("mail: smtp send: %w", err)
	}
	return nil
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
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	return b.Bytes()
}
