// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Command cell-controller is the entry point for the control plane's
// cell provisioner. It lives in the control plane's Kubernetes cluster
// and applies/maintains the cell Helm chart per tenant cell, then
// registers the cell's endpoints back to the control plane's directory.
//
// On-premise deployments do not run this service.
//
// At this stage this is a scaffold: it starts, prints its version, and
// exits when interrupted.
package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/integration-monitor/integration-monitor/pkg/log"
	"github.com/integration-monitor/integration-monitor/pkg/version"
)

const serviceName = "cell-controller"

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
