// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Command web serves the Sluicio web app (see package web).
//
//	WEB_ADDR          listen address           (default ":80")
//	WEB_ROOT          the built bundle         (default "/www")
//	WEB_API_UPSTREAM  cell-api's base URL      (default "http://cell-api:8081")
//
// Port 80 is the default because every deployment already maps to it.
// Under Docker and Podman a non-root process may not bind it, so when
// started as root the server binds first and then drops to an
// unprivileged user (WEB_UID / WEB_GID, default 65532) before serving a
// single request: the root part of its life is opening one socket. Where
// it already runs as non-root - the Helm chart sets an unprivileged port
// - there is nothing to drop.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/sluicio/sluicio-app/pkg/env"
	"github.com/sluicio/sluicio-app/services/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "web")
	upstream, err := url.Parse(env.String("WEB_API_UPSTREAM", "http://cell-api:8081"))
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		logger.Error("WEB_API_UPSTREAM must be a URL like http://cell-api:8081", "value", os.Getenv("WEB_API_UPSTREAM"))
		os.Exit(1)
	}
	root := env.String("WEB_ROOT", "/www")
	addr := env.String("WEB_ADDR", ":80")

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("listen failed", "addr", addr, "err", err)
		os.Exit(1)
	}
	if err := dropPrivileges(); err != nil {
		logger.Error("could not drop root after binding; refusing to serve as root", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Handler:           web.New(web.Config{Root: root, APIUpstream: upstream}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	logger.Info("serving", "addr", ln.Addr().String(), "root", root, "api_upstream", upstream.String(), "uid", os.Getuid())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// dropPrivileges switches a root process to WEB_UID/WEB_GID. A non-root
// process is left as it is.
func dropPrivileges() error {
	if os.Getuid() != 0 {
		return nil
	}
	uid := envInt("WEB_UID", 65532)
	gid := envInt("WEB_GID", 65532)
	if err := syscall.Setgroups(nil); err != nil {
		return err
	}
	if err := syscall.Setgid(gid); err != nil {
		return err
	}
	if err := syscall.Setuid(uid); err != nil {
		return err
	}
	if os.Getuid() == 0 {
		return errors.New("still root after setuid")
	}
	return nil
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return def
}
