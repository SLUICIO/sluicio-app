# plugins

Plugin contracts for third-party extensions to Integration Monitor.

This is a separate Go module (`github.com/integration-monitor/integration-monitor/plugins`)
under Apache 2.0 so external authors can depend on it without inheriting
the Functional Source License that covers the rest of the product.

## Contracts

- [`notifier`](./notifier) — `Notifier` interface implemented by every
  notification channel plugin.

More contracts (adapters for BYO backends, event handlers) will be added
here as they stabilize.

## Stability

These interfaces are versioned in a backwards-compatible manner once we
publish a `v1`. Until then, expect breaking changes — the project is
pre-1.0.

License: Apache-2.0.
