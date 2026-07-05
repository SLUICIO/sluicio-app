# pkg

Internal shared Go libraries used across the Integration Monitor
services. These packages are imported by every service that needs them
(logging conventions, version metadata, common HTTP middleware, etc.).

Although Go calls this `pkg/` (publicly importable in Go conventions),
it is licensed FSL-1.1-Apache-2.0 and is part of the product, not the
periphery. Public-facing contracts for third-party plugin authors live
in the [`plugins/`](../plugins) module under Apache 2.0.

License: FSL-1.1-Apache-2.0.
