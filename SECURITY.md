<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Security Policy

We take the security of Sluicio seriously. Sluicio is **self-hosted** — your
telemetry stays in your own infrastructure — so a vulnerability in the code is
something we want to fix fast and disclose responsibly.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately to **support@sluicio.com**. Include:

- a description of the issue and its impact,
- steps to reproduce (a proof of concept if you have one),
- affected version / commit, and
- any suggested remediation.

We aim to acknowledge reports within **3 business days** and to provide a
remediation timeline after triage. We're happy to credit reporters in the
release notes unless you prefer to remain anonymous.

## Scope

In scope: the services under `services/`, the shared libraries under `pkg/`,
the frontend, the deployment manifests under `deploy/`, and the Enterprise
code under `ee/`.

Of particular interest:

- **Authentication / session handling** and the RBAC enforcement
  (`visibleServiceFilter`, role caps, `RequireWriteAnywhere`).
- **The OAuth 2.1 authorization server** and the SSO/OIDC client.
- **License verification** (`pkg/license`) — note that the security of the
  license system rests on the **private signing key**, which never ships;
  forging a token requires breaking Ed25519, not reading the (open) verifier.
- **Tenant isolation** — any path where one org could read or affect another
  org's data.
- **Ingestion** (`cell-ingest`) — untrusted OTLP payloads.

## What is *not* a vulnerability

- The ability to fork the source and remove license gates. The product is
  source-available; the license system protects honest customers, it is not a
  DRM boundary. (Issuing a *valid* license still requires the private key.)
- Default development credentials in `docker-compose` / e2e fixtures — these
  are clearly placeholders for local use and must be changed before any real
  deployment (see `deploy/server/sluicio.env.example`).

## Handling secrets

Never commit secrets. The signing **private key**, license tokens, TLS keys,
and real `.env` files are git-ignored by policy (see `.gitignore`); only the
license **public** key (`pkg/license/*.pub`) is committed, by design.
