# Building & pushing container images

Sluicio ships five images, all built from this repo:

| Image | Dockerfile | Notes |
|---|---|---|
| `cell-api` | `services/cell-api/Dockerfile` | HTTP API (:8081) |
| `cell-ingest` | `services/cell-ingest/Dockerfile` | OTLP ingest (:4318) |
| `cell-alerting` | `services/cell-alerting/Dockerfile` | background worker (no port) |
| `controlplane` | `services/controlplane/Dockerfile` | control plane (:8080) |
| `frontend` | `frontend/Dockerfile` | Vite build → nginx (:80), SPA fallback |

The Go images build with **context = repo root** (the Go workspace must be
visible); the frontend builds with context = `frontend/`. A `.dockerignore`
at each context keeps the upload lean.

## Build locally

```sh
make docker-build                 # tags <namespace>/<image>:<git-sha>
make docker-build IMAGE_TAG=dev   # custom tag
```

## Push to a registry

```sh
make docker-push REGISTRY=registry.lan:5000
# → registry.lan:5000/sluicio/cell-api:<git-sha>, …/frontend:<git-sha>, etc.
```

Variables (all overridable on the `make` line):

- `REGISTRY` — registry host:port. **Required** for `docker-push`.
- `IMAGE_NAMESPACE` — defaults to `sluicio` (the path under the registry).
- `IMAGE_TAG` — defaults to the short git SHA (falls back to `dev`).
- `CONTAINER` — `docker` or `podman` (auto-detected).

## Local / LAN registry over plain HTTP

A registry that isn't reachable from the internet is usually served over
**plain HTTP**, which Docker treats as "insecure" and refuses by default.
On every machine that builds/pushes **or** pulls, add it to the daemon
config (client-side — not committed here):

```jsonc
// /etc/docker/daemon.json
{ "insecure-registries": ["registry.lan:5000"] }
```
then restart the daemon. (Podman: `[[registry]]` with `insecure = true` in
`/etc/containers/registries.conf`.) Prefer real TLS once it's not just for
testing.

## Deploying the pushed images

- **Helm** (`deploy/helm/cell`, `deploy/helm/controlplane`): point each
  chart's `image.repository` at `registry.lan:5000/sluicio/<image>` and set
  `image.tag` to the `IMAGE_TAG` you pushed.
- **Compose** (`deploy/server/docker-compose.yml`): it currently *builds*
  cell-api/cell-ingest from source; swap the `build:` blocks for
  `image: registry.lan:5000/sluicio/<image>:<tag>` to pull instead.
