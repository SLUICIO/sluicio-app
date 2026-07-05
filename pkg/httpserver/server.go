// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package httpserver wraps net/http with the conventions every service
// in this repo follows: graceful shutdown tied to a context, sensible
// timeouts, structured logging, and a few response helpers.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Config controls the server.
type Config struct {
	// Addr is the listen address (e.g. ":8081").
	Addr string

	// Handler is the root HTTP handler.
	Handler http.Handler

	// Logger receives lifecycle messages. If nil, the default slog
	// logger is used.
	Logger *slog.Logger

	// ReadHeaderTimeout limits how long the server waits for request
	// headers. Defaults to 10s. Setting this is a CodeQL / gosec
	// recommendation to defend against Slowloris-style clients.
	ReadHeaderTimeout time.Duration

	// ShutdownTimeout caps how long graceful shutdown is allowed to
	// run after the context is cancelled. Defaults to 15s.
	ShutdownTimeout time.Duration
}

// Run blocks running an HTTP server with the given config until ctx is
// cancelled or the server returns an error. On ctx cancellation it
// initiates graceful shutdown bounded by Config.ShutdownTimeout.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Addr == "" {
		return errors.New("httpserver: Addr is required")
	}
	if cfg.Handler == nil {
		return errors.New("httpserver: Handler is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	readHeaderTimeout := cfg.ReadHeaderTimeout
	if readHeaderTimeout == 0 {
		readHeaderTimeout = 10 * time.Second
	}
	shutdownTimeout := cfg.ShutdownTimeout
	if shutdownTimeout == 0 {
		shutdownTimeout = 15 * time.Second
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           cfg.Handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", listener.Addr().String())
		errs <- srv.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		logger.Info("http server shutting down")
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// WriteJSON writes the given value as a JSON response with the given
// status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Error("json encode failed", "err", err)
	}
}

// WriteError writes a JSON error response.
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]any{
		"error": map[string]any{
			"status":  status,
			"message": message,
		},
	})
}

// AllowCORSForDev wraps a handler in a permissive CORS middleware that
// is appropriate for local development (where the frontend dev server
// runs on a different port). It is NOT appropriate for production.
func AllowCORSForDev(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
