# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Multi-stage build for cell-api. Stage 1 compiles a static Go binary;
# stage 2 ships it on distroless so the runtime image carries nothing
# but the binary + a tiny libc + CA certs.
#
# Build context must be the repo root so the Go workspace files
# (go.work, go.mod, services/, pkg/, plugins/) are all visible:
#
#   docker build -f services/cell-api/Dockerfile -t sluicio/cell-api .
#
# The compose in deploy/server/ wires this up correctly.

FROM golang:1.25-bookworm AS build

WORKDIR /src

# Copy the workspace skeleton first so module-graph resolution is
# cacheable independent of source-file edits. A rebuild that doesn't
# touch go.{work,mod,sum} skips the dependency-download layer.
COPY go.work go.work.sum ./
COPY go.mod go.sum ./
COPY plugins/go.mod ./plugins/

# Then everything else — sources for pkg/, services/, plugins/.
COPY pkg ./pkg
COPY services ./services
COPY plugins ./plugins
# ee/ holds the Enterprise packages cell-api imports (license, audit) and the
# embedded license public key — it must be in the build context.
COPY ee ./ee

# Static build: CGO disabled, no dynamic linking, stripped symbols.
# The clickhouse-go driver is pure Go, and pgx/v5 is too, so we don't
# need any C dependencies. Trimpath strips the absolute build-time
# paths from the binary so two boxes building the same SHA produce
# byte-identical binaries.
# Build metadata stamped into pkg/version via -ldflags. Defaults keep a
# bare `docker build` working; publish.docker.sh + the Makefile pass real
# git-derived values with --build-arg.
ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

ENV CGO_ENABLED=0 GOFLAGS=-trimpath
RUN go build -ldflags="-s -w \
      -X github.com/sluicio/sluicio-app/pkg/version.Version=${VERSION} \
      -X github.com/sluicio/sluicio-app/pkg/version.Commit=${COMMIT} \
      -X github.com/sluicio/sluicio-app/pkg/version.BuildDate=${BUILD_DATE}" \
      -o /out/cell-api ./services/cell-api/cmd/cell-api

# ── Runtime image ──────────────────────────────────────────────────────
# distroless/static-debian12 is ~2 MB and has no shell. Smaller blast
# radius than scratch (which has zero CA certs, breaking TLS).

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/cell-api /cell-api

# 8081 is the documented cell-api port.
EXPOSE 8081

# nonroot user is uid 65532 in the distroless image.
# Numeric so Kubernetes runAsNonRoot can verify the user without a chart-side runAsUser pin.
USER 65532:65532

ENTRYPOINT ["/cell-api"]
