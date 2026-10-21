# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# The Sluicio web app: the Vite bundle, served by a small Go server
# (services/web) that also passes /api through to cell-api.
#
# Build context is the REPO ROOT (the server is Go, in the workspace):
#
#   docker build -f frontend/Dockerfile -t sluicio/frontend .
#
# The runtime image is empty apart from the bundle, the server and their
# third-party notices: no operating system, no shell, no packages. It used
# to be nginx on Alpine, which delivered busybox and its GPL with every
# image only to serve static files and proxy one path (issue #41).

# ── 1. The bundle ──────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS bundle
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
# The git-derived version shown in the app (vite.config.ts).
ARG APP_VERSION=0.0.0-dev
ENV APP_VERSION=${APP_VERSION}
COPY frontend/ .
# Also writes dist/third-party.json: the bundle's third-party components,
# and fails on a licence that is not allowed.
RUN npm run build

# ── 2. The server ──────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS server
WORKDIR /src
COPY go.work go.work.sum go.mod go.sum ./
COPY plugins ./plugins
COPY pkg ./pkg
COPY services/web ./services/web
COPY scripts/thirdparty ./scripts/thirdparty
ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /out/web ./services/web/cmd/web
# The server's own third-party notices (the Go standard library), for
# the TARGET platform's dependency graph; see the cell-api Dockerfile.
RUN go build -o /tmp/thirdparty ./scripts/thirdparty && \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} /tmp/thirdparty -check -product "Sluicio web server" \
      -json /out/third-party.json -text /out/THIRD-PARTY-NOTICES.txt \
      ./services/web/cmd/web

# ── Runtime ────────────────────────────────────────────────────────────
FROM scratch
COPY --from=server /out/web /web
COPY --from=server /out/third-party.json /out/THIRD-PARTY-NOTICES.txt /usr/share/sluicio/
COPY --from=bundle /app/dist /www
# :80 by default, the port every deployment maps to. Started as root it
# binds the port and then drops to uid 65532 before serving anything;
# set WEB_ADDR to an unprivileged port to run as non-root from the start
# (the Helm chart does).
EXPOSE 80
ENTRYPOINT ["/web"]
