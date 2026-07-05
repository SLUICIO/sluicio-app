# cell-alerting

The custom alert evaluation engine and notification dispatcher.

Rules are structured objects spanning metric, trace, and log signals.
The engine polls each rule on its cadence, runs the appropriate backend
query through the adapter layer, evaluates the condition, transitions
alert state, and emits notification jobs into a durable outbound queue.

Notification delivery is plugin-based: in v1 the built-in implementations
of `plugins/notifier.Notifier` (email, webhook, AMQP, Kafka) are
compiled in. A later revision will expose the same contract over gRPC
so third-party plugins can ship as separate processes.

License: FSL-1.1-Apache-2.0.
