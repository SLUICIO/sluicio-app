<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Contributing to Sluicio

Thanks for your interest in Sluicio — integration monitoring built on
OpenTelemetry. This guide covers how to build, test, and propose changes.

## Open-core model (read this first)

Almost everything here is open under **FSL-1.1-Apache-2.0** (which becomes
Apache 2.0 two years after each release). The product — services, frontend,
shared libraries, **and the Enterprise feature code and license verification**
— is all in the open core and fully auditable.

Only two things under `ee/` are proprietary (Sluicio Enterprise License,
`ee/LICENSE.md`): the **audit-log persistence** and the **license-mint tool**.
The core compiles and runs with **no `ee/` code present** — Enterprise is
wired in only at a service's `main()`, and the audit store sits behind the
`audit.Recorder` interface in `pkg/audit`. Contributions to the open core are
welcome; the `ee/` directory is maintained by Sluicio.

## Licensing of contributions

Sluicio uses the **license-in / license-out** model common to
source-available projects:

- **Inbound**: by submitting a contribution you license it to ROMA IT AB
  under the **Apache License 2.0**, and you grant ROMA IT AB the right to
  distribute it as part of Sluicio under the repository's outbound licenses
  (FSL-1.1-Apache-2.0 / Apache-2.0 / SEL, per directory).
- **Outbound**: the code you receive is licensed per `NOTICE` and the
  per-file SPDX headers, exactly as before.

Every commit must carry a **Developer Certificate of Origin** sign-off
([developercertificate.org](https://developercertificate.org)) certifying you
have the right to submit the work:

```sh
git commit -s   # adds "Signed-off-by: Your Name <you@example.com>"
```

Pull requests with unsigned commits will be asked to rebase with `-s`.

## Prerequisites

- Go 1.22+
- Node 20+
- Podman or Docker (for Postgres + ClickHouse)

## Build & run

```bash
make help            # list common tasks
docker compose up -d # Postgres, ClickHouse, and the services for local dev
make dev-rebuild     # rebuild + restart app containers after a code change
```

Frontend:

```bash
cd frontend && npm install && npm run dev   # Vite dev server (proxies /api → cell-api)
```

### Testing Enterprise features locally

Enterprise features are gated by a license key, **not** by a separate build —
the default build includes all of it. To exercise them, load a dev license at
runtime:

```bash
# one-time: generate a dev keypair (private key stays out of git),
# write the public key to pkg/license/sluicio_license_ed25519.pub, rebuild.
go run ./ee/cmd/sluicio-license keygen -out sluicio_license_dev.key

# mint a token (e.g. Business plan, 75 integrations) and load it:
go run ./ee/cmd/sluicio-license mint -key sluicio_license_dev.key \
    -customer "Dev" -max-integrations 75 > sluicio_license_dev.token
export SLUICIO_LICENSE_FILE=$PWD/sluicio_license_dev.token   # cell-api reads this
```

With no key loaded, the app runs as the free Community edition (all core
features, EE gates off) — which is exactly what a community user sees.

## Checks before opening a PR

```bash
go build ./...                 # everything compiles
go vet ./...
go test ./...                  # unit tests
make openapi-check             # OpenAPI spec is in sync with the route table
cd frontend && npx tsc --noEmit && npm run build   # typecheck + build the UI
```

Match the surrounding code's style (comment density, naming, idioms). Every
new source file needs an `SPDX-License-Identifier` header matching its
directory (`FSL-1.1-Apache-2.0` for the core; `Apache-2.0` for `plugins/`,
`deploy/otel-collector/`, and `docs/`).

## Reporting security issues

Do **not** file them as public issues — see [`SECURITY.md`](SECURITY.md).
