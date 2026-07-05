// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Command cell-alerting is the entry point for the cell's alert
// evaluation and notification dispatch service. It polls each
// configured rule on its cadence, translates the rule into a backend
// query via the same adapter layer cell-api uses, evaluates the
// condition, and emits notification jobs into a durable outbound queue.
// A worker pool drains the queue and delivers via the configured
// channel plugins.
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

const serviceName = "cell-alerting"

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
