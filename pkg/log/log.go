// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package log is a thin wrapper around log/slog with the conventions
// every Integration Monitor binary should follow:
//
//   - Structured (JSON) logging by default in production, text in
//     development.
//   - A short service name on every line.
//   - A consistent set of attribute keys (tenant_id, integration_id,
//     rule_id) so log search and dashboards align across services.
//
// The wrapper is intentionally minimal; callers can drop down to slog
// for anything not exposed here.
package log

import (
	"log/slog"
	"os"
	"strings"
)

// Format controls the output format of the default logger.
type Format string

const (
	FormatJSON Format = "json"
	FormatText Format = "text"
)

// New returns a slog.Logger configured for the given service name and
// format. Level is parsed from the LOG_LEVEL environment variable
// (debug|info|warn|error), defaulting to info.
func New(service string, format Format) *slog.Logger {
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: levelFromEnv()}

	switch format {
	case FormatText:
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler).With("service", service)
}

func levelFromEnv() slog.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
