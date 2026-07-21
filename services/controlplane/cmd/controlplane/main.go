// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Command controlplane is the entry point for the Integration Monitor
// control plane service. The control plane owns the global directory:
// organizations, users, memberships, invitations, billing, and the
// mapping of which tenants live in which cells. It does not handle
// telemetry; that lives in cells.
//
// At this stage this is a scaffold: it starts, prints its version, and
// exits when interrupted.
package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/sluicio/sluicio-app/pkg/log"
	"github.com/sluicio/sluicio-app/pkg/version"
)

const serviceName = "controlplane"

func main() {
	logger := log.New(serviceName, log.FormatJSON)
	logger.Info("starting",
		"version", version.Version,
		"commit", version.Commit,
		"build_date", version.BuildDate,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	logger.Info("shutting down")
}
