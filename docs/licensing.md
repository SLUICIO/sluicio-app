# Licensing

Sluicio uses three licenses, chosen deliberately: FSL for the product,
Apache-2.0 for the periphery third parties build on, and the Sluicio
Enterprise License for the small `ee/` directory (see `NOTICE` for the
per-directory map and `ee/LICENSE.md` for the Enterprise terms).

## The product itself — FSL-1.1-Apache-2.0

The services, frontend, internal shared libraries, and the control-plane
Helm chart are licensed under the [Functional Source License v1.1, Apache
2.0 future grant](https://fsl.software/FSL-1.1-Apache-2.0.template.md).

What this means in plain language:

- Anyone may read, modify, fork, self-host, and audit the code.
- A customer may run the software for their own integration monitoring,
  with no restriction on scale or revenue.
- A customer may run the software to support their own commercial
  products and services, as long as the value of those products and
  services does not derive primarily from this software.
- A **competing managed service** — i.e. selling Sluicio as
  a service, in substitution for our SaaS — is not permitted.
- Two years after each version is published, that version becomes
  available under Apache 2.0 with no restriction.

We chose FSL over Apache/MIT because the modern playbook for hyperscalers
and large competitors is to take a permissively-licensed observability
project, host it themselves, and capture the revenue while the original
authors do the engineering. Grafana, Elastic, MongoDB, HashiCorp, Redis,
and Sentry all started permissive and re-licensed when they hit a certain
size, which is painful for the community and contributors.

We chose FSL over BSL because the conversion to a true open-source
license happens after two years rather than four, and the wording is
narrower and easier for enterprise legal teams to understand.

We chose source-available over fully proprietary because our target
customers run regulated workloads (healthcare, finance) where the ability
to audit, fork, and self-host the software is a real procurement
requirement.

The cost of FSL is that it is not OSI-approved "open source", so the
project cannot host under CNCF or Apache umbrellas, and some F/OSS-purist
communities will reject it. We consider this a reasonable trade.

## The periphery — Apache 2.0

Code that customers and third parties need to *build on* is Apache 2.0
from the first commit. That includes:

- **`plugins/`** — the Go interface contracts for notification channels
  (and later adapters and event handlers). Third-party authors need to
  link this without restriction.
- **`deploy/helm/cell/`** — the cell Helm chart. Customers fork and
  modify it for their own deployments and Kubernetes flavors. The chart
  itself contains no proprietary product code; it deploys our FSL-licensed
  service images. The license on the chart and the license on the
  software it deploys are independent.
- **`deploy/otel-collector/`** — example OTel Collector configurations.
  These are templates, not differentiated product.
- **`sdk/`** — future SDK helpers for customer code.
- **`docs/`** — all documentation, including this file.

## How licenses are applied in practice

Each source file carries an `SPDX-License-Identifier` header.
`SPDX-License-Identifier: FSL-1.1-Apache-2.0` or `SPDX-License-Identifier:
Apache-2.0`. The header is the authoritative source of the license that
applies to that file. The [`NOTICE`](../NOTICE) file records the default
license per directory.

## Contributing

By submitting a contribution to a file in this repository you agree that
your contribution is licensed under the same license as the file. A more
formal Developer Certificate of Origin or CLA may be introduced before
the project is opened up.
